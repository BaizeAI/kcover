package preflight

import (
	"encoding/json"
	"fmt"
	"math"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"k8s.io/klog/v2"
)

func extractNodeReport(txt string) (Report, workloadPlan, []batchResult, error) {
	report, err := parseReport(txt)
	if err != nil {
		return Report{}, workloadPlan{}, nil, err
	}
	plan, err := buildWorkloadPlan(report.WorkloadSize)
	if err != nil {
		return Report{}, workloadPlan{}, nil, err
	}
	// fail-fast: gpu/storage failure means this node is already abnormal, so we
	// skip pairwise preflight parsing and let detectSlowNodes report it directly.
	if report.GPUCheck == CheckResultFail || report.StorageCheck == CheckResultFail {
		return report, plan, nil, nil
	}

	payload, err := unmarshalReportPayload(txt)
	if err != nil {
		return Report{}, workloadPlan{}, nil, err
	}
	busBWThresholdGBPS, err := extractBusBWThreshold(payload)
	if err != nil {
		return Report{}, workloadPlan{}, nil, err
	}

	m, err := extractBatchResults(payload, report, plan, busBWThresholdGBPS)
	if err != nil {
		return Report{}, workloadPlan{}, nil, err
	}

	results := orderedBatchResults(m)

	return report, plan, results, nil
}

func unmarshalReportPayload(txt string) (map[string]any, error) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(txt), &payload); err != nil {
		return nil, fmt.Errorf("unmarshal preflight payload: %w", err)
	}

	return payload, nil
}

/*
# 状态值
# 0: SKIP
# 1: PASS（已经执行且通过）
# 2: FAIL（已经执行但失败）

	{
	  "version": 1,
	  "workload": "demo-train",
	  "workload_size": 4,
	  "rank": 0,
	  "node_name": "node-7",
	  "storage_check": 1,
	  "gpu_check": 1,
	  "node_check_busbw_threshold_gbps": "12.5",
	  "batches": [
	    {
	      "batch_idx": 0,
	      "pair": ["10.0.0.7", "10.0.0.8"],
	      "self_ip": "10.0.0.7",
	      "status": "ok",
	      "allreduce_ms": 12.345,
	      "world_size": 16,
	      "allreduce_shape": 268435456,
	      "dtype_bytes": 4,
	    },
	    {
	      "batch_idx": 1,
	      "pair": ["10.0.0.7", "10.0.0.9"],
	      "self_ip": "10.0.0.7",
	      "status": "fail",
	    }
	  ]
	}
*/
func extractBatchResults(payload map[string]any, report Report, plan workloadPlan, busBWThresholdGBPS float64) (map[batchIndex]batchResult, error) {
	batchRaw, _ := payload["batches"].([]any)

	batchResultsByIndex := make(map[batchIndex]batchResult, plan.batchCount)
	for _, item := range batchRaw {
		batchMap, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid batch payload type %T", item)
		}

		rawBatchIdx, err := intField(batchMap["batch_idx"])
		if err != nil {
			return nil, fmt.Errorf("invalid batch_idx: %w", err)
		}
		if rawBatchIdx < 0 {
			return nil, fmt.Errorf("batch_idx %d out of range [0,%d)", rawBatchIdx, plan.batchCount)
		}
		if rawBatchIdx >= plan.batchCount {
			continue
		}

		result, err := extractBatchResult(item, report.NodeIP, plan.batchCount, busBWThresholdGBPS)
		if err != nil {
			return nil, err
		}
		if _, exists := batchResultsByIndex[result.BatchIdx]; exists {
			return nil, fmt.Errorf("duplicate batch_idx %d in report for %s", result.BatchIdx, report.NodeName)
		}
		batchResultsByIndex[result.BatchIdx] = result
	}

	return batchResultsByIndex, nil
}

