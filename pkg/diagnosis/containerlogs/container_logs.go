package containerlogs

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"recovery.baizeai.io/pkg/constants"
	"recovery.baizeai.io/pkg/diagnosis"
	"recovery.baizeai.io/pkg/events"
	"recovery.baizeai.io/pkg/runner"
)

var _ runner.Runner = (*containerLogDiagnostic)(nil)
var _ diagnosis.Diagnostic = (*containerLogDiagnostic)(nil)

type containerLogDiagnostic struct {
	client     kubernetes.Interface
	eventsChan chan events.CollectorEvent
	stop       chan struct{}
}

func NewContainerLogDiagnostic(cli kubernetes.Interface) (diagnosis.Diagnostic, error) {
	return &containerLogDiagnostic{
		client:     cli,
		eventsChan: make(chan events.CollectorEvent),
		stop:       make(chan struct{}),
	}, nil
}

func (c *containerLogDiagnostic) getContainerLogs(namespace, podName, containerName string) (string, error) {
	podLogOpts := corev1.PodLogOptions{
		Container: containerName,
	}

	req := c.client.CoreV1().Pods(namespace).GetLogs(podName, &podLogOpts)
	podLogs, err := req.Stream(context.TODO())
	if err != nil {
		return "", fmt.Errorf("error in opening logs stream: %v", err)
	}

	defer podLogs.Close()

	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(podLogs)
	if err != nil {
		return "", fmt.Errorf("error in copy information from pod logs to buf: %v", err)
	}

	return buf.String(), nil
}

func (c *containerLogDiagnostic) searchForErrors(logs, errorString string) bool {
	return strings.Contains(logs, errorString)
}

func getLinesBeforeAndAfter(logs []string, index int, beforeNum int, afterNum int) []string {
	start := index - beforeNum
	if start > len(logs)-1 {
		start = len(logs) - 1
	}
	if start < 0 {
		start = 0
	}

	end := index + afterNum
	if end > len(logs)-1 {
		end = len(logs) - 1
	}
	if end < 0 {
		end = 0
	}

	return logs[start : end+1]
}

