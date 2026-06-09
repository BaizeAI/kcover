package preflight

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

type nodeName string
type nodeIP string
type batchIndex int

type nodeNameSet map[nodeName]struct{}
type nodeIPSet map[nodeIP]struct{}

type batchResult struct {
	BatchIdx   batchIndex
	PairFirst  nodeIP
	PairSecond nodeIP
	SelfIP     nodeIP
	Failed     bool
}

type workloadKey struct {
	namespace    string
	workloadName string
}

type workload struct {
	expectedReportCount int
	expectedBatchCount  int
	nodeReports         map[nodeName]nodeReport
	lastReportAt        time.Time
}

type nodeReport struct {
	nodeName     nodeName
	selfIP       nodeIP
	failFast     bool
	batchResults []batchResult
}

type workloadPlan struct {
	reportCount int
	batchCount  int
}

const maxBatchCount = 5

type WorkloadTimeoutError struct {
	Namespace       string
	WorkloadName    string
	ReportedNodes   []string
	ReceivedReports int
	ExpectedReports int
	Timeout         time.Duration
}

// SlowNodeAggregator 聚合同一个 workload 的多份 preflight JSON 报告，报告收齐后输出慢节点。
type SlowNodeAggregator struct {
	mu        sync.Mutex
	workloads map[workloadKey]*workload
	timeout   time.Duration
	now       func() time.Time
}

var ErrWorkloadReportTimeout = errors.New("preflight workload report collection timed out")

func (e WorkloadTimeoutError) Error() string {
	return fmt.Sprintf(
		"%s for %s/%s: got %d/%d reports within %s",
		ErrWorkloadReportTimeout,
		e.Namespace,
		e.WorkloadName,
		e.ReceivedReports,
		e.ExpectedReports,
		e.Timeout,
	)
}

func (e WorkloadTimeoutError) Unwrap() error {
	return ErrWorkloadReportTimeout
}

func (e WorkloadTimeoutError) FirstReportedNode() string {
	if len(e.ReportedNodes) == 0 {
		return ""
	}

	return e.ReportedNodes[0]
}

func NewSlowNodeAggregator(timeout time.Duration) *SlowNodeAggregator {
	if timeout <= 0 {
		timeout = DefaultReportCollectionTimeout
	}

	return &SlowNodeAggregator{
		workloads: make(map[workloadKey]*workload),
		timeout:   timeout,
		now:       time.Now,
	}
}

func (c *SlowNodeAggregator) SetNowForTest(now func() time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if now == nil {
		c.now = time.Now
		return
	}

	c.now = now
}

