package preflight

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func ExampleSlowNodeAggregator_AddReport_allSkipReportsProduceAllSlowNodes() {
	aggregator := NewSlowNodeAggregator(0)
	reports := []string{
		`{"version": 1, "workload": "preflight8-node-0", "workload_size": 4, "rank": 1, "node_name": "c500-worker1", "node_ip": "10.107.204.141", "storage_check": 1, "gpu_check": 1, "node_check_busbw_threshold_gbps": "5", "batches": [{"schema": "v3", "batch_idx": 0, "pair": ["10.107.204.141", "10.107.204.142"], "self_ip": "10.107.204.141", "status": "skip"}, {"schema": "v3", "batch_idx": 1, "pair": ["10.107.204.141", "10.107.204.143"], "self_ip": "10.107.204.141", "status": "skip"}, {"schema": "v3", "batch_idx": 2, "pair": ["10.107.204.141", "10.107.204.146"], "self_ip": "10.107.204.141", "status": "skip"}]}`,
		`{"version": 1, "workload": "preflight8-node-0", "workload_size": 4, "rank": 2, "node_name": "c500-worker2", "node_ip": "10.107.204.142", "storage_check": 1, "gpu_check": 1, "node_check_busbw_threshold_gbps": "5", "batches": [{"schema": "v3", "batch_idx": 0, "pair": ["10.107.204.141", "10.107.204.142"], "self_ip": "10.107.204.142", "status": "skip", "reason": "gid_index_mismatch"}, {"schema": "v3", "batch_idx": 1, "pair": ["10.107.204.142", "10.107.204.146"], "self_ip": "10.107.204.142", "status": "skip", "reason": "gid_index_mismatch"}, {"schema": "v3", "batch_idx": 2, "pair": ["10.107.204.142", "10.107.204.143"], "self_ip": "10.107.204.142", "status": "skip", "reason": "gid_index_mismatch"}]}`,
		`{"version": 1, "workload": "preflight8-node-0", "workload_size": 4, "rank": 3, "node_name": "c500-worker3", "node_ip": "10.107.204.143", "storage_check": 1, "gpu_check": 1, "node_check_busbw_threshold_gbps": "5", "batches": [{"schema": "v3", "batch_idx": 0, "pair": ["10.107.204.143", "10.107.204.146"], "self_ip": "10.107.204.143", "status": "skip"}, {"schema": "v3", "batch_idx": 1, "pair": ["10.107.204.141", "10.107.204.143"], "self_ip": "10.107.204.143", "status": "skip"}, {"schema": "v3", "batch_idx": 2, "pair": ["10.107.204.142", "10.107.204.143"], "self_ip": "10.107.204.143", "status": "skip"}]}`,
		`{"version": 1, "workload": "preflight8-node-0", "workload_size": 4, "rank": 0, "node_name": "c500-worker4", "node_ip": "10.107.204.146", "storage_check": 1, "gpu_check": 1, "node_check_busbw_threshold_gbps": "5", "batches": [{"schema": "v3", "batch_idx": 0, "pair": ["10.107.204.143", "10.107.204.146"], "self_ip": "10.107.204.146", "status": "skip", "reason": "gid_index_mismatch"}, {"schema": "v3", "batch_idx": 1, "pair": ["10.107.204.142", "10.107.204.146"], "self_ip": "10.107.204.146", "status": "skip", "reason": "gid_index_mismatch"}, {"schema": "v3", "batch_idx": 2, "pair": ["10.107.204.141", "10.107.204.146"], "self_ip": "10.107.204.146", "status": "skip", "reason": "gid_index_mismatch"}]}`,
	}

	for _, report := range reports[:len(reports)-1] {
		ready, slowNodes, err := aggregator.AddReport("default", "preflight8-node-0", report)
		if err != nil {
			fmt.Printf("error: %v\n", err)
			return
		}
		fmt.Printf("ready=%v slowNodes=%v\n", ready, slowNodes)
	}

	ready, slowNodes, err := aggregator.AddReport("default", "preflight8-node-0", reports[len(reports)-1])
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}

	fmt.Printf("ready=%v slowNodes=%v\n", ready, slowNodes)

	// Output:
	// ready=false slowNodes=[]
	// ready=false slowNodes=[]
	// ready=false slowNodes=[]
	// ready=true slowNodes=[c500-worker1 c500-worker2 c500-worker3 c500-worker4]
}

func ExampleSlowNodeAggregator_AddReport_twoWorkersAllOkProduceNoSlowNodes() {
	aggregator := NewSlowNodeAggregator(0)
	reports := []string{
		`{"version": 1, "workload": "preflight9-2workers-rerun2-node-0", "workload_size": 2, "rank": 0, "node_name": "c500-worker1", "node_ip": "10.107.204.141", "storage_check": 1, "gpu_check": 1, "node_check_busbw_threshold_gbps": "5", "batches": [{"schema": "v3", "phase": "pairwise", "batch_idx": 0, "pair": ["10.107.204.141", "10.107.204.143"], "self_ip": "10.107.204.141", "local_rank": 0, "device": "MetaX C500", "status": "ok", "allreduce_ms": 211.52, "world_size": 16, "allreduce_shape": 536870912, "dtype_bytes": 4, "ranks_recorded": 8}]}`,
		`{"version": 1, "workload": "preflight9-2workers-rerun2-node-0", "workload_size": 2, "rank": 1, "node_name": "c500-worker3", "node_ip": "10.107.204.143", "storage_check": 1, "gpu_check": 1, "node_check_busbw_threshold_gbps": "5", "batches": [{"schema": "v3", "phase": "pairwise", "batch_idx": 0, "pair": ["10.107.204.141", "10.107.204.143"], "self_ip": "10.107.204.143", "local_rank": 0, "device": "MetaX C500", "status": "ok", "allreduce_ms": 210.512, "world_size": 16, "allreduce_shape": 536870912, "dtype_bytes": 4, "ranks_recorded": 8}]}`,
	}

	ready, slowNodes, err := aggregator.AddReport("default", "preflight9-2workers-rerun2-node-0", reports[0])
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}
	fmt.Printf("ready=%v slowNodes=%v\n", ready, slowNodes)

	ready, slowNodes, err = aggregator.AddReport("default", "preflight9-2workers-rerun2-node-0", reports[1])
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}
	fmt.Printf("ready=%v slowNodes=%v\n", ready, slowNodes)

	// Output:
	// ready=false slowNodes=[]
	// ready=true slowNodes=[]
}

func ExampleSlowNodeAggregator_AddReport_skipReportsAreTreatedAsAbnormal() {
	aggregator := NewSlowNodeAggregator(0)
	reports := []string{
		`{"version": 1, "workload": "preflight9-2workers-fail4-node-0", "workload_size": 3, "rank": 2, "node_name": "c500-worker1", "node_ip": "10.107.204.141", "storage_check": 1, "gpu_check": 1, "node_check_busbw_threshold_gbps": "5", "batches": [{"schema": "v3", "batch_idx": 0, "pair": ["10.107.204.141", "10.107.204.142"], "self_ip": "10.107.204.141", "status": "skip"}, {"schema": "v3", "batch_idx": 1, "pair": ["10.107.204.141", "10.107.204.143"], "self_ip": "10.107.204.141", "status": "skip"}, {"schema": "v3", "batch_idx": 2, "pair": [], "self_ip": "10.107.204.141", "status": "skip", "reason": "idle_roll_over"}]}`,
		`{"version": 1, "workload": "preflight9-2workers-fail4-node-0", "workload_size": 3, "rank": 1, "node_name": "c500-worker2", "node_ip": "10.107.204.142", "storage_check": 1, "gpu_check": 1, "node_check_busbw_threshold_gbps": "5", "batches": [{"schema": "v3", "batch_idx": 0, "pair": ["10.107.204.141", "10.107.204.142"], "self_ip": "10.107.204.142", "status": "skip", "reason": "gid_index_mismatch"}, {"schema": "v3", "batch_idx": 1, "pair": [], "self_ip": "10.107.204.142", "status": "skip", "reason": "idle_roll_over"}, {"schema": "v3", "batch_idx": 2, "pair": ["10.107.204.142", "10.107.204.143"], "self_ip": "10.107.204.142", "status": "skip", "reason": "gid_index_mismatch"}]}`,
		`{"version": 1, "workload": "preflight9-2workers-fail4-node-0", "workload_size": 3, "rank": 0, "node_name": "c500-worker3", "node_ip": "10.107.204.143", "storage_check": 1, "gpu_check": 1, "node_check_busbw_threshold_gbps": "5", "batches": [{"schema": "v3", "batch_idx": 0, "pair": [], "self_ip": "10.107.204.143", "status": "skip", "reason": "idle_roll_over"}, {"schema": "v3", "batch_idx": 1, "pair": ["10.107.204.141", "10.107.204.143"], "self_ip": "10.107.204.143", "status": "skip"}, {"schema": "v3", "batch_idx": 2, "pair": ["10.107.204.142", "10.107.204.143"], "self_ip": "10.107.204.143", "status": "skip"}]}`,
	}

	for _, report := range reports[:len(reports)-1] {
		ready, slowNodes, err := aggregator.AddReport("default", "preflight9-2workers-fail4-node-0", report)
		if err != nil {
			fmt.Printf("error: %v\n", err)
			return
		}
		fmt.Printf("ready=%v slowNodes=%v\n", ready, slowNodes)
	}

	ready, slowNodes, err := aggregator.AddReport("default", "preflight9-2workers-fail4-node-0", reports[len(reports)-1])
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}
	fmt.Printf("ready=%v slowNodes=%v\n", ready, slowNodes)

	// Output:
	// error: extract preflight report: odd workload sizes are not supported: workload_size=3
}

