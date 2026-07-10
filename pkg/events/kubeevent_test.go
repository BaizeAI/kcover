package events

import (
	"context"
	"testing"
	"time"

	"github.com/baizeai/kcover/pkg/constants"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestRecordEventStoresPreflightPayloadInEventAnnotation(t *testing.T) {
	t.Parallel()

	client := fake.NewSimpleClientset(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}})
	sink := NewKubeEventSink(client).(*kubeEventSink)
	payload := `{"workload_size":2,"rank":0,"node_name":"node-a","gpu_check":1,"storage_check":1,"batches":[{"batch_idx":0,"pair":["10.0.0.1","10.0.0.2"],"self_ip":"10.0.0.1","status":"fail"}]}`

	err := sink.RecordEvent(Event{
		ResourceType: Node,
		Namespace:    "default",
		Name:         "node-a",
		EventType:    Error,
		Message:      payload,
		Annotations: map[string]string{
			constants.PreflightWorkloadAnnotation: "job-a",
		},
	})
	if err != nil {
		t.Fatalf("RecordEvent(...) error = %v", err)
	}

	events, err := client.CoreV1().Events("default").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("List(events) error = %v", err)
	}
	if len(events.Items) != 1 {
		t.Fatalf("len(events.Items) = %d, want 1", len(events.Items))
	}
	stored := events.Items[0]
	if stored.Type != corev1.EventTypeNormal {
		t.Fatalf("event type = %q, want %q", stored.Type, corev1.EventTypeNormal)
	}
	if stored.Reason != preflightEventReason {
		t.Fatalf("event reason = %q, want %q", stored.Reason, preflightEventReason)
	}
	if stored.Message != "preflight report available for workload(job-a) on node(node-a)" {
		t.Fatalf("event message = %q, want %q", stored.Message, "preflight report available for workload(job-a) on node(node-a)")
	}
	if stored.Annotations[constants.PreflightPayloadAnnotation] != payload {
		t.Fatalf("event preflight payload annotation = %q, want %q", stored.Annotations[constants.PreflightPayloadAnnotation], payload)
	}
	if stored.Annotations[constants.PreflightNamespaceAnnotation] != "default" {
		t.Fatalf("event preflight namespace annotation = %q, want %q", stored.Annotations[constants.PreflightNamespaceAnnotation], "default")
	}
	if stored.InvolvedObject.Namespace != stored.Namespace {
		t.Fatalf("involved object namespace = %q, want %q", stored.InvolvedObject.Namespace, stored.Namespace)
	}
}

func TestToInternalEventHydratesPreflightPayloadFromEventAnnotation(t *testing.T) {
	t.Parallel()

	payload := `{"workload_size":2,"rank":0,"node_name":"node-a","gpu_check":1,"storage_check":1,"batches":[{"batch_idx":0,"pair":["10.0.0.1","10.0.0.2"],"self_ip":"10.0.0.1","status":"fail"}]}`
	client := fake.NewSimpleClientset(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}})
	bridge := NewKubeEventBridge(client).(*kubeEventBridge)

	event, ok := bridge.toInternalEvent(&corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "preflight-event",
			Namespace: "default",
			Annotations: map[string]string{
				constants.PreflightNamespaceAnnotation: "train-ns",
				constants.PreflightPayloadAnnotation:   payload,
				constants.PreflightWorkloadAnnotation:  "job-a",
			},
		},
		Message: "preflight report available for workload job-a on node node-a",
		InvolvedObject: corev1.ObjectReference{
			APIVersion: "v1",
			Kind:       "Node",
			Name:       "node-a",
			FieldPath:  "",
		},
	})
	if !ok {
		t.Fatal("toInternalEvent(...) ok = false, want true")
	}
	if event.Namespace != "train-ns" {
		t.Fatalf("event.Namespace = %q, want %q", event.Namespace, "train-ns")
	}
	if event.Message != payload {
		t.Fatalf("event.Message = %q, want %q", event.Message, payload)
	}
	if event.Annotations[constants.PreflightWorkloadAnnotation] != "job-a" {
		t.Fatalf("job annotation = %q, want %q", event.Annotations[constants.PreflightWorkloadAnnotation], "job-a")
	}
}

