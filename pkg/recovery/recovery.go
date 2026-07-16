package recovery

import (
	"context"
	"errors"
	"fmt"
	"time"

	kcoverv1alpha1 "github.com/baizeai/kcover/pkg/apis/kcover/v1alpha1"
	"github.com/baizeai/kcover/pkg/constants"
	"github.com/baizeai/kcover/pkg/events"
	"github.com/baizeai/kcover/pkg/kube"
	"github.com/baizeai/kcover/pkg/preflight"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

type RecoveryController struct {
	client                 kubernetes.Interface
	eventStream            events.Stream
	reportCh               <-chan *kcoverv1alpha1.PreflightReport
	cancel                 context.CancelFunc
	doneCh                 chan struct{}
	preflight              *preflightTracker
	preflightSweepInterval time.Duration
	restartDuration        time.Duration
	jobRestartLedger       *jobRestartLedger
}

const DefaultPreflightSweepInterval = time.Minute

func NewController(cli kubernetes.Interface, stream events.Stream, reportCh <-chan *kcoverv1alpha1.PreflightReport, preflightReportCollectionTimeout, preflightSweepInterval time.Duration) *RecoveryController {
	if preflightSweepInterval <= 0 {
		preflightSweepInterval = DefaultPreflightSweepInterval
	}

	controller := &RecoveryController{
		client:                 cli,
		eventStream:            stream,
		reportCh:               reportCh,
		preflight:              newPreflightTracker(preflightReportCollectionTimeout),
		preflightSweepInterval: preflightSweepInterval,
		restartDuration:        time.Second * 30,
		jobRestartLedger:       newJobRestartLedger(cli, kube.CurrentNamespace()),
	}
	return controller
}

func (r *RecoveryController) handlePreflightTimeout(timeoutErr preflight.WorkloadTimeoutError) {
	anchorNode := timeoutErr.FirstReportedNode()
	if anchorNode == "" {
		klog.ErrorS(nil, "preflight aggregation timeout", "namespace", timeoutErr.Namespace, "workload", timeoutErr.WorkloadName, "timeout", timeoutErr.Timeout, "receivedReports", timeoutErr.ReceivedReports, "expectedReports", timeoutErr.ExpectedReports, "reason", "no reported nodes")
		return
	}

	klog.ErrorS(nil, "preflight aggregation timeout", "node", anchorNode, "namespace", timeoutErr.Namespace, "workload", timeoutErr.WorkloadName, "timeout", timeoutErr.Timeout, "receivedReports", timeoutErr.ReceivedReports, "expectedReports", timeoutErr.ExpectedReports, "reportedNodes", timeoutErr.ReportedNodes)
}

func (r *RecoveryController) onPodError(ctx context.Context, namespace, name string) {
	klog.V(2).InfoS("handle pod error", "namespace", namespace, "pod", name)
	requestCtx, cancel := kube.WithRequestTimeout(ctx)
	pod, err := r.client.CoreV1().Pods(namespace).Get(requestCtx, name, metav1.GetOptions{})
	cancel()
	if err != nil {
		klog.ErrorS(err, "failed to get pod", "namespace", namespace, "pod", name)
		return
	}

	recoveryEnabled, err := r.isRecoveryEnabledForPod(ctx, pod)
	if err != nil {
		klog.ErrorS(err, "failed to get recovery labels", "namespace", namespace, "pod", name)
		return
	}
	if !recoveryEnabled {
		klog.V(4).InfoS("skip recovery for pod", "namespace", namespace, "pod", name, "reason", "pod and owner job have no recovery label")
		return
	}

	jobLabel, ok := pod.Labels[constants.KubeflowJobLabel]
	if !ok {
		klog.V(2).InfoS("skip recovery for pod", "namespace", namespace, "pod", name, "reason", "missing job label")
		return
	}

	if pod.Spec.RestartPolicy == corev1.RestartPolicyNever {
		klog.V(2).InfoS("skip recovery for pod", "namespace", namespace, "pod", name, "reason", "restartPolicy is Never")
		return
	}
	restartAllowed, err := r.allowJobRestart(ctx, namespace, jobLabel)
	if err != nil {
		klog.ErrorS(err, "failed to determine whether job restart is allowed", "namespace", namespace, "job", jobLabel)
		return
	}
	if !restartAllowed {
		return
	}

	klog.V(2).InfoS("trigger pod recovery restart", "namespace", namespace, "job", jobLabel, "pod", name)
	r.restartJob(ctx, namespace, jobLabel)
}

func (r *RecoveryController) isRecoveryEnabledForPod(ctx context.Context, pod *corev1.Pod) (bool, error) {
	if pod.Labels[constants.EnabledRecoveryLabel] == constants.True {
		return true, nil
	}

	labels, err := getPodRelatedJobLabels(ctx, r.client, pod)
	if err != nil {
		return false, err
	}

	return labels[constants.EnabledRecoveryLabel] == constants.True, nil
}

func (r *RecoveryController) allowJobRestart(ctx context.Context, namespace, jobLabel string) (bool, error) {
	restartAllowed, lastRestartAt, err := r.jobRestartLedger.allowRestart(ctx, namespace, jobLabel, r.restartDuration)
	if err != nil {
		return false, err
	}
	if !restartAllowed {
		klog.V(2).InfoS("skip restart for job", "namespace", namespace, "job", jobLabel, "lastRestartAt", lastRestartAt, "retryWindow", r.restartDuration)
	}

	return restartAllowed, nil
}

func (r *RecoveryController) restartJob(ctx context.Context, namespace, name string) {
	ctx, cancel := kube.WithRequestTimeout(ctx)
	defer cancel()

	err := r.client.CoreV1().Pods(namespace).DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", constants.KubeflowJobLabel, name),
	})
	if err != nil {
		klog.ErrorS(err, "failed to restart job", "namespace", namespace, "job", name)
	} else {
		klog.InfoS("restarted job", "namespace", namespace, "job", name)
	}
}

