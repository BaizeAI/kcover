package recovery

import (
	"context"
	"fmt"
	"time"

	"github.com/samber/lo"

	"github.com/baizeai/kcover/pkg/constants"
	"github.com/baizeai/kcover/pkg/events"
	"github.com/jellydator/ttlcache/v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

type RecoveryController struct {
	client          kubernetes.Interface
	recorder        events.Recorder
	stop            chan struct{}
	restartDuration time.Duration
	restarts        *ttlcache.Cache[string, time.Time]
}

func NewRecoveryController(cli kubernetes.Interface, recorder events.Recorder) *RecoveryController {
	return &RecoveryController{
		client:          cli,
		recorder:        recorder,
		stop:            make(chan struct{}),
		restartDuration: time.Second * 30,
		restarts:        ttlcache.New[string, time.Time](),
	}
}

func (r *RecoveryController) onPodError(namespace, name string) {
	pod, err := r.client.CoreV1().Pods(namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		klog.Errorf("get pod %s/%s error events error: %v", namespace, name, err)
		return
	}
	if pod.Labels[constants.EnabledRecoveryLabel] != constants.True {
		ls, err := getPodRelatedJobLabels(r.client, pod)
		if err != nil {
			klog.Errorf("get pod %s/%s related job labels error: %v", namespace, name, err)
			return
		}
		if ls[constants.EnabledRecoveryLabel] != constants.True {
			klog.Infof("pod %s/%s or its owner job has no recovery label", namespace, name)
			return
		}
	}
	if jobLabel, ok := pod.Labels[constants.KubeflowJobLabel]; !ok {
		klog.Warningf("pod %s/%s has no job label", namespace, name)
		return
	} else {
		if pod.Spec.RestartPolicy == corev1.RestartPolicyNever {
			klog.Warningf("pod %s/%s has RestartPolicyNever, will not restart", namespace, name)
			return
		}
		key := fmt.Sprintf("%s/%s", namespace, jobLabel)
		tv := r.restarts.Get(key)
		if tv != nil {
			klog.Infof("job %s/%s has been restarted at %v, will not restart again in %v", namespace, jobLabel, tv.Value(), r.restartDuration)
			return
		}
		now := time.Now()
		r.restarts.Set(key, now, r.restartDuration) // only restart once in 60 seconds
		r.restartJob(context.Background(), namespace, jobLabel)
		go func() {
			<-time.After(r.restartDuration - time.Second)
			r.restarts.Delete(key) //
		}()
	}
}

func (r *RecoveryController) restartJob(ctx context.Context, namespace, name string) {
	err := r.client.CoreV1().Pods(namespace).DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", constants.KubeflowJobLabel, name),
	})
	if err != nil {
		klog.Errorf("restart job %s/%s error: %v", namespace, name, err)
	} else {
		klog.Infof("restart job %s/%s successfully", namespace, name)
	}
}

type nsName struct {
	ns   string
	name string
}

func (r *RecoveryController) onNodeError(name string) {
	node, err := r.client.CoreV1().Nodes().Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		klog.Errorf("get node %s error: %v", name, err)
		return
	}
	if node.Spec.Unschedulable {
		klog.Infof("the node %s status has been set to unschedulable", name)
		return
	}
	// query jobs
	pods, err := r.client.CoreV1().Pods("").List(context.Background(), metav1.ListOptions{
		LabelSelector: constants.KubeflowJobLabel,
		FieldSelector: fmt.Sprintf("spec.nodeName=%s", name),
	})
	if err != nil {
		klog.Errorf("fetch pods list for node %s error: %v", node, err)
		return
	}
	jobs := map[nsName]struct{}{}
	lo.ForEach(pods.Items, func(pod corev1.Pod, index int) {
		if jobLabel, ok := pod.Labels[constants.KubeflowJobLabel]; !ok {
			return
		} else {
			jobs[nsName{
				ns:   pod.Namespace,
				name: jobLabel,
			}] = struct{}{}
		}
	})
	lo.ForEach(lo.Keys(jobs), func(item nsName, index int) {
		r.onPodError(item.ns, item.name)
	})
	node.Spec.Unschedulable = true
	_, err = r.client.CoreV1().Nodes().Update(context.Background(), node, metav1.UpdateOptions{})
	if err != nil {
		klog.Errorf("update node %s to unschedulable error: %v", name, err)
	}
}

func (r *RecoveryController) onEvent(e events.CollectorEvent) {
	klog.Infof("recover controller received event: %+v", e)
	switch e.TargetType {
	case events.Pod:
		if e.EventType == events.Error {
			r.onPodError(e.Namespace, e.Name)
		}
	case events.Node:
		r.onNodeError(e.Name)
	default:
		klog.Errorf("unsupported target type: %s", e.TargetType)
	}
}

func (r *RecoveryController) Start() error {
	if r.recorder == nil {
		return fmt.Errorf("recorder is nil")
	}
	go func() {
		for e := range r.recorder.EventChan() {
			r.onEvent(e)
		}
	}()
	return nil
}

func (r *RecoveryController) Stop() {
	close(r.stop)
}