func TestReasonForEventUsesDay2ReasonForNodeEvent(t *testing.T) {
	t.Parallel()

	reason := reasonForEvent(Event{
		ResourceType: Node,
		Name:         "node-a",
		Reason:       Day2EventReason,
		EventType:    Error,
		Message:      "day2 check failed",
	})
	if reason != Day2EventReason {
		t.Fatalf("reasonForEvent(...) = %q, want %q", reason, Day2EventReason)
	}
}

func TestShouldWatchEventAllowsPreflightNodeEvent(t *testing.T) {
	t.Parallel()

	bridge := NewKubeEventBridge(fake.NewSimpleClientset()).(*kubeEventBridge)

	if !bridge.shouldWatchEvent(&corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: metav1.Now(),
			Annotations: map[string]string{
				constants.PreflightWorkloadAnnotation: "job-a",
			},
		},
		InvolvedObject: corev1.ObjectReference{APIVersion: "v1", Kind: "Node", Name: "node-a"},
	}) {
		t.Fatal("shouldWatchEvent(preflight node event) = false, want true")
	}
}

func TestShouldWatchEventAllowsDay2NodeEvent(t *testing.T) {
	t.Parallel()

	bridge := NewKubeEventBridge(fake.NewSimpleClientset()).(*kubeEventBridge)

	if !bridge.shouldWatchEvent(&corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: metav1.Now(),
			Annotations: map[string]string{
				constants.NeedRecoveryAnnotation: constants.True,
			},
		},
		Reason:         Day2EventReason,
		InvolvedObject: corev1.ObjectReference{APIVersion: "v1", Kind: "Node", Name: "node-a"},
	}) {
		t.Fatal("shouldWatchEvent(day2 node event) = false, want true")
	}
}

func TestToInternalEventPreservesDay2Reason(t *testing.T) {
	t.Parallel()

	bridge := NewKubeEventBridge(fake.NewSimpleClientset()).(*kubeEventBridge)

	event, ok := bridge.toInternalEvent(&corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: metav1.Now(),
			Namespace:         "default",
			Annotations: map[string]string{
				constants.NeedRecoveryAnnotation: constants.True,
			},
		},
		Reason:         Day2EventReason,
		Message:        "insufficient available GPUs: expected 9, found 8",
		InvolvedObject: corev1.ObjectReference{APIVersion: "v1", Kind: "Node", Name: "node-a"},
	})
	if !ok {
		t.Fatal("toInternalEvent(...) ok = false, want true")
	}
	if event.Reason != Day2EventReason {
		t.Fatalf("event.Reason = %q, want %q", event.Reason, Day2EventReason)
	}
}

func TestStartStopClosesEventChannel(t *testing.T) {
	t.Parallel()

	bridge := NewKubeEventBridge(fake.NewSimpleClientset()).(*kubeEventBridge)
	if err := bridge.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		bridge.Stop()
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop() did not finish")
	}

	select {
	case _, ok := <-bridge.EventChan():
		if ok {
			t.Fatal("event channel still open after Stop()")
		}
	case <-time.After(time.Second):
		t.Fatal("event channel was not closed after Stop()")
	}
}

