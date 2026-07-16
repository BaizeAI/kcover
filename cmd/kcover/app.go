package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/baizeai/kcover/pkg/detector/pod"
	"github.com/baizeai/kcover/pkg/events"
	"github.com/baizeai/kcover/pkg/preflight"
	"github.com/baizeai/kcover/pkg/recovery"
	"github.com/baizeai/kcover/pkg/runner"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

type controllerComponent struct {
	name   string
	runner runner.Runner
}

type controllerApp struct {
	mu         sync.Mutex
	build      func() ([]controllerComponent, error)
	components []controllerComponent
	started    int
	stopped    bool
}

func newControllerApp(cli kubernetes.Interface, dynCli dynamic.Interface, reportCollectionTimeout, sweepInterval time.Duration) *controllerApp {
	return &controllerApp{build: func() ([]controllerComponent, error) {
		eventTransport := events.NewKubeEventTransport(cli)
		reportWatcher := preflight.NewKubeReportWatcher(dynCli)
		recoveryController := recovery.NewController(
			cli,
			eventTransport,
			reportWatcher.Reports(),
			reportCollectionTimeout,
			sweepInterval,
		)
		podDetector, err := pod.NewDetector(cli, eventTransport)
		if err != nil {
			return nil, fmt.Errorf("create pod detector: %w", err)
		}

		return []controllerComponent{
			{name: "recovery controller", runner: recoveryController},
			{name: "pod detector", runner: podDetector},
			{name: "event transport", runner: eventTransport},
			{name: "preflight report watcher", runner: reportWatcher},
		}, nil
	}}
}

func (a *controllerApp) Start(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.stopped {
		return fmt.Errorf("controller app is stopped")
	}
	if a.started != 0 {
		return nil
	}
	if a.components == nil {
		components, err := a.build()
		if err != nil {
			a.stopped = true
			return err
		}
		a.components = components
	}

	for i := range a.components {
		component := a.components[i]
		if err := component.runner.Start(ctx); err != nil {
			a.stopStartedLocked()
			a.stopped = true
			return fmt.Errorf("start %s: %w", component.name, err)
		}
		a.started++
	}
	return nil
}

func (a *controllerApp) Stop() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.stopped {
		return
	}
	a.stopped = true
	a.stopStartedLocked()
}

func (a *controllerApp) stopStartedLocked() {
	for a.started > 0 {
		a.started--
		a.components[a.started].runner.Stop()
	}
}
