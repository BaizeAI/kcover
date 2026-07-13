package podobserver

import (
	"testing"

	"github.com/baizeai/kcover/pkg/events"

	corev1 "k8s.io/api/core/v1"
)

type addCountingRule struct {
	adds int
}

func (r *addCountingRule) OnAdd(*corev1.Pod) []events.Event {
	r.adds++
	return nil
}

func (*addCountingRule) OnUpdate(*corev1.Pod, *corev1.Pod) []events.Event {
	return nil
}

type initialListCountingRule struct {
	addCountingRule
}

func (*initialListCountingRule) HandleInitialList() bool {
	return true
}

func TestHandleAddSkipsInitialListPod(t *testing.T) {
	t.Parallel()

	rule := &addCountingRule{}
	observer := &observer{rules: []PodRule{rule}}
	pod := &corev1.Pod{}

	observer.handleAdd(pod, true)
	if rule.adds != 0 {
		t.Fatalf("rule OnAdd calls after initial List = %d, want 0", rule.adds)
	}

	observer.handleAdd(pod, false)
	if rule.adds != 1 {
		t.Fatalf("rule OnAdd calls after live add = %d, want 1", rule.adds)
	}
}

func TestHandleAddAllowsOptedInInitialListRule(t *testing.T) {
	t.Parallel()

	rule := &initialListCountingRule{}
	observer := &observer{rules: []PodRule{rule}}
	observer.handleAdd(&corev1.Pod{}, true)

	if rule.adds != 1 {
		t.Fatalf("opted-in rule OnAdd calls after initial List = %d, want 1", rule.adds)
	}
}