func TestHandleK8sEventUpdateForwardsFoldedDay2Event(t *testing.T) {
	t.Parallel()

	bridge := NewKubeEventBridge(fake.NewSimpleClientset()).(*kubeEventBridge)
	startQueueWorkerForTest(t, bridge)
	defer bridge.Stop()
	now := metav1.NewTime(time.Now())
	oldEvent := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: now,
			Namespace:         "default",
			Annotations: map[string]string{
				constants.NeedRecoveryAnnotation: constants.True,
			},
		},
		Reason:         Day2EventReason,
		Message:        "insufficient available GPUs: expected 9, found 8",
		Count:          1,
		LastTimestamp:  now,
		InvolvedObject: corev1.ObjectReference{APIVersion: "v1", Kind: "Node", Name: "node-a"},
	}
	newEvent := oldEvent.DeepCopy()
	newEvent.Count = 2
	newEvent.LastTimestamp = metav1.NewTime(now.Add(time.Minute))

	bridge.handleK8sEventUpdate(context.Background(), oldEvent, newEvent)

	select {
	case event := <-bridge.EventChan():
		if event.ResourceType != Node || event.Name != "node-a" || event.Reason != Day2EventReason {
			t.Fatalf("forwarded event = %+v, want day2 node event", event)
		}
	case <-time.After(time.Second):
		t.Fatal("handleK8sEventUpdate(...) forwarded no event, want one")
	}
}

func TestHandleK8sEventAddForwardsDay2EventDirectlyWhenEventChannelHasCapacity(t *testing.T) {
	t.Parallel()

	bridge := NewKubeEventBridge(fake.NewSimpleClientset()).(*kubeEventBridge)
	startQueueWorkerForTest(t, bridge)
	defer bridge.Stop()

	bridge.handleK8sEventAdd(context.Background(), &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: metav1.Now(),
			Namespace:         "default",
			Annotations: map[string]string{
				constants.NeedRecoveryAnnotation: constants.True,
			},
		},
		Reason:         Day2EventReason,
		Message:        "insufficient available GPUs: expected 9, found 8",
		InvolvedObject: corev1.ObjectReference{APIVersion: "v1", Kind: "Node", Name: "node-a"},
	}, false)

	select {
	case event := <-bridge.EventChan():
		if event.ResourceType != Node || event.Name != "node-a" || event.Reason != Day2EventReason {
			t.Fatalf("forwarded event = %+v, want day2 node event", event)
		}
	case <-time.After(time.Second):
		t.Fatal("handleK8sEventAdd(day2) forwarded no event, want one")
	}
}

func TestHandleK8sEventAddSkipsInitialListEvent(t *testing.T) {
	t.Parallel()

	bridge := NewKubeEventBridge(fake.NewSimpleClientset()).(*kubeEventBridge)
	bridge.handleK8sEventAdd(context.Background(), &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: metav1.Now(),
			Namespace:         "default",
			Annotations: map[string]string{
				constants.NeedRecoveryAnnotation: constants.True,
			},
		},
		Message:        "old pod failure",
		InvolvedObject: corev1.ObjectReference{APIVersion: "v1", Kind: "Pod", Namespace: "default", Name: "pod-a"},
	}, true)

	select {
	case event := <-bridge.EventChan():
		t.Fatalf("initial List event was forwarded: %+v", event)
	default:
	}
}

func TestHandleK8sEventAddKeepsInitialPreflightReport(t *testing.T) {
	t.Parallel()

	payload := `{"workload_size":2,"rank":0,"node_name":"node-a","gpu_check":1,"storage_check":1}`
	bridge := NewKubeEventBridge(fake.NewSimpleClientset()).(*kubeEventBridge)
	bridge.handleK8sEventAdd(context.Background(), &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: metav1.Now(),
			Namespace:         "default",
			Annotations: map[string]string{
				constants.PreflightWorkloadAnnotation:  "job-a",
				constants.PreflightNamespaceAnnotation: "train-ns",
				constants.PreflightPayloadAnnotation:   payload,
			},
		},
		InvolvedObject: corev1.ObjectReference{APIVersion: "v1", Kind: "Node", Name: "node-a"},
	}, true)

	select {
	case event := <-bridge.EventChan():
		if event.Message != payload || !IsPreflightEvent(event.Annotations) {
			t.Fatalf("initial preflight event = %+v, want preserved report", event)
		}
	default:
		t.Fatal("initial preflight report was dropped")
	}
}

