//go:build !metax

package node

import (
	"github.com/baizeai/kcover/cmd/agent/config"
	d "github.com/baizeai/kcover/pkg/detector"
	"github.com/baizeai/kcover/pkg/events"

	"k8s.io/client-go/kubernetes"
)

func newDetector(_ string, _ config.Agent, _ kubernetes.Interface) (d.Detector, error) {
	return &noopDetector{
		events: make(chan events.Event),
	}, nil
}

var _ d.Detector = (*noopDetector)(nil)

type noopDetector struct {
	events chan events.Event
}

func (d *noopDetector) Start() error { return nil }

func (d *noopDetector) Stop() {
	close(d.events)
}

func (d *noopDetector) EventChan() <-chan events.Event {
	return d.events
}

func (d *noopDetector) String() string {
	return "Noop"
}
