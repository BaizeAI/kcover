package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	kcoverv1alpha1 "github.com/baizeai/kcover/pkg/apis/kcover/v1alpha1"
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

type agentStubReportSubmitter struct{}

func (agentStubReportSubmitter) Submit(*kcoverv1alpha1.PreflightReport) error {
	return nil
}

type recordingReportSubmitter struct {
	reports []*kcoverv1alpha1.PreflightReport
	names   map[string]struct{}
}

func (s *recordingReportSubmitter) Submit(report *kcoverv1alpha1.PreflightReport) error {
	if s.names == nil {
		s.names = make(map[string]struct{})
	}
	key := report.Namespace + "/" + report.Name
	if _, exists := s.names[key]; exists {
		return nil
	}
	s.names[key] = struct{}{}
	s.reports = append(s.reports, report)
	return nil
}

func TestNewReportCollectorReturnsRunner(t *testing.T) {
	t.Parallel()

	collector, err := newReportCollector(fake.NewSimpleClientset(), agentStubSink{}, agentStubReportSubmitter{}, "node-a")
	if err != nil {
		t.Fatalf("newReportCollector() error = %v", err)
	}
	if collector == nil {
		t.Fatal("newReportCollector() = nil, want runner")
	}
}

func TestPreflightRuleSubmitsReportIdempotently(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(baseDir, "train-ns"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	payload := `{"version":1,"workload":"job-a","workload_size":2,"rank":0,"node_name":"node-a","node_ip":"10.0.0.1","gpu_check":1,"storage_check":1,"batches":[]}`
	if err := os.WriteFile(preflight.ReportPath(baseDir, "train-ns", "job-a-0"), []byte(payload), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	oldPod := &corev1.Pod{Status: corev1.PodStatus{InitContainerStatuses: []corev1.ContainerStatus{{
		Name:  preflightInitContainerName,
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
	}}}}
	newPod := oldPod.DeepCopy()
	controller := true
	newPod.ObjectMeta = metav1.ObjectMeta{
		Name:      "job-a-worker-0",
		Namespace: "train-ns",
		UID:       "pod-uid",
		Labels: map[string]string{
			constants.PreflightLabel:    constants.True,
			constants.BatchJobNameLabel: "job-a",
		},
		Annotations: map[string]string{constants.BatchJobCompletionIndexAnnotation: "0"},
		OwnerReferences: []metav1.OwnerReference{{
			APIVersion: "batch/v1",
			Kind:       "Job",
			Name:       "job-a",
			UID:        "job-uid",
			Controller: &controller,
		}},
	}
	newPod.Spec.NodeName = "node-a"
	newPod.Status.InitContainerStatuses = []corev1.ContainerStatus{{
		Name:  preflightInitContainerName,
		State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0, FinishedAt: metav1.NewTime(time.Unix(100, 0))}},
	}}

	submitter := &recordingReportSubmitter{}
	rule := preflightRule{baseDir: baseDir, reports: submitter}
	observations := rule.OnUpdate(oldPod, newPod)
	if len(submitter.reports) != 1 {
		t.Fatalf("submitted reports = %d, want 1", len(submitter.reports))
	}
	if len(observations) != 0 {
		t.Fatalf("observation events returned by rule = %d, want 0", len(observations))
	}
	if len(submitter.reports[0].OwnerReferences) != 1 || submitter.reports[0].OwnerReferences[0].UID != newPod.UID {
		t.Fatalf("report ownerReferences = %v, want source Pod", submitter.reports[0].OwnerReferences)
	}

	observations = rule.OnAdd(newPod)
	if len(observations) != 0 || len(submitter.reports) != 1 {
		t.Fatalf("initial-list reconciliation = %d observations, %d reports; want 0 observations and one idempotent submit", len(observations), len(submitter.reports))
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

	if !shouldReconcilePreflightPod(newPod) {
		t.Fatal("shouldHandlePreflightPodUpdate(oldPod, newPod) = false, want true")
	}

	unchangedFailed := newPod.DeepCopy()
	if !shouldReconcilePreflightPod(unchangedFailed) {
		t.Fatal("shouldHandlePreflightPodUpdate(newPod, unchangedFailed) = false, want true for idempotent reconciliation")
	}
}

func TestShouldHandlePreflightPodUpdateRequiresLabel(t *testing.T) {
	t.Parallel()

	newPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{}},
		Status: corev1.PodStatus{InitContainerStatuses: []corev1.ContainerStatus{{
			Name:  "preflight",
			State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}},
		}}},
	}

	if shouldReconcilePreflightPod(newPod) {
		t.Fatal("shouldHandlePreflightPodUpdate(oldPod, newPod) = true, want false")
	}
}

func TestPreflightRuleOnAddRequiresLabel(t *testing.T) {
	t.Parallel()

	submitter := &recordingReportSubmitter{}
	rule := preflightRule{reports: submitter}
	pod := &corev1.Pod{
		Status: corev1.PodStatus{InitContainerStatuses: []corev1.ContainerStatus{{
			Name:  preflightInitContainerName,
			State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{FinishedAt: metav1.NewTime(time.Unix(100, 0))}},
		}}},
	}

	rule.OnAdd(pod)
	if len(submitter.reports) != 0 {
		t.Fatalf("submitted reports for unlabeled initial Pod = %d, want 0", len(submitter.reports))
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