func TestHandleK8sEventAddQueuesDay2EventWhenEventChannelIsFull(t *testing.T) {
	t.Parallel()

	bridge := NewKubeEventBridge(fake.NewSimpleClientset()).(*kubeEventBridge)
	bridge.eventCh = make(chan Event, 1)
	bridge.eventCh <- Event{ResourceType: Pod, Namespace: "default", Name: "existing", EventType: Error}
	startQueueWorkerForTest(t, bridge)
	defer bridge.Stop()

	done := make(chan struct{})
	go func() {
		defer close(done)
		bridge.handleK8sEventAdd(context.Background(), &corev1.Event{
			ObjectMeta: metav1.ObjectMeta{
				CreationTimestamp: metav1.Now(),
				Namespace:         "default",
				Annotations: map[string]string{
					constants.NeedRecoveryAnnotation: constants.True,
				},
			},
			Reason:         Day2EventReason,
			Message:        "insufficient available GPUs: expected 9, found 8",
			InvolvedObject: corev1.ObjectReference{APIVersion: "v1", Kind: "Node", Name: "node-a"},
		}, false)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handleK8sEventAdd(day2) did not return after queueing the event")
	}

	select {
	case event := <-bridge.eventCh:
		if event.Name != "existing" {
			t.Fatalf("first queued event name = %q, want existing", event.Name)
		}
	case <-time.After(50 * time.Millisecond):
		t.Fatal("existing buffered event was not readable")
	}

	select {
	case event := <-bridge.eventCh:
		if event.ResourceType != Node || event.Name != "node-a" || event.Reason != Day2EventReason {
			t.Fatalf("forwarded event = %+v, want day2 node event", event)
		}
	case <-time.After(time.Second):
		t.Fatal("handleK8sEventAdd(day2) forwarded no event, want one")
	}
}

func TestHandleK8sEventAddQueuesPreflightEventWhenEventChannelIsFull(t *testing.T) {
	t.Parallel()

	payload := `{"workload_size":2,"rank":0,"node_name":"node-a","gpu_check":1,"storage_check":1}`
	bridge := NewKubeEventBridge(fake.NewSimpleClientset()).(*kubeEventBridge)
	bridge.eventCh = make(chan Event, 1)
	bridge.eventCh <- Event{ResourceType: Pod, Namespace: "default", Name: "existing", EventType: Error}
	startQueueWorkerForTest(t, bridge)
	defer bridge.Stop()

	done := make(chan struct{})
	go func() {
		defer close(done)
		bridge.handleK8sEventAdd(context.Background(), &corev1.Event{
			ObjectMeta: metav1.ObjectMeta{
				CreationTimestamp: metav1.Now(),
				Namespace:         "default",
				Annotations: map[string]string{
					constants.PreflightWorkloadAnnotation:  "job-a",
					constants.PreflightNamespaceAnnotation: "train-ns",
					constants.PreflightPayloadAnnotation:   payload,
				},
			},
			Message:        "preflight report available",
			InvolvedObject: corev1.ObjectReference{APIVersion: "v1", Kind: "Node", Name: "node-a"},
		}, false)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handleK8sEventAdd(preflight) did not return after queueing the event")
	}

	select {
	case event := <-bridge.eventCh:
		if event.Name != "existing" {
			t.Fatalf("first queued event name = %q, want existing", event.Name)
		}
	case <-time.After(50 * time.Millisecond):
		t.Fatal("existing buffered event was not readable")
	}

	select {
	case event := <-bridge.eventCh:
		if event.ResourceType != Node || event.Name != "node-a" || event.Message != payload || !IsPreflightEvent(event.Annotations) {
			t.Fatalf("forwarded event = %+v, want preflight node event", event)
		}
	case <-time.After(time.Second):
		t.Fatal("handleK8sEventAdd(preflight) forwarded no event, want one")
	}
}

func TestShouldWatchEventRejectsNonDay2NodeRecoveryEvent(t *testing.T) {
	t.Parallel()

	bridge := NewKubeEventBridge(fake.NewSimpleClientset()).(*kubeEventBridge)

	if bridge.shouldWatchEvent(&corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: metav1.Now(),
			Annotations: map[string]string{
				constants.NeedRecoveryAnnotation: constants.True,
			},
		},
		InvolvedObject: corev1.ObjectReference{APIVersion: "v1", Kind: "Node", Name: "node-a"},
	}) {
		t.Fatal("shouldWatchEvent(non-day2 node recovery event) = true, want false")
	}
}

