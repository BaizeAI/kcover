package preflight

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	kcoverv1a1 "github.com/baizeai/kcover/pkg/apis/kcover/v1alpha1"
	"github.com/baizeai/kcover/pkg/kube"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicinformer "k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

var PreflightReportGVR = schema.GroupVersionResource{
	Group: kcoverv1a1.Group, Version: kcoverv1a1.Version, Resource: "preflightreports",
}

// ReportSink performs one persistence attempt for a PreflightReport. Retry,
// concurrency, and lifecycle management belong to ReportPublisher.
type ReportSink interface {
	WriteReport(*kcoverv1a1.PreflightReport) (bool, error)
}

// KubeReportSink implements ReportSink through the Kubernetes API. It reports
// whether the call created the resource so the publisher emits one observation.
type KubeReportSink struct {
	client dynamic.Interface
}

func NewKubeReportSink(client dynamic.Interface) *KubeReportSink {
	return &KubeReportSink{client: client}
}

func BuildPreflightReport(namespace, nodeName, workloadName, workloadUID, reportText string,
	observedAt time.Time, owners ...metav1.OwnerReference) (*kcoverv1a1.PreflightReport, error) {
	if namespace == "" {
		return nil, fmt.Errorf("preflight namespace is empty")
	}
	if workloadName == "" {
		return nil, fmt.Errorf("preflight workload name is empty")
	}
	if workloadUID == "" {
		return nil, fmt.Errorf("preflight workload UID is empty")
	}
	if nodeName == "" {
		return nil, fmt.Errorf("preflight node name is empty")
	}

	report, err := parseReport(reportText)
	if err != nil {
		return nil, fmt.Errorf("parse preflight report: %w", err)
	}
	if observedAt.IsZero() {
		return nil, fmt.Errorf("preflight observation time is empty")
	}
	identity := reportIdentity(namespace, workloadUID, nodeName, report.Rank, reportText)
	sum := sha256.Sum256([]byte(identity))

	return &kcoverv1a1.PreflightReport{
		TypeMeta:   metav1.TypeMeta{APIVersion: kcoverv1a1.Group + "/" + kcoverv1a1.Version, Kind: "PreflightReport"},
		ObjectMeta: metav1.ObjectMeta{Name: "preflight-" + hex.EncodeToString(sum[:20]), Namespace: namespace, OwnerReferences: owners},
		Spec: kcoverv1a1.PreflightReportSpec{
			WorkloadName: workloadName,
			WorkloadUID:  workloadUID,
			NodeName:     nodeName,
			Rank:         int32(report.Rank),
			Report:       reportText,
			ObservedAt:   metav1.NewTime(observedAt.UTC()),
		},
	}, nil
}

func (s *KubeReportSink) WriteReport(report *kcoverv1a1.PreflightReport) (bool, error) {
	if s == nil || s.client == nil {
		return false, fmt.Errorf("preflight report client is nil")
	}

	if report == nil {
		return false, fmt.Errorf("preflight report is nil")
	}
	content, err := runtime.DefaultUnstructuredConverter.ToUnstructured(report)
	if err != nil {
		return false, fmt.Errorf("convert PreflightReport %s/%s: %w", report.Namespace, report.Name, err)
	}
	object := &unstructured.Unstructured{Object: content}

	ctx, cancel := kube.WithRequestTimeout(context.Background())
	defer cancel()
	_, err = s.client.Resource(PreflightReportGVR).Namespace(report.Namespace).Create(ctx, object, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("create PreflightReport %s/%s: %w", report.Namespace, report.Name, err)
	}
	return true, nil
}

// KubeReportWatcher watches PreflightReport resources and exposes validated
// reports through a process-local channel.
type KubeReportWatcher struct {
	client      dynamic.Interface
	reportCh    chan *kcoverv1a1.PreflightReport
	syncTimeout time.Duration
	cancel      context.CancelFunc
	doneCh      chan struct{}
}

const defaultReportSyncTimeout = 30 * time.Second

