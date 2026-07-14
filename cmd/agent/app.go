package main

import (
	"context"
	"fmt"

	config "github.com/baizeai/kcover/pkg/agentconfig"
	"github.com/baizeai/kcover/pkg/detector/node"
	"github.com/baizeai/kcover/pkg/events"
	"github.com/baizeai/kcover/pkg/preflight"
	"github.com/baizeai/kcover/pkg/runner"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// agentApp wires the collector, publisher, and node detector together and owns
// their ordered startup, rollback, and shutdown.
type agentApp struct {
	reportPublisher runner.Runner
	nodeDetector    runner.Runner
	reportCollector runner.Runner
}

func newAgentApp(k8sConfig *rest.Config, agentConfig config.Agent, nodeName string) (*agentApp, error) {
	kubeClient, err := kubernetes.NewForConfig(k8sConfig)
	if err != nil {
		return nil, fmt.Errorf("create Kubernetes client: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(k8sConfig)
	if err != nil {
		return nil, fmt.Errorf("create Kubernetes dynamic client: %w", err)
	}

	eventSink := events.NewKubeEventSink(kubeClient)
	reportSink := preflight.NewKubeReportSink(dynamicClient)
	reportPublisher, err := preflight.NewReportPublisher(reportSink, eventSink)
	if err != nil {
		return nil, fmt.Errorf("create preflight report publisher: %w", err)
	}

	nodeDetector, err := node.NewDetector(nodeName, agentConfig, kubeClient, eventSink)
	if err != nil {
		return nil, fmt.Errorf("create node detector: %w", err)
	}

	reportCollector, err := newReportCollector(kubeClient, eventSink, reportPublisher, nodeName)
	if err != nil {
		return nil, fmt.Errorf("create report collector: %w", err)
	}

	return &agentApp{
		reportPublisher: reportPublisher,
		nodeDetector:    nodeDetector,
		reportCollector: reportCollector,
	}, nil
}

func (a *agentApp) Start(ctx context.Context) error {
	if err := a.reportPublisher.Start(ctx); err != nil {
		return fmt.Errorf("start preflight report publisher: %w", err)
	}

	if err := a.nodeDetector.Start(ctx); err != nil {
		a.reportPublisher.Stop()
		return fmt.Errorf("start node detector: %w", err)
	}

	if err := a.reportCollector.Start(ctx); err != nil {
		a.nodeDetector.Stop()
		a.reportPublisher.Stop()
		return fmt.Errorf("start report collector: %w", err)
	}

	return nil
}

func (a *agentApp) Stop() {
	a.reportCollector.Stop()
	a.nodeDetector.Stop()
	a.reportPublisher.Stop()
}