func TestShouldWatchEventKeepsPodRecoveryLogic(t *testing.T) {
	t.Parallel()

	bridge := NewKubeEventBridge(fake.NewSimpleClientset()).(*kubeEventBridge)

	if !bridge.shouldWatchEvent(&corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: metav1.Now(),
			Annotations: map[string]string{
				constants.NeedRecoveryAnnotation: constants.True,
			},
		},
		InvolvedObject: corev1.ObjectReference{APIVersion: "v1", Kind: "Pod", Namespace: "default", Name: "pod-a"},
	}) {
		t.Fatal("shouldWatchEvent(pod recovery event) = false, want true")
	}
}

func TestHandleK8sEventAddQueuesPodEventWhenEventChannelIsFull(t *testing.T) {
	t.Parallel()

	bridge := NewKubeEventBridge(fake.NewSimpleClientset()).(*kubeEventBridge)
	bridge.eventCh = make(chan Event, 1)
	bridge.eventCh <- Event{ResourceType: Pod, Namespace: "default", Name: "existing", EventType: Error}
	startQueueWorkerForTest(t, bridge)
	defer bridge.Stop()

	bridge.handleK8sEventAdd(context.Background(), &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: metav1.Now(),
			Annotations: map[string]string{
				constants.NeedRecoveryAnnotation: constants.True,
			},
		},
		Message:        "pod failed",
		InvolvedObject: corev1.ObjectReference{APIVersion: "v1", Kind: "Pod", Namespace: "default", Name: "pod-a"},
	}, false)

	select {
	case event := <-bridge.eventCh:
		if event.Name != "existing" {
			t.Fatalf("first queued event name = %q, want existing", event.Name)
		}
	case <-time.After(time.Second):
		t.Fatal("existing buffered event was not readable")
	}

	select {
	case event := <-bridge.eventCh:
		if event.ResourceType != Pod || event.Name != "pod-a" || event.Message != "pod failed" {
			t.Fatalf("forwarded event = %+v, want pod recovery event", event)
		}
	case <-time.After(time.Second):
		t.Fatal("queued pod event was not forwarded")
	}
}

func TestStopClosesEventChannelWithQueuedEvents(t *testing.T) {
	t.Parallel()

	bridge := NewKubeEventBridge(fake.NewSimpleClientset()).(*kubeEventBridge)
	bridge.eventCh = make(chan Event)
	startQueueWorkerForTest(t, bridge)

	bridge.queue.Add(&Event{ResourceType: Node, Namespace: "default", Name: "node-a", Reason: Day2EventReason, EventType: Error, Message: "boom"})

	done := make(chan struct{})
	go func() {
		defer close(done)
		bridge.Stop()
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop() did not finish with queued events")
	}

	select {
	case _, ok := <-bridge.EventChan():
		if ok {
			t.Fatal("event channel still open after Stop() completed")
		}
	case <-time.After(time.Second):
		t.Fatal("event channel was not closed after Stop()")
	}
}

func TestStopReturnsWhenQueuedForwarderHasNoConsumer(t *testing.T) {
	t.Parallel()

	bridge := NewKubeEventBridge(fake.NewSimpleClientset()).(*kubeEventBridge)
	bridge.eventCh = make(chan Event)
	startQueueWorkerForTest(t, bridge)

	bridge.queue.Add(&Event{ResourceType: Node, Namespace: "default", Name: "node-a", Reason: Day2EventReason, EventType: Error, Message: "boom"})

	done := make(chan struct{})
	go func() {
		defer close(done)
		bridge.Stop()
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop() did not finish when queued event had no consumer")
	}
}

func startQueueWorkerForTest(t *testing.T, bridge *kubeEventBridge) {
	t.Helper()

	bridge.doneCh = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	bridge.cancel = cancel
	go bridge.runQueueForwarder(ctx)
}
