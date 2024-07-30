package main

import (
	"context"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	coordinationv1client "k8s.io/client-go/kubernetes/typed/coordination/v1"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/klog/v2"
	"recovery.baizeai.io/pkg/diagnosis/controller"
	"recovery.baizeai.io/pkg/events"
	"recovery.baizeai.io/pkg/kube"
	"recovery.baizeai.io/pkg/recovery"
	"recovery.baizeai.io/pkg/runner"
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
				Name: "fast-recovery",
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

				klog.Info("fast-recovery started")
			},
			OnStoppedLeading: func() {
				rec.Stop()
				diag.Stop()
				eventBus.Stop()
				klog.Info("fast-recovery stopped")
			},
		},
	}

	leaderelection.RunOrDie(context.Background(), leaderElectionConfig)
}
