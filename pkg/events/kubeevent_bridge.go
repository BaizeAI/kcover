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

type kubeEventBridge struct {
	*kubeEventSink
	eventCh chan Event
	queue   workqueue.TypedRateLimitingInterface[*Event]

	cancel context.CancelFunc
	doneCh chan struct{}
}

const eventMaxAge = 3 * time.Minute
const bridgeEventBufferSize = 1

var (
	podObjectGVK  = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"}
	nodeObjectGVK = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Node"}
)

func NewKubeEventBridge(cli kubernetes.Interface) Bridge {
	sink := NewKubeEventSink(cli).(*kubeEventSink)

	return &kubeEventBridge{
		kubeEventSink: sink,
		eventCh:       make(chan Event, bridgeEventBufferSize),
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[*Event](),
			workqueue.TypedRateLimitingQueueConfig[*Event]{
				Name: "kcover-kube-events",
			},
		),
		doneCh: make(chan struct{}),
	}
}

func (bridge *kubeEventBridge) Start(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	bridge.cancel = cancel

	factory := informers.NewSharedInformerFactory(bridge.client, 0)
	informer := factory.Core().V1().Events().Informer()

	_, err := informer.AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: bridge.shouldWatchEvent,
		Handler: cache.ResourceEventHandlerDetailedFuncs{
			AddFunc:    func(obj any, isInInitialList bool) { bridge.handleK8sEventAdd(ctx, obj, isInInitialList) },
			UpdateFunc: func(oldObj, newObj any) { bridge.handleK8sEventUpdate(ctx, oldObj, newObj) },
		},
	})
	if err != nil {
		return err
	}

	go informer.Run(ctx.Done())
	go bridge.runQueueForwarder(ctx)
	go func() {
		<-ctx.Done()
		bridge.queue.ShutDown()
	}()
	klog.InfoS("kube event bridge started")
	return nil
}

func (bridge *kubeEventBridge) shouldWatchEvent(obj any) bool {
	event, ok := obj.(*corev1.Event)
	if !ok {
		return false
	}

	if bridge.isExpiredEvent(event, time.Now()) {
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

func (bridge *kubeEventBridge) handleK8sEventAdd(ctx context.Context, obj any, isInInitialList bool) {
	event, ok := obj.(*corev1.Event)
	if !ok {
		return
	}
	if isInInitialList {
		return
	}

	if bridge.isExpiredEvent(event, time.Now()) {
		return
	}

	bridge.forwardK8sEvent(ctx, event)
}

func (bridge *kubeEventBridge) handleK8sEventUpdate(ctx context.Context, oldObj, newObj any) {
	oldEvent, oldOK := oldObj.(*corev1.Event)
	newEvent, newOK := newObj.(*corev1.Event)
	if !oldOK || !newOK {
		return
	}

	if !hasNewEventOccurrence(oldEvent, newEvent) {
		return
	}

	if bridge.isExpiredEvent(newEvent, time.Now()) {
		return
	}

	bridge.forwardK8sEvent(ctx, newEvent)
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

func (bridge *kubeEventBridge) forwardK8sEvent(ctx context.Context, event *corev1.Event) {
	evt, ok := bridge.toInternalEvent(event)
	if !ok {
		return
	}

	klog.V(3).InfoS("forward internal event", "resourceType", evt.ResourceType, "namespace", evt.Namespace, "name", evt.Name, "reason", evt.Reason, "eventType", evt.EventType)
	select {
	case <-ctx.Done():
		return
	case bridge.eventCh <- evt:
		return
	default:
	}

	bridge.queue.Add(&evt)
}

func (bridge *kubeEventBridge) runQueueForwarder(ctx context.Context) {
	defer close(bridge.doneCh)

	for {
		evt, shutdown := bridge.queue.Get()
		if shutdown {
			return
		}
		if evt == nil {
			bridge.finishQueueItem(evt)
			klog.ErrorS(nil, "kube event bridge received nil queue item")
			continue
		}

		select {
		case bridge.eventCh <- *evt:
		case <-ctx.Done():
			bridge.finishQueueItem(evt)
			return
		}
		bridge.finishQueueItem(evt)
	}
}

func (bridge *kubeEventBridge) finishQueueItem(evt *Event) {
	bridge.queue.Forget(evt)
	bridge.queue.Done(evt)
}

func (bridge *kubeEventBridge) isExpiredEvent(event *corev1.Event, now time.Time) bool {
	eventTimestamp := event.LastTimestamp
	if eventTimestamp.IsZero() {
		eventTimestamp = event.CreationTimestamp
	}

	if eventTimestamp.Add(eventMaxAge).Before(now) {
		return true
	}

	return false
}

func (bridge *kubeEventBridge) toInternalEvent(event *corev1.Event) (Event, bool) {
	if IsPreflightEvent(event.Annotations) {
		return Event{}, false
	}

	if event.Annotations[constants.NeedRecoveryAnnotation] != constants.True {
		return Event{}, false
	}

	return bridge.toInternalRecoveryEvent(event)
}

func (bridge *kubeEventBridge) toInternalRecoveryEvent(event *corev1.Event) (Event, bool) {
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

func (bridge *kubeEventBridge) Stop() {
	if bridge.cancel != nil {
		bridge.cancel()
	}
	bridge.queue.ShutDown()
	<-bridge.doneCh
	close(bridge.eventCh)
}

func (bridge *kubeEventBridge) EventChan() <-chan Event {
	return bridge.eventCh
}
