package node

import (
	"fmt"

	"github.com/baizeai/kcover/cmd/agent/config"
	detectorpkg "github.com/baizeai/kcover/pkg/detector"
	"github.com/baizeai/kcover/pkg/events"
	"github.com/baizeai/kcover/pkg/runner"

	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

var _ runner.Runner = (*detector)(nil)

type detector struct {
	eventSink events.Sink
	detector  detectorpkg.Detector
}

func NewDetector(nodeName string, cfg config.Agent, client kubernetes.Interface, sink events.Sink) (runner.Runner, error) {
	if sink == nil {
		return nil, fmt.Errorf("event sink cannot be nil")
	}

	nodeDetector, err := newDetector(nodeName, cfg, client)
	if err != nil {
		return nil, err
	}

	return &detector{
		eventSink: sink,
		detector:  nodeDetector,
	}, nil
}

func (d *detector) Start() error {
	if err := d.detector.Start(); err != nil {
		return err
	}
	klog.InfoS("detector started", "detector", d.detector)

	go func() {
		for evt := range d.detector.EventChan() {
			if err := d.eventSink.RecordEvent(evt); err != nil {
				klog.ErrorS(err, "failed to record event", "detector", d.detector)
			}
		}
	}()

	return nil
}

func (d *detector) Stop() {
	d.detector.Stop()
}
