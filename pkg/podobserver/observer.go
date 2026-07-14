package podobserver

import (
	"context"
	"fmt"

	"github.com/baizeai/kcover/pkg/events"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

type PodRule interface {
	OnAdd(pod *corev1.Pod) []events.Event
	OnUpdate(oldPod, newPod *corev1.Pod) []events.Event
}

type initialListPolicy interface {
	ShouldHandleInitialList() bool
}

// Observer watches Pods and publishes events produced by its rules.
type Observer struct {
	client   kubernetes.Interface
	sink     events.Sink
	rules    []PodRule
	cancel   context.CancelFunc
	doneCh   chan struct{}
	logName  string
	nodeName string
}

func New(cli kubernetes.Interface, sink events.Sink, logName string, rules ...PodRule) (*Observer, error) {
	if sink == nil {
		return nil, fmt.Errorf("event sink cannot be nil")
	}

	return &Observer{
		client:  cli,
		sink:    sink,
		rules:   rules,
		logName: logName,
	}, nil
}

func NewForNode(cli kubernetes.Interface, sink events.Sink, logName, nodeName string, rules ...PodRule) (*Observer, error) {
	if nodeName == "" {
		return nil, fmt.Errorf("observer node name cannot be empty")
	}

	observer, err := New(cli, sink, logName, rules...)
	if err != nil {
		return nil, err
	}

	observer.nodeName = nodeName

	return observer, nil
}

func (o *Observer) Start(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	o.cancel = cancel
	o.doneCh = make(chan struct{})

	factory := informers.NewSharedInformerFactory(o.client, 0)
	if o.nodeName != "" {
		factory = informers.NewSharedInformerFactoryWithOptions(
			o.client,
			0,
			informers.WithTweakListOptions(func(options *metav1.ListOptions) {
				options.FieldSelector = fields.OneTermEqualSelector("spec.nodeName", o.nodeName).String()
			}),
		)
	}
	informer := factory.Core().V1().Pods().Informer()

	_, err := informer.AddEventHandler(cache.ResourceEventHandlerDetailedFuncs{
		AddFunc: o.handleAdd,
		UpdateFunc: func(oldObj, newObj any) {
			oldPod, ok := oldObj.(*corev1.Pod)
			if !ok {
				return
			}
			newPod, ok := newObj.(*corev1.Pod)
			if !ok {
				return
			}
			if newPod.ResourceVersion == oldPod.ResourceVersion {
				return
			}
			o.onUpdate(oldPod, newPod)
		},
	})
	if err != nil {
		return err
	}

	go func() {
		defer close(o.doneCh)
		informer.Run(ctx.Done())
	}()

	if o.nodeName == "" {
		klog.InfoS("pod observer started", "observer", o.logName)
		return nil
	}

	klog.InfoS("pod observer started", "observer", o.logName, "node", o.nodeName)
	return nil
}

func (o *Observer) handleAdd(obj any, isInInitialList bool) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return
	}
	if !isInInitialList {
		o.onAdd(pod)
		return
	}

	for _, rule := range o.rules {
		policy, ok := rule.(initialListPolicy)
		if ok && policy.ShouldHandleInitialList() {
			o.publish(rule, rule.OnAdd(pod))
		}
	}
}

func (o *Observer) Stop() {
	if o.cancel != nil {
		o.cancel()
	}
	if o.doneCh != nil {
		<-o.doneCh
	}
}

func (o *Observer) onAdd(pod *corev1.Pod) {
	for _, rule := range o.rules {
		o.publish(rule, rule.OnAdd(pod))
	}
}

func (o *Observer) onUpdate(oldPod, newPod *corev1.Pod) {
	for _, rule := range o.rules {
		events := rule.OnUpdate(oldPod, newPod)
		o.publish(rule, events)
	}
}

func (o *Observer) publish(rule PodRule, events []events.Event) {
	ruleName := fmt.Sprintf("%T", rule)
	if len(events) == 0 {
		return
	}

	for _, e := range events {
		if err := o.sink.RecordEvent(e); err != nil {
			klog.ErrorS(err, "failed to record event", "rule", rule)
			continue
		}
		klog.V(3).InfoS("pod observer published event", "observer", o.logName, "rule", ruleName, "resourceType", e.ResourceType, "namespace", e.Namespace, "name", e.Name)
	}
}
