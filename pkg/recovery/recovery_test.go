package recovery

import (
	"context"
	"strconv"
	"strings"
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

func TestPreflightEventMarksSlowNodesAtDefaultThreshold(t *testing.T) {
	t.Parallel()

	client := fake.NewSimpleClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-b"}},
	)

	controller := NewController(client, nil, nil, 0, 0)
	controller.onPreflightReport(context.Background(), preflightReport("default", "node-a", "job-a", reportText("job-a", 2, 0, "node-a")))
	controller.onPreflightReport(context.Background(), preflightReport("default", "node-b", "job-a", reportText("job-a", 2, 1, "node-b")))

	assertNodeUnschedulable(t, client, "node-a", true)
	assertNodeUnschedulable(t, client, "node-b", true)
}

func TestPreflightEventUsesDefaultThresholdWithoutOverride(t *testing.T) {
	t.Parallel()

	client := fake.NewSimpleClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-b"}},
	)

	controller := NewController(client, nil, nil, 0, 0)
	controller.onPreflightReport(context.Background(), preflightReport("default", "node-a", "job-a", reportText("job-a", 2, 0, "node-a")))
	controller.onPreflightReport(context.Background(), preflightReport("default", "node-b", "job-a", reportText("job-a", 2, 1, "node-b")))

	assertNodeUnschedulable(t, client, "node-a", true)
	assertNodeUnschedulable(t, client, "node-b", true)
}

func TestSweepExpiredPreflightReportsDropsIncompleteWorkload(t *testing.T) {
	t.Parallel()

	client := fake.NewSimpleClientset(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}})
	controller := NewController(client, nil, nil, 0, 0)
	now := time.Unix(100, 0)
	controller.preflight.aggregator = preflight.NewSlowNodeAggregator(10 * time.Second)
	controller.preflight.aggregator.SetNowForTest(func() time.Time { return now })

	controller.onPreflightReport(context.Background(), preflightReport("default", "node-a", "job-a", reportText("job-a", 2, 0, "node-a")))
	if len(controller.preflight.aggregator.ExpireTimedOutWorkloads()) != 0 {
		t.Fatal("ExpireTimedOutWorkloads() returned errors before timeout, want none")
	}

	now = now.Add(11 * time.Second)
	errs := controller.preflight.aggregator.ExpireTimedOutWorkloads()
	if len(errs) != 1 {
		t.Fatalf("len(ExpireTimedOutWorkloads()) = %d, want 1", len(errs))
	}
	if errs[0].FirstReportedNode() != "node-a" {
		t.Fatalf("errs[0].FirstReportedNodeName() = %q, want node-a", errs[0].FirstReportedNode())
	}
	if !strings.Contains(errs[0].Error(), "got 1/2 reports") {
		t.Fatalf("ExpireTimedOutWorkloads error = %q, want report count detail", errs[0])
	}

	controller.onPreflightReport(context.Background(), preflightReport("default", "node-b", "job-a", reportText("job-a", 2, 1, "node-b")))
	assertNodeUnschedulable(t, client, "node-a", false)
}

func TestDuplicatePreflightEventDoesNotRefreshTimeoutWindow(t *testing.T) {
	t.Parallel()

	client := fake.NewSimpleClientset(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}})
	controller := NewController(client, nil, nil, 0, 0)
	now := time.Unix(100, 0)
	controller.preflight.aggregator = preflight.NewSlowNodeAggregator(10 * time.Second)
	controller.preflight.aggregator.SetNowForTest(func() time.Time { return now })

	report := preflightReport("default", "node-a", "job-a", reportText("job-a", 2, 0, "node-a"))
	controller.onPreflightReport(context.Background(), report)

	now = now.Add(9 * time.Second)
	controller.onPreflightReport(context.Background(), report)

	now = now.Add(2 * time.Second)
	errs := controller.preflight.aggregator.ExpireTimedOutWorkloads()
	if len(errs) != 1 {
		t.Fatalf("len(ExpireTimedOutWorkloads()) = %d, want 1", len(errs))
	}
	if errs[0].ReceivedReports != 1 {
		t.Fatalf("errs[0].ReceivedReports = %d, want 1", errs[0].ReceivedReports)
	}
}

