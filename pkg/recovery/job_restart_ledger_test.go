package recovery

import (
	"context"
	"testing"
	"time"

	"github.com/baizeai/kcover/pkg/constants"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestJobRestartLedgerPersistsRestartWindowAcrossControllers(t *testing.T) {
	t.Parallel()

	client := fake.NewSimpleClientset()
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	first := NewController(client, nil, nil, 0, 0)
	second := NewController(client, nil, nil, 0, 0)
	first.jobRestartLedger.now = func() time.Time { return now }
	second.jobRestartLedger.now = func() time.Time { return now.Add(10 * time.Second) }

	restartAllowed, err := first.allowJobRestart(context.Background(), "train-ns", "job-a")
	if err != nil {
		t.Fatalf("first allowJobRestart() error = %v", err)
	}
	if !restartAllowed {
		t.Fatal("first allowJobRestart() = false, want true")
	}

	restartAllowed, err = second.allowJobRestart(context.Background(), "train-ns", "job-a")
	if err != nil {
		t.Fatalf("second allowJobRestart() error = %v", err)
	}
	if restartAllowed {
		t.Fatal("second allowJobRestart() = true after controller restart, want false")
	}

	configMap, err := client.CoreV1().ConfigMaps("default").Get(context.Background(), constants.JobRestartLedgerName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get(job restart ledger) error = %v", err)
	}
	if len(configMap.Data) != 1 {
		t.Fatalf("len(job restart ledger data) = %d, want 1", len(configMap.Data))
	}
}

func TestJobRestartLedgerAllowsRestartAfterRetryWindow(t *testing.T) {
	t.Parallel()

	client := fake.NewSimpleClientset()
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	controller := NewController(client, nil, nil, 0, 0)
	controller.jobRestartLedger.now = func() time.Time { return now }

	restartAllowed, err := controller.allowJobRestart(context.Background(), "train-ns", "job-a")
	if err != nil || !restartAllowed {
		t.Fatalf("first allowJobRestart() = %v, %v, want true, nil", restartAllowed, err)
	}

	controller.jobRestartLedger.now = func() time.Time { return now.Add(controller.restartDuration) }
	restartAllowed, err = controller.allowJobRestart(context.Background(), "train-ns", "job-a")
	if err != nil || !restartAllowed {
		t.Fatalf("allowJobRestart() after retry window = %v, %v, want true, nil", restartAllowed, err)
	}
}
