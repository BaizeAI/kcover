package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/baizeai/kcover/pkg/constants"
	"github.com/baizeai/kcover/pkg/events"
	"github.com/baizeai/kcover/pkg/podobserver"
	"github.com/baizeai/kcover/pkg/preflight"
	"github.com/baizeai/kcover/pkg/runner"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

// /var/lib/kcover/preflight/<namespace>/<workload-name>-<rank>.json
const preflightReportDir = "/var/lib/kcover/preflight"
const preflightInitContainerName = "preflight"

type preflightRule struct {
	baseDir   string
	publisher preflight.ReportSubmitter
}

func (preflightRule) HandleInitialList() bool {
	return true
}

func newPreflightObserver(cli kubernetes.Interface, eventSink events.Sink, publisher preflight.ReportSubmitter, nodeName string) (runner.Runner, error) {
	if publisher == nil {
		return nil, fmt.Errorf("preflight report publisher cannot be nil")
	}
	observer, err := podobserver.NewForNode(cli, eventSink, "preflight pod observer", nodeName, preflightRule{
		baseDir:   preflightReportDir,
		publisher: publisher,
	})
	if err != nil {
		return nil, fmt.Errorf("create preflight pod observer: %w", err)
	}

	return observer, nil
}

func (r preflightRule) OnAdd(pod *corev1.Pod) []events.Event {
	if !shouldReconcilePreflightPod(pod) {
		return nil
	}
	return r.reconcile(pod)
}

func (r preflightRule) OnUpdate(_ *corev1.Pod, newPod *corev1.Pod) []events.Event {
	if !shouldReconcilePreflightPod(newPod) {
		return nil
	}
	return r.reconcile(newPod)
}

func (r preflightRule) reconcile(pod *corev1.Pod) []events.Event {
	workloadUID, observedAt, ok := preflightReportIdentity(pod)
	if !ok {
		return nil
	}

	workloadName := preflightWorkloadName(pod)
	reportName, ok := preflightReportName(pod, workloadName)
	if !ok {
		return nil
	}

	nodeName := strings.TrimSpace(pod.Spec.NodeName)
	if nodeName == "" {
		return nil
	}

	reportText, nodeName, err := loadPreflightReportPayload(r.baseDir, pod.Namespace, reportName, nodeName)
	if err != nil {
		klog.V(4).InfoS("failed to load preflight report", "namespace", pod.Namespace, "pod", pod.Name, "report", reportName, "node", nodeName, "error", err)
		return nil
	}
	if nodeName == "" {
		klog.ErrorS(nil, "preflight report node name is empty", "namespace", pod.Namespace, "pod", pod.Name, "report", reportName)
		return nil
	}

	report, err := preflight.BuildPreflightReport(pod.Namespace, nodeName, workloadName, workloadUID, reportText, observedAt, metav1.OwnerReference{
		APIVersion: "v1",
		Kind:       "Pod",
		Name:       pod.Name,
		UID:        pod.UID,
	})
	if err != nil {
		klog.ErrorS(err, "failed to build PreflightReport", "namespace", pod.Namespace, "pod", pod.Name, "report", reportName)
		return nil
	}
	if err := r.publisher.SubmitReport(report); err != nil {
		klog.ErrorS(err, "failed to submit PreflightReport", "namespace", report.Namespace, "name", report.Name, "pod", pod.Name, "node", report.Spec.NodeName, "workload", workloadName)
		return nil
	}
	klog.V(3).InfoS("submitted PreflightReport", "namespace", report.Namespace, "name", report.Name, "pod", pod.Name, "node", report.Spec.NodeName, "workload", workloadName)
	return nil
}

func preflightReportIdentity(pod *corev1.Pod) (string, time.Time, bool) {
	if pod == nil {
		return "", time.Time{}, false
	}
	owner := metav1.GetControllerOf(pod)
	if owner == nil || owner.UID == "" {
		return "", time.Time{}, false
	}
	status, ok := initContainerStatusByName(pod.Status.InitContainerStatuses, preflightInitContainerName)
	if !ok || status.State.Terminated == nil || status.State.Terminated.FinishedAt.IsZero() {
		return "", time.Time{}, false
	}
	return string(owner.UID), status.State.Terminated.FinishedAt.Time, true
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

func shouldReconcilePreflightPod(newPod *corev1.Pod) bool {
	if newPod == nil {
		return false
	}
	if newPod.Labels[constants.PreflightLabel] != constants.True {
		return false
	}

	status, ok := initContainerStatusByName(newPod.Status.InitContainerStatuses, preflightInitContainerName)
	return ok && initContainerTerminated(status)
}

func initContainerTerminated(status corev1.ContainerStatus) bool {
	return status.State.Terminated != nil
}

func initContainerStatusByName(statuses []corev1.ContainerStatus, name string) (corev1.ContainerStatus, bool) {
	for _, status := range statuses {
		if status.Name == name {
			return status, true
		}
	}

	return corev1.ContainerStatus{}, false
}
