package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/baizeai/kcover/pkg/detector/pod"
	"github.com/baizeai/kcover/pkg/events"
	"github.com/baizeai/kcover/pkg/kube"
	"github.com/baizeai/kcover/pkg/preflight"
	"github.com/baizeai/kcover/pkg/recovery"
	"github.com/baizeai/kcover/pkg/runner"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	coordv1 "k8s.io/client-go/kubernetes/typed/coordination/v1"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/klog/v2"
)

func init() {
	klog.InitFlags(flag.CommandLine)
}

var preflightReportCollectionTimeout = flag.Duration(
	"preflight-report-collection-timeout",
	preflight.DefaultReportCollectionTimeout,
	"maximum time to wait for a complete set of preflight reports before expiring the partial aggregation",
)

var preflightSweepInterval = flag.Duration(
	"preflight-sweep-interval",
	recovery.DefaultPreflightSweepInterval,
	"interval for sweeping expired preflight report aggregations",
)

var leaderElect = flag.Bool(
	"leader-elect",
	true,
	"enable leader election for the controller",
)

type controllerConfig struct {
	reportCollectionTimeout time.Duration
	sweepInterval           time.Duration
	leaderElectionEnabled   bool
}

func mustHostName() string {
	if nodeName := kube.NodeNameFromEnv(); nodeName != "" {
		return nodeName
	}

	hn, err := os.Hostname()
	if err != nil {
		panic(err)
	}
	return hn
}
func lock(hostName string) *resourcelock.LeaseLock {
	return &resourcelock.LeaseLock{
		Client: coordv1.NewForConfigOrDie(kube.GetK8sConfigConfigWithFile("", "")),
		LeaseMeta: metav1.ObjectMeta{
			Name:      "kcover",
			Namespace: kube.CurrentNamespace(),
		},
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: hostName,
		},
	}
}

func makeElectionCallback(reportCollectionTimeout, sweepInterval time.Duration) (func(ctx context.Context), func()) {
	cfg := kube.GetK8sConfigConfigWithFile("", "")
	client := kubernetes.NewForConfigOrDie(cfg)
	dynamicClient := dynamic.NewForConfigOrDie(cfg)

	var (
		recov    runner.Runner
		detector runner.Runner
		bridge   events.Bridge
		reports  runner.Runner
	)

	return func(ctx context.Context) {
			// 当前实例成为 leader 时，开始执行 controller 逻辑
			var err error
			bridge = events.NewKubeEventBridge(client)
			reportTransport := preflight.NewKubeReportStream(dynamicClient)
			reports = reportTransport
			recov = recovery.NewController(
				client,
				bridge,
				reportTransport,
				reportCollectionTimeout,
				sweepInterval,
			)
			detector, err = pod.NewDetector(client, bridge)
			if err != nil {
				panic(err)
			}
			if err := recov.Start(ctx); err != nil {
				panic(err)
			}
			if err := detector.Start(ctx); err != nil {
				panic(err)
			}
			if err := bridge.Start(ctx); err != nil {
				panic(err)
			}
			if err := reports.Start(ctx); err != nil {
				panic(err)
			}

			klog.InfoS("kcover started")
		},
		func() {
			detector.Stop()
			reports.Stop()
			bridge.Stop()
			recov.Stop()
			klog.InfoS("kcover stopped")
		}
}

func runtimeConfig() controllerConfig {
	return controllerConfig{
		reportCollectionTimeout: *preflightReportCollectionTimeout,
		sweepInterval:           *preflightSweepInterval,
		leaderElectionEnabled:   *leaderElect,
	}
}

func leaderElectionConfig(started func(context.Context), stopped func()) leaderelection.LeaderElectionConfig {
	return leaderelection.LeaderElectionConfig{
		Lock:            lock(mustHostName()),
		ReleaseOnCancel: true,
		LeaseDuration:   15 * time.Second,
		RenewDeadline:   10 * time.Second,
		RetryPeriod:     2 * time.Second,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: started,
			OnStoppedLeading: stopped,
		},
	}
}

func run(ctx context.Context, cfg controllerConfig) {
	started, stopped := makeElectionCallback(cfg.reportCollectionTimeout, cfg.sweepInterval)
	if !cfg.leaderElectionEnabled {
		started(ctx)
		<-ctx.Done()
		stopped()
		return
	}

	leaderelection.RunOrDie(ctx, leaderElectionConfig(started, stopped))
}

func main() {
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	run(ctx, runtimeConfig())
}
