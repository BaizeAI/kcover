package pod

import (
	"testing"

	"github.com/baizeai/kcover/pkg/constants"
	"github.com/baizeai/kcover/pkg/events"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestContainerErrorRuleHandlesInitialList(t *testing.T) {
	t.Parallel()

	if !((containerErrorRule{}).ShouldHandleInitialList()) {
		t.Fatal("containerErrorRule.ShouldHandleInitialList() = false, want true")
	}
}

func TestShouldCheckPodUpdate(t *testing.T) {
	t.Parallel()

	oldPod := &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses:     []corev1.ContainerStatus{{Name: "main"}},
			InitContainerStatuses: []corev1.ContainerStatus{{Name: "preflight"}},
		},
	}

	if !shouldCheckPodUpdate(nil, oldPod) {
		t.Fatal("shouldCheckPodUpdate(nil, newPod) = false, want true")
	}

	newPod := oldPod.DeepCopy()
	if shouldCheckPodUpdate(oldPod, newPod) {
		t.Fatal("shouldCheckPodUpdate(oldPod, newPod) = true, want false for unchanged statuses")
	}

	updatedPod := oldPod.DeepCopy()
	updatedPod.Status.InitContainerStatuses = []corev1.ContainerStatus{{
		Name:  "preflight",
		State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Message: `{"version":1,"node_name":"node-a","gpu_check":1,"storage_check":1}`}},
	}}
	if !shouldCheckPodUpdate(oldPod, updatedPod) {
		t.Fatal("shouldCheckPodUpdate(oldPod, updatedPod) = false, want true for changed init statuses")
	}
}

func TestShouldHandlePod(t *testing.T) {
	t.Parallel()

	if shouldHandlePod(nil) {
		t.Fatal("shouldHandlePod(nil) = true, want false")
	}

	recoveryPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{constants.EnabledRecoveryLabel: constants.True}}}
	if !shouldHandlePod(recoveryPod) {
		t.Fatal("shouldHandlePod(recoveryPod) = false, want true")
	}

	plainPod := &corev1.Pod{}
	if shouldHandlePod(plainPod) {
		t.Fatal("shouldHandlePod(plainPod) = true, want false")
	}
}

func TestPodEvents(t *testing.T) {
	t.Parallel()

	plainPod := &corev1.Pod{}
	if events := podEvents(plainPod); len(events) != 0 {
		t.Fatalf("len(podEvents(plainPod)) = %d, want 0", len(events))
	}

	recoveryPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "worker-0",
			Labels:    map[string]string{constants.EnabledRecoveryLabel: constants.True},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: "main",
				State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
					Reason:   "Error",
					Message:  "boom",
					ExitCode: 1,
				}},
			}},
		},
	}

	collected := podEvents(recoveryPod)
	if len(collected) != 1 {
		t.Fatalf("len(podEvents(recoveryPod)) = %d, want 1", len(collected))
	}
}

func assertEvent(t *testing.T, event events.Event, targetType events.ResourceType, namespace, name string) {
	t.Helper()

	if event.ResourceType != targetType {
		t.Fatalf("event.TargetType = %s, want %s", event.ResourceType, targetType)
	}
	if event.Namespace != namespace {
		t.Fatalf("event.Namespace = %q, want %q", event.Namespace, namespace)
	}
	if event.Name != name {
		t.Fatalf("event.Name = %q, want %q", event.Name, name)
	}
	if event.EventType != events.Error {
		t.Fatalf("event.EventType = %d, want %d", event.EventType, events.Error)
	}
}
