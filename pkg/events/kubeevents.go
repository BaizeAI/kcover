package events

import (
	"context"
	"fmt"
	"time"

	"k8s.io/klog/v2"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/tools/reference"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"recovery.baizeai.io/pkg/constants"
)

type kubeEventsRecorder struct {
	client     kubernetes.Interface
	eventChan  chan CollectorEvent
	stop       chan struct{}
	watchEvent bool
	recorder   record.EventRecorder
}

func NewKubeEventsRecorder(cli kubernetes.Interface, watchEvent bool) Recorder {
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(&v1.EventSinkImpl{
		Interface: cli.CoreV1().Events(""),
	})
	recorder := eventBroadcaster.NewRecorder(runtime.NewScheme(), corev1.EventSource{Component: "fast-recovery"})
	return &kubeEventsRecorder{
		client:     cli,
		eventChan:  make(chan CollectorEvent),
		stop:       make(chan struct{}),
		watchEvent: watchEvent,
		recorder:   recorder,
	}
}

func (a *kubeEventsRecorder) Start() error {
	if !a.watchEvent {
		return nil
	}

	factory := informers.NewSharedInformerFactory(a.client, time.Minute)
	informer := factory.Core().V1().Events().Informer()

	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			event := obj.(*corev1.Event)
			if event.LastTimestamp.Add(3 * time.Minute).Before(time.Now()) {
				klog.Infof("event %s is too old, ignore it", event.Name)
				return
			}
			if event.Annotations[constants.NeedRecoveryAnnotation] == "true" {
				obj := event.InvolvedObject
				switch obj.GroupVersionKind() {
				case schema.GroupVersionKind{
					Group:   "",
					Version: "v1",
					Kind:    "Pod",
				}:
					a.eventChan <- CollectorEvent{
						TargetType: Pod,
						Namespace:  obj.Namespace,
						Name:       obj.Name,
						EventType:  Error, // todo change me
						Message:    event.Message,
					}
				case schema.GroupVersionKind{
					Group:   "",
					Version: "v1",
					Kind:    "Node",
				}:
					a.eventChan <- CollectorEvent{
						TargetType: Node,
						Name:       obj.Name,
						EventType:  Error, // todo change me
						Message:    event.Message,
					}
				}
				return
			}
		},
	})

	if err != nil {
		return err
	}
	go informer.Run(a.stop)

	return nil
}

func (a *kubeEventsRecorder) Stop() {
	close(a.stop)
}

func (a *kubeEventsRecorder) recordToPod(e CollectorEvent) error {
	pod, err := a.client.CoreV1().Pods(e.Namespace).Get(context.Background(), e.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	ref, err := reference.GetReference(scheme.Scheme, pod)
	if err != nil {
		return err
	}

	// 记录事件
	a.recorder.AnnotatedEventf(ref, map[string]string{
		constants.NeedRecoveryAnnotation: "true",
	}, corev1.EventTypeWarning, "Error", e.Message)

	return nil
}

func (a *kubeEventsRecorder) recordToNode(e CollectorEvent) error {
	// patch pod with annotation
	node, err := a.client.CoreV1().Nodes().Get(context.Background(), e.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	ref, err := reference.GetReference(scheme.Scheme, node)
	if err != nil {
		return err
	}

	// 记录事件
	a.recorder.AnnotatedEventf(ref, map[string]string{
		constants.NeedRecoveryAnnotation: "true",
	}, corev1.EventTypeWarning, "Error", e.Message)

	return nil
}

func (a *kubeEventsRecorder) RecordEvent(e CollectorEvent) error {
	var err error
	switch e.TargetType {
	case Pod:
		err = a.recordToPod(e)
	case Node:
		err = a.recordToNode(e)
	default:
		//TODO implement me
		return fmt.Errorf("unsupported target type: %s", e.TargetType)
	}
	return err
}

func (a *kubeEventsRecorder) EventChan() <-chan CollectorEvent {
	return a.eventChan
}
