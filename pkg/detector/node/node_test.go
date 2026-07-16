package node

import (
	"context"
	"testing"

	kcoverconfig "github.com/baizeai/kcover/pkg/agentconfig"
	"github.com/baizeai/kcover/pkg/events"
	"k8s.io/client-go/kubernetes/fake"
)

type stubSink struct{}

func (stubSink) RecordEvent(events.Event) error {
	return nil
}

func TestNewDetectorRejectsNilSink(t *testing.T) {
	t.Parallel()

	if _, err := NewDetector("node-a", kcoverconfig.DefaultAgent(), nil, nil); err == nil {
		t.Fatal("NewDetector error = nil, want non-nil for nil sink")
	}
}

func TestNewDetectorReturnsRunner(t *testing.T) {
	t.Parallel()

	detector, err := NewDetector("node-a", kcoverconfig.DefaultAgent(), fake.NewSimpleClientset(), stubSink{})
	if err != nil {
		t.Fatalf("NewDetector returned error: %v", err)
	}
	if detector == nil {
		t.Fatal("NewDetector result = nil, want non-nil")
	}
}

func TestNewDetectorStartsDetector(t *testing.T) {
	t.Parallel()

	detector, err := NewDetector("node-a", kcoverconfig.DefaultAgent(), fake.NewSimpleClientset(), stubSink{})
	if err != nil {
		t.Fatalf("NewDetector returned error: %v", err)
	}

	if err := detector.Start(context.Background()); err != nil {
		t.Fatalf("detector.Start() error = %v", err)
	}
	detector.Stop()
}