func NewKubeReportWatcher(client dynamic.Interface) *KubeReportWatcher {
	return &KubeReportWatcher{
		client:      client,
		reportCh:    make(chan *kcoverv1a1.PreflightReport, 128),
		syncTimeout: defaultReportSyncTimeout,
	}
}

func (s *KubeReportWatcher) Start(parent context.Context) error {
	if s.client == nil {
		return fmt.Errorf("preflight report client is nil")
	}

	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	s.doneCh = make(chan struct{})
	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(s.client, 0, metav1.NamespaceAll, nil)
	informer := factory.ForResource(PreflightReportGVR).Informer()
	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) { s.forward(ctx, obj) },
	})
	if err != nil {
		cancel()
		return err
	}

	go func() {
		defer close(s.doneCh)
		informer.Run(ctx.Done())
	}()

	syncCtx, syncCancel := context.WithTimeout(ctx, s.syncTimeout)
	defer syncCancel()
	if !cache.WaitForCacheSync(syncCtx.Done(), informer.HasSynced) {
		cancel()
		<-s.doneCh
		return fmt.Errorf("sync PreflightReport informer within %s", s.syncTimeout)
	}
	return nil
}

func (s *KubeReportWatcher) forward(ctx context.Context, obj any) {
	report, err := reportFromObject(obj)
	if err != nil {
		klog.ErrorS(err, "ignore invalid PreflightReport")
		return
	}

	select {
	case s.reportCh <- report:
	case <-ctx.Done():
	}
}

func reportFromObject(obj any) (*kcoverv1a1.PreflightReport, error) {
	resource, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return nil, fmt.Errorf("unexpected PreflightReport object %T", obj)
	}
	report := &kcoverv1a1.PreflightReport{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(resource.Object, report); err != nil {
		return nil, fmt.Errorf("convert PreflightReport %s/%s: %w", resource.GetNamespace(), resource.GetName(), err)
	}
	if err := validateReport(report); err != nil {
		return nil, fmt.Errorf("invalid PreflightReport %s/%s: %w", resource.GetNamespace(), resource.GetName(), err)
	}
	return report, nil
}

func validateReport(report *kcoverv1a1.PreflightReport) error {
	switch {
	case report == nil:
		return fmt.Errorf("report is nil")
	case report.Name == "":
		return fmt.Errorf("metadata.name is empty")
	case report.Namespace == "":
		return fmt.Errorf("metadata.namespace is empty")
	case report.Spec.WorkloadName == "":
		return fmt.Errorf("spec.workloadName is empty")
	case report.Spec.WorkloadUID == "":
		return fmt.Errorf("spec.workloadUID is empty")
	case report.Spec.NodeName == "":
		return fmt.Errorf("spec.nodeName is empty")
	case report.Spec.Report == "":
		return fmt.Errorf("spec.report is empty")
	case report.Spec.ObservedAt.IsZero():
		return fmt.Errorf("spec.observedAt is empty")
	}

	payload, err := parseReport(report.Spec.Report)
	if err != nil {
		return fmt.Errorf("spec.report is invalid: %w", err)
	}
	if payload.NodeName != report.Spec.NodeName {
		return fmt.Errorf("spec.nodeName %q does not match payload node_name %q", report.Spec.NodeName, payload.NodeName)
	}
	if payload.Rank != int(report.Spec.Rank) {
		return fmt.Errorf("spec.rank %d does not match payload rank %d", report.Spec.Rank, payload.Rank)
	}
	if payload.Workload != "" && payload.Workload != report.Spec.WorkloadName {
		return fmt.Errorf("spec.workloadName %q does not match payload workload %q", report.Spec.WorkloadName, payload.Workload)
	}
	return nil
}

func (s *KubeReportWatcher) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.doneCh != nil {
		<-s.doneCh
	}
	close(s.reportCh)
}

func (s *KubeReportWatcher) Reports() <-chan *kcoverv1a1.PreflightReport {
	return s.reportCh
}
