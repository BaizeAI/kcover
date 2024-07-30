package podstatus

import (
	"fmt"
	"reflect"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"recovery.baizeai.io/pkg/constants"
	"recovery.baizeai.io/pkg/diagnosis"
	"recovery.baizeai.io/pkg/events"
	"recovery.baizeai.io/pkg/runner"
)

var _ runner.Runner = (*podStatusCollector)(nil)
var _ diagnosis.Diagnostic = (*podStatusCollector)(nil)

type podStatusCollector struct {
	client     kubernetes.Interface
	eventsChan chan events.CollectorEvent
	stop       chan struct{}
}

func NewPodStatusCollector(cli kubernetes.Interface) (diagnosis.Diagnostic, error) {
	return &podStatusCollector{
		client:     cli,
		eventsChan: make(chan events.CollectorEvent),
		stop:       make(chan struct{}),
	}, nil
}

func (p *podStatusCollector) onPodUpdate(oldPod, newPod *corev1.Pod) {
	if oldPod != nil {
		if reflect.DeepEqual(oldPod.Status.ContainerStatuses, newPod.Status.ContainerStatuses) {
			// no need to check
			return
		}
	}
	for _, cs := range newPod.Status.ContainerStatuses {
		if cs.State.Terminated != nil {
			if cs.State.Terminated.Reason == "Error" {
				p.eventsChan <- events.CollectorEvent{
					TargetType: events.Pod,
					Namespace:  newPod.Namespace,
					Name:       newPod.Name,
					EventType:  events.Error,
					Message:    fmt.Sprintf("container %s terminated with error: %s, exit code: %d", cs.Name, cs.State.Terminated.Message, cs.State.Terminated.ExitCode),
				}
			}
		}
	}
}

func (p *podStatusCollector) Start() error {
	factory := informers.NewSharedInformerFactory(p.client, time.Minute)
	informer := factory.Core().V1().Pods().Informer()
	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			newPod := obj.(*corev1.Pod)
			if newPod.Labels[constants.EnabledRecoveryLabel] == "" {
				return
			}

			p.onPodUpdate(nil, newPod)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			newPod := newObj.(*corev1.Pod)
			oldPod := oldObj.(*corev1.Pod)
			if newPod.ResourceVersion == oldPod.ResourceVersion {
				return
			}
			if newPod.Labels[constants.EnabledRecoveryLabel] == "" {
				return
			}

			p.onPodUpdate(oldPod, newPod)
		},
	})
	if err != nil {
		return err
	}

	go informer.Run(p.stop)
	return nil
}

func (p *podStatusCollector) Stop() {
	close(p.stop)
	close(p.eventsChan)
}

func (p *podStatusCollector) Events() <-chan events.CollectorEvent {
	return p.eventsChan
}
