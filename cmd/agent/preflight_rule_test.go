package main

import (
	"path/filepath"
	"testing"

	"github.com/baizeai/kcover/pkg/constants"
	"github.com/baizeai/kcover/pkg/events"
	"github.com/baizeai/kcover/pkg/preflight"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

type agentStubSink struct{}

func (agentStubSink) RecordEvent(events.Event) error {
	return nil
}

func TestNewPreflightObserverReturnsObserver(t *testing.T) {
	t.Parallel()

	observer, err := newPreflightObserver(fake.NewSimpleClientset(), agentStubSink{}, "node-a")
	if err != nil {
		t.Fatalf("newPreflightObserver() error = %v", err)
	}
	if observer == nil {
		t.Fatal("newPreflightObserver() = nil, want observer")
	}
}

func TestShouldHandlePreflightPodUpdate(t *testing.T) {
	t.Parallel()

	oldPod := &corev1.Pod{
		Status: corev1.PodStatus{
			InitContainerStatuses: []corev1.ContainerStatus{{
				Name:  "preflight",
				State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
			}},
		},
	}

	newPod := oldPod.DeepCopy()
	newPod.ObjectMeta.Labels = map[string]string{constants.PreflightLabel: constants.True}
	newPod.Status.InitContainerStatuses = []corev1.ContainerStatus{{
		Name:  "preflight",
		State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}},
	}}

	if !shouldHandlePodUpdate(oldPod, newPod) {
		t.Fatal("shouldHandlePreflightPodUpdate(oldPod, newPod) = false, want true")
	}

	unchangedFailed := newPod.DeepCopy()
	if shouldHandlePodUpdate(newPod, unchangedFailed) {
		t.Fatal("shouldHandlePreflightPodUpdate(newPod, unchangedFailed) = true, want false")
	}
}

func TestIsPreflightCompletedTransition(t *testing.T) {
	t.Parallel()

	oldStatuses := []corev1.ContainerStatus{{
		Name:  "preflight",
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
	}}
	newStatuses := []corev1.ContainerStatus{{
		Name:  "preflight",
		State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}},
	}}

	if !isPreflightCompleted(oldStatuses, newStatuses) {
		t.Fatal("isPreflightCompletedTransition(oldStatuses, newStatuses) = false, want true")
	}
}

func TestIsPreflightCompletedTransitionRequiresExactPreflightName(t *testing.T) {
	t.Parallel()

	oldStatuses := []corev1.ContainerStatus{{
		Name:  "other-init",
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
	}}
	newStatuses := []corev1.ContainerStatus{{
		Name:  "other-init",
		State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}},
	}}

	if isPreflightCompleted(oldStatuses, newStatuses) {
		t.Fatal("isPreflightCompletedTransition(oldStatuses, newStatuses) = true, want false")
	}
}

func TestIsPreflightCompletedTransitionWithSucceededStatus(t *testing.T) {
	t.Parallel()

	oldStatuses := []corev1.ContainerStatus{{
		Name:  "preflight",
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
	}}
	newStatuses := []corev1.ContainerStatus{{
		Name:  "preflight",
		State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}},
	}}

	if !isPreflightCompleted(oldStatuses, newStatuses) {
		t.Fatal("isPreflightCompletedTransition(oldStatuses, newStatuses) = false, want true")
	}
}

func TestShouldHandlePreflightPodUpdateRequiresLabel(t *testing.T) {
	t.Parallel()

	oldPod := &corev1.Pod{}
	newPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{}},
		Status: corev1.PodStatus{InitContainerStatuses: []corev1.ContainerStatus{{
			Name:  "preflight",
			State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}},
		}}},
	}

	if shouldHandlePodUpdate(oldPod, newPod) {
		t.Fatal("shouldHandlePreflightPodUpdate(oldPod, newPod) = true, want false")
	}
}

func TestPreflightWorkloadNameUsesLeaderWorkerSetGroup(t *testing.T) {
	t.Parallel()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
			constants.LeaderWorkerSetNameLabel:       "lws-job",
			constants.LeaderWorkerSetGroupIndexLabel: "2",
			constants.BatchJobNameLabel:              "batch-job-node-0",
		}},
	}

	if got := preflightWorkloadName(pod); got != "lws-job-2" {
		t.Fatalf("preflightWorkloadName(pod) = %q, want lws-job-2", got)
	}
}

func TestPreflightWorkloadNameLWSRequiresGroupIndex(t *testing.T) {
	t.Parallel()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
			constants.LeaderWorkerSetNameLabel: "lws-job",
			constants.BatchJobNameLabel:        "batch-job-node-0",
		}},
	}

	if got := preflightWorkloadName(pod); got != "" {
		t.Fatalf("preflightWorkloadName(pod) = %q, want empty", got)
	}
}

func TestPreflightWorkloadNameFallsBackToBatchJobLabel(t *testing.T) {
	t.Parallel()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
			constants.BatchJobNameLabel: "batch-job-node-0",
		}},
	}

	if got := preflightWorkloadName(pod); got != "batch-job-node-0" {
		t.Fatalf("preflightWorkloadName(pod) = %q, want batch-job-node-0", got)
	}
}

