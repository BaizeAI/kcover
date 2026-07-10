package recovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/baizeai/kcover/pkg/constants"
	"github.com/baizeai/kcover/pkg/kube"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
)

type recoveryLedger struct {
	client    kubernetes.Interface
	namespace string
	now       func() time.Time
}

func newRecoveryLedger(client kubernetes.Interface, namespace string) *recoveryLedger {
	return &recoveryLedger{
		client:    client,
		namespace: namespace,
		now:       time.Now,
	}
}

func recoveryLedgerKey(namespace, jobName string) string {
	sum := sha256.Sum256([]byte(namespace + "\x00" + jobName))
	return hex.EncodeToString(sum[:])
}

func (l *recoveryLedger) allowRestart(ctx context.Context, namespace, jobName string, ttl time.Duration) (bool, time.Time, error) {
	key := recoveryLedgerKey(namespace, jobName)
	now := l.now().UTC()
	restartAllowed := false
	lastRestartAt := time.Time{}

	err := retry.OnError(retry.DefaultRetry, func(err error) bool {
		return apierrors.IsConflict(err) || apierrors.IsAlreadyExists(err)
	}, func() error {
		restartAllowed = false
		lastRestartAt = time.Time{}

		requestCtx, cancel := kube.WithRequestTimeout(ctx)
		defer cancel()

		configMap, err := l.client.CoreV1().ConfigMaps(l.namespace).Get(requestCtx, constants.RecoveryLedgerName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			configMap = &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: constants.RecoveryLedgerName, Namespace: l.namespace},
				Data:       map[string]string{key: now.Format(time.RFC3339Nano)},
			}
			_, err = l.client.CoreV1().ConfigMaps(l.namespace).Create(requestCtx, configMap, metav1.CreateOptions{})
			if err == nil {
				restartAllowed = true
			}
			return err
		}
		if err != nil {
			return err
		}

		if configMap.Data == nil {
			configMap.Data = make(map[string]string)
		}
		for existingKey, value := range configMap.Data {
			restartAt, parseErr := time.Parse(time.RFC3339Nano, value)
			if parseErr != nil || !now.Before(restartAt.Add(ttl)) {
				delete(configMap.Data, existingKey)
			}
		}

		if value, exists := configMap.Data[key]; exists {
			lastRestartAt, err = time.Parse(time.RFC3339Nano, value)
			return err
		}

		configMap.Data[key] = now.Format(time.RFC3339Nano)
		_, err = l.client.CoreV1().ConfigMaps(l.namespace).Update(requestCtx, configMap, metav1.UpdateOptions{})
		if err == nil {
			restartAllowed = true
		}
		return err
	})

	return restartAllowed, lastRestartAt, err
}