func (c *containerLogDiagnostic) onPodUpdate(oldPod, newPod *corev1.Pod) {
	if oldPod != nil {
		if reflect.DeepEqual(oldPod.Status.ContainerStatuses, newPod.Status.ContainerStatuses) {
			// no need to check
			return
		}
	}
	for _, cs := range newPod.Status.ContainerStatuses {
		if cs.State.Terminated != nil {
			if cs.State.Terminated.Reason == "Error" {
				logs, err := c.getContainerLogs(newPod.Namespace, newPod.Name, cs.Name)
				if err != nil {
					klog.Errorf("failed to get logs for container %s in pod %s: %v", cs.Name, newPod.Name, err)
					continue
				}

				logLines := strings.Split(logs, "\n")

				for lineNum, line := range logLines {
					if c.searchForErrors(line, "ncclRemoteError") { // ncclRemoteError: A call failed possibly due to a network error or a remote process exiting prematurely.
						c.eventsChan <- events.CollectorEvent{
							TargetType: events.Pod,
							Namespace:  newPod.Namespace,
							Name:       newPod.Name,
							EventType:  events.Error,
							Message:    fmt.Sprintf("container %s terminated with ncclRemoteError: %s, log: %s, exit code: %d", cs.Name, cs.State.Terminated.Message, strings.Join(getLinesBeforeAndAfter(logLines, lineNum, 2, 10), "\n"), cs.State.Terminated.ExitCode),
						}
					}
					if c.searchForErrors(line, "NCCL error") { // what():  [Rank 2] NCCL watchdog thread terminated with exception: NCCL error: remote process exited or there was a network error, NCCL version 2.19.3
						c.eventsChan <- events.CollectorEvent{
							TargetType: events.Pod,
							Namespace:  newPod.Namespace,
							Name:       newPod.Name,
							EventType:  events.Error,
							Message:    fmt.Sprintf("container %s terminated with NCCL error: %s, log: %s, exit code: %d", cs.Name, cs.State.Terminated.Message, strings.Join(getLinesBeforeAndAfter(logLines, lineNum, 5, 5), "\n"), cs.State.Terminated.ExitCode),
						}
					}
					if c.searchForErrors(line, "torch.distributed.elastic.multiprocessing.api: [ERROR]") { // [2024-01-25 04:10:31,303] torch.distributed.elastic.multiprocessing.api: [ERROR] failed (exitcode: -6) local_rank: 0 (pid: 164) of binary: /usr/bin/python
						c.eventsChan <- events.CollectorEvent{
							TargetType: events.Pod,
							Namespace:  newPod.Namespace,
							Name:       newPod.Name,
							EventType:  events.Error,
							Message:    fmt.Sprintf("container %s terminated with torch.distributed.elastic.multiprocessing.api error: %s, log: %s, exit code: %d", cs.Name, cs.State.Terminated.Message, strings.Join(getLinesBeforeAndAfter(logLines, lineNum, 20, 10), "\n"), cs.State.Terminated.ExitCode),
						}
					}
					if c.searchForErrors(line, "CUDA error: an illegal memory access was encountered") { // CUDA error: an illegal memory access was encountered\nCUDA kernel errors might be asynchronously reported at some other API call, so the stacktrace below might be incorrect.\nFor debugging consider passing CUDA_LAUNCH_BLOCKING=1.\nCompile with `TORCH_USE_CUDA_DSA` to enable device-side assertions.
						c.eventsChan <- events.CollectorEvent{
							TargetType: events.Pod,
							Namespace:  newPod.Namespace,
							Name:       newPod.Name,
							EventType:  events.Error,
							Message:    fmt.Sprintf("container %s terminated with CUDA error: an illegal memory access was encountered: %s, log: %s, exit code: %d", cs.Name, cs.State.Terminated.Message, strings.Join(getLinesBeforeAndAfter(logLines, lineNum, 10, 10), "\n"), cs.State.Terminated.ExitCode),
						}
					}
					if c.searchForErrors(line, "CUDA error: the launch timed out and was terminated") { // CUDA error: the launch timed out and was terminated
						c.eventsChan <- events.CollectorEvent{
							TargetType: events.Pod,
							Namespace:  newPod.Namespace,
							Name:       newPod.Name,
							EventType:  events.Error,
							Message:    fmt.Sprintf("container %s terminated with CUDA error: the launch timed out and was terminated: %s, log: %s, exit code: %d", cs.Name, cs.State.Terminated.Message, strings.Join(getLinesBeforeAndAfter(logLines, lineNum, 10, 10), "\n"), cs.State.Terminated.ExitCode),
						}
					}
					if c.searchForErrors(line, "CUDA error: device-side assert triggered") { // RuntimeError: CUDA error: device-side assert triggered\n, DoInference is dead!
						c.eventsChan <- events.CollectorEvent{
							TargetType: events.Pod,
							Namespace:  newPod.Namespace,
							Name:       newPod.Name,
							EventType:  events.Error,
							Message:    fmt.Sprintf("container %s terminated with CUDA error: device-side assert triggered: %s, log: %s, exit code: %d", cs.Name, cs.State.Terminated.Message, strings.Join(getLinesBeforeAndAfter(logLines, lineNum, 20, 20), "\n"), cs.State.Terminated.ExitCode),
						}
					}
					if c.searchForErrors(line, "cuDNN error:") { // RuntimeError: cuDNN error: CUDNN_STATUS_INTERNAL_ERROR
						c.eventsChan <- events.CollectorEvent{
							TargetType: events.Pod,
							Namespace:  newPod.Namespace,
							Name:       newPod.Name,
							EventType:  events.Error,
							Message:    fmt.Sprintf("container %s terminated with RuntimeError: CUDA error: the launch timed out and was terminated: %s, log: %s, exit code: %d", cs.Name, cs.State.Terminated.Message, strings.Join(getLinesBeforeAndAfter(logLines, lineNum, 20, 20), "\n"), cs.State.Terminated.ExitCode),
						}
					}
					if c.searchForErrors(line, "unhandled cuda error") { // Test NCCL failure common.cu:954 'unhandled cuda error (run with NCCL_DEBUG=INFO for details)
						c.eventsChan <- events.CollectorEvent{
							TargetType: events.Pod,
							Namespace:  newPod.Namespace,
							Name:       newPod.Name,
							EventType:  events.Error,
							Message:    fmt.Sprintf("container %s terminated with unhandled cuda error: %s, log: %s, exit code: %d", cs.Name, cs.State.Terminated.Message, strings.Join(getLinesBeforeAndAfter(logLines, lineNum, 30, 30), "\n"), cs.State.Terminated.ExitCode),
						}
					}
				}
			}
		}
	}
}

func (c *containerLogDiagnostic) Start() error {
	factory := informers.NewSharedInformerFactory(c.client, time.Minute)
	informer := factory.Core().V1().Pods().Informer()
	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			newPod := obj.(*corev1.Pod)
			if newPod.Labels[constants.EnabledRecoveryLabel] == "" {
				return
			}

			c.onPodUpdate(nil, newPod)
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

			c.onPodUpdate(oldPod, newPod)
		},
	})
	if err != nil {
		return err
	}

	go informer.Run(c.stop)
	return nil
}

func (c *containerLogDiagnostic) Stop() {
	close(c.stop)
	close(c.eventsChan)
}

func (c *containerLogDiagnostic) Events() <-chan events.CollectorEvent {
	return c.eventsChan
}