func ExampleSlowNodeAggregator_AddReport_skipReportsWithEmptyGPUCheckReport() {
	aggregator := NewSlowNodeAggregator(0)
	reports := []string{
		`{"version": 1, "workload": "preflight9-2workers-fail11-node-0", "workload_size": 3, "rank": 0, "node_name": "c500-worker1", "node_ip": "10.107.204.141", "storage_check": 1, "gpu_check": 1, "node_check_busbw_threshold_gbps": "5", "batches": [{"schema": "v3", "batch_idx": 0, "pair": ["10.107.204.141", "10.107.204.142"], "self_ip": "10.107.204.141", "status": "skip"}, {"schema": "v3", "batch_idx": 1, "pair": ["10.107.204.141", "10.107.204.146"], "self_ip": "10.107.204.141", "status": "skip"}, {"schema": "v3", "batch_idx": 2, "pair": [], "self_ip": "10.107.204.141", "status": "skip", "reason": "idle_roll_over"}]}`,
		`{"version": 1, "workload": "preflight9-2workers-fail11-node-0", "workload_size": 3, "rank": 1, "node_name": "c500-worker2", "node_ip": "10.107.204.142", "storage_check": 1, "gpu_check": 1, "node_check_busbw_threshold_gbps": "5", "batches": [{"schema": "v3", "batch_idx": 0, "pair": ["10.107.204.141", "10.107.204.142"], "self_ip": "10.107.204.142", "status": "skip", "reason": "gid_index_mismatch"}, {"schema": "v3", "batch_idx": 1, "pair": [], "self_ip": "10.107.204.142", "status": "skip", "reason": "idle_roll_over"}, {"schema": "v3", "batch_idx": 2, "pair": ["10.107.204.142", "10.107.204.146"], "self_ip": "10.107.204.142", "status": "skip", "reason": "gid_index_mismatch"}]}`,
		`{"version": 1, "workload": "preflight9-2workers-fail11-node-0", "workload_size": 3, "rank": 2, "node_name": "c500-worker4", "node_ip": "10.107.204.146", "storage_check": 1, "gpu_check": 0, "node_check_busbw_threshold_gbps": "5", "batches": []}`,
	}

	for _, report := range reports[:len(reports)-1] {
		ready, slowNodes, err := aggregator.AddReport("default", "preflight9-2workers-fail11-node-0", report)
		if err != nil {
			fmt.Printf("error: %v\n", err)
			return
		}
		fmt.Printf("ready=%v slowNodes=%v\n", ready, slowNodes)
	}

	ready, slowNodes, err := aggregator.AddReport("default", "preflight9-2workers-fail11-node-0", reports[len(reports)-1])
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}
	fmt.Printf("ready=%v slowNodes=%v\n", ready, slowNodes)

	// Output:
	// error: extract preflight report: odd workload sizes are not supported: workload_size=3
}

func TestSlowNodeAggregator_FailFastA_OnlyAIsSlowNode(t *testing.T) {
	t.Parallel()
	aggregator := NewSlowNodeAggregator(0)

	// 4节点: a,b,c,d
	// a fail-fast (gpu_check=2), batches为空
	// b/c/d的batches: (b,a),(c,a),(d,a)均fail，其它正常
	reports := []string{
		// node-a: fail-fast, batches为空
		`{"version":1,"workload":"job-z","workload_size":4,"rank":0,"node_name":"node-a","node_ip":"10.0.0.1","gpu_check":2,"storage_check":1,"batches":[]}`,
		// node-b: batch0 (b,a):fail, batch1 (b,c):ok, batch2 (b,d):ok
		`{"version":1,"workload":"job-z","workload_size":4,"rank":1,"node_name":"node-b","node_ip":"10.0.0.2","gpu_check":1,"storage_check":1,"batches":[{"batch_idx":0,"pair":["10.0.0.2","10.0.0.1"],"self_ip":"10.0.0.2","status":"fail"},{"batch_idx":1,"pair":["10.0.0.2","10.0.0.3"],"self_ip":"10.0.0.2","allreduce_ms":0.5,"world_size":16,"allreduce_shape":16777216,"dtype_bytes":4},{"batch_idx":2,"pair":["10.0.0.2","10.0.0.4"],"self_ip":"10.0.0.2","allreduce_ms":0.5,"world_size":16,"allreduce_shape":16777216,"dtype_bytes":4}]}`,
		// node-c: batch0 (c,d):ok, batch1 (c,a):fail, batch2 (c,b):ok
		`{"version":1,"workload":"job-z","workload_size":4,"rank":2,"node_name":"node-c","node_ip":"10.0.0.3","gpu_check":1,"storage_check":1,"batches":[{"batch_idx":0,"pair":["10.0.0.3","10.0.0.4"],"self_ip":"10.0.0.3","allreduce_ms":0.5,"world_size":16,"allreduce_shape":16777216,"dtype_bytes":4},{"batch_idx":1,"pair":["10.0.0.3","10.0.0.1"],"self_ip":"10.0.0.3","status":"fail"},{"batch_idx":2,"pair":["10.0.0.3","10.0.0.2"],"self_ip":"10.0.0.3","allreduce_ms":0.5,"world_size":16,"allreduce_shape":16777216,"dtype_bytes":4}]}`,
		// node-d: batch0 (d,c):ok, batch1 (d,b):ok, batch2 (d,a):fail
		`{"version":1,"workload":"job-z","workload_size":4,"rank":3,"node_name":"node-d","node_ip":"10.0.0.4","gpu_check":1,"storage_check":1,"batches":[{"batch_idx":0,"pair":["10.0.0.4","10.0.0.3"],"self_ip":"10.0.0.4","allreduce_ms":0.5,"world_size":16,"allreduce_shape":16777216,"dtype_bytes":4},{"batch_idx":1,"pair":["10.0.0.4","10.0.0.2"],"self_ip":"10.0.0.4","allreduce_ms":0.5,"world_size":16,"allreduce_shape":16777216,"dtype_bytes":4},{"batch_idx":2,"pair":["10.0.0.4","10.0.0.1"],"self_ip":"10.0.0.4","status":"fail"}]}`,
	}

	for i := 0; i < len(reports)-1; i++ {
		ready, slowNodes, err := aggregator.AddReport("default", "job-z", reports[i])
		if err != nil {
			t.Fatalf("aggregator.AddReport(...) error = %v", err)
		}
		if ready {
			t.Fatalf("aggregator.AddReport(...) ready = true too early, slowNodes=%v", slowNodes)
		}
	}

	ready, slowNodes, err := aggregator.AddReport("default", "job-z", reports[len(reports)-1])
	if err != nil {
		t.Fatalf("aggregator.AddReport(...) error = %v", err)
	}
	if !ready {
		t.Fatal("aggregator.AddReport(...) ready = false, want true")
	}
	if !reflect.DeepEqual(slowNodes, []string{"node-a"}) {
		t.Fatalf("aggregator.AddReport(...) slowNodes = %v, want [node-a]", slowNodes)
	}
}

func testNodeIP(idx int) string {
	return fmt.Sprintf("10.0.0.%d", idx+1)
}

func TestPairFieldSortsIPsAscending(t *testing.T) {
	t.Parallel()

	pair, ok := pairField([]any{"10.0.1.2", "10.0.1.1"})
	if !ok {
		t.Fatal("pairField(...) ok = false, want true")
	}
	if pair[0] != "10.0.1.1" || pair[1] != "10.0.1.2" {
		t.Fatalf("pairField(...) = %q/%q, want 10.0.1.1/10.0.1.2", pair[0], pair[1])
	}
}

func TestSlowNodeAggregator_LayoutChangeResetsStaleWorkload(t *testing.T) {
	t.Parallel()

	aggregator := NewSlowNodeAggregator(0)
	staleReport := `{"version":1,"workload":"job-a","workload_size":4,"rank":0,"node_name":"node-a","node_ip":"10.0.0.1","gpu_check":1,"storage_check":1,"batches":[{"batch_idx":0,"pair":["10.0.0.1","10.0.0.2"],"self_ip":"10.0.0.1","status":"fail"}]}`
	newReportA := `{"version":1,"workload":"job-a","workload_size":2,"rank":0,"node_name":"node-a","node_ip":"10.0.0.1","gpu_check":0,"storage_check":1,"batches":[]}`
	newReportB := `{"version":1,"workload":"job-a","workload_size":2,"rank":1,"node_name":"node-b","node_ip":"10.0.0.2","gpu_check":1,"storage_check":1,"batches":[{"batch_idx":0,"pair":["10.0.0.1","10.0.0.2"],"self_ip":"10.0.0.2","status":"skip"}]}`

	ready, _, err := aggregator.AddReport("default", "job-a", staleReport)
	if err != nil {
		t.Fatalf("aggregator.AddReport(staleReport) error = %v", err)
	}
	if ready {
		t.Fatal("aggregator.AddReport(staleReport) ready = true, want false")
	}

	ready, _, err = aggregator.AddReport("default", "job-a", newReportA)
	if err != nil {
		t.Fatalf("aggregator.AddReport(newReportA) error = %v", err)
	}
	if ready {
		t.Fatal("aggregator.AddReport(newReportA) ready = true, want false")
	}

	ready, slowNodes, err := aggregator.AddReport("default", "job-a", newReportB)
	if err != nil {
		t.Fatalf("aggregator.AddReport(newReportB) error = %v", err)
	}
	if !ready {
		t.Fatal("aggregator.AddReport(newReportB) ready = false, want true")
	}
	if !reflect.DeepEqual(slowNodes, []string{"node-a", "node-b"}) {
		t.Fatalf("aggregator.AddReport(newReportB) slowNodes = %v, want [node-a node-b]", slowNodes)
	}
}

