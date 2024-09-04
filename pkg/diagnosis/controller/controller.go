package controller

import (
	"fmt"

	"github.com/baizeai/kcover/pkg/diagnosis"
	"github.com/baizeai/kcover/pkg/diagnosis/podstatus"
	"github.com/baizeai/kcover/pkg/events"
	"github.com/baizeai/kcover/pkg/runner"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

var _ runner.Runner = (*controllerDiagnostic)(nil)

type controllerDiagnostic struct {
	diagnostics []diagnosis.Diagnostic
	recorder    events.Recorder
}

func NewControllerDiagnostic(cli kubernetes.Interface, recorder events.Recorder) (runner.Runner, error) {
	diags := make([]diagnosis.Diagnostic, 0)

	diagPodCollector, err := podstatus.NewPodStatusCollector(cli)
	if err != nil {
		return nil, fmt.Errorf("failed to create pod status collector: %v", err)
	}

	diags = append(diags, diagPodCollector)

	if recorder == nil {
		return nil, fmt.Errorf("recorder can not be nil")
	}

	return &controllerDiagnostic{
		diagnostics: diags,
		recorder:    recorder,
	}, nil
}

func (c *controllerDiagnostic) Start() error {
	for _, d := range c.diagnostics {
		if err := d.Start(); err != nil {
			return err
		}
	}
	for _, d := range c.diagnostics {
		go func(d diagnosis.Diagnostic) {
			for e := range d.Events() {
				err := c.recorder.RecordEvent(e)
				if err != nil {
					klog.Errorf("failed to record event of %T: %v", d, err)
				}
			}
		}(d)
	}

	return nil
}

func (c *controllerDiagnostic) Stop() {
	for _, d := range c.diagnostics {
		d.Stop()
	}
}
