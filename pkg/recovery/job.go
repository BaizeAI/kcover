package recovery

import (
	"context"
	"fmt"
	"time"

	"github.com/jellydator/ttlcache/v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes"
)

var ttlCache = ttlcache.New[string, map[string]string]()

func getPodRelatedJobLabels(cli kubernetes.Interface, pod *corev1.Pod) (map[string]string, error) {
	if len(pod.OwnerReferences) < 1 {
		return nil, fmt.Errorf("pod %s/%s has no owner", pod.Namespace, pod.Name)
	}

	owner := pod.OwnerReferences[0]
	if v := ttlCache.Get(string(owner.UID)); v != nil {
		return v.Value(), nil
	}

	var resource string
	switch owner.Kind {
	case "PyTorchJob":
		resource = "pytorchjobs"
	case "TFJob":
		resource = "tfjobs"
	}

	un := unstructured.Unstructured{}
	err := cli.Discovery().RESTClient().Get().
		AbsPath(fmt.Sprintf("/apis/%s/namespaces/%s/%s/%s", owner.APIVersion, pod.Namespace, resource, owner.Name)).
		Do(context.Background()).Into(&un)
	if err != nil {
		return nil, err
	}

	ls := un.GetLabels()
	ttlCache.Set(string(owner.UID), ls, time.Second*30)

	return ls, nil
}
