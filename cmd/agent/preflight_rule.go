package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/baizeai/kcover/pkg/constants"
	"github.com/baizeai/kcover/pkg/detector/node"
	"github.com/baizeai/kcover/pkg/events"
	"github.com/baizeai/kcover/pkg/podobserver"
	"github.com/baizeai/kcover/pkg/preflight"
	"github.com/baizeai/kcover/pkg/runner"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

// /var/lib/kcover/preflight/<namespace>/<workload-name>-<rank>.json
const preflightReportDir = "/var/lib/kcover/preflight"
const preflightInitContainerName = "preflight"

type preflightRule struct {
	baseDir string
}

type nvidiaPreflightObserver struct{}

var _ runner.Runner = nvidiaPreflightObserver{}

func newPreflightObserver(cli kubernetes.Interface, sink events.Sink, nodeName string, vendor node.Vendor) (runner.Runner, error) {
	switch vendor {
	case node.MetaX:
		return newMetaXPreflightObserver(cli, sink, nodeName)
	case node.Nvidia:
		return nvidiaPreflightObserver{}, nil
	default:
		return nil, fmt.Errorf("unsupported vendor: %d", vendor)
	}
}

func (nvidiaPreflightObserver) Start() error {
	klog.V(2).InfoS("preflight pod observer placeholder started", "vendor", node.Nvidia)
	return nil
}

func (nvidiaPreflightObserver) Stop() {}

func newMetaXPreflightObserver(cli kubernetes.Interface, sink events.Sink, nodeName string) (runner.Runner, error) {
	observer, err := podobserver.NewForNode(cli, sink, "preflight pod observer", nodeName, preflightRule{baseDir: preflightReportDir})
	if err != nil {
		return nil, fmt.Errorf("create preflight pod observer: %w", err)
	}

	return observer, nil
}

func (r preflightRule) OnAdd(*corev1.Pod) []events.Event {
	return nil
}

func (r preflightRule) OnUpdate(oldPod, newPod *corev1.Pod) []events.Event {
	if !shouldHandlePodUpdate(oldPod, newPod) {
		return nil
	}

	workloadName := preflightWorkloadName(newPod)
	reportName, ok := preflightReportName(newPod, workloadName)
	if !ok {
		return nil
	}

	nodeName := strings.TrimSpace(newPod.Spec.NodeName)
	if nodeName == "" {
		return nil
	}

	reportText, nodeName, err := loadPreflightReportPayload(r.baseDir, newPod.Namespace, reportName, nodeName)
	if err != nil {
		klog.V(4).InfoS("failed to load preflight report", "namespace", newPod.Namespace, "pod", newPod.Name, "report", reportName, "node", nodeName, "error", err)
		return nil
	}
	if nodeName == "" {
		klog.ErrorS(nil, "preflight report node name is empty", "namespace", newPod.Namespace, "pod", newPod.Name, "report", reportName)
		return nil
	}

	event, err := preflight.BuildEventFromReport(newPod.Namespace, nodeName, workloadName, reportText)
	if err != nil {
		klog.ErrorS(err, "failed to build preflight delivery event", "namespace", newPod.Namespace, "pod", newPod.Name, "report", reportName)
		return nil
	}
	klog.V(3).InfoS("prepare preflight delivery event", "namespace", event.Namespace, "pod", newPod.Name, "node", event.Name, "workload", workloadName)

	return []events.Event{event}
}

func loadPreflightReportPayload(baseDir, namespace, reportName, nodeName string) (string, string, error) {
	nodeName = strings.TrimSpace(nodeName)
	if nodeName == "" {
		return "", "", fmt.Errorf("preflight report node name is empty")
	}

	return preflight.LoadReportPayload(baseDir, namespace, reportName)
}

func preflightWorkloadName(pod *corev1.Pod) string {
	if pod == nil {
		return ""
	}

	labels := pod.Labels
	if labels == nil {
		return ""
	}
	if name := labels[constants.LeaderWorkerSetNameLabel]; name != "" {
		return lwsWorkloadName(name, labels)
	}

	return labels[constants.BatchJobNameLabel]
}

func lwsWorkloadName(name string, labels map[string]string) string {
	groupIndex := strings.TrimSpace(labels[constants.LeaderWorkerSetGroupIndexLabel])
	if !isNumeric(groupIndex) {
		return ""
	}

	return name + "-" + groupIndex
}

func preflightReportName(pod *corev1.Pod, workloadName string) (string, bool) {
	if pod == nil || pod.Name == "" || workloadName == "" {
		return "", false
	}

	reportSuffix, ok := preflightReportSuffix(pod, workloadName)
	if !ok {
		return "", false
	}

	return fmt.Sprintf("%s-%s", workloadName, reportSuffix), true
}

func preflightReportSuffix(pod *corev1.Pod, workloadName string) (string, bool) {
	if pod == nil || workloadName == "" {
		return "", false
	}

	labels := pod.Labels
	if labels[constants.LeaderWorkerSetNameLabel] != "" {
		return lwsReportSuffix(labels)
	}

	annotations := pod.Annotations
	raw := strings.TrimSpace(annotations[constants.BatchJobCompletionIndexAnnotation])
	if raw == "" {
		return "", false
	}
	if _, err := strconv.Atoi(raw); err != nil {
		return "", false
	}

	return raw, true
}

func lwsReportSuffix(labels map[string]string) (string, bool) {
	groupIndex := strings.TrimSpace(labels[constants.LeaderWorkerSetGroupIndexLabel])
	workerIndex := strings.TrimSpace(labels[constants.LeaderWorkerSetWorkerIndexLabel])
	if isNumeric(groupIndex) && isNumeric(workerIndex) {
		return workerIndex, true
	}

	return "", false
}

func isNumeric(raw string) bool {
	if raw == "" {
		return false
	}

	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return false
		}
	}

	return true
}

func shouldHandlePodUpdate(oldPod, newPod *corev1.Pod) bool {
	if newPod == nil {
		return false
	}
	if newPod.Labels[constants.PreflightLabel] != constants.True {
		return false
	}

	var oldStatuses []corev1.ContainerStatus
	if oldPod != nil {
		oldStatuses = oldPod.Status.InitContainerStatuses
	}

	return isPreflightFailed(oldStatuses, newPod.Status.InitContainerStatuses)
}

// isPreflightFailed returns true only when the init container named "preflight"
// transitions from non-failed (or missing) to failed on this update.
func isPreflightFailed(oldStatuses, newStatuses []corev1.ContainerStatus) bool {
	newStatus, ok := initContainerStatusByName(newStatuses, preflightInitContainerName)
	if !ok || !initContainerFailed(newStatus) {
		return false
	}

	oldStatus, ok := initContainerStatusByName(oldStatuses, preflightInitContainerName)
	if !ok {
		return true
	}

	return !initContainerFailed(oldStatus)
}

func initContainerFailed(status corev1.ContainerStatus) bool {
	return status.State.Terminated != nil && status.State.Terminated.ExitCode != 0
}

func initContainerStatusByName(statuses []corev1.ContainerStatus, name string) (corev1.ContainerStatus, bool) {
	for _, status := range statuses {
		if status.Name == name {
			return status, true
		}
	}

	return corev1.ContainerStatus{}, false
}
