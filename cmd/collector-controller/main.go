package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/baizeai/kcover/pkg/diagnosis"
	"github.com/baizeai/kcover/pkg/diagnosis/nvidiadiag"
	"github.com/baizeai/kcover/pkg/events"
	"github.com/baizeai/kcover/pkg/kube"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

func main() {
	var hostName string
	if hn := os.Getenv("FAST_RECOVERY_NODE_NAME"); hn != "" {
		hostName = hn
	} else {
		hn, err := os.Hostname()
		if err != nil {
			panic(err)
		}
		hostName = hn
	}

	dcgmDiag, err := nvidiadiag.NewDCGMDiagnosis(hostName)
	if err != nil {
		panic(err)
	}

	diags := []diagnosis.Diagnostic{dcgmDiag}
	cfg := kube.GetK8sConfigConfigWithFile("", "")
	client := kubernetes.NewForConfigOrDie(cfg)
	recorder := events.NewKubeEventsRecorder(client, false)

	for _, d := range diags {
		if err := d.Start(); err != nil {
			panic(err)
		}

		klog.Infof("diag %T started", d)

		go func(d diagnosis.Diagnostic) {
			for e := range d.Events() {
				if err := recorder.RecordEvent(e); err != nil {
					klog.Errorf("record event %+v error: %v", e, err)
				}
			}
		}(d)
	}

	cc := make(chan os.Signal, 1)
	signal.Notify(cc, os.Interrupt, syscall.SIGTERM)
	<-cc
	klog.Info("collector stopped")
}