func TestExtractBusBWThresholdUsesDefaultForEmptyString(t *testing.T) {
	t.Parallel()

	got, err := extractBusBWThreshold(map[string]any{busbwThreshold: "  "})
	if err != nil {
		t.Fatalf("extractBusBWThreshold(...) error = %v", err)
	}
	if got != DefaultBusBWThresholdGBPS {
		t.Fatalf("extractBusBWThreshold(...) = %v, want %v", got, DefaultBusBWThresholdGBPS)
	}
}

func TestExtractBusBWThresholdAcceptsZeroToDisableGate(t *testing.T) {
	t.Parallel()

	got, err := extractBusBWThreshold(map[string]any{busbwThreshold: "0"})
	if err != nil {
		t.Fatalf("extractBusBWThreshold(...) error = %v", err)
	}
	if got != 0 {
		t.Fatalf("extractBusBWThreshold(...) = %v, want 0", got)
	}
}

func TestBatchFailedSkipsBusBWCalculationWhenThresholdDisabled(t *testing.T) {
	t.Parallel()

	failed, err := batchFailed(map[string]any{"status": "ok"}, 0, 0)
	if err != nil {
		t.Fatalf("batchFailed(...) error = %v", err)
	}
	if failed {
		t.Fatal("batchFailed(...) = true, want false")
	}
}

func TestSlowNodeAggregatorDetectsSlowNodeFromBatchIntersection(t *testing.T) {
	t.Parallel()

	aggregator := NewSlowNodeAggregator(0)

	reports := []string{
		`{"version":1,"workload":"job-a","workload_size":4,"rank":0,"node_name":"node-a","node_ip":"10.0.0.1","gpu_check":1,"storage_check":1,"batches":[{"schema":"v3","batch_idx":0,"pair":["10.0.0.1","10.0.0.2"],"self_ip":"10.0.0.1","allreduce_ms":0.5,"world_size":16,"allreduce_shape":16777216,"dtype_bytes":4},{"schema":"v3","batch_idx":1,"pair":["10.0.0.1","10.0.0.3"],"self_ip":"10.0.0.1","status":"fail","rc":1},{"schema":"v3","batch_idx":2,"pair":["10.0.0.1","10.0.0.4"],"self_ip":"10.0.0.1","allreduce_ms":0.5,"world_size":16,"allreduce_shape":16777216,"dtype_bytes":4}]}`,
		`{"version":1,"workload":"job-a","workload_size":4,"rank":1,"node_name":"node-b","node_ip":"10.0.0.2","gpu_check":1,"storage_check":1,"batches":[{"schema":"v3","batch_idx":0,"pair":["10.0.0.1","10.0.0.2"],"self_ip":"10.0.0.2","allreduce_ms":0.5,"world_size":16,"allreduce_shape":16777216,"dtype_bytes":4},{"schema":"v3","batch_idx":1,"pair":["10.0.0.2","10.0.0.4"],"self_ip":"10.0.0.2","allreduce_ms":0.5,"world_size":16,"allreduce_shape":16777216,"dtype_bytes":4},{"schema":"v3","batch_idx":2,"pair":["10.0.0.2","10.0.0.3"],"self_ip":"10.0.0.2","status":"fail","rc":1}]}`,
		`{"version":1,"workload":"job-a","workload_size":4,"rank":2,"node_name":"node-c","node_ip":"10.0.0.3","gpu_check":1,"storage_check":1,"batches":[{"schema":"v3","batch_idx":0,"pair":["10.0.0.3","10.0.0.4"],"self_ip":"10.0.0.3","status":"fail","rc":1},{"schema":"v3","batch_idx":1,"pair":["10.0.0.1","10.0.0.3"],"self_ip":"10.0.0.3","status":"fail","rc":1},{"schema":"v3","batch_idx":2,"pair":["10.0.0.2","10.0.0.3"],"self_ip":"10.0.0.3","status":"fail","rc":1}]}`,
		`{"version":1,"workload":"job-a","workload_size":4,"rank":3,"node_name":"node-d","node_ip":"10.0.0.4","gpu_check":1,"storage_check":1,"batches":[{"schema":"v3","batch_idx":0,"pair":["10.0.0.3","10.0.0.4"],"self_ip":"10.0.0.4","status":"fail","rc":1},{"schema":"v3","batch_idx":1,"pair":["10.0.0.2","10.0.0.4"],"self_ip":"10.0.0.4","allreduce_ms":0.5,"world_size":16,"allreduce_shape":16777216,"dtype_bytes":4},{"schema":"v3","batch_idx":2,"pair":["10.0.0.1","10.0.0.4"],"self_ip":"10.0.0.4","allreduce_ms":0.5,"world_size":16,"allreduce_shape":16777216,"dtype_bytes":4}]}`,
	}

	for i := 0; i < len(reports)-1; i++ {
		ready, slowNodes, err := aggregator.AddReport("default", "job-a", reports[i])
		if err != nil {
			t.Fatalf("aggregator.AddReport(...) error = %v", err)
		}
		if ready {
			t.Fatalf("aggregator.AddReport(...) ready = true too early, slowNodes=%v", slowNodes)
		}
	}

	ready, slowNodes, err := aggregator.AddReport("default", "job-a", reports[len(reports)-1])
	if err != nil {
		t.Fatalf("aggregator.AddReport(...) error = %v", err)
	}
	if !ready {
		t.Fatal("aggregator.AddReport(...) ready = false, want true")
	}
	if !reflect.DeepEqual(slowNodes, []string{"node-c"}) {
		t.Fatalf("aggregator.AddReport(...) slowNodes = %v, want [node-c]", slowNodes)
	}
}

func TestIntersectFailedNodeIPsReturnsSharedNodeAcrossAllFailedBatches(t *testing.T) {
	t.Parallel()

	failedByBatch := map[batchIndex]nodeIPSet{
		batchIndex(0): {nodeIP("node-c"): {}, nodeIP("node-d"): {}},
		batchIndex(1): {nodeIP("node-a"): {}, nodeIP("node-c"): {}},
		batchIndex(2): {nodeIP("node-b"): {}, nodeIP("node-c"): {}},
	}

	actual := intersectFailedNodeIPs(failedByBatch, nil)
	if !reflect.DeepEqual(actual, []string{"node-c"}) {
		t.Fatalf("intersectFailedNodeIPs(...) = %v, want [node-c]", actual)
	}
}

func TestIntersectFailedNodeIPsReturnsNilForEmptyInput(t *testing.T) {
	t.Parallel()

	actual := intersectFailedNodeIPs(map[batchIndex]nodeIPSet{}, nil)
	if actual != nil {
		t.Fatalf("intersectFailedNodeIPs(empty, nil) = %v, want nil", actual)
	}
}

func TestIntersectFailedNodeIPsResolvesNodeNames(t *testing.T) {
	t.Parallel()

	failedByBatch := map[batchIndex]nodeIPSet{
		batchIndex(0): {nodeIP("10.0.0.1"): {}, nodeIP("10.0.0.2"): {}},
		batchIndex(1): {nodeIP("10.0.0.1"): {}, nodeIP("10.0.0.3"): {}},
		batchIndex(2): {nodeIP("10.0.0.1"): {}, nodeIP("10.0.0.4"): {}},
	}
	nodeIPToName := map[nodeIP]nodeName{
		nodeIP("10.0.0.1"): nodeName("node-a"),
		nodeIP("10.0.0.2"): nodeName("node-b"),
		nodeIP("10.0.0.3"): nodeName("node-c"),
		nodeIP("10.0.0.4"): nodeName("node-d"),
	}

	actual := intersectFailedNodeIPs(failedByBatch, nodeIPToName)
	if !reflect.DeepEqual(actual, []string{"node-a"}) {
		t.Fatalf("intersectFailedNodeIPs(...) = %v, want [node-a]", actual)
	}
}