type nsName struct {
	ns   string
	name string
}

func (r *RecoveryController) ensureNodeUnschedulable(ctx context.Context, name string) bool {
	ctx, cancel := kube.WithRequestTimeout(ctx)
	defer cancel()

	if err := kube.TaintNodeUnschedulable(ctx, r.client, name); err != nil {
		klog.ErrorS(err, "failed to mark node unschedulable", "node", name)
		return false
	}

	klog.InfoS("marked node unschedulable", "node", name, "taint", "NoSchedule")
	return true
}

func (r *RecoveryController) onNodeError(ctx context.Context, e events.Event) {
	klog.V(2).InfoS("handle node error", "node", e.Name, "reason", e.Reason)
	requestCtx, cancel := kube.WithRequestTimeout(ctx)
	node, err := r.client.CoreV1().Nodes().Get(requestCtx, e.Name, metav1.GetOptions{})
	cancel()
	if err != nil {
		klog.ErrorS(err, "failed to get node", "node", e.Name)
		return
	}

	if node.Spec.Unschedulable {
		r.ensureNodeUnschedulable(ctx, e.Name)
		return
	}

	if e.Reason == events.Day2EventReason {
		klog.V(2).InfoS("skip job recovery for day2 event", "node", e.Name, "reason", e.Reason)
		r.ensureNodeUnschedulable(ctx, e.Name)
		return
	}

	jobs, err := r.listJobsOnNode(ctx, e.Name)
	if err != nil {
		klog.ErrorS(err, "failed to list jobs on node", "node", e.Name)
		return
	}

	for _, job := range jobs {
		r.onPodError(ctx, job.ns, job.name)
	}

	r.ensureNodeUnschedulable(ctx, e.Name)
}

func (r *RecoveryController) listJobsOnNode(ctx context.Context, nodeName string) ([]nsName, error) {
	// TODO: traner v2 & lws?
	ctx, cancel := kube.WithRequestTimeout(ctx)
	defer cancel()

	pods, err := r.client.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		LabelSelector: constants.KubeflowJobLabel,
		FieldSelector: fmt.Sprintf("spec.nodeName=%s", nodeName),
	})
	if err != nil {
		return nil, err
	}

	jobs := make(map[nsName]struct{})
	for _, pod := range pods.Items {
		jobLabel, ok := pod.Labels[constants.KubeflowJobLabel]
		if !ok {
			continue
		}

		jobs[nsName{ns: pod.Namespace, name: jobLabel}] = struct{}{}
	}

	items := make([]nsName, 0, len(jobs))
	for job := range jobs {
		items = append(items, job)
	}

	return items, nil
}

