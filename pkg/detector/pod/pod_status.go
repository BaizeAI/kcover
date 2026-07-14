package pod

import (
	"fmt"
	"reflect"

	"github.com/baizeai/kcover/pkg/constants"
	"github.com/baizeai/kcover/pkg/events"
	"github.com/baizeai/kcover/pkg/podobserver"
	"github.com/baizeai/kcover/pkg/runner"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
)

type containerErrorRule struct{}

// ShouldHandleInitialList ensures a newly elected controller observes failures
// that happened before its Pod informer started.
func (containerErrorRule) ShouldHandleInitialList() bool {
	return true
}

// NewDetector 创建由 pod 检测管理的检测器，目前只支持 Pod 容器错误检测。
func NewDetector(cli kubernetes.Interface, sink events.Sink) (runner.Runner, error) {
	observer, err := podobserver.New(cli, sink, "pod detector", containerErrorRule{})
	if err != nil {
		return nil, fmt.Errorf("create pod observer: %w", err)
	}

	return observer, nil
}

func shouldCheckPodUpdate(oldPod, newPod *corev1.Pod) bool {
	if oldPod == nil {
		return true
	}

	return !reflect.DeepEqual(oldPod.Status.ContainerStatuses, newPod.Status.ContainerStatuses) ||
		!reflect.DeepEqual(oldPod.Status.InitContainerStatuses, newPod.Status.InitContainerStatuses)
}

func containerEvents(pod *corev1.Pod) []events.Event {
	ee := make([]events.Event, 0)
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Terminated != nil {
			if cs.State.Terminated.Reason == "Error" {
				ee = append(ee, events.Event{
					ResourceType: events.Pod,
					Namespace:    pod.Namespace,
					Name:         pod.Name,
					EventType:    events.Error,
					Message:      fmt.Sprintf("container %s terminated with error: %s, exit code: %d", cs.Name, cs.State.Terminated.Message, cs.State.Terminated.ExitCode),
				})
			}
		}
	}

	return ee
}

func podEvents(pod *corev1.Pod) []events.Event {
	if !shouldHandlePod(pod) {
		return nil
	}

	return containerEvents(pod)
}

func shouldHandlePod(pod *corev1.Pod) bool {
	if pod == nil {
		return false
	}

	return pod.Labels[constants.EnabledRecoveryLabel] != ""
}

func (containerErrorRule) OnAdd(pod *corev1.Pod) []events.Event {
	return podEvents(pod)
}

func (containerErrorRule) OnUpdate(oldPod, newPod *corev1.Pod) []events.Event {
	if !shouldCheckPodUpdate(oldPod, newPod) {
		return nil
	}

	return podEvents(newPod)
}