func TestSlowNodeAggregatorReturnsAllNodesMeetingDefaultThreshold(t *testing.T) {
	t.Parallel()

	aggregator := NewSlowNodeAggregator(0)

	reports := []string{
		`{"version":1,"workload":"job-a","workload_size":4,"rank":0,"node_name":"node-a","node_ip":"10.0.0.1","gpu_check":1,"storage_check":1,"batches":[{"schema":"v3","batch_idx":0,"pair":["10.0.0.1","10.0.0.2"],"self_ip":"10.0.0.1","allreduce_ms":0.5,"world_size":16,"allreduce_shape":16777216,"dtype_bytes":4},{"schema":"v3","batch_idx":1,"pair":["10.0.0.1","10.0.0.3"],"self_ip":"10.0.0.1","status":"fail","rc":1},{"schema":"v3","batch_idx":2,"pair":["10.0.0.1","10.0.0.4"],"self_ip":"10.0.0.1","allreduce_ms":0.5,"world_size":16,"allreduce_shape":16777216,"dtype_bytes":4}]}`,
		`{"version":1,"workload":"job-a","workload_size":4,"rank":1,"node_name":"node-b","node_ip":"10.0.0.2","gpu_check":1,"storage_check":1,"batches":[{"schema":"v3","batch_idx":0,"pair":["10.0.0.1","10.0.0.2"],"self_ip":"10.0.0.2","allreduce_ms":0.5,"world_size":16,"allreduce_shape":16777216,"dtype_bytes":4},{"schema":"v3","batch_idx":1,"pair":["10.0.0.2","10.0.0.4"],"self_ip":"10.0.0.2","allreduce_ms":0.5,"world_size":16,"allreduce_shape":16777216,"dtype_bytes":4},{"schema":"v3","batch_idx":2,"pair":["10.0.0.2","10.0.0.3"],"self_ip":"10.0.0.2","status":"fail","rc":1}]}`,
		`{"version":1,"workload":"job-a","workload_size":4,"rank":2,"node_name":"node-c","node_ip":"10.0.0.3","gpu_check":1,"storage_check":1,"batches":[{"schema":"v3","batch_idx":0,"pair":["10.0.0.3","10.0.0.4"],"self_ip":"10.0.0.3","status":"fail","rc":1},{"schema":"v3","batch_idx":1,"pair":["10.0.0.1","10.0.0.3"],"self_ip":"10.0.0.3","status":"fail","rc":1},{"schema":"v3","batch_idx":2,"pair":["10.0.0.2","10.0.0.3"],"self_ip":"10.0.0.3","status":"fail","rc":1}]}`,
		`{"version":1,"workload":"job-a","workload_size":4,"rank":3,"node_name":"node-d","node_ip":"10.0.0.4","gpu_check":1,"storage_check":1,"batches":[{"schema":"v3","batch_idx":0,"pair":["10.0.0.3","10.0.0.4"],"self_ip":"10.0.0.4","status":"fail","rc":1},{"schema":"v3","batch_idx":1,"pair":["10.0.0.2","10.0.0.4"],"self_ip":"10.0.0.4","allreduce_ms":0.5,"world_size":16,"allreduce_shape":16777216,"dtype_bytes":4},{"schema":"v3","batch_idx":2,"pair":["10.0.0.1","10.0.0.4"],"self_ip":"10.0.0.4","allreduce_ms":0.5,"world_size":16,"allreduce_shape":16777216,"dtype_bytes":4}]}`,
	}

	for i := 0; i < len(reports)-1; i++ {
		ready, slowNodes, err := aggregator.AddReport("default", "job-a", reports[i])
		if err != nil {
			t.Fatalf("aggregator.AddReport(...) error = %v", err)
		}
		if ready {
			t.Fatalf("aggregator.AddReport(...) ready = true too early, slowNodes=%v", slowNodes)
		}
	}

	ready, slowNodes, err := aggregator.AddReport("default", "job-a", reports[len(reports)-1])
	if err != nil {
		t.Fatalf("aggregator.AddReport(...) error = %v", err)
	}
	if !ready {
		t.Fatal("aggregator.AddReport(...) ready = false, want true")
	}
	if !reflect.DeepEqual(slowNodes, []string{"node-c"}) {
		t.Fatalf("aggregator.AddReport(...) slowNodes = %v, want [node-c]", slowNodes)
	}
}

func TestSlowNodeAggregatorReturnsNoSlowNodeWhenAllPairsPass(t *testing.T) {
	t.Parallel()

	aggregator := NewSlowNodeAggregator(0)

	reports := []string{
		`{"version":1,"workload":"job-b","workload_size":2,"rank":0,"node_name":"node-a","node_ip":"10.0.0.1","gpu_check":1,"storage_check":1,"batches":[{"schema":"v3","batch_idx":0,"pair":["10.0.0.1","10.0.0.2"],"self_ip":"10.0.0.1","allreduce_ms":0.5,"world_size":16,"allreduce_shape":16777216,"dtype_bytes":4}]}`,
		`{"version":1,"workload":"job-b","workload_size":2,"rank":1,"node_name":"node-b","node_ip":"10.0.0.2","gpu_check":1,"storage_check":1,"batches":[{"schema":"v3","batch_idx":0,"pair":["10.0.0.1","10.0.0.2"],"self_ip":"10.0.0.2","allreduce_ms":0.5,"world_size":16,"allreduce_shape":16777216,"dtype_bytes":4}]}`,
	}

	_, _, _ = aggregator.AddReport("default", "job-b", reports[0])
	ready, slowNodes, err := aggregator.AddReport("default", "job-b", reports[1])
	if err != nil {
		t.Fatalf("aggregator.AddReport(...) error = %v", err)
	}
	if !ready {
		t.Fatal("aggregator.AddReport(...) ready = false, want true")
	}
	if len(slowNodes) != 0 {
		t.Fatalf("aggregator.AddReport(...) slowNodes = %v, want []", slowNodes)
	}
}

func TestSlowNodeAggregatorFailFastMarksNodeAbnormalWithoutBatchProcessing(t *testing.T) {
	t.Parallel()

	aggregator := NewSlowNodeAggregator(0)

	fastFailReport := `{"version":1,"workload":"job-ff","workload_size":2,"rank":0,"node_name":"node-a","node_ip":"10.0.0.1","gpu_check":2,"storage_check":1}`
	normalReport := `{"version":1,"workload":"job-ff","workload_size":2,"rank":1,"node_name":"node-b","node_ip":"10.0.0.2","gpu_check":1,"storage_check":1,"batches":[{"batch_idx":0,"pair":["10.0.0.1","10.0.0.2"],"self_ip":"10.0.0.2","allreduce_ms":0.5,"world_size":16,"allreduce_shape":16777216,"dtype_bytes":4}]}`

	ready, slowNodes, err := aggregator.AddReport("default", "job-ff", fastFailReport)
	if err != nil {
		t.Fatalf("aggregator.AddReport(...) fast-fail error = %v", err)
	}
	if ready || slowNodes != nil {
		t.Fatalf("first AddReport = (%v, %v), want (false, nil)", ready, slowNodes)
	}

	ready, slowNodes, err = aggregator.AddReport("default", "job-ff", normalReport)
	if err != nil {
		t.Fatalf("aggregator.AddReport(...) normal report error = %v", err)
	}
	if !ready {
		t.Fatal("aggregator.AddReport(...) ready = false, want true")
	}
	if !reflect.DeepEqual(slowNodes, []string{"node-a"}) {
		t.Fatalf("aggregator.AddReport(...) slowNodes = %v, want [node-a]", slowNodes)
	}
}

func TestSlowNodeAggregatorFailFastAndPreflightSlowNodesAreMerged(t *testing.T) {
	t.Parallel()

	aggregator := NewSlowNodeAggregator(0)

	reports := []string{
		`{"version":1,"workload":"job-mix","workload_size":4,"rank":0,"node_name":"node-a","node_ip":"10.0.0.1","gpu_check":1,"storage_check":1,"batches":[{"schema":"v3","batch_idx":0,"pair":["10.0.0.1","10.0.0.2"],"self_ip":"10.0.0.1","allreduce_ms":0.5,"world_size":16,"allreduce_shape":16777216,"dtype_bytes":4},{"schema":"v3","batch_idx":1,"pair":["10.0.0.1","10.0.0.3"],"self_ip":"10.0.0.1","status":"fail","rc":1},{"schema":"v3","batch_idx":2,"pair":["10.0.0.1","10.0.0.4"],"self_ip":"10.0.0.1","allreduce_ms":0.5,"world_size":16,"allreduce_shape":16777216,"dtype_bytes":4}]}`,
		`{"version":1,"workload":"job-mix","workload_size":4,"rank":1,"node_name":"node-b","node_ip":"10.0.0.2","gpu_check":1,"storage_check":1,"batches":[{"schema":"v3","batch_idx":0,"pair":["10.0.0.1","10.0.0.2"],"self_ip":"10.0.0.2","allreduce_ms":0.5,"world_size":16,"allreduce_shape":16777216,"dtype_bytes":4},{"schema":"v3","batch_idx":1,"pair":["10.0.0.2","10.0.0.4"],"self_ip":"10.0.0.2","allreduce_ms":0.5,"world_size":16,"allreduce_shape":16777216,"dtype_bytes":4},{"schema":"v3","batch_idx":2,"pair":["10.0.0.2","10.0.0.3"],"self_ip":"10.0.0.2","status":"fail","rc":1}]}`,
		`{"version":1,"workload":"job-mix","workload_size":4,"rank":2,"node_name":"node-c","node_ip":"10.0.0.3","gpu_check":1,"storage_check":1,"batches":[{"schema":"v3","batch_idx":0,"pair":["10.0.0.3","10.0.0.4"],"self_ip":"10.0.0.3","status":"fail","rc":1},{"schema":"v3","batch_idx":1,"pair":["10.0.0.1","10.0.0.3"],"self_ip":"10.0.0.3","status":"fail","rc":1},{"schema":"v3","batch_idx":2,"pair":["10.0.0.2","10.0.0.3"],"self_ip":"10.0.0.3","status":"fail","rc":1}]}`,
		`{"version":1,"workload":"job-mix","workload_size":4,"rank":3,"node_name":"node-d","node_ip":"10.0.0.4","gpu_check":2,"storage_check":1}`,
	}

	for i := 0; i < len(reports)-1; i++ {
		ready, slowNodes, err := aggregator.AddReport("default", "job-mix", reports[i])
		if err != nil {
			t.Fatalf("aggregator.AddReport(...) error = %v", err)
		}
		if ready {
			t.Fatalf("aggregator.AddReport(...) ready = true too early, slowNodes=%v", slowNodes)
		}
	}

	ready, slowNodes, err := aggregator.AddReport("default", "job-mix", reports[len(reports)-1])
	if err != nil {
		t.Fatalf("aggregator.AddReport(...) error = %v", err)
	}
	if !ready {
		t.Fatal("aggregator.AddReport(...) ready = false, want true")
	}
	if !reflect.DeepEqual(slowNodes, []string{"node-c", "node-d"}) {
		t.Fatalf("aggregator.AddReport(...) slowNodes = %v, want [node-c node-d]", slowNodes)
	}
}

