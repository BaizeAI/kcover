package events

import (
	"context"
	"time"

	"github.com/baizeai/kcover/pkg/constants"
	"github.com/baizeai/kcover/pkg/kube"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
)

// KubeEventTransport watches Kubernetes Events as an internal recovery stream
// and records internal events through a Kubernetes event sink.
type KubeEventTransport struct {
	sink    *KubeEventSink
	eventCh chan Event
	queue   workqueue.TypedInterface[*Event]

	cancel context.CancelFunc
	doneCh chan struct{}
}

const eventMaxAge = 3 * time.Minute
const transportEventBufferSize = 1

var (
	podObjectGVK  = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"}
	nodeObjectGVK = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Node"}
)

func NewKubeEventTransport(cli kubernetes.Interface) *KubeEventTransport {
	sink := NewKubeEventSink(cli)

	return &KubeEventTransport{
		sink:    sink,
		eventCh: make(chan Event, transportEventBufferSize),
		queue: workqueue.NewTypedWithConfig(
			workqueue.TypedQueueConfig[*Event]{Name: "kcover-kube-events"},
		),
		doneCh: make(chan struct{}),
	}
}

func (tr *KubeEventTransport) Start(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	tr.cancel = cancel

	factory := informers.NewSharedInformerFactory(tr.sink.client, 0)
	informer := factory.Core().V1().Events().Informer()

	_, err := informer.AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: tr.shouldWatchEvent,
		Handler: cache.ResourceEventHandlerDetailedFuncs{
			AddFunc:    func(obj any, isInInitialList bool) { tr.handleK8sEventAdd(ctx, obj, isInInitialList) },
			UpdateFunc: func(oldObj, newObj any) { tr.handleK8sEventUpdate(ctx, oldObj, newObj) },
		},
	})
	if err != nil {
		return err
	}

	go informer.Run(ctx.Done())
	go tr.runQueueForwarder(ctx)
	go func() {
		<-ctx.Done()
		tr.queue.ShutDown()
	}()
	klog.InfoS("kube event transport started")
	return nil
}

func (tr *KubeEventTransport) shouldWatchEvent(obj any) bool {
	event, ok := obj.(*corev1.Event)
	if !ok {
		return false
	}

	if tr.isExpiredEvent(event, time.Now()) {
		return false
	}

	if IsPreflightEvent(event.Annotations) {
		return false
	}

	if event.Annotations[constants.NeedRecoveryAnnotation] != constants.True {
		return false
	}

	if isNodeObjectRef(event.InvolvedObject) {
		return event.Reason == Day2EventReason
	}

	return isPodObjectRef(event.InvolvedObject)
}

func (tr *KubeEventTransport) handleK8sEventAdd(ctx context.Context, obj any, _ bool) {
	event, ok := obj.(*corev1.Event)
	if !ok {
		return
	}

	if tr.isExpiredEvent(event, time.Now()) {
		return
	}

	tr.forwardK8sEvent(ctx, event)
}

func (tr *KubeEventTransport) handleK8sEventUpdate(ctx context.Context, oldObj, newObj any) {
	oldEvent, oldOK := oldObj.(*corev1.Event)
	newEvent, newOK := newObj.(*corev1.Event)
	if !oldOK || !newOK {
		return
	}

	if !hasNewEventOccurrence(oldEvent, newEvent) {
		return
	}

	if tr.isExpiredEvent(newEvent, time.Now()) {
		return
	}

	tr.forwardK8sEvent(ctx, newEvent)
}

func hasNewEventOccurrence(oldEvent, newEvent *corev1.Event) bool {
	if newEvent.Count > oldEvent.Count {
		return true
	}

	oldTimestamp := oldEvent.LastTimestamp
	newTimestamp := newEvent.LastTimestamp
	if oldTimestamp.IsZero() || newTimestamp.IsZero() {
		return false
	}

	return newTimestamp.After(oldTimestamp.Time)

}

func (tr *KubeEventTransport) forwardK8sEvent(ctx context.Context, event *corev1.Event) {
	evt, ok := tr.toInternalEvent(event)
	if !ok {
		return
	}

	klog.V(3).InfoS("forward internal event", "resourceType", evt.ResourceType, "namespace", evt.Namespace, "name", evt.Name, "reason", evt.Reason, "eventType", evt.EventType)
	select {
	case <-ctx.Done():
		return
	case tr.eventCh <- evt:
		return
	default:
	}

	tr.queue.Add(&evt)
}

func (tr *KubeEventTransport) runQueueForwarder(ctx context.Context) {
	defer close(tr.doneCh)

	for {
		evt, shutdown := tr.queue.Get()
		if shutdown {
			return
		}
		if evt == nil {
			tr.finishQueueItem(evt)
			klog.ErrorS(nil, "kube event transport received nil queue item")
			continue
		}

		select {
		case tr.eventCh <- *evt:
		case <-ctx.Done():
			tr.finishQueueItem(evt)
			return
		}
		tr.finishQueueItem(evt)
	}
}

func (tr *KubeEventTransport) finishQueueItem(evt *Event) {
	tr.queue.Done(evt)
}

func (tr *KubeEventTransport) isExpiredEvent(event *corev1.Event, now time.Time) bool {
	eventTimestamp := event.LastTimestamp
	if eventTimestamp.IsZero() {
		eventTimestamp = event.CreationTimestamp
	}

	if eventTimestamp.Add(eventMaxAge).Before(now) {
		return true
	}

	return false
}

func (tr *KubeEventTransport) toInternalEvent(event *corev1.Event) (Event, bool) {
	if IsPreflightEvent(event.Annotations) {
		return Event{}, false
	}

	if event.Annotations[constants.NeedRecoveryAnnotation] != constants.True {
		return Event{}, false
	}

	return tr.toInternalRecoveryEvent(event)
}

func (tr *KubeEventTransport) toInternalRecoveryEvent(event *corev1.Event) (Event, bool) {
	obj := event.InvolvedObject
	if isPodObjectRef(obj) {
		return Event{
			ResourceType: Pod,
			Namespace:    obj.Namespace,
			Name:         obj.Name,
			Reason:       event.Reason,
			EventType:    Error,
			Message:      event.Message,
		}, true
	}

	if isNodeObjectRef(obj) {
		if event.Namespace != kube.CurrentNamespace() {
			return Event{}, false
		}
		return Event{
			ResourceType: Node,
			Namespace:    event.Namespace,
			Name:         obj.Name,
			Reason:       event.Reason,
			EventType:    Error,
			Message:      event.Message,
		}, true
	}
	return Event{}, false
}

func isNodeObjectRef(ref corev1.ObjectReference) bool {
	return ref.GroupVersionKind() == nodeObjectGVK
}

func isPodObjectRef(ref corev1.ObjectReference) bool {
	return ref.GroupVersionKind() == podObjectGVK
}

func (tr *KubeEventTransport) Stop() {
	if tr.cancel != nil {
		tr.cancel()
	}
	tr.queue.ShutDown()
	<-tr.doneCh
	close(tr.eventCh)
}

func (tr *KubeEventTransport) EventChan() <-chan Event {
	return tr.eventCh
}

func (tr *KubeEventTransport) RecordEvent(event Event) error {
	return tr.sink.RecordEvent(event)
}