func extractBatchResult(item any, reporterIP string, batchCount int, busBWThresholdGBPS float64) (batchResult, error) {
	batchMap, ok := item.(map[string]any)
	if !ok {
		return batchResult{}, fmt.Errorf("invalid batch payload type %T", item)
	}

	rawBatchIdx, err := intField(batchMap["batch_idx"])
	if err != nil {
		return batchResult{}, fmt.Errorf("invalid batch_idx: %w", err)
	}
	if rawBatchIdx < 0 || rawBatchIdx >= batchCount {
		return batchResult{}, fmt.Errorf("batch_idx %d out of range [0,%d)", rawBatchIdx, batchCount)
	}
	batchIdx := batchIndex(rawBatchIdx)
	status, _ := batchMap["status"].(string)
	if strings.EqualFold(status, "skip") {
		pair, selfIP, err := skippedBatchIdentity(batchMap, nodeIP(reporterIP))
		if err != nil {
			return batchResult{}, fmt.Errorf("batch %d: %w", batchIdx, err)
		}
		failed, err := batchFailed(batchMap, rawBatchIdx, busBWThresholdGBPS)
		if err != nil {
			return batchResult{}, err
		}

		return batchResult{
			BatchIdx:   batchIdx,
			PairFirst:  pair[0],
			PairSecond: pair[1],
			SelfIP:     selfIP,
			Failed:     failed,
		}, nil
	}

	pair, ok := pairField(batchMap["pair"])
	if !ok {
		return batchResult{}, fmt.Errorf("invalid pair in batch %d", batchIdx)
	}
	selfIP, err := batchSelfIP(batchMap, nodeIP(reporterIP), pair)
	if err != nil {
		return batchResult{}, fmt.Errorf("batch %d: %w", batchIdx, err)
	}

	failed, err := batchFailed(batchMap, rawBatchIdx, busBWThresholdGBPS)
	if err != nil {
		return batchResult{}, err
	}

	return batchResult{
		BatchIdx:   batchIdx,
		PairFirst:  pair[0],
		PairSecond: pair[1],
		SelfIP:     selfIP,
		Failed:     failed,
	}, nil
}

func batchFailed(batchMap map[string]any, batchIdx int, busBWThresholdGBPS float64) (bool, error) {
	if status, ok := batchMap["status"].(string); ok && strings.EqualFold(status, "fail") {
		return true, nil
	}
	if status, ok := batchMap["status"].(string); ok && strings.EqualFold(status, "skip") {
		klog.Warningf("preflight batch skipped and treated as abnormal: batchIdx=%d pair=%v selfIP=%v reason=%v", batchIdx, batchMap["pair"], batchMap["self_ip"], batchMap["reason"])
		return true, nil
	}
	if busBWThresholdGBPS == 0 {
		return false, nil
	}

	allreduceMS, errMS := floatField(batchMap["allreduce_ms"])
	allreduceShape, errShape := intField(batchMap["allreduce_shape"])
	dtypeBytes, errBytes := intField(batchMap["dtype_bytes"])
	batchWorldSize, errWS := intField(batchMap["world_size"])
	if errMS != nil || errShape != nil || errBytes != nil || errWS != nil {
		return false, fmt.Errorf("invalid performance fields in batch %d", batchIdx)
	}

	if allreduceMS <= 0 || allreduceShape <= 0 || dtypeBytes <= 0 || batchWorldSize <= 1 {
		return true, nil
	}
	bw := calculateBusBW(allreduceMS, allreduceShape, dtypeBytes, batchWorldSize)
	klog.V(4).InfoS("calculated preflight batch bus bandwidth", "batchIdx", batchIdx, "allreduceMS", allreduceMS, "allreduceShape", allreduceShape, "dtypeBytes", dtypeBytes, "worldSize", batchWorldSize, "busBWGBPS", bw, "thresholdGBPS", busBWThresholdGBPS, "failed", bw < busBWThresholdGBPS)
	return bw < busBWThresholdGBPS, nil
}

func calculateBusBW(allreduceMS float64, allreduceShape, dtypeBytes, batchWorldSize int) float64 {
	// busbw = (allreduce_shape × dtype_bytes / 1024^3) / (allreduce_ms / 1000) × 2 × (world_size - 1) / world_size

	payloadBytes := float64(allreduceShape * dtypeBytes)
	algoBW := payloadBytes / math.Pow(1024, 3) / (allreduceMS / 1000)
	return algoBW * 2 * float64(batchWorldSize-1) / float64(batchWorldSize)
}

func orderedBatchResults(m map[batchIndex]batchResult) []batchResult {
	batchIndexes := make([]batchIndex, 0, len(m))
	for idx := range m {
		batchIndexes = append(batchIndexes, idx)
	}
	sort.Slice(batchIndexes, func(i, j int) bool {
		return batchIndexes[i] < batchIndexes[j]
	})

	results := make([]batchResult, 0, len(batchIndexes))
	for _, idx := range batchIndexes {
		results = append(results, m[idx])
	}

	return results
}

