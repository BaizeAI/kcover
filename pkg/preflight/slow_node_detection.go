package preflight

import (
	"fmt"
	"sort"

	"k8s.io/klog/v2"
)

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