func TestPreflightReportNameUsesWorkloadNameAndRank(t *testing.T) {
	t.Parallel()

	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "test2-worker-1",
		Labels: map[string]string{
			constants.BatchJobNameLabel: "test2",
		},
		Annotations: map[string]string{
			constants.BatchJobCompletionIndexAnnotation: "1",
		},
	}}

	reportName, ok := preflightReportName(pod, "test2")
	if !ok {
		t.Fatal("preflightReportName(pod, test2) = false, want true")
	}
	if reportName != "test2-1" {
		t.Fatalf("preflightReportName(pod, test2) = %q, want test2-1", reportName)
	}

	path := preflight.ReportPath("/var/lib/kcover/preflight", "baize-test", reportName)
	want := filepath.Join("/var/lib/kcover/preflight", "baize-test", "test2-1.json")
	if path != want {
		t.Fatalf("ReportPath(...) = %q, want %q", path, want)
	}
}

func TestPreflightReportNameJobSetUsesBatchCompletionIndex(t *testing.T) {
	t.Parallel()

	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "hydra-preflight-test-worker-ignored",
		Labels: map[string]string{
			constants.BatchJobNameLabel: "hydra-preflight-test-node-0",
		},
		Annotations: map[string]string{
			constants.BatchJobCompletionIndexAnnotation: "3",
		},
	}}

	workloadName := preflightWorkloadName(pod)
	if workloadName != "hydra-preflight-test-node-0" {
		t.Fatalf("preflightWorkloadName(pod) = %q, want hydra-preflight-test-node-0", workloadName)
	}

	reportName, ok := preflightReportName(pod, workloadName)
	if !ok {
		t.Fatal("preflightReportName(pod, workloadName) = false, want true")
	}
	if reportName != "hydra-preflight-test-node-0-3" {
		t.Fatalf("preflightReportName(pod, workloadName) = %q, want hydra-preflight-test-node-0-3", reportName)
	}
}

func TestPreflightReportNameRejectsUnmatchedPodName(t *testing.T) {
	t.Parallel()

	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "plain-pod-name",
		Labels: map[string]string{
			constants.BatchJobNameLabel: "test2",
		},
	}}

	if reportName, ok := preflightReportName(pod, "test2"); ok {
		t.Fatalf("preflightReportName(pod, test2) = (%q, true), want false", reportName)
	}
}

func TestPreflightReportNameRejectsInvalidBatchCompletionIndex(t *testing.T) {
	t.Parallel()

	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "test2-worker-abc",
		Labels: map[string]string{
			constants.BatchJobNameLabel: "test2",
		},
		Annotations: map[string]string{
			constants.BatchJobCompletionIndexAnnotation: "abc",
		},
	}}

	if reportName, ok := preflightReportName(pod, "test2"); ok {
		t.Fatalf("preflightReportName(pod, test2) = (%q, true), want false", reportName)
	}
}

func TestPreflightReportNameLWSUsesGroupAndWorkerIndexes(t *testing.T) {
	t.Parallel()

	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "hydra-preflight-test-0-1",
		Labels: map[string]string{
			constants.LeaderWorkerSetNameLabel:        "hydra-preflight-test",
			constants.LeaderWorkerSetGroupIndexLabel:  "0",
			constants.LeaderWorkerSetWorkerIndexLabel: "1",
		},
	}}

	reportName, ok := preflightReportName(pod, "hydra-preflight-test-0")
	if !ok {
		t.Fatal("preflightReportName(pod, hydra-preflight-test-0) = false, want true")
	}
	if reportName != "hydra-preflight-test-0-1" {
		t.Fatalf("preflightReportName(pod, hydra-preflight-test-0) = %q, want hydra-preflight-test-0-1", reportName)
	}

	path := preflight.ReportPath("/var/lib/kcover/preflight", "default", reportName)
	want := filepath.Join("/var/lib/kcover/preflight", "default", "hydra-preflight-test-0-1.json")
	if path != want {
		t.Fatalf("ReportPath(...) = %q, want %q", path, want)
	}
}

func TestPreflightReportNameLWSRejectsInvalidIndexes(t *testing.T) {
	t.Parallel()

	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "plain-pod-name",
		Labels: map[string]string{
			constants.LeaderWorkerSetNameLabel:        "hydra-preflight-test",
			constants.LeaderWorkerSetGroupIndexLabel:  "group-a",
			constants.LeaderWorkerSetWorkerIndexLabel: "worker-b",
		},
	}}

	if reportName, ok := preflightReportName(pod, "hydra-preflight-test-0"); ok {
		t.Fatalf("preflightReportName(pod, hydra-preflight-test-0) = (%q, true), want false", reportName)
	}
}

func TestPreflightReportNameLWSRequiresIndexes(t *testing.T) {
	t.Parallel()

	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "hydra-preflight-test-0-0",
		Labels: map[string]string{
			constants.LeaderWorkerSetNameLabel: "hydra-preflight-test",
		},
	}}

	if reportName, ok := preflightReportName(pod, "hydra-preflight-test-0"); ok {
		t.Fatalf("preflightReportName(pod, hydra-preflight-test-0) = (%q, true), want false", reportName)
	}
}