func TestSlowNodeAggregatorFailFastAndPairwiseSlowNodeProduceAB(t *testing.T) {
	t.Parallel()

	aggregator := NewSlowNodeAggregator(0)

	reports := []string{
		`{"version":1,"workload":"job-ab","workload_size":4,"rank":0,"node_name":"node-a","node_ip":"10.0.0.1","gpu_check":2,"storage_check":1,"batches":[]}`,
		`{"version":1,"workload":"job-ab","workload_size":4,"rank":1,"node_name":"node-b","node_ip":"10.0.0.2","gpu_check":1,"storage_check":1,"batches":[{"batch_idx":0,"pair":["10.0.0.2","10.0.0.1"],"self_ip":"10.0.0.2","status":"fail"},{"batch_idx":1,"pair":["10.0.0.2","10.0.0.4"],"self_ip":"10.0.0.2","status":"fail"},{"batch_idx":2,"pair":["10.0.0.2","10.0.0.3"],"self_ip":"10.0.0.2","status":"fail"}]}`,
		`{"version":1,"workload":"job-ab","workload_size":4,"rank":2,"node_name":"node-c","node_ip":"10.0.0.3","gpu_check":1,"storage_check":1,"batches":[{"batch_idx":0,"pair":["10.0.0.3","10.0.0.4"],"self_ip":"10.0.0.3","allreduce_ms":0.5,"world_size":16,"allreduce_shape":16777216,"dtype_bytes":4},{"batch_idx":1,"pair":["10.0.0.3","10.0.0.1"],"self_ip":"10.0.0.3","status":"fail"},{"batch_idx":2,"pair":["10.0.0.3","10.0.0.2"],"self_ip":"10.0.0.3","status":"fail"}]}`,
		`{"version":1,"workload":"job-ab","workload_size":4,"rank":3,"node_name":"node-d","node_ip":"10.0.0.4","gpu_check":1,"storage_check":1,"batches":[{"batch_idx":0,"pair":["10.0.0.4","10.0.0.3"],"self_ip":"10.0.0.4","allreduce_ms":0.5,"world_size":16,"allreduce_shape":16777216,"dtype_bytes":4},{"batch_idx":1,"pair":["10.0.0.4","10.0.0.2"],"self_ip":"10.0.0.4","status":"fail"},{"batch_idx":2,"pair":["10.0.0.4","10.0.0.1"],"self_ip":"10.0.0.4","status":"fail"}]}`,
	}

	for i := 0; i < len(reports)-1; i++ {
		ready, slowNodes, err := aggregator.AddReport("default", "job-ab", reports[i])
		if err != nil {
			t.Fatalf("aggregator.AddReport(...) error = %v", err)
		}
		if ready {
			t.Fatalf("aggregator.AddReport(...) ready = true too early, slowNodes=%v", slowNodes)
		}
	}

	ready, slowNodes, err := aggregator.AddReport("default", "job-ab", reports[len(reports)-1])
	if err != nil {
		t.Fatalf("aggregator.AddReport(...) error = %v", err)
	}
	if !ready {
		t.Fatal("aggregator.AddReport(...) ready = false, want true")
	}
	if !reflect.DeepEqual(slowNodes, []string{"node-a", "node-b"}) {
		t.Fatalf("aggregator.AddReport(...) slowNodes = %v, want [node-a node-b]", slowNodes)
	}
}

func TestSlowNodeAggregatorRequiresCompleteReportMatrix(t *testing.T) {
	t.Parallel()

	aggregator := NewSlowNodeAggregator(0)
	incomplete := `{"version":1,"workload":"job-c","workload_size":16,"rank":0,"node_name":"node-00","node_ip":"10.0.0.1","gpu_check":1,"storage_check":1,"batches":[{"batch_idx":0,"pair":["10.0.0.1","10.0.0.16"],"self_ip":"10.0.0.1","allreduce_ms":0.5,"world_size":16,"allreduce_shape":16777216,"dtype_bytes":4},{"batch_idx":1,"pair":["10.0.0.1","10.0.0.15"],"self_ip":"10.0.0.1","allreduce_ms":0.5,"world_size":16,"allreduce_shape":16777216,"dtype_bytes":4}]}`

	ready, slowNodes, err := aggregator.AddReport("default", "job-c", incomplete)
	if err != nil {
		t.Fatalf("aggregator.AddReport(...) error = %v, want nil", err)
	}
	if ready {
		t.Fatalf("aggregator.AddReport(...) ready = %v, want false for incomplete first report", ready)
	}
	if slowNodes != nil {
		t.Fatalf("aggregator.AddReport(...) slowNodes = %v, want nil", slowNodes)
	}
}

func TestSlowNodeAggregatorKeepsFailFastResultWhenPreflightBatchesMissing(t *testing.T) {
	t.Parallel()

	aggregator := NewSlowNodeAggregator(0)

	reports := []string{
		`{"version":1,"workload":"job-x","workload_size":4,"rank":0,"node_name":"node-a","node_ip":"10.0.0.1","gpu_check":1,"storage_check":1,"batches":[{"batch_idx":0,"pair":["10.0.0.1","10.0.0.2"],"self_ip":"10.0.0.1","allreduce_ms":0.5,"world_size":16,"allreduce_shape":16777216,"dtype_bytes":4},{"batch_idx":1,"pair":["10.0.0.1","10.0.0.3"],"self_ip":"10.0.0.1","status":"fail"}]}`,
		`{"version":1,"workload":"job-x","workload_size":4,"rank":1,"node_name":"node-b","node_ip":"10.0.0.2","gpu_check":1,"storage_check":1,"batches":[{"batch_idx":0,"pair":["10.0.0.1","10.0.0.2"],"self_ip":"10.0.0.2","allreduce_ms":0.5,"world_size":16,"allreduce_shape":16777216,"dtype_bytes":4},{"batch_idx":2,"pair":["10.0.0.2","10.0.0.3"],"self_ip":"10.0.0.2","status":"fail"}]}`,
		`{"version":1,"workload":"job-x","workload_size":4,"rank":2,"node_name":"node-c","node_ip":"10.0.0.3","gpu_check":1,"storage_check":1,"batches":[{"batch_idx":1,"pair":["10.0.0.1","10.0.0.3"],"self_ip":"10.0.0.3","status":"fail"},{"batch_idx":2,"pair":["10.0.0.2","10.0.0.3"],"self_ip":"10.0.0.3","status":"fail"}]}`,
		`{"version":1,"workload":"job-x","workload_size":4,"rank":3,"node_name":"node-d","node_ip":"10.0.0.4","gpu_check":2,"storage_check":1}`,
	}

	for i := 0; i < len(reports)-1; i++ {
		ready, slowNodes, err := aggregator.AddReport("default", "job-x", reports[i])
		if err != nil {
			t.Fatalf("aggregator.AddReport(...) error = %v", err)
		}
		if ready {
			t.Fatalf("aggregator.AddReport(...) ready = true too early, slowNodes=%v", slowNodes)
		}
	}

	ready, slowNodes, err := aggregator.AddReport("default", "job-x", reports[len(reports)-1])
	if err != nil {
		t.Fatalf("aggregator.AddReport(...) error = %v", err)
	}
	if !ready {
		t.Fatal("aggregator.AddReport(...) ready = false, want true")
	}
	if !reflect.DeepEqual(slowNodes, []string{"node-c", "node-d"}) {
		t.Fatalf("aggregator.AddReport(...) slowNodes = %v, want [node-c node-d]", slowNodes)
	}
}

