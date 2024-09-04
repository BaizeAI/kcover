package main

import (
	"context"
	"os"
	"time"

	"github.com/baizeai/kcover/pkg/diagnosis/controller"
	"github.com/baizeai/kcover/pkg/events"
	"github.com/baizeai/kcover/pkg/kube"
	"github.com/baizeai/kcover/pkg/recovery"
	"github.com/baizeai/kcover/pkg/runner"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	coordinationv1client "k8s.io/client-go/kubernetes/typed/coordination/v1"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/klog/v2"
)

func main() {
	hostName, err := os.Hostname()
	if err != nil {
		panic(err)
	}

	cfg := kube.GetK8sConfigConfigWithFile("", "")
	client := kubernetes.NewForConfigOrDie(cfg)
	var eventBus events.Recorder
	var rec runner.Runner
	var diag runner.Runner
	leaderElectionConfig := leaderelection.LeaderElectionConfig{
		Lock: &resourcelock.LeaseLock{
			Client: coordinationv1client.NewForConfigOrDie(kube.GetK8sConfigConfigWithFile("", "")),
			LeaseMeta: metav1.ObjectMeta{
				Name: "kcover",
				Namespace: func() string {
					if bs, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
						return string(bs)
					}
					return "default"
				}(),
			},
			LockConfig: resourcelock.ResourceLockConfig{
				Identity: hostName,
			},
		},
		ReleaseOnCancel: true,
		LeaseDuration:   15 * time.Second,
		RenewDeadline:   10 * time.Second,
		RetryPeriod:     2 * time.Second,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				// 当当前实例成为 leader 时，开始执行 controller 逻辑
				var err error
				eventBus = events.NewKubeEventsRecorder(client, true)
				rec = recovery.NewRecoveryController(client, eventBus)
				diag, err = controller.NewControllerDiagnostic(client, eventBus)
				if err != nil {
					panic(err)
				}
				if err := rec.Start(); err != nil {
					panic(err)
				}
				if err := diag.Start(); err != nil {
					panic(err)
				}
				if err := eventBus.Start(); err != nil {
					panic(err)
				}

				klog.Info("kcover started")
			},
			OnStoppedLeading: func() {
				rec.Stop()
				diag.Stop()
				eventBus.Stop()
				klog.Info("kcover stopped")
			},
		},
	}

	leaderelection.RunOrDie(context.Background(), leaderElectionConfig)
}
