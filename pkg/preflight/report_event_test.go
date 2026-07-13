package preflight

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/baizeai/kcover/pkg/constants"
	"github.com/baizeai/kcover/pkg/events"
)

func TestLoadReportPayloadReturnsNodeName(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	path := filepath.Join(baseDir, "default")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	reportPath := filepath.Join(path, "worker-0.json")
	if err := os.WriteFile(reportPath, []byte(`{"version":1,"workload":"job-a","workload_size":2,"rank":0,"node_name":"node-a","node_ip":"10.0.0.1","gpu_check":1,"storage_check":1,"batches":[{"batch_idx":0,"pair":["10.0.0.1","10.0.0.2"],"self_ip":"10.0.0.1","status":"fail"}]}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	payload, nodeName, err := LoadReportPayload(baseDir, "default", "worker-0")
	if err != nil {
		t.Fatalf("LoadReportPayload() error = %v", err)
	}
	if nodeName != "node-a" {
		t.Fatalf("nodeName = %q, want %q", nodeName, "node-a")
	}
	if payload == "" {
		t.Fatal("payload = empty, want non-empty")
	}
}

func TestLoadReportPayloadCompactsToMinimalManagerFields(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	path := filepath.Join(baseDir, "default")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	reportPath := filepath.Join(path, "worker-0.json")
	raw := `{
	  "version": 1,
	  "workload": "demo-train",
	  "workload_size": 4,
	  "rank": 0,
	  "node_name": "node-7",
	  "node_ip": "10.0.0.7",
	  "gpu_check": 1,
	  "storage_check": 1,
	  "node_check_busbw_threshold_gbps": "12.5",
	  "batches": [
	    {"schema":"v3","phase":"pairwise","batch_idx":0,"pair":["10.0.0.7","10.0.0.8"],"self_ip":"10.0.0.7","local_rank":0,"device":"MetaX C500","status":"ok","allreduce_ms":12.345,"world_size":16,"allreduce_shape":268435456,"dtype_bytes":4,"ranks_recorded":8},
	    {"schema":"v3","batch_idx":1,"pair":["10.0.0.7","10.0.0.9"],"self_ip":"10.0.0.7","status":"fail","rc":124}
	  ]
	}`
	if err := os.WriteFile(reportPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	payload, nodeName, err := LoadReportPayload(baseDir, "default", "worker-0")
	if err != nil {
		t.Fatalf("LoadReportPayload() error = %v", err)
	}
	if nodeName != "node-7" {
		t.Fatalf("nodeName = %q, want %q", nodeName, "node-7")
	}
	if len(payload) >= len(raw) {
		t.Fatalf("len(payload) = %d, want < %d", len(payload), len(raw))
	}

	var compact map[string]any
	if err := json.Unmarshal([]byte(payload), &compact); err != nil {
		t.Fatalf("json.Unmarshal(payload) error = %v", err)
	}
	if _, exists := compact["version"]; exists {
		t.Fatal("compact payload unexpectedly keeps version")
	}
	if _, exists := compact["workload"]; exists {
		t.Fatal("compact payload unexpectedly keeps workload")
	}
	if compact["node_ip"] != "10.0.0.7" {
		t.Fatalf("compact node_ip = %v, want 10.0.0.7", compact["node_ip"])
	}
	if compact["node_check_busbw_threshold_gbps"] != "12.5" {
		t.Fatalf("compact threshold = %v, want 12.5", compact["node_check_busbw_threshold_gbps"])
	}
	batches, ok := compact["batches"].([]any)
	if !ok || len(batches) != 2 {
		t.Fatalf("compact batches = %v, want two batches", compact["batches"])
	}
	batch0 := batches[0].(map[string]any)
	if batch0["status"] != "ok" {
		t.Fatalf("batch0 status = %v, want ok", batch0["status"])
	}
	if _, exists := batch0["device"]; exists {
		t.Fatal("compact batch unexpectedly keeps device")
	}
	if _, exists := batch0["ranks_recorded"]; exists {
		t.Fatal("compact batch unexpectedly keeps ranks_recorded")
	}
	if batch0["allreduce_ms"] != 12.345 {
		t.Fatalf("batch0 allreduce_ms = %v, want 12.345", batch0["allreduce_ms"])
	}
	batch1 := batches[1].(map[string]any)
	if batch1["status"] != "fail" {
		t.Fatalf("batch1 status = %v, want fail", batch1["status"])
	}
	if _, exists := batch1["rc"]; exists {
		t.Fatal("compact batch unexpectedly keeps rc")
	}
}

func TestLoadReportPayloadDropsEmptyBusBWThreshold(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	path := filepath.Join(baseDir, "default")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	reportPath := filepath.Join(path, "worker-0.json")
	raw := `{"version":1,"workload":"job-a","workload_size":2,"rank":0,"node_name":"node-a","node_ip":"10.0.0.1","gpu_check":1,"storage_check":1,"node_check_busbw_threshold_gbps":"  ","batches":[{"batch_idx":0,"pair":["10.0.0.1","10.0.0.2"],"self_ip":"10.0.0.1","allreduce_ms":0.5,"world_size":16,"allreduce_shape":16777216,"dtype_bytes":4}]}`
	if err := os.WriteFile(reportPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	payload, _, err := LoadReportPayload(baseDir, "default", "worker-0")
	if err != nil {
		t.Fatalf("LoadReportPayload() error = %v", err)
	}

	var compact map[string]any
	if err := json.Unmarshal([]byte(payload), &compact); err != nil {
		t.Fatalf("json.Unmarshal(payload) error = %v", err)
	}
	if _, exists := compact["node_check_busbw_threshold_gbps"]; exists {
		t.Fatal("compact payload unexpectedly keeps empty busbw threshold")
	}
}

func TestReportPath(t *testing.T) {
	t.Parallel()

	path := ReportPath("/var/lib/kcover/preflight", "default", "worker-0")
	if path != "/var/lib/kcover/preflight/default/worker-0.json" {
		t.Fatalf("ReportPath(...) = %q, want %q", path, "/var/lib/kcover/preflight/default/worker-0.json")
	}
}

func TestReportToEvent(t *testing.T) {
	t.Parallel()

	report, err := BuildPreflightReport("default", "node-a", "job-a", "job-uid", `{"version":1,"rank":0,"node_name":"node-a"}`, time.Unix(100, 0))
	if err != nil {
		t.Fatalf("BuildPreflightReport() error = %v", err)
	}
	event := ObservationEvent(report)
	if event.ResourceType != events.Node {
		t.Fatalf("event.ResourceType = %s, want %s", event.ResourceType, events.Node)
	}
	if event.Name != "node-a" {
		t.Fatalf("event.Name = %q, want %q", event.Name, "node-a")
	}
	if event.Annotations[constants.PreflightWorkloadAnnotation] != "job-a" {
		t.Fatalf("workload annotation = %q, want %q", event.Annotations[constants.PreflightWorkloadAnnotation], "job-a")
	}
	if event.Message != "preflight report received for workload job-a on node node-a" {
		t.Fatalf("event.Message = %q, want human-readable observation", event.Message)
	}
	if event.EventType != 0 {
		t.Fatalf("event.EventType = %d, want 0", event.EventType)
	}
}

func TestReportToEventRejectsEmptyWorkload(t *testing.T) {
	t.Parallel()

	_, err := BuildPreflightReport("default", "node-a", "", "job-uid", `{"version":1,"rank":0,"node_name":"node-a"}`, time.Unix(100, 0))
	if err == nil {
		t.Fatal("ReportToEvent() error = nil, want non-nil")
	}
}
