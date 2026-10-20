package recovery

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/baizeai/kcover/pkg/constants"
	"github.com/baizeai/kcover/pkg/events"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func TestNodeErrorRestartsJobsOnFaultyNode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		pods []*corev1.Pod
		want map[string]int
	}{
		{
			name: "pod names differ from job names",
			pods: []*corev1.Pod{
				nodeRecoveryPod("train", "job-a-worker-0", "job-a", "node-a", true, corev1.RestartPolicyAlways),
				nodeRecoveryPod("train", "job-b-worker-0", "job-b", "node-b", true, corev1.RestartPolicyAlways),
				nodeRecoveryPod("train", "ordinary-pod", "", "node-a", true, corev1.RestartPolicyAlways),
			},
			want: map[string]int{"train/job-a": 1},
		},
		{
			name: "multiple pods restart the same job once",
			pods: []*corev1.Pod{
				nodeRecoveryPod("train", "job-a-worker-0", "job-a", "node-a", true, corev1.RestartPolicyAlways),
				nodeRecoveryPod("train", "job-a-worker-1", "job-a", "node-a", true, corev1.RestartPolicyAlways),
			},
			want: map[string]int{"train/job-a": 1},
		},
		{
			name: "same job name in different namespaces",
			pods: []*corev1.Pod{
				nodeRecoveryPod("train-a", "job-worker-0", "job", "node-a", true, corev1.RestartPolicyAlways),
				nodeRecoveryPod("train-b", "job-worker-0", "job", "node-a", true, corev1.RestartPolicyAlways),
			},
			want: map[string]int{"train-a/job": 1, "train-b/job": 1},
		},
		{
			name: "eligible pod follows disabled and never pods",
			pods: []*corev1.Pod{
				nodeRecoveryPod("train", "job-disabled", "job", "node-a", false, corev1.RestartPolicyAlways),
				nodeRecoveryPod("train", "job-never", "job", "node-a", true, corev1.RestartPolicyNever),
				nodeRecoveryPod("train", "job-worker", "job", "node-a", true, corev1.RestartPolicyAlways),
			},
			want: map[string]int{"train/job": 1},
		},
		{
			name: "eligible pod precedes never and disabled pods",
			pods: []*corev1.Pod{
				nodeRecoveryPod("train", "job-worker", "job", "node-a", true, corev1.RestartPolicyAlways),
				nodeRecoveryPod("train", "job-never", "job", "node-a", true, corev1.RestartPolicyNever),
				nodeRecoveryPod("train", "job-disabled", "job", "node-a", false, corev1.RestartPolicyAlways),
			},
			want: map[string]int{"train/job": 1},
		},
		{
			name: "only ineligible pods",
			pods: []*corev1.Pod{
				nodeRecoveryPod("train", "job-disabled", "job", "node-a", false, corev1.RestartPolicyAlways),
				nodeRecoveryPod("train", "job-never", "job", "node-a", true, corev1.RestartPolicyNever),
				nodeRecoveryPod("train", "other-worker", "other", "node-b", true, corev1.RestartPolicyAlways),
			},
			want: map[string]int{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			objects := []runtime.Object{
				&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}},
				&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-b"}},
			}
			for _, pod := range tt.pods {
				objects = append(objects, pod)
			}
			client := fake.NewSimpleClientset(objects...)
			// The fake client does not apply field selectors itself.
			client.PrependReactor("list", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
				list := action.(ktesting.ListAction)
				restrictions := list.GetListRestrictions()
				if restrictions.Fields.String() != "spec.nodeName=node-a" {
					t.Fatalf("pod field selector = %q, want spec.nodeName=node-a", restrictions.Fields)
				}
				pods := &corev1.PodList{}
				for _, pod := range tt.pods {
					if list.GetNamespace() != "" && pod.Namespace != list.GetNamespace() {
						continue
					}
					if restrictions.Fields.Matches(fields.Set{"spec.nodeName": pod.Spec.NodeName}) &&
						restrictions.Labels.Matches(labels.Set(pod.Labels)) {
						pods.Items = append(pods.Items, *pod.DeepCopy())
					}
				}
				return true, pods, nil
			})
			restarts := map[string]int{}
			client.PrependReactor("delete-collection", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
				selector := action.(ktesting.DeleteCollectionAction).GetListRestrictions().Labels
				job, ok := selector.RequiresExactMatch(constants.KubeflowJobLabel)
				if !ok {
					t.Fatalf("pod deletion selector %q does not select a job", selector)
				}
				restarts[action.GetNamespace()+"/"+job]++
				// Keep pods available so the restart ledger must deduplicate subsequent pods.
				return true, nil, nil
			})

			controller := NewController(client, nil, nil, 0, 0)
			controller.jobRestartLedger.now = func() time.Time { return time.Unix(100, 0) }
			controller.onEvent(context.Background(), events.Event{
				ResourceType: events.Node,
				Name:         "node-a",
				EventType:    events.Error,
			})

			if !reflect.DeepEqual(restarts, tt.want) {
				t.Errorf("job restarts = %v, want %v", restarts, tt.want)
			}
			assertNodeUnschedulable(t, client, "node-a", true)
			assertNodeUnschedulable(t, client, "node-b", false)
		})
	}
}

func nodeRecoveryPod(namespace, name, job, node string, enabled bool, policy corev1.RestartPolicy) *corev1.Pod {
	labels := map[string]string{}
	if job != "" {
		labels[constants.KubeflowJobLabel] = job
	}
	if enabled {
		labels[constants.EnabledRecoveryLabel] = constants.True
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name, Labels: labels},
		Spec:       corev1.PodSpec{NodeName: node, RestartPolicy: policy},
	}
}