func TestSlowNodeAggregatorReturnsNoSlowNodeWhenMissingBatchesWithoutFailFast(t *testing.T) {
	t.Parallel()

	aggregator := NewSlowNodeAggregator(0)

	reports := []string{
		`{"version":1,"workload":"job-y","workload_size":4,"rank":0,"node_name":"node-a","node_ip":"10.0.0.1","gpu_check":1,"storage_check":1,"batches":[{"batch_idx":0,"pair":["10.0.0.1","10.0.0.2"],"self_ip":"10.0.0.1","allreduce_ms":0.5,"world_size":16,"allreduce_shape":16777216,"dtype_bytes":4}]}`,
		`{"version":1,"workload":"job-y","workload_size":4,"rank":1,"node_name":"node-b","node_ip":"10.0.0.2","gpu_check":1,"storage_check":1,"batches":[{"batch_idx":0,"pair":["10.0.0.1","10.0.0.2"],"self_ip":"10.0.0.2","allreduce_ms":0.5,"world_size":16,"allreduce_shape":16777216,"dtype_bytes":4}]}`,
		`{"version":1,"workload":"job-y","workload_size":4,"rank":2,"node_name":"node-c","node_ip":"10.0.0.3","gpu_check":1,"storage_check":1,"batches":[{"batch_idx":1,"pair":["10.0.0.1","10.0.0.3"],"self_ip":"10.0.0.3","status":"fail"}]}`,
		`{"version":1,"workload":"job-y","workload_size":4,"rank":3,"node_name":"node-d","node_ip":"10.0.0.4","gpu_check":1,"storage_check":1,"batches":[{"batch_idx":2,"pair":["10.0.0.2","10.0.0.4"],"self_ip":"10.0.0.4","status":"fail"}]}`,
	}

	for i := 0; i < len(reports)-1; i++ {
		ready, slowNodes, err := aggregator.AddReport("default", "job-y", reports[i])
		if err != nil {
			t.Fatalf("aggregator.AddReport(...) error = %v", err)
		}
		if ready {
			t.Fatalf("aggregator.AddReport(...) ready = true too early, slowNodes=%v", slowNodes)
		}
	}

	ready, slowNodes, err := aggregator.AddReport("default", "job-y", reports[len(reports)-1])
	if err != nil {
		t.Fatalf("aggregator.AddReport(...) error = %v", err)
	}
	if !ready {
		t.Fatal("aggregator.AddReport(...) ready = false, want true")
	}
	if len(slowNodes) != 0 {
		t.Fatalf("aggregator.AddReport(...) slowNodes = %v, want [] when batches are incomplete without fail-fast", slowNodes)
	}
}

func TestSlowNodeAggregatorDetectsSlowNodeIn16NodeTopology(t *testing.T) {
	t.Parallel()

	aggregator := NewSlowNodeAggregator(0)
	reports := roundRobinReports(16, map[string]struct{}{"node-03": {}})

	for i := 0; i < len(reports)-1; i++ {
		ready, slowNodes, err := aggregator.AddReport("default", "job-d", reports[i])
		if err != nil {
			t.Fatalf("aggregator.AddReport(...) error = %v", err)
		}
		if ready {
			t.Fatalf("aggregator.AddReport(...) ready = true too early, slowNodes=%v", slowNodes)
		}
	}

	ready, slowNodes, err := aggregator.AddReport("default", "job-d", reports[len(reports)-1])
	if err != nil {
		t.Fatalf("aggregator.AddReport(...) error = %v", err)
	}
	if !ready {
		t.Fatal("aggregator.AddReport(...) ready = false, want true")
	}
	if !reflect.DeepEqual(slowNodes, []string{"node-03"}) {
		t.Fatalf("aggregator.AddReport(...) slowNodes = %v, want [node-03]", slowNodes)
	}
}

func TestSlowNodeAggregatorRequiresWorldSize(t *testing.T) {
	t.Parallel()

	aggregator := NewSlowNodeAggregator(0)
	reports := roundRobinReportsWithoutWorldSize(8, map[string]struct{}{"node-03": {}})

	ready, slowNodes, err := aggregator.AddReport("default", "job-e", reports[0])
	if err == nil {
		t.Fatal("aggregator.AddReport(...) error = nil, want non-nil when workload_size is missing")
	}
	if ready {
		t.Fatal("aggregator.AddReport(...) ready = true, want false")
	}
	if slowNodes != nil {
		t.Fatalf("aggregator.AddReport(...) slowNodes = %v, want nil", slowNodes)
	}
}

func TestExtractNodeReportRejectsOddWorkloadSize(t *testing.T) {
	t.Parallel()

	reports := []string{
		`{"version":1,"workload":"job-odd","workload_size":3,"rank":0,"node_name":"node-a","node_ip":"10.0.0.1","gpu_check":1,"storage_check":1,"batches":[]}`,
		`{"version":1,"workload":"job-odd","workload_size":3,"rank":1,"node_name":"node-b","node_ip":"10.0.0.2","gpu_check":1,"storage_check":1,"batches":[]}`,
		`{"version":1,"workload":"job-odd","workload_size":3,"rank":2,"node_name":"node-c","node_ip":"10.0.0.3","gpu_check":1,"storage_check":1,"batches":[]}`,
	}

	for _, reportText := range reports {
		_, _, _, err := extractNodeReport(reportText)
		if err == nil || !strings.Contains(err.Error(), "odd workload sizes are not supported") {
			t.Fatalf("extractNodeReport(...) error = %v, want odd node count error", err)
		}
	}
}

func TestExtractNodeReportSupportsSuccessfulReportWithSelfIP(t *testing.T) {
	t.Parallel()

	reportText := `{
	  "version": 1,
	  "workload": "pre-4-node-0",
	  "workload_size": 4,
	  "rank": 0,
	  "node_name": "c500-worker1",
	  "gpu_check": 1,
	  "storage_check": 1,
	  "batches": [
	    {
	      "schema": "v3",
	      "phase": "pairwise",
	      "batch_idx": 0,
	      "pair": ["10.0.0.1", "10.0.0.2"],
	      "self_ip": "10.0.0.1",
	      "local_rank": 0,
	      "allreduce_ms": 1.234,
	      "world_size": 16,
	      "allreduce_shape": 16777216,
	      "dtype_bytes": 4,
	      "ranks_recorded": 8
	    },
	    {
	      "schema": "v3",
	      "phase": "pairwise",
	      "batch_idx": 1,
	      "pair": ["10.0.0.1", "10.0.0.3"],
	      "self_ip": "10.0.0.1",
	      "local_rank": 0,
	      "allreduce_ms": 1.234,
	      "world_size": 16,
	      "allreduce_shape": 16777216,
	      "dtype_bytes": 4,
	      "ranks_recorded": 8
	    },
	    {
	      "schema": "v3",
	      "phase": "pairwise",
	      "batch_idx": 2,
	      "pair": ["10.0.0.1", "10.0.0.4"],
	      "self_ip": "10.0.0.1",
	      "local_rank": 0,
	      "allreduce_ms": 1.234,
	      "world_size": 16,
	      "allreduce_shape": 16777216,
	      "dtype_bytes": 4,
	      "ranks_recorded": 8
	    }
	  ]
	}`

	report, plan, batchResults, err := extractNodeReport(reportText)
	if err != nil {
		t.Fatalf("extractNodeReport(...) error = %v", err)
	}
	if report.NodeName != "c500-worker1" {
		t.Fatalf("report.NodeName = %q, want %q", report.NodeName, "c500-worker1")
	}
	if plan.reportCount != 4 || plan.batchCount != 3 {
		t.Fatalf("plan = %+v, want reports=4 batches=3", plan)
	}
	if len(batchResults) != 3 {
		t.Fatalf("len(batchResults) = %d, want 3", len(batchResults))
	}
	if batchResults[0].SelfIP != "10.0.0.1" {
		t.Fatalf("batchResults[0].SelfIP = %q, want %q", batchResults[0].SelfIP, "10.0.0.1")
	}
	if batchResults[0].Failed {
		t.Fatal("batchResults[0].Failed = true, want false")
	}
}

func TestExtractNodeReportFallsBackToReportNodeIPWhenSelfIPMissing(t *testing.T) {
	t.Parallel()

	reportText := `{
	  "version": 1,
	  "workload": "pre-2-node-0",
	  "workload_size": 2,
	  "rank": 0,
	  "node_name": "node-a",
	  "node_ip": "10.0.0.1",
	  "gpu_check": 1,
	  "storage_check": 1,
	  "batches": [
	    {
	      "batch_idx": 0,
	      "pair": ["10.0.0.1", "10.0.0.2"],
	      "allreduce_ms": 1.234,
	      "world_size": 16,
	      "allreduce_shape": 16777216,
	      "dtype_bytes": 4
	    }
	  ]
	}`

	_, _, batchResults, err := extractNodeReport(reportText)
	if err != nil {
		t.Fatalf("extractNodeReport(...) error = %v", err)
	}
	if len(batchResults) != 1 {
		t.Fatalf("len(batchResults) = %d, want 1", len(batchResults))
	}
	if batchResults[0].SelfIP != "10.0.0.1" {
		t.Fatalf("batchResults[0].SelfIP = %q, want %q", batchResults[0].SelfIP, "10.0.0.1")
	}
}

func TestExtractNodeReportSupportsFailedReportWithSelfIP(t *testing.T) {
	t.Parallel()

	reportText := `{
	  "version": 1,
	  "workload": "pre-4-node-0",
	  "workload_size": 4,
	  "rank": 0,
	  "node_name": "c500-worker1",
	  "gpu_check": 1,
	  "storage_check": 1,
	  "batches": [
	    {
	      "schema": "v3",
	      "batch_idx": 0,
	      "pair": ["10.0.0.1", "10.0.0.2"],
	      "self_ip": "10.0.0.1",
	      "status": "fail",
	      "rc": 1
	    },
	    {
	      "schema": "v3",
	      "batch_idx": 1,
	      "pair": ["10.0.0.1", "10.0.0.3"],
	      "self_ip": "10.0.0.1",
	      "status": "fail",
	      "rc": 1
	    },
	    {
	      "schema": "v3",
	      "batch_idx": 2,
	      "pair": ["10.0.0.1", "10.0.0.4"],
	      "self_ip": "10.0.0.1",
	      "status": "fail",
	      "rc": 1
	    }
	  ]
	}`

	_, plan, batchResults, err := extractNodeReport(reportText)
	if err != nil {
		t.Fatalf("extractNodeReport(...) error = %v", err)
	}
	if plan.reportCount != 4 || plan.batchCount != 3 {
		t.Fatalf("plan = %+v, want reports=4 batches=3", plan)
	}
	if len(batchResults) != 3 {
		t.Fatalf("len(batchResults) = %d, want 3", len(batchResults))
	}
	if !batchResults[0].Failed {
		t.Fatal("batchResults[0].Failed = false, want true")
	}
	if batchResults[0].PairFirst != "10.0.0.1" || batchResults[0].PairSecond != "10.0.0.2" {
		t.Fatalf("batchResults[0] pair = %q/%q, want 10.0.0.1/10.0.0.2", batchResults[0].PairFirst, batchResults[0].PairSecond)
	}
}

