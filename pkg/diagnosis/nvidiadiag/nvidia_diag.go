package nvidiadiag

import (
	"time"

	"github.com/baizeai/kcover/pkg/diagnosis"
	"github.com/baizeai/kcover/pkg/events"
	"github.com/baizeai/kcover/pkg/runner"
	"k8s.io/klog/v2"
)

var _ runner.Runner = (*dcgmDiag)(nil)
var _ diagnosis.Diagnostic = (*dcgmDiag)(nil)

type dcgmDiag struct {
	nodeName string
	events   chan events.CollectorEvent
	stop     chan struct{}
}

func NewDCGMDiagnosis(nodeName string) (diagnosis.Diagnostic, error) {
	return &dcgmDiag{
		events:   make(chan events.CollectorEvent),
		stop:     make(chan struct{}),
		nodeName: nodeName,
	}, nil
}

func (d *dcgmDiag) Start() error {
	go func() {
		t := time.NewTicker(time.Second * 30)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				// run dcgmi
				// parse results
				klog.Infof("start dcgmi diag -r 1")
				//d.events <- events.CollectorEvent{
				//	TargetType: events.Node,
				//	Name:       "worker-a800-2",
				//	EventType:  events.Error,
				//	Message:    "test event for worker-a800-2",
				//}
			case <-d.stop:
				return
			}
		}
	}()
	return nil
}

func (d *dcgmDiag) Stop() {
	close(d.stop)
}

func (d *dcgmDiag) Events() <-chan events.CollectorEvent {
	return d.events
}