func TestSameNameDifferentWorkloadUIDsDoNotAggregate(t *testing.T) {
	t.Parallel()

	client := fake.NewSimpleClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-b"}},
	)
	controller := NewController(client, nil, nil, time.Minute, 0)
	first := preflightReport("default", "node-a", "job-a", reportText("job-a", 2, 0, "node-a"))
	second := preflightReport("default", "node-b", "job-a", reportText("job-a", 2, 1, "node-b"))
	first.Spec.WorkloadUID = "old-job-uid"
	second.Spec.WorkloadUID = "new-job-uid"

	controller.onPreflightReport(context.Background(), first)
	controller.onPreflightReport(context.Background(), second)

	assertNodeUnschedulable(t, client, "node-a", false)
	assertNodeUnschedulable(t, client, "node-b", false)
}

func TestProcessedPreflightEntriesExpireDuringSweep(t *testing.T) {
	t.Parallel()

	controller := NewController(fake.NewSimpleClientset(), nil, nil, 10*time.Millisecond, 0)

	report := preflightReport("train-ns", "node-a", "job-a", reportText("job-a", 2, 0, "node-a"))
	duplicate := controller.preflight.markProcessed(report)
	if duplicate {
		t.Fatal("first markProcessed(...) = true, want false")
	}
	if controller.preflight.processed.Len() != 1 {
		t.Fatalf("processed.Len() = %d, want 1", controller.preflight.processed.Len())
	}

	time.Sleep(20 * time.Millisecond)
	controller.sweepExpiredPreflightReports()
	if controller.preflight.processed.Len() != 0 {
		t.Fatalf("processed.Len() = %d, want 0 after sweep", controller.preflight.processed.Len())
	}
	if len(controller.preflight.workloads) != 0 {
		t.Fatalf("len(workloads) = %d, want 0 after sweep", len(controller.preflight.workloads))
	}
	duplicate = controller.preflight.markProcessed(report)
	if duplicate {
		t.Fatal("markProcessed(...) after expiry = true, want false")
	}
}

func TestInvalidPreflightReportDoesNotRemainProcessed(t *testing.T) {
	t.Parallel()

	controller := NewController(fake.NewSimpleClientset(), nil, nil, time.Minute, 0)
	report := preflightReport("train-ns", "node-a", "job-a", reportText("job-a", 2, 0, "node-a"))
	report.Spec.Report = "not-json"

	if _, err := controller.preflight.handleReport(report); err == nil {
		t.Fatal("handleReport(invalid payload) error = nil, want non-nil")
	}
	if controller.preflight.processed.Len() != 0 {
		t.Fatalf("processed.Len() = %d, want 0 after invalid report", controller.preflight.processed.Len())
	}
	if len(controller.preflight.workloads) != 0 {
		t.Fatalf("len(workloads) = %d, want 0 after invalid report", len(controller.preflight.workloads))
	}
}

func TestProcessedPreflightEntriesAreDroppedWhenWorkloadCompletes(t *testing.T) {
	t.Parallel()

	client := fake.NewSimpleClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-b"}},
	)
	controller := NewController(client, nil, nil, time.Minute, 0)

	controller.onPreflightReport(context.Background(), preflightReport("default", "node-a", "job-a", reportText("job-a", 2, 0, "node-a")))
	if controller.preflight.processed.Len() != 1 {
		t.Fatalf("processed.Len() after first report = %d, want 1", controller.preflight.processed.Len())
	}

	controller.onPreflightReport(context.Background(), preflightReport("default", "node-b", "job-a", reportText("job-a", 2, 1, "node-b")))
	if controller.preflight.processed.Len() != 0 {
		t.Fatalf("processed.Len() after workload completion = %d, want 0", controller.preflight.processed.Len())
	}
}

