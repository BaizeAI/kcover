package preflight

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	kcoverv1alpha1 "github.com/baizeai/kcover/pkg/apis/kcover/v1alpha1"
	"github.com/baizeai/kcover/pkg/constants"
	"github.com/baizeai/kcover/pkg/events"
)

type compactReportPayload struct {
	report Report
	raw    map[string]any
}

func ReportPath(baseDir, namespace, reportName string) string {
	return filepath.Join(baseDir, namespace, reportName+".json")
}

func LoadReportPayload(baseDir, namespace, reportName string) (string, string, error) {
	path := ReportPath(baseDir, namespace, reportName)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("read %s: %w", path, err)
	}

	payload, nodeName, err := CompactReport(string(data))
	if err != nil {
		return "", "", fmt.Errorf("parse %s: %w", path, err)
	}

	if nodeName == "" {
		return "", "", fmt.Errorf("parse compacted %s: preflight report node name is empty", path)
	}

	return payload, nodeName, nil
}

func ObservationEvent(report *kcoverv1alpha1.PreflightReport) events.Event {
	annotations := map[string]string{
		constants.PreflightWorkloadAnnotation: report.Spec.WorkloadName,
		constants.PreflightReportAnnotation:   report.Name,
	}

	return events.Event{
		ResourceType: events.Node,
		Namespace:    report.Namespace,
		Name:         report.Spec.NodeName,
		Annotations:  annotations,
		Message:      fmt.Sprintf("preflight report %s received for workload %s on node %s", report.Name, report.Spec.WorkloadName, report.Spec.NodeName),
	}
}

func reportIdentity(namespace, workloadUID, nodeName string, rank int, reportText string) string {
	sum := sha256.Sum256([]byte(reportText))
	return fmt.Sprintf("%s/%s/%s/%d/%s", namespace, workloadUID, nodeName, rank, hex.EncodeToString(sum[:]))
}

func CompactReport(reportText string) (string, string, error) {
	payload, err := parseCompactReportPayload(reportText)
	if err != nil {
		return "", "", err
	}

	return compactReportPayloadToJSON(payload)
}

func parseCompactReportPayload(reportText string) (compactReportPayload, error) {
	report, err := parseReport(reportText)
	if err != nil {
		return compactReportPayload{}, err
	}

	var raw map[string]any
	if err := json.Unmarshal([]byte(reportText), &raw); err != nil {
		return compactReportPayload{}, fmt.Errorf("unmarshal preflight payload: %w", err)
	}

	return compactReportPayload{report: report, raw: raw}, nil
}

func compactReportPayloadToJSON(payload compactReportPayload) (string, string, error) {
	report := payload.report
	raw := payload.raw

	compact := map[string]any{
		"workload_size": report.WorkloadSize,
		"rank":          report.Rank,
		"node_name":     report.NodeName,
		"node_ip":       report.NodeIP,
		"gpu_check":     report.GPUCheck,
		"storage_check": report.StorageCheck,
	}
	if threshold, ok, err := compactBusBWThreshold(raw); err != nil {
		return "", "", err
	} else if ok {
		compact["node_check_busbw_threshold_gbps"] = threshold
	}

	batchRaw, _ := raw["batches"].([]any)
	batches := make([]map[string]any, 0, len(batchRaw))
	for _, item := range batchRaw {
		batch, ok := item.(map[string]any)
		if !ok {
			return "", "", fmt.Errorf("invalid batch payload type %T", item)
		}

		compactedBatch := make(map[string]any, 8)
		copyIfPresent(compactedBatch, batch, "batch_idx")
		copyIfPresent(compactedBatch, batch, "pair")
		copyIfPresent(compactedBatch, batch, "self_ip")
		copyIfPresent(compactedBatch, batch, "local_rank")
		copyIfPresent(compactedBatch, batch, "status")
		copyIfPresent(compactedBatch, batch, "allreduce_ms")
		copyIfPresent(compactedBatch, batch, "world_size")
		copyIfPresent(compactedBatch, batch, "allreduce_shape")
		copyIfPresent(compactedBatch, batch, "dtype_bytes")
		batches = append(batches, compactedBatch)
	}
	compact["batches"] = batches

	encoded, err := json.Marshal(compact)
	if err != nil {
		return "", "", fmt.Errorf("marshal compact preflight report: %w", err)
	}

	return string(encoded), report.NodeName, nil
}

func compactBusBWThreshold(raw map[string]any) (string, bool, error) {
	threshold, ok := raw[busbwThreshold]
	if !ok {
		return "", false, nil
	}

	thresholdText, ok := threshold.(string)
	if !ok {
		return "", false, fmt.Errorf("invalid %s: unsupported type %T", busbwThreshold, threshold)
	}

	thresholdText = strings.TrimSpace(thresholdText)
	if thresholdText == "" {
		return "", false, nil
	}

	return thresholdText, true, nil
}

func copyIfPresent(dst, src map[string]any, key string) {
	value, ok := src[key]
	if !ok {
		return
	}

	dst[key] = value
}
