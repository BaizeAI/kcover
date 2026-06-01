package node

import (
	"fmt"

	"github.com/baizeai/kcover/cmd/agent/config"
	detectorpkg "github.com/baizeai/kcover/pkg/detector"
	"github.com/baizeai/kcover/pkg/detector/node/metax"
	"github.com/baizeai/kcover/pkg/detector/node/nvidia"
	"github.com/baizeai/kcover/pkg/events"
	"github.com/baizeai/kcover/pkg/runner"

	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

type Vendor int

const (
	_ Vendor = iota
	Nvidia
	MetaX
)

var _ runner.Runner = (*detector)(nil)

type detector struct {
	eventSink events.Sink
	detectors []detectorpkg.Detector
}

func NewDetector(nodeName string, vendor Vendor, interval int, cfg config.MetaX, client kubernetes.Interface, sink events.Sink) (runner.Runner, error) {
	if sink == nil {
		return nil, fmt.Errorf("event sink cannot be nil")
	}

	var d detectorpkg.Detector
	switch vendor {
	case MetaX:
		if client == nil {
			return nil, fmt.Errorf("kubernetes client cannot be nil for MetaX detector")
		}
		cfg.NodeName = nodeName
		d = metax.NewDetector(cfg, interval, client)
	case Nvidia:
		d = nvidia.NewDetector(nodeName, interval)
	default:
		return nil, fmt.Errorf("unsupported vendor: %d", vendor)
	}

	return &detector{
		eventSink: sink,
		detectors: []detectorpkg.Detector{d},
	}, nil
}

func (d *detector) Start() error {
	for _, v := range d.detectors {
		if err := v.Start(); err != nil {
			return err
		}
		klog.InfoS("detector started", "detector", v)
	}

	for _, v := range d.detectors {
		go func(detector detectorpkg.Detector) {
			for evt := range detector.EventChan() {
				if err := d.eventSink.RecordEvent(evt); err != nil {
					klog.ErrorS(err, "failed to record event", "detector", detector)
				}
			}
		}(v)
	}

	return nil
}

func (d *detector) Stop() {
	for _, detector := range d.detectors {
		detector.Stop()
	}
}