// AddReport 将单条 JSON 报告并入对应 workload。
// 当返回 ready=true 时，slowNodes 是完整聚合后的慢节点结论。
func (c *SlowNodeAggregator) AddReport(ns, workloadName, reportText string) (ready bool, slowNodes []string, err error) {
	if ns == "" || workloadName == "" {
		return false, nil, fmt.Errorf("namespace and workload name must not be empty")
	}

	report, plan, batchResults, err := extractNodeReport(reportText)
	if err != nil {
		return false, nil, fmt.Errorf("extract preflight report: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	key := workloadKey{namespace: ns, workloadName: workloadName}
	if expired, ok := c.expireWorkloadIfTimedOut(key, now); ok {
		return false, nil, expired
	}
	wkl := c.workloadForReport(key, plan, now)

	failFast := report.GPUCheck == CheckResultFail || report.StorageCheck == CheckResultFail
	if !failFast && len(batchResults) == 0 {
		klog.Warningf("preflight report has no batch results, falling back to fail-fast: namespace=%s workload=%s node=%s workloadSize=%d", ns, workloadName, report.NodeName, report.WorkloadSize)
		failFast = true
	}
	np := nodeReport{
		nodeName:     nodeName(report.NodeName),
		selfIP:       nodeIP(report.NodeIP),
		failFast:     failFast,
		batchResults: batchResults}
	wkl.nodeReports[nodeName(report.NodeName)] = np
	wkl.lastReportAt = now

	if len(wkl.nodeReports) < wkl.expectedReportCount {
		return false, nil, nil
	}

	slowNodes = detectSlowNodes(ns, workloadName, wkl.nodeReports, wkl.expectedBatchCount)

	delete(c.workloads, key)
	return true, slowNodes, nil
}

func (c *SlowNodeAggregator) workloadForReport(key workloadKey, plan workloadPlan, now time.Time) *workload {
	wkl, ok := c.workloads[key]
	if !ok {
		return c.ensureWorkload(key, plan, now)
	}
	if wkl.isSamePlan(plan) {
		return wkl
	}

	klog.Warningf(
		"preflight layout changed for %s/%s: got %d reports/%d batches, resetting previous %d reports/%d batches state",
		key.namespace,
		key.workloadName,
		plan.reportCount,
		plan.batchCount,
		wkl.expectedReportCount,
		wkl.expectedBatchCount,
	)
	return c.ensureWorkload(key, plan, now)
}

func (c *SlowNodeAggregator) ensureWorkload(key workloadKey, plan workloadPlan, now time.Time) *workload {
	wkl := &workload{
		expectedReportCount: plan.reportCount,
		expectedBatchCount:  plan.batchCount,
		nodeReports:         make(map[nodeName]nodeReport, plan.reportCount),
		lastReportAt:        now,
	}
	c.workloads[key] = wkl
	return wkl
}

func (w *workload) isSamePlan(plan workloadPlan) bool {
	return w.expectedReportCount == plan.reportCount && w.expectedBatchCount == plan.batchCount
}

func (c *SlowNodeAggregator) expireWorkloadIfTimedOut(key workloadKey, now time.Time) (WorkloadTimeoutError, bool) {
	workload, ok := c.workloads[key]
	if !ok {
		return WorkloadTimeoutError{}, false
	}
	if len(workload.nodeReports) >= workload.expectedReportCount {
		return WorkloadTimeoutError{}, false
	}
	if workload.lastReportAt.Add(c.timeout).After(now) {
		return WorkloadTimeoutError{}, false
	}

	delete(c.workloads, key)
	return c.buildTimeoutError(key, workload), true
}

func (c *SlowNodeAggregator) expireOneTimedOutWorkload(now time.Time) (WorkloadTimeoutError, bool) {
	deadline := c.timeout
	for key, workload := range c.workloads {
		if len(workload.nodeReports) >= workload.expectedReportCount {
			continue
		}
		if workload.lastReportAt.Add(deadline).After(now) {
			continue
		}
		delete(c.workloads, key)
		return c.buildTimeoutError(key, workload), true
	}

	return WorkloadTimeoutError{}, false
}

func (c *SlowNodeAggregator) ExpireTimedOutWorkloads() []WorkloadTimeoutError {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	var errs []WorkloadTimeoutError
	for {
		err, ok := c.expireOneTimedOutWorkload(now)
		if !ok {
			return errs
		}
		errs = append(errs, err)
	}
}

func (c *SlowNodeAggregator) buildTimeoutError(key workloadKey, workload *workload) WorkloadTimeoutError {
	reportedNodes := make([]string, 0, len(workload.nodeReports))
	for nodeName := range workload.nodeReports {
		reportedNodes = append(reportedNodes, string(nodeName))
	}
	sort.Strings(reportedNodes)

	return WorkloadTimeoutError{
		Namespace:       key.namespace,
		WorkloadName:    key.workloadName,
		ReportedNodes:   reportedNodes,
		ReceivedReports: len(workload.nodeReports),
		ExpectedReports: workload.expectedReportCount,
		Timeout:         c.timeout,
	}
}

type pairKey struct {
	batch  batchIndex
	first  nodeIP
	second nodeIP
}

// detectSlowNodes implements the slow-node rule used by the manager side.
//
// Each node report is handled in two paths:
//
//  1. fail-fast path: if gpu_check/storage_check already failed, the node is
//     directly marked as abnormal and excluded from pairwise preflight logic.
//  2. pairwise path: only non-fail-fast reports participate in preflight
//     batch aggregation.
//
// For the pairwise path, this function applies the rule:
//
//  1. Deduplicate the same (batch_idx, sorted(pair)) reported by both ends.
//  2. For each batch, collect the node IPs that appear in failed pairs.
//  3. Intersect those failed-node sets across all expected batches.
//  4. Map node IPs back to node names when possible.
//
// Final result = union(fail-fast abnormal nodes, pairwise slow nodes).
// This ensures fail-fast nodes are always reported, while they do not distort
// the batch-intersection slow-node decision.
func detectSlowNodes(ns, workloadName string, nodeReports map[nodeName]nodeReport, expectedBatchCount int) []string {
	abnormalNodes, preflightReports, hasFailFast := classifyNodeReports(ns, workloadName, nodeReports)

	failedByBatch := failedNodeIPsByBatch(preflightReports)
	observedBatchCount := observedBatchCount(preflightReports)
	logFailedNodesByBatch(ns, workloadName, failedByBatch)

	if shouldReturnNoSlowNodes(expectedBatchCount, observedBatchCount, len(failedByBatch), hasFailFast) {
		logNoSlowNodeReason(ns, workloadName, observedBatchCount, expectedBatchCount, len(failedByBatch))
		return nil
	}

	mergePairwiseSlowNodes(ns, workloadName, abnormalNodes, nodeReports, failedByBatch)
	return finalizeSlowNodes(ns, workloadName, abnormalNodes)
}

func classifyNodeReports(ns, workloadName string, nodeReports map[nodeName]nodeReport) (nodeNameSet, map[nodeName]nodeReport, bool) {
	abnormalNodes := make(nodeNameSet, len(nodeReports))
	preflightReports := make(map[nodeName]nodeReport, len(nodeReports))
	hasFailFast := false

	for key, report := range nodeReports {
		if !report.failFast {
			preflightReports[key] = report
			continue
		}

		hasFailFast = true
		abnormalNodes[report.nodeName] = struct{}{}
		klog.V(4).InfoS("preflight report marked fail-fast", "namespace", ns, "workload", workloadName, "node", report.nodeName, "nodeIP", report.selfIP)
	}

	return abnormalNodes, preflightReports, hasFailFast
}

func logFailedNodesByBatch(ns, workloadName string, failedByBatch map[batchIndex]nodeIPSet) {
	if len(failedByBatch) == 0 {
		return
	}

	klog.V(4).InfoS("preflight failed nodes by batch", "namespace", ns, "workload", workloadName, "batches", formatFailedNodeIPsByBatch(failedByBatch))
}

func shouldReturnNoSlowNodes(expectedBatchCount, observedBatchCount, failedBatchCount int, hasFailFast bool) bool {
	if hasFailFast || expectedBatchCount <= 0 {
		return false
	}

	if observedBatchCount >= expectedBatchCount && failedBatchCount == 0 {
		return true
	}

	return failedBatchCount < expectedBatchCount
}

func logNoSlowNodeReason(ns, workloadName string, observedBatchCount, expectedBatchCount, failedBatchCount int) {
	if observedBatchCount >= expectedBatchCount && failedBatchCount == 0 {
		return
	}

	if observedBatchCount < expectedBatchCount {
		klog.V(4).InfoS("preflight missing observed batches", "namespace", ns, "workload", workloadName, "observedBatchCount", observedBatchCount, "expectedBatchCount", expectedBatchCount, "failedBatchCount", failedBatchCount)
		return
	}

	klog.V(4).InfoS("preflight at least one expected batch has no failed pair", "namespace", ns, "workload", workloadName, "observedBatchCount", observedBatchCount, "expectedBatchCount", expectedBatchCount, "failedBatchCount", failedBatchCount, "nonFailedBatchCount", observedBatchCount-failedBatchCount)
}

func mergePairwiseSlowNodes(ns, workloadName string, abnormalNodes nodeNameSet, nodeReports map[nodeName]nodeReport, failedByBatch map[batchIndex]nodeIPSet) {
	if len(failedByBatch) == 0 {
		return
	}

	nodeIPToName := nodeIPToName(nodeReports)
	slowNodes := intersectFailedNodeIPs(failedByBatch, nodeIPToName)
	klog.V(4).InfoS("preflight pairwise slow nodes", "namespace", ns, "workload", workloadName, "nodes", slowNodes)
	for _, slowNodeName := range slowNodes {
		abnormalNodes[nodeName(slowNodeName)] = struct{}{}
	}
}

func nodeIPToName(nodeReports map[nodeName]nodeReport) map[nodeIP]nodeName {
	result := make(map[nodeIP]nodeName, len(nodeReports))
	for _, report := range nodeReports {
		if report.selfIP == "" {
			continue
		}
		result[report.selfIP] = report.nodeName
	}

	return result
}

func finalizeSlowNodes(ns, workloadName string, abnormalNodes nodeNameSet) []string {
	if len(abnormalNodes) == 0 {
		klog.V(4).InfoS("preflight final slow nodes", "namespace", ns, "workload", workloadName, "nodes", []string{})
		return nil
	}

	result := make([]string, 0, len(abnormalNodes))
	for nodeName := range abnormalNodes {
		result = append(result, string(nodeName))
	}
	sort.Strings(result)
	klog.V(4).InfoS("preflight final slow nodes", "namespace", ns, "workload", workloadName, "nodes", result)

	return result
}

// failedNodeIPsByBatch converts deduplicated failed pairs into a per-batch set
// of node IPs. These IPs come from pair/self_ip fields and are later translated
// back to node name.
func failedNodeIPsByBatch(nodeReports map[nodeName]nodeReport) map[batchIndex]nodeIPSet {
	// dedup 记录每个 (batch, sorted pair) 的最终失败状态；同一对节点可能被两端
	// 各上报一次，这里先把它们折叠成一条 pair 结论。
	dedup := make(map[pairKey]bool)
	for _, report := range nodeReports {
		for _, result := range report.batchResults {
			key := pairKey{batch: result.BatchIdx, first: result.PairFirst, second: result.PairSecond}
			// 同一个 batch 内，相同 pair 的两端上报会落到同一个 key；只要任意一端失败，
			// 这个 pair 的最终状态就记为 failed=true。
			if result.Failed {
				dedup[key] = true
			} else if _, exists := dedup[key]; !exists {
				// 仅在首次见到该 pair 时记录 pass，避免后续 pass 覆盖已出现的 fail。
				dedup[key] = false
			}
		}
	}

	// failedByBatch 是本函数的输出：batch_idx -> 该 batch 中所有出现在失败 pair
	// 里的节点 IP 集合。
	failedByBatch := make(map[batchIndex]nodeIPSet)
	for key, failed := range dedup {
		if !failed {
			// 只关心最终失败的 pair；成功 pair 不参与后续交集计算。
			continue
		}
		// 这里按 batch 聚合失败节点集合：某个失败 pair 的两个端点都算该 batch 的
		// failed node IPs，后续 detectSlowNodes 会对所有 batch 的集合做交集。
		nodeIPs := failedByBatch[key.batch]
		if nodeIPs == nil {
			nodeIPs = make(nodeIPSet, 2)
			failedByBatch[key.batch] = nodeIPs
		}
		nodeIPs[key.first] = struct{}{}
		nodeIPs[key.second] = struct{}{}
	}

	return failedByBatch
}

// intersectFailedNodeIPs computes the intersection of failed node-IP sets from
// all batches. When an IP can be resolved to a node name, the node name is
// returned; otherwise the original IP is kept for diagnostics.
func intersectFailedNodeIPs(failedNodeIPsByBatch map[batchIndex]nodeIPSet, nodeIPToName map[nodeIP]nodeName) []string {
	if len(failedNodeIPsByBatch) == 0 {
		return nil
	}

	// 先取出所有出现过失败 pair 的 batch，并按 batch_idx 排序，保证交集计算顺序稳定。
	batchIndexes := make([]batchIndex, 0, len(failedNodeIPsByBatch))
	for batchIdx := range failedNodeIPsByBatch {
		batchIndexes = append(batchIndexes, batchIdx)
	}
	sort.Slice(batchIndexes, func(i, j int) bool {
		return batchIndexes[i] < batchIndexes[j]
	})

	// 用第一个 batch 的失败节点集合作为交集初值，后续 batch 只会不断把它收缩。
	intersection := make(nodeIPSet, len(failedNodeIPsByBatch[batchIndexes[0]]))
	for nodeIP := range failedNodeIPsByBatch[batchIndexes[0]] {
		intersection[nodeIP] = struct{}{}
	}

	for _, batchIdx := range batchIndexes[1:] {
		nodeIPs := failedNodeIPsByBatch[batchIdx]
		// 若某个节点不在当前 batch 的失败集合中，说明它不是“所有 batch 都失败”的
		// 公共节点，需要从交集中移除。
		for nodeIP := range intersection {
			if _, exists := nodeIPs[nodeIP]; !exists {
				delete(intersection, nodeIP)
			}
		}
		if len(intersection) == 0 {
			// 交集为空时可以提前返回：后续 batch 不可能再引入新的公共失败节点。
			return nil
		}
	}

	// 最后把公共失败节点的 IP 映射回 node_name；若没有映射，则保留原始 nodeIP。
	result := make([]string, 0, len(intersection))
	for nodeIP := range intersection {
		result = append(result, resolveNodeName(nodeIPToName, nodeIP))
	}

	return result
}

func formatFailedNodeIPsByBatch(failedNodeIPsByBatch map[batchIndex]nodeIPSet) map[int][]string {
	formatted := make(map[int][]string, len(failedNodeIPsByBatch))
	for batchIdx, nodeIPs := range failedNodeIPsByBatch {
		batchNodes := make([]string, 0, len(nodeIPs))
		for ip := range nodeIPs {
			batchNodes = append(batchNodes, string(ip))
		}
		sort.Strings(batchNodes)
		formatted[int(batchIdx)] = batchNodes
	}

	return formatted
}

func skippedBatchIdentity(batchMap map[string]any, reporterIP nodeIP) ([2]nodeIP, nodeIP, error) {
	if pair, ok := pairField(batchMap["pair"]); ok {
		selfIP, err := batchSelfIP(batchMap, reporterIP, pair)
		if err != nil {
			return [2]nodeIP{}, "", err
		}
		return pair, selfIP, nil
	}

	selfIP, ok := batchMap["self_ip"].(string)
	if !ok || selfIP == "" {
		return [2]nodeIP{}, "", fmt.Errorf("invalid self_ip")
	}
	selfIPValue := nodeIP(selfIP)

	// Some skip-only batches (for example idle_roll_over) omit the peer pair.
	// Treat them as a self-reported abnormal signal for the reporting node.
	return [2]nodeIP{selfIPValue, selfIPValue}, selfIPValue, nil
}

func observedBatchCount(nodeReports map[nodeName]nodeReport) int {
	observed := make(map[batchIndex]struct{})
	for _, report := range nodeReports {
		for _, result := range report.batchResults {
			observed[result.BatchIdx] = struct{}{}
		}
	}

	return len(observed)
}

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
	if value <= 0 {
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
	plan := workloadPlan{}
	if workloadSize <= 0 {
		return workloadPlan{}, fmt.Errorf("cannot resolve preflight layout without workload_size")
	}
	if workloadSize%2 != 0 {
		return workloadPlan{}, fmt.Errorf("odd workload sizes are not supported: workload_size=%d", workloadSize)
	}
	plan.reportCount = workloadSize
	plan.batchCount = min(workloadSize-1, maxBatchCount)
	if plan.reportCount <= 1 {
		return workloadPlan{}, fmt.Errorf("invalid expected report count: %d", plan.reportCount)
	}
	if plan.batchCount <= 0 {
		return workloadPlan{}, fmt.Errorf("invalid expected batch count: %d", plan.batchCount)
	}

	return plan, nil
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
