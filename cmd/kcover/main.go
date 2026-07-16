package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/baizeai/kcover/pkg/kube"
	"github.com/baizeai/kcover/pkg/preflight"
	"github.com/baizeai/kcover/pkg/recovery"

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

const podNameEnv = "POD_NAME"

func mustControllerIdentity() string {
	if podName := os.Getenv(podNameEnv); podName != "" {
		return podName
	}

	hn, err := os.Hostname()
	if err != nil {
		panic(err)
	}
	return hn
}

func lock(identity string) *resourcelock.LeaseLock {
	return &resourcelock.LeaseLock{
		Client: coordv1.NewForConfigOrDie(kube.GetK8sConfigConfigWithFile("", "")),
		LeaseMeta: metav1.ObjectMeta{
			Name:      "kcover",
			Namespace: kube.CurrentNamespace(),
		},
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: identity,
		},
	}
}

func makeElectionCallback(reportCollectionTimeout, sweepInterval time.Duration) (func(ctx context.Context), func()) {
	cfg := kube.GetK8sConfigConfigWithFile("", "")
	cli := kubernetes.NewForConfigOrDie(cfg)
	dynCli := dynamic.NewForConfigOrDie(cfg)
	app := newControllerApp(cli, dynCli, reportCollectionTimeout, sweepInterval)

	return func(ctx context.Context) {
			if err := app.Start(ctx); err != nil {
				if ctx.Err() != nil {
					klog.InfoS("controller startup canceled", "error", err)
					return
				}
				panic(err)
			}
			klog.InfoS("kcover started")
		},
		func() {
			app.Stop()
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
		Lock:            lock(mustControllerIdentity()),
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
