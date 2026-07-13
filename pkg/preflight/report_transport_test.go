package preflight

import (
	"context"
	"errors"
	"testing"
	"time"

	kcoverv1alpha1 "github.com/baizeai/kcover/pkg/apis/kcover/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
)

const transportTestPayload = `{"workload_size":2,"rank":0,"node_name":"node-a","node_ip":"10.0.0.1","gpu_check":1,"storage_check":1,"batches":[]}`

func TestBuildPreflightReportUsesDeterministicNameAndOwner(t *testing.T) {
	t.Parallel()

	owner := metav1.OwnerReference{APIVersion: "v1", Kind: "Pod", Name: "worker-0", UID: types.UID("pod-uid")}
	observedAt := time.Unix(100, 0)
	first, err := BuildPreflightReport("train-ns", "node-a", "job-a", "job-uid", transportTestPayload, observedAt, owner)
	if err != nil {
		t.Fatalf("BuildPreflightReport() error = %v", err)
	}
	second, err := BuildPreflightReport("train-ns", "node-a", "job-a", "job-uid", transportTestPayload, observedAt, owner)
	if err != nil {
		t.Fatalf("BuildPreflightReport() second error = %v", err)
	}

	if first.Name == "" || first.Name != second.Name {
		t.Fatalf("delivery names = %q, %q, want same non-empty name", first.Name, second.Name)
	}
	differentRun, err := BuildPreflightReport("train-ns", "node-a", "job-a", "new-job-uid", transportTestPayload, observedAt, owner)
	if err != nil {
		t.Fatalf("BuildPreflightReport() for different workload UID error = %v", err)
	}
	if differentRun.Name == first.Name {
		t.Fatalf("different workload UIDs produced the same report name %q", first.Name)
	}
	if len(first.OwnerReferences) != 1 || first.OwnerReferences[0].UID != owner.UID {
		t.Fatalf("delivery ownerReferences = %v, want pod owner", first.OwnerReferences)
	}
	if first.Spec.WorkloadUID != "job-uid" || first.Spec.Rank != 0 || !first.Spec.ObservedAt.Time.Equal(observedAt) {
		t.Fatalf("report = %+v, want rank 0 and observedAt", first)
	}
}

func TestKubeReportSinkCreatesImmutablePreflightReport(t *testing.T) {
	t.Parallel()

	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	sink := NewKubeReportSink(client)
	report, err := BuildPreflightReport("train-ns", "node-a", "job-a", "job-uid", transportTestPayload, time.Unix(100, 0))
	if err != nil {
		t.Fatalf("BuildPreflightReport() error = %v", err)
	}

	created, err := sink.WriteReport(report)
	if err != nil || !created {
		t.Fatalf("WriteReport() error = %v", err)
	}
	created, err = sink.WriteReport(report)
	if err != nil || created {
		t.Fatalf("WriteReport() duplicate error = %v", err)
	}

	stored, err := client.Resource(PreflightReportGVR).Namespace("train-ns").Get(context.Background(), report.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get(PreflightReport) error = %v", err)
	}
	delivery, err := reportFromObject(stored)
	if err != nil {
		t.Fatalf("reportFromObject() error = %v", err)
	}
	if delivery.Spec.WorkloadName != "job-a" || delivery.Spec.NodeName != "node-a" || delivery.Spec.Report != transportTestPayload {
		t.Fatalf("stored delivery = %+v, want original report", delivery)
	}
}

func TestKubeReportStreamListsExistingReport(t *testing.T) {
	t.Parallel()

	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{PreflightReportGVR: "PreflightReportList"},
	)
	sink := NewKubeReportSink(client)
	report, err := BuildPreflightReport("train-ns", "node-a", "job-a", "job-uid", transportTestPayload, time.Unix(100, 0))
	if err != nil {
		t.Fatalf("BuildPreflightReport() error = %v", err)
	}
	if _, err := sink.WriteReport(report); err != nil {
		t.Fatalf("WriteReport() error = %v", err)
	}

	stream := NewKubeReportStream(client)
	if err := stream.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer stream.Stop()

	select {
	case got := <-stream.Reports():
		if got.Name != report.Name || got.Spec.Report != report.Spec.Report {
			t.Fatalf("stream report = %+v, want %+v", got, report)
		}
	case <-time.After(time.Second):
		t.Fatal("existing PreflightReport was not listed")
	}
}

func TestKubeReportStreamFailsWhenInitialListDoesNotSync(t *testing.T) {
	t.Parallel()

	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{PreflightReportGVR: "PreflightReportList"},
	)
	client.PrependReactor("list", "preflightreports", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("API unavailable")
	})
	stream := NewKubeReportStream(client)
	stream.syncTimeout = 50 * time.Millisecond

	if err := stream.Start(context.Background()); err == nil {
		t.Fatal("Start() error = nil when informer cannot sync")
	}
	stream.Stop()
}

func TestReportFromObjectRejectsInvalidPayloadAndMetadataMismatch(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*kcoverv1alpha1.PreflightReport){
		"invalid payload": func(report *kcoverv1alpha1.PreflightReport) {
			report.Spec.Report = "not-json"
		},
		"node mismatch": func(report *kcoverv1alpha1.PreflightReport) {
			report.Spec.NodeName = "node-b"
		},
		"rank mismatch": func(report *kcoverv1alpha1.PreflightReport) {
			report.Spec.Rank = 1
		},
		"workload mismatch": func(report *kcoverv1alpha1.PreflightReport) {
			report.Spec.Report = `{"workload":"other-job","workload_size":2,"rank":0,"node_name":"node-a","node_ip":"10.0.0.1","gpu_check":1,"storage_check":1,"batches":[]}`
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			report, err := BuildPreflightReport("train-ns", "node-a", "job-a", "job-uid", transportTestPayload, time.Unix(100, 0))
			if err != nil {
				t.Fatalf("BuildPreflightReport() error = %v", err)
			}
			mutate(report)
			content, err := runtime.DefaultUnstructuredConverter.ToUnstructured(report)
			if err != nil {
				t.Fatalf("ToUnstructured() error = %v", err)
			}
			if _, err := reportFromObject(&unstructured.Unstructured{Object: content}); err == nil {
				t.Fatal("reportFromObject() error = nil, want invalid report rejected")
			}
		})
	}
}