func TestExtractNodeReportUsesReportBusBWThreshold(t *testing.T) {
	t.Parallel()

	reportText := `{
	  "version": 1,
	  "workload": "pre-2-node-0",
	  "workload_size": 2,
	  "rank": 0,
	  "node_name": "node-a",
	  "node_check_busbw_threshold_gbps": "3.0",
	  "gpu_check": 1,
	  "storage_check": 1,
	  "batches": [
	    {
	      "schema": "v3",
	      "batch_idx": 0,
	      "pair": ["10.0.0.1", "10.0.0.2"],
	      "self_ip": "10.0.0.1",
	      "allreduce_ms": 30,
	      "world_size": 16,
	      "allreduce_shape": 16777216,
	      "dtype_bytes": 4
	    }
	  ]
	}`

	_, _, batchResults, err := extractNodeReport(reportText)
	if err != nil {
		t.Fatalf("extractNodeReport(...) error = %v", err)
	}
	if len(batchResults) != 1 {
		t.Fatalf("len(batchResults) = %d, want 1", len(batchResults))
	}
	if batchResults[0].Failed {
		t.Fatal("batchResults[0].Failed = true, want false when report threshold is lower than measured bus bw")
	}
}

func TestBuildWorkloadPlanCapsBatchCountAtMaxLogicalBatchCount(t *testing.T) {
	t.Parallel()

	plan, err := buildWorkloadPlan(8)
	if err != nil {
		t.Fatalf("buildWorkloadPlan(...) error = %v", err)
	}
	if plan.reportCount != 8 || plan.batchCount != maxBatchCount {
		t.Fatalf("plan = %+v, want reports=8 batches=%d", plan, maxBatchCount)
	}
}

func TestExtractNodeReportIgnoresBatchIndexesBeyondMaxBatchCount(t *testing.T) {
	t.Parallel()

	reportText := `{
	  "version": 1,
	  "workload": "pre-8-node-0",
	  "workload_size": 8,
	  "rank": 0,
	  "node_name": "node-a",
	  "node_ip": "10.0.0.1",
	  "gpu_check": 1,
	  "storage_check": 1,
	  "batches": [
	    {"batch_idx": 0, "pair": ["10.0.0.1", "10.0.0.2"], "self_ip": "10.0.0.1", "allreduce_ms": 1.234, "world_size": 16, "allreduce_shape": 16777216, "dtype_bytes": 4},
	    {"batch_idx": 1, "pair": ["10.0.0.1", "10.0.0.3"], "self_ip": "10.0.0.1", "allreduce_ms": 1.234, "world_size": 16, "allreduce_shape": 16777216, "dtype_bytes": 4},
	    {"batch_idx": 2, "pair": ["10.0.0.1", "10.0.0.4"], "self_ip": "10.0.0.1", "allreduce_ms": 1.234, "world_size": 16, "allreduce_shape": 16777216, "dtype_bytes": 4},
	    {"batch_idx": 3, "pair": ["10.0.0.1", "10.0.0.5"], "self_ip": "10.0.0.1", "allreduce_ms": 1.234, "world_size": 16, "allreduce_shape": 16777216, "dtype_bytes": 4},
	    {"batch_idx": 4, "pair": ["10.0.0.1", "10.0.0.6"], "self_ip": "10.0.0.1", "allreduce_ms": 1.234, "world_size": 16, "allreduce_shape": 16777216, "dtype_bytes": 4},
	    {"batch_idx": 5, "pair": ["10.0.0.1", "10.0.0.7"], "self_ip": "10.0.0.1", "allreduce_ms": 1.234, "world_size": 16, "allreduce_shape": 16777216, "dtype_bytes": 4},
	    {"batch_idx": 6, "pair": ["10.0.0.1", "10.0.0.8"], "self_ip": "10.0.0.1", "allreduce_ms": 1.234, "world_size": 16, "allreduce_shape": 16777216, "dtype_bytes": 4}
	  ]
	}`

	_, plan, batchResults, err := extractNodeReport(reportText)
	if err != nil {
		t.Fatalf("extractNodeReport(...) error = %v", err)
	}
	if plan.batchCount != maxBatchCount {
		t.Fatalf("plan.batchCount = %d, want %d", plan.batchCount, maxBatchCount)
	}
	if len(batchResults) != maxBatchCount {
		t.Fatalf("len(batchResults) = %d, want %d", len(batchResults), maxBatchCount)
	}
	if batchResults[len(batchResults)-1].BatchIdx != batchIndex(maxBatchCount-1) {
		t.Fatalf("last batch index = %d, want %d", batchResults[len(batchResults)-1].BatchIdx, maxBatchCount-1)
	}
}

func TestExtractNodeReportRejectsNonStringBusBWThreshold(t *testing.T) {
	t.Parallel()

	reportText := `{
	  "version": 1,
	  "workload": "pre-2-node-0",
	  "workload_size": 2,
	  "rank": 0,
	  "node_name": "node-a",
	  "node_check_busbw_threshold_gbps": 3.0,
	  "gpu_check": 1,
	  "storage_check": 1,
	  "batches": [
	    {
	      "batch_idx": 0,
	      "pair": ["10.0.0.1", "10.0.0.2"],
	      "self_ip": "10.0.0.1",
	      "status": "fail"
	    }
	  ]
	}`

	_, _, _, err := extractNodeReport(reportText)
	if err == nil || !strings.Contains(err.Error(), "invalid node_check_busbw_threshold_gbps") {
		t.Fatalf("extractNodeReport(...) error = %v, want invalid node_check_busbw_threshold_gbps", err)
	}
}

func TestExtractNodeReportAcceptsStringBatchIdx(t *testing.T) {
	t.Parallel()

	reportText := `{
	  "version": 1,
	  "workload": "pre-2-node-0",
	  "workload_size": 2,
	  "rank": 0,
	  "node_name": "node-a",
	  "gpu_check": 1,
	  "storage_check": 1,
	  "node_check_busbw_threshold_gbps": "5",
	  "batches": [
	    {
	      "batch_idx": "0",
	      "pair": ["10.0.0.1", "10.0.0.2"],
	      "self_ip": "10.0.0.1",
	      "status": "fail"
	    }
	  ]
	}`

	_, _, batchResults, err := extractNodeReport(reportText)
	if err != nil {
		t.Fatalf("extractNodeReport(...) error = %v", err)
	}
	if len(batchResults) != 1 {
		t.Fatalf("len(batchResults) = %d, want 1", len(batchResults))
	}
	if !batchResults[0].Failed {
		t.Fatal("batchResults[0].Failed = false, want true")
	}
}

func TestExtractNodeReportRejectsFractionalBatchIdx(t *testing.T) {
	t.Parallel()

	reportText := `{
	  "version": 1,
	  "workload": "pre-2-node-0",
	  "workload_size": 2,
	  "rank": 0,
	  "node_name": "node-a",
	  "gpu_check": 1,
	  "storage_check": 1,
	  "batches": [
	    {
	      "batch_idx": 0.5,
	      "pair": ["10.0.0.1", "10.0.0.2"],
	      "self_ip": "10.0.0.1",
	      "status": "fail"
	    }
	  ]
	}`

	_, _, _, err := extractNodeReport(reportText)
	if err == nil || !strings.Contains(err.Error(), "invalid batch_idx") {
		t.Fatalf("extractNodeReport(...) error = %v, want invalid batch_idx", err)
	}
}

func TestExtractNodeReportRejectsFractionalPerformanceIntegerFields(t *testing.T) {
	t.Parallel()

	reportText := `{
	  "version": 1,
	  "workload": "pre-2-node-0",
	  "workload_size": 2,
	  "rank": 0,
	  "node_name": "node-a",
	  "gpu_check": 1,
	  "storage_check": 1,
	  "node_check_busbw_threshold_gbps": "5",
	  "batches": [
	    {
	      "batch_idx": 0,
	      "pair": ["10.0.0.1", "10.0.0.2"],
	      "self_ip": "10.0.0.1",
	      "allreduce_ms": 30,
	      "world_size": 16.5,
	      "allreduce_shape": 16777216,
	      "dtype_bytes": 4
	    }
	  ]
	}`

	_, _, _, err := extractNodeReport(reportText)
	if err == nil || !strings.Contains(err.Error(), "invalid performance fields") {
		t.Fatalf("extractNodeReport(...) error = %v, want invalid performance fields", err)
	}
}