func (r *RecoveryController) onPreflightReport(ctx context.Context, report *kcoverv1alpha1.PreflightReport) {
	klog.V(2).InfoS("handle PreflightReport", "namespace", report.Namespace, "name", report.Name, "node", report.Spec.NodeName, "workload", report.Spec.WorkloadName, "reportBytes", len(report.Spec.Report))

	result, err := r.preflight.handleReport(report)
	if err != nil {
		var timeoutErr preflight.WorkloadTimeoutError
		if errors.As(err, &timeoutErr) {
			r.handlePreflightTimeout(timeoutErr)
			return
		}
		klog.ErrorS(err, "failed to aggregate PreflightReport", "namespace", report.Namespace, "name", report.Name, "node", report.Spec.NodeName)
		return
	}

	if result.skipped && result.workloadName == "" {
		klog.V(2).InfoS("skip PreflightReport", "namespace", report.Namespace, "name", report.Name, "node", report.Spec.NodeName, "reason", "collector unavailable or workload name missing")
		return
	}

	if result.waiting {
		klog.V(2).InfoS("buffer PreflightReport", "namespace", report.Namespace, "workload", result.workloadName, "state", "waiting")
		return
	}
	if len(result.slowNodes) == 0 {
		klog.InfoS("preflight report finished without slow nodes", "namespace", report.Namespace, "workload", result.workloadName)
		return
	}

	klog.InfoS("preflight report finished with slow nodes", "namespace", report.Namespace, "workload", result.workloadName, "slowNodes", result.slowNodes)

	for _, node := range result.slowNodes {
		klog.V(2).InfoS("preflight marked slow node", "node", node, "namespace", report.Namespace, "workload", result.workloadName)
		r.ensureNodeUnschedulable(ctx, node)
	}
}

func (r *RecoveryController) sweepExpiredPreflightReports() {
	for _, err := range r.preflight.sweepExpired() {
		r.handlePreflightTimeout(err)
	}
}

func (r *RecoveryController) onEvent(ctx context.Context, e events.Event) {
	klog.V(4).InfoS("recovery controller received event", "event", e)
	switch e.ResourceType {
	case events.Pod:
		if e.EventType == events.Error {
			klog.V(2).InfoS("dispatch pod event", "namespace", e.Namespace, "pod", e.Name)
			r.onPodError(ctx, e.Namespace, e.Name)
		}
	case events.Node:
		klog.V(2).InfoS("dispatch node event", "node", e.Name)
		r.onNodeError(ctx, e)
	default:
		klog.ErrorS(nil, "unsupported event resource type", "resourceType", e.ResourceType)
	}
}

func (r *RecoveryController) Start(parent context.Context) error {
	if r.eventStream == nil {
		return fmt.Errorf("event stream cannot be nil")
	}
	ctx, cancel := context.WithCancel(parent)
	r.cancel = cancel
	r.doneCh = make(chan struct{})

	go func(ctx context.Context) {
		defer close(r.doneCh)
		ticker := time.NewTicker(r.preflightSweepInterval)
		defer ticker.Stop()
		eventCh := r.eventStream.EventChan()
		reportCh := r.reportCh
		for {
			select {
			case <-ctx.Done():
				return

			case <-ticker.C:
				r.sweepExpiredPreflightReports()

			case e, ok := <-eventCh:
				if !ok {
					klog.InfoS("recovery event stream closed")
					return
				}
				r.onEvent(ctx, e)

			case report, ok := <-reportCh:
				if !ok {
					reportCh = nil
					continue
				}
				r.onPreflightReport(ctx, report)
			}
		}

	}(ctx)

	klog.InfoS("recovery controller started")
	return nil
}

func (r *RecoveryController) Stop() {
	if r.cancel != nil {
		r.cancel()
	}
	if r.doneCh != nil {
		<-r.doneCh
	}
}