func TestDay2NodeEventSkipsJobRecovery(t *testing.T) {
	t.Parallel()

	client := fake.NewSimpleClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      "pod-a",
				Labels: map[string]string{
					constants.KubeflowJobLabel:     "job-a",
					constants.EnabledRecoveryLabel: constants.True,
				},
			},
			Spec: corev1.PodSpec{NodeName: "node-a"},
		},
	)
	controller := NewController(client, nil, nil, 0, 0)

	controller.onEvent(context.Background(), events.Event{
		ResourceType: events.Node,
		Name:         "node-a",
		Reason:       events.Day2EventReason,
		EventType:    events.Error,
	})

	if _, err := client.CoreV1().Pods("default").Get(context.Background(), "pod-a", metav1.GetOptions{}); err != nil {
		t.Fatalf("Get(pod-a) error = %v, want pod to remain because day2 should skip job recovery", err)
	}
	assertNodeUnschedulable(t, client, "node-a", true)
}

func TestStopReturnsWithoutEventStreamClose(t *testing.T) {
	t.Parallel()

	stream := blockingEventStream{ch: make(chan events.Event)}
	controller := NewController(fake.NewSimpleClientset(), stream, nil, 0, time.Hour)
	if err := controller.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		controller.Stop()
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop() did not return before event stream closed")
	}
}

func TestControllerConsumesPreflightReportStream(t *testing.T) {
	t.Parallel()

	client := fake.NewSimpleClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-b"}},
	)
	eventStream := blockingEventStream{ch: make(chan events.Event)}
	reports := make(chan *kcoverv1alpha1.PreflightReport, 2)
	reports <- preflightReport("default", "node-a", "job-a", reportText("job-a", 2, 0, "node-a"))
	reports <- preflightReport("default", "node-b", "job-a", reportText("job-a", 2, 1, "node-b"))
	controller := NewController(client, eventStream, blockingReportStream{ch: reports}, 0, time.Hour)
	if err := controller.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer controller.Stop()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		node, err := client.CoreV1().Nodes().Get(context.Background(), "node-a", metav1.GetOptions{})
		if err == nil && node.Spec.Unschedulable {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("controller did not consume PreflightReport stream")
}

type blockingEventStream struct {
	ch <-chan events.Event
}

type blockingReportStream struct {
	ch <-chan *kcoverv1alpha1.PreflightReport
}

func (s blockingReportStream) Reports() <-chan *kcoverv1alpha1.PreflightReport {
	return s.ch
}

func (s blockingEventStream) EventChan() <-chan events.Event {
	return s.ch
}

func preflightReport(namespace, nodeName, workloadName, report string) *kcoverv1alpha1.PreflightReport {
	built, err := preflight.BuildPreflightReport(namespace, nodeName, workloadName, "workload-uid", report, time.Unix(100, 0))
	if err != nil {
		panic(err)
	}
	return built
}

func reportText(workloadName string, worldSize, rank int, nodeName string) string {
	selfIP := "10.0.0.1"
	if rank != 0 {
		selfIP = "10.0.0.2"
	}

	return `{"version":1,"workload":"` + workloadName + `","workload_size":` + strconv.Itoa(worldSize) + `,"rank":` + strconv.Itoa(rank) + `,"node_name":"` + nodeName + `","gpu_check":2,"storage_check":2,"batches":[{"batch_idx":0,"pair":["10.0.0.1","10.0.0.2"],"self_ip":"` + selfIP + `","status":"fail"}]}`
}

func assertNodeUnschedulable(t *testing.T, client *fake.Clientset, nodeName string, want bool) {
	t.Helper()

	node, err := client.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get(node=%s) error = %v", nodeName, err)
	}
	if node.Spec.Unschedulable != want {
		t.Fatalf("node %s unschedulable = %v, want %v", nodeName, node.Spec.Unschedulable, want)
	}
}
