//go:build !metax

package node

import (
	"context"
	"sync"

	config "github.com/baizeai/kcover/pkg/agentconfig"
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
	cancel context.CancelFunc
	once   sync.Once
}

func (d *noopDetector) Start(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	d.cancel = cancel
	go func() {
		<-ctx.Done()
		d.Stop()
	}()
	return nil
}

func (d *noopDetector) Stop() {
	if d.cancel != nil {
		d.cancel()
	}
	d.once.Do(func() { close(d.events) })
}

func (d *noopDetector) EventChan() <-chan events.Event {
	return d.events
}

func (d *noopDetector) String() string {
	return "Noop"
}