func TestSlowNodeAggregatorExpiresIncompleteJob(t *testing.T) {
	t.Parallel()

	aggregator := NewSlowNodeAggregator(10 * time.Second)
	now := time.Unix(100, 0)
	aggregator.now = func() time.Time { return now }

	report := `{"version":1,"workload":"job-a","workload_size":2,"rank":0,"node_name":"node-a","node_ip":"10.0.0.1","gpu_check":1,"storage_check":1,"batches":[{"batch_idx":0,"pair":["10.0.0.1","10.0.0.2"],"self_ip":"10.0.0.1","status":"fail"}]}`
	ready, slowNodes, err := aggregator.AddReport("default", "job-a", report)
	if err != nil {
		t.Fatalf("aggregator.AddReport(...) error = %v", err)
	}
	if ready || slowNodes != nil {
		t.Fatalf("first AddReport = (%v, %v), want (false, nil)", ready, slowNodes)
	}

	now = now.Add(11 * time.Second)
	errs := aggregator.ExpireTimedOutWorkloads()
	if len(errs) != 1 {
		t.Fatalf("len(ExpireTimedOutWorkloads()) = %d, want 1", len(errs))
	}
	if errs[0].FirstReportedNode() != "node-a" {
		t.Fatalf("errs[0].FirstReportedNodeName() = %q, want node-a", errs[0].FirstReportedNode())
	}
	if !strings.Contains(errs[0].Error(), "got 1/2 reports") {
		t.Fatalf("ExpireTimedOutWorkloads error = %q, want report count detail", errs[0])
	}
	if len(aggregator.workloads) != 0 {
		t.Fatalf("len(aggregator.workloads) = %d, want 0", len(aggregator.workloads))
	}
	if !errors.Is(errs[0], ErrWorkloadReportTimeout) {
		t.Fatalf("ExpireTimedOutWorkloads error = %v, want ErrWorkloadReportTimeout", errs[0])
	}

	ready, slowNodes, err = aggregator.AddReport("default", "job-a", report)
	if err != nil {
		t.Fatalf("aggregator.AddReport(...) after expiry error = %v", err)
	}
	if ready || slowNodes != nil {
		t.Fatalf("AddReport after expiry = (%v, %v), want (false, nil)", ready, slowNodes)
	}
}

func TestSlowNodeAggregatorAddReportIgnoresOtherStaleWorkloads(t *testing.T) {
	t.Parallel()

	aggregator := NewSlowNodeAggregator(10 * time.Second)
	now := time.Unix(100, 0)
	aggregator.SetNowForTest(func() time.Time { return now })

	staleReport := `{"version":1,"workload":"job-a","workload_size":2,"rank":0,"node_name":"node-a","node_ip":"10.0.0.1","gpu_check":1,"storage_check":1,"batches":[{"batch_idx":0,"pair":["10.0.0.1","10.0.0.2"],"self_ip":"10.0.0.1","status":"fail"}]}`
	currentReport := `{"version":1,"workload":"job-b","workload_size":2,"rank":0,"node_name":"node-c","node_ip":"10.0.0.3","gpu_check":1,"storage_check":1,"batches":[{"batch_idx":0,"pair":["10.0.0.3","10.0.0.4"],"self_ip":"10.0.0.3","status":"fail"}]}`

	ready, slowNodes, err := aggregator.AddReport("default", "job-a", staleReport)
	if err != nil {
		t.Fatalf("aggregator.AddReport(...) stale seed error = %v", err)
	}
	if ready || slowNodes != nil {
		t.Fatalf("stale seed AddReport = (%v, %v), want (false, nil)", ready, slowNodes)
	}

	now = now.Add(11 * time.Second)
	ready, slowNodes, err = aggregator.AddReport("default", "job-b", currentReport)
	if err != nil {
		t.Fatalf("aggregator.AddReport(...) current workload error = %v", err)
	}
	if ready || slowNodes != nil {
		t.Fatalf("current workload AddReport = (%v, %v), want (false, nil)", ready, slowNodes)
	}
	if len(aggregator.workloads) != 2 {
		t.Fatalf("len(aggregator.workloads) = %d, want 2", len(aggregator.workloads))
	}

	errs := aggregator.ExpireTimedOutWorkloads()
	if len(errs) != 1 {
		t.Fatalf("len(ExpireTimedOutWorkloads()) = %d, want 1", len(errs))
	}
	if errs[0].WorkloadName != "job-a" {
		t.Fatalf("errs[0].WorkloadName = %q, want job-a", errs[0].WorkloadName)
	}
	if len(aggregator.workloads) != 1 {
		t.Fatalf("len(aggregator.workloads) after expiry = %d, want 1", len(aggregator.workloads))
	}
	if _, ok := aggregator.workloads[workloadKey{namespace: "default", workloadUID: "job-b", workloadName: "job-b"}]; !ok {
		t.Fatal("job-b workload missing after ExpireTimedOutWorkloads()")
	}
}

func TestSlowNodeAggregatorDoesNotCombineReportsOutsideObservationWindow(t *testing.T) {
	t.Parallel()

	aggregator := NewSlowNodeAggregator(10 * time.Second)
	firstObservedAt := time.Unix(100, 0)
	first := `{"version":1,"workload":"job-a","workload_size":2,"rank":0,"node_name":"node-a","node_ip":"10.0.0.1","gpu_check":1,"storage_check":1,"batches":[{"batch_idx":0,"pair":["10.0.0.1","10.0.0.2"],"self_ip":"10.0.0.1","status":"fail"}]}`
	second := `{"version":1,"workload":"job-a","workload_size":2,"rank":1,"node_name":"node-b","node_ip":"10.0.0.2","gpu_check":1,"storage_check":1,"batches":[{"batch_idx":0,"pair":["10.0.0.1","10.0.0.2"],"self_ip":"10.0.0.2","status":"fail"}]}`

	ready, _, err := aggregator.AddReportForWorkload("default", "job-uid", "job-a", first, firstObservedAt)
	if err != nil || ready {
		t.Fatalf("first AddReportForWorkload() = %v, %v, want false, nil", ready, err)
	}
	ready, _, err = aggregator.AddReportForWorkload("default", "job-uid", "job-a", second, firstObservedAt.Add(11*time.Second))
	if ready || !errors.Is(err, ErrWorkloadReportTimeout) {
		t.Fatalf("late AddReportForWorkload() = %v, %v, want false, timeout", ready, err)
	}

	ready, _, err = aggregator.AddReportForWorkload("default", "job-uid", "job-a", first, firstObservedAt.Add(12*time.Second))
	if err != nil || !ready {
		t.Fatalf("replacement-window AddReportForWorkload() = %v, %v, want true, nil", ready, err)
	}
}

func roundRobinReports(nodeCount int, failingNodes map[string]struct{}) []string {
	nodeNames := make([]string, 0, nodeCount)
	nodeIPs := make([]string, 0, nodeCount)
	for idx := 0; idx < nodeCount; idx++ {
		nodeNames = append(nodeNames, fmt.Sprintf("node-%02d", idx))
		nodeIPs = append(nodeIPs, testNodeIP(idx))
	}

	schedule := roundRobinPairs(nodeIPs)
	reports := make([]string, 0, nodeCount)
	for rank, nodeName := range nodeNames {
		nodeIP := nodeIPs[rank]
		batches := make([]string, 0, len(schedule))
		for batchIdx, pairs := range schedule {
			for _, pair := range pairs {
				if pair[0] != nodeIP && pair[1] != nodeIP {
					continue
				}

				entry := fmt.Sprintf(`{"batch_idx":%d,"pair":["%s","%s"]`, batchIdx, pair[0], pair[1])
				entry += fmt.Sprintf(`,"self_ip":"%s"`, nodeIP)
				if _, exists := failingNodes[nodeName]; exists {
					entry += `,"status":"fail"}`
				} else {
					entry += `,"allreduce_ms":0.5,"world_size":16,"allreduce_shape":16777216,"dtype_bytes":4}`
				}
				batches = append(batches, entry)
				break
			}
		}

		reports = append(reports, fmt.Sprintf(
			`{"version":1,"workload":"job-16","workload_size":%d,"rank":%d,"node_name":"%s","node_ip":"%s","gpu_check":1,"storage_check":1,"batches":[%s]}`,
			nodeCount,
			rank,
			nodeName,
			nodeIP,
			strings.Join(batches, ","),
		))
	}

	return reports
}

func roundRobinReportsWithoutWorldSize(nodeCount int, failingNodes map[string]struct{}) []string {
	reports := roundRobinReports(nodeCount, failingNodes)
	trimmed := make([]string, 0, len(reports))
	for _, report := range reports {
		trimmed = append(trimmed, strings.Replace(report, fmt.Sprintf(`"workload_size":%d,`, nodeCount), "", 1))
	}

	return trimmed
}

func roundRobinPairs(nodeNames []string) [][][2]string {
	working := append([]string(nil), nodeNames...)
	batchCount := len(working) - 1
	batches := make([][][2]string, 0, batchCount)

	for batchIdx := 0; batchIdx < batchCount; batchIdx++ {
		pairs := make([][2]string, 0, len(working)/2)
		for left := 0; left < len(working)/2; left++ {
			right := len(working) - 1 - left
			pair := [2]string{working[left], working[right]}
			if pair[0] > pair[1] {
				pair[0], pair[1] = pair[1], pair[0]
			}
			pairs = append(pairs, pair)
		}
		batches = append(batches, pairs)

		rotated := append([]string{working[0], working[len(working)-1]}, working[1:len(working)-1]...)
		working = rotated
	}

	return batches
}