const busbwThreshold = "node_check_busbw_threshold_gbps"

func extractBusBWThreshold(payload map[string]any) (float64, error) {
	threshold, exists := payload[busbwThreshold]
	if !exists {
		return DefaultBusBWThresholdGBPS, nil
	}

	thresholdText, ok := threshold.(string)
	if !ok {
		return 0, fmt.Errorf("invalid %s: unsupported type %T", busbwThreshold, threshold)
	}
	thresholdText = strings.TrimSpace(thresholdText)
	if thresholdText == "" {
		return DefaultBusBWThresholdGBPS, nil
	}

	value, err := strconv.ParseFloat(thresholdText, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", busbwThreshold, err)
	}
	if value < 0 {
		return 0, fmt.Errorf("invalid %s: %v", busbwThreshold, value)
	}

	return value, nil
}

func resolveNodeName(nodeIPToName map[nodeIP]nodeName, ip nodeIP) string {
	if nodeName, ok := nodeIPToName[ip]; ok {
		return string(nodeName)
	}

	return string(ip)
}

func buildWorkloadPlan(workloadSize int) (workloadPlan, error) {
	if err := validateWorkloadSize(workloadSize); err != nil {
		return workloadPlan{}, err
	}

	plan := workloadPlan{
		reportCount: workloadSize,
		batchCount:  min(workloadSize-1, maxBatchCount),
	}
	return plan, nil
}

func validateWorkloadSize(workloadSize int) error {
	if workloadSize <= 0 {
		return fmt.Errorf("cannot resolve preflight layout without workload_size")
	}
	if workloadSize > maxWorkloadSize {
		return fmt.Errorf("workload_size %d exceeds maximum %d", workloadSize, maxWorkloadSize)
	}
	if workloadSize%2 != 0 {
		return fmt.Errorf("odd workload sizes are not supported: workload_size=%d", workloadSize)
	}

	return nil
}

func intField(v any) (int, error) {
	value, err := floatField(v)
	if err != nil {
		return 0, err
	}
	if math.Trunc(value) != value {
		return 0, fmt.Errorf("must be an integer")
	}

	return int(value), nil
}

func floatField(v any) (float64, error) {
	switch value := v.(type) {
	case float64:
		return value, nil
	case string:
		if value == "" {
			return 0, fmt.Errorf("empty")
		}
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return 0, err
		}
		return parsed, nil
	case json.Number:
		parsed, err := value.Float64()
		if err != nil {
			return 0, err
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("unsupported type %T", v)
	}
}

func pairField(v any) ([2]nodeIP, bool) {
	items, ok := v.([]any)
	if !ok || len(items) != 2 {
		return [2]nodeIP{}, false
	}

	a, okA := items[0].(string)
	b, okB := items[1].(string)
	if !okA || !okB || a == "" || b == "" {
		return [2]nodeIP{}, false
	}

	pair := [2]nodeIP{nodeIP(a), nodeIP(b)}
	if shouldSwapPair(string(pair[0]), string(pair[1])) {
		pair[0], pair[1] = pair[1], pair[0]
	}

	return pair, true
}

func shouldSwapPair(left, right string) bool {
	leftIP, leftErr := netip.ParseAddr(left)
	rightIP, rightErr := netip.ParseAddr(right)
	if leftErr == nil && rightErr == nil {
		return leftIP.Compare(rightIP) > 0
	}

	return left > right
}

func batchSelfIP(batchMap map[string]any, reporterIP nodeIP, pair [2]nodeIP) (nodeIP, error) {
	if rawSelfIP, ok := batchMap["self_ip"]; ok {
		selfIP, ok := rawSelfIP.(string)
		if !ok || selfIP == "" {
			return "", fmt.Errorf("invalid self_ip")
		}
		selfIPValue := nodeIP(selfIP)
		if pair[0] != selfIPValue && pair[1] != selfIPValue {
			return "", fmt.Errorf("pair %q/%q does not include self_ip %s", pair[0], pair[1], selfIP)
		}
		return selfIPValue, nil
	}

	if pair[0] != reporterIP && pair[1] != reporterIP {
		return "", fmt.Errorf("pair %q/%q does not include reporter_ip %s", pair[0], pair[1], reporterIP)
	}

	return reporterIP, nil
}
