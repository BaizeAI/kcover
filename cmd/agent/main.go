package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/baizeai/kcover/cmd/agent/config"
	"github.com/baizeai/kcover/pkg/detector/node"
	"github.com/baizeai/kcover/pkg/events"
	"github.com/baizeai/kcover/pkg/kube"

	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

func init() {
	klog.InitFlags(flag.CommandLine)
}

var pConfigPath = flag.String("config", config.DefaultPath, "path to the agent config file")

func main() {
	if err := run(); err != nil {
		klog.ErrorS(err, "agent exited with error")
		os.Exit(1)
	}
}

func run() error {
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load(*pConfigPath)
	if err != nil {
		return fmt.Errorf("load agent config: %w", err)
	}
	klog.V(2).InfoS("load agent config", "config", cfg.String())

	hostName, err := hostName()
	if err != nil {
		return fmt.Errorf("resolve node name: %w", err)
	}

	k8sConfig := kube.GetK8sConfigConfigWithFile("", "")
	if k8sConfig == nil {
		return fmt.Errorf("load Kubernetes config: returned nil config")
	}

	client, err := kubernetes.NewForConfig(k8sConfig)
	if err != nil {
		return fmt.Errorf("create kubernetes client: %w", err)
	}

	sink := events.NewKubeEventSink(client)

	detector, err := node.NewDetector(hostName, node.Vendor(cfg.Vendor), cfg.Interval, cfg.MetaX, client, sink)
	if err != nil {
		return fmt.Errorf("create node detector: %w", err)
	}
	defer detector.Stop()

	if err := detector.Start(); err != nil {
		return fmt.Errorf("start node detector: %w", err)
	}

	observer, err := newPreflightObserver(client, sink, hostName, node.Vendor(cfg.Vendor))
	if err != nil {
		return fmt.Errorf("create preflight pod observer: %w", err)
	}
	defer observer.Stop()

	if err := observer.Start(); err != nil {
		return fmt.Errorf("start preflight pod observer: %w", err)
	}

	klog.InfoS("agent started")
	<-ctx.Done()

	klog.InfoS("agent stopped")
	return nil
}

func hostName() (string, error) {
	if hn := kube.NodeNameFromEnv(); hn != "" {
		return hn, nil
	}
	hn, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("get hostname: %w", err)
	}
	return hn, nil
}
