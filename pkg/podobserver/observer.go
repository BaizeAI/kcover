package podobserver

import (
	"context"
	"fmt"

	"github.com/baizeai/kcover/pkg/events"
	"github.com/baizeai/kcover/pkg/runner"

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

type observer struct {
	client   kubernetes.Interface
	sink     events.Sink
	rules    []PodRule
	cancel   context.CancelFunc
	doneCh   chan struct{}
	logName  string
	nodeName string
}

var _ runner.Runner = (*observer)(nil)

func New(cli kubernetes.Interface, sink events.Sink, logName string, rules ...PodRule) (runner.Runner, error) {
	if sink == nil {
		return nil, fmt.Errorf("event sink cannot be nil")
	}

	return &observer{
		client:  cli,
		sink:    sink,
		rules:   rules,
		logName: logName,
	}, nil
}

func NewForNode(cli kubernetes.Interface, sink events.Sink, logName, nodeName string, rules ...PodRule) (runner.Runner, error) {
	if nodeName == "" {
		return nil, fmt.Errorf("observer node name cannot be empty")
	}

	runner, err := New(cli, sink, logName, rules...)
	if err != nil {
		return nil, err
	}

	obs, ok := runner.(*observer)
	if !ok {
		return nil, fmt.Errorf("unexpected observer type %T", runner)
	}
	obs.nodeName = nodeName

	return obs, nil
}

func (o *observer) Start() error {
	ctx, cancel := context.WithCancel(context.Background())
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

func (o *observer) handleAdd(obj any, isInInitialList bool) {
	if isInInitialList {
		return
	}

	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return
	}
	o.onAdd(pod)
}

func (o *observer) Stop() {
	if o.cancel != nil {
		o.cancel()
	}
	if o.doneCh != nil {
		<-o.doneCh
	}
}

func (o *observer) onAdd(pod *corev1.Pod) {
	for _, rule := range o.rules {
		o.publish(rule, rule.OnAdd(pod))
	}
}

func (o *observer) onUpdate(oldPod, newPod *corev1.Pod) {
	for _, rule := range o.rules {
		events := rule.OnUpdate(oldPod, newPod)
		o.publish(rule, events)
	}
}

func (o *observer) publish(rule PodRule, events []events.Event) {
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
