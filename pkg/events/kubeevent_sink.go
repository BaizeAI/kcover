package events

import (
	"context"
	"fmt"

	"github.com/baizeai/kcover/pkg/constants"
	"github.com/baizeai/kcover/pkg/kube"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/tools/reference"
	"k8s.io/klog/v2"
)

// KubeEventSink records internal recovery events as Kubernetes Events.
type KubeEventSink struct {
	client   kubernetes.Interface
	recorder record.EventRecorder
}

const (
	standardEventReason  = "Error"
	preflightEventReason = "PreflightReportAvailable"
	Day2EventReason      = "Day2CheckFailed"
)

func NewKubeEventSink(cli kubernetes.Interface) *KubeEventSink {
	return &KubeEventSink{
		client:   cli,
		recorder: newEventRecorder(cli),
	}
}

func newEventRecorder(cli kubernetes.Interface) record.EventRecorder {
	eb := record.NewBroadcaster()
	eb.StartRecordingToSink(&v1.EventSinkImpl{
		Interface: cli.CoreV1().Events(""),
	})

	return eb.NewRecorder(runtime.NewScheme(), corev1.EventSource{Component: "kcover"})
}

func (sink *KubeEventSink) recordToPod(e Event) error {
	ctx, cancel := kube.WithRequestTimeout(context.Background())
	defer cancel()

	pod, err := sink.client.CoreV1().Pods(e.Namespace).Get(ctx, e.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	return sink.recordEvent(pod, e)
}

func (sink *KubeEventSink) recordToNode(e Event) error {
	ctx, cancel := kube.WithRequestTimeout(context.Background())
	defer cancel()

	node, err := sink.client.CoreV1().Nodes().Get(ctx, e.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	return sink.recordEvent(node, e)
}

func (sink *KubeEventSink) recordEvent(obj runtime.Object, event Event) error {
	ref, err := reference.GetReference(scheme.Scheme, obj)
	if err != nil {
		return err
	}
	ensureEventNamespace(ref, event)

	if IsPreflightEvent(event.Annotations) {
		return sink.recordPreflightEvent(ref, event)
	}

	return sink.recordStdEvent(ref, event)
}

func ensureEventNamespace(ref *corev1.ObjectReference, event Event) {
	if ref == nil || ref.Namespace != "" || event.ResourceType != Node {
		return
	}

	// Kubernetes requires involvedObject.namespace to match the Event namespace,
	// even when the involved object itself is cluster-scoped like a Node.
	ref.Namespace = kube.CurrentNamespace()
}

func (sink *KubeEventSink) recordStdEvent(ref *corev1.ObjectReference, event Event) error {
	sink.recorder.AnnotatedEventf(ref, annotationsForEvent(event), corev1.EventTypeWarning, reasonForEvent(event), "%s", event.Message)
	klog.V(3).InfoS("record standard event", "kind", ref.Kind, "namespace", ref.Namespace, "name", ref.Name, "eventType", event.EventType)
	return nil
}

func (sink *KubeEventSink) recordPreflightEvent(ref *corev1.ObjectReference, event Event) error {
	if ref == nil {
		return fmt.Errorf("preflight event reference is nil")
	}

	evt := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "kcover-preflight-",
			Namespace:    kube.CurrentNamespace(),
			Annotations:  annotationsForEvent(event),
		},
		InvolvedObject: *ref,
		Reason:         preflightEventReason,
		Message:        preflightEventMessage(event),
		Type:           corev1.EventTypeNormal,
		Source:         corev1.EventSource{Component: "kcover"},
	}
	ctx, cancel := kube.WithRequestTimeout(context.Background())
	defer cancel()

	_, err := sink.client.CoreV1().Events(kube.CurrentNamespace()).Create(ctx, evt, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("create preflight event for %s: %w", ref.Name, err)
	}
	klog.V(3).InfoS("record preflight event", "eventNamespace", evt.Namespace, "involvedName", ref.Name, "reason", evt.Reason, "workload", event.Annotations[constants.PreflightWorkloadAnnotation], "report", event.Annotations[constants.PreflightReportAnnotation])

	return nil
}

func preflightEventMessage(event Event) string {
	workload := event.Annotations[constants.PreflightWorkloadAnnotation]
	report := event.Annotations[constants.PreflightReportAnnotation]
	if report != "" && workload != "" {
		return fmt.Sprintf("preflight report(%s) available for workload(%s) on node(%s)", report, workload, event.Name)
	}
	if workload == "" {
		return fmt.Sprintf("preflight report available for node(%s)", event.Name)
	}

	return fmt.Sprintf("preflight report available for workload(%s) on node(%s)", workload, event.Name)
}

func reasonForEvent(event Event) string {
	if event.Reason != "" {
		return event.Reason
	}
	return standardEventReason
}

func annotationsForEvent(event Event) map[string]string {
	annotations := make(map[string]string, len(event.Annotations)+1)
	for key, value := range event.Annotations {
		annotations[key] = value
	}

	if IsPreflightEvent(event.Annotations) {
		return annotations
	}

	if _, ok := annotations[constants.NeedRecoveryAnnotation]; !ok {
		annotations[constants.NeedRecoveryAnnotation] = constants.True
	}

	return annotations
}

func (sink *KubeEventSink) RecordEvent(e Event) error {
	var err error
	switch e.ResourceType {
	case Pod:
		err = sink.recordToPod(e)
	case Node:
		err = sink.recordToNode(e)
	default:
		return fmt.Errorf("unsupported target type: %s", e.ResourceType)
	}
	if err != nil {
		return err
	}

	return err
}
