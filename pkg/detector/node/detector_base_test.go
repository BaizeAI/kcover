//go:build !metax

package node

import (
	"fmt"
	"strings"
	"testing"

	kcoverconfig "github.com/baizeai/kcover/pkg/agentconfig"
	"k8s.io/client-go/kubernetes/fake"
)

func TestNewDetectorUsesNoopDetector(t *testing.T) {
	t.Parallel()

	instance, err := NewDetector("node-a", kcoverconfig.DefaultAgent(), fake.NewSimpleClientset(), stubSink{})
	if err != nil {
		t.Fatalf("NewDetector returned error: %v", err)
	}

	if got := fmt.Sprint(instance.(*detector).detector); !strings.Contains(got, "Noop") {
		t.Fatalf("detector name = %q, want Noop", got)
	}
}
