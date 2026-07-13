package preflight

import (
	"errors"
	"fmt"
	"sort"
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
	workloadUID  string
	workloadName string
}

type workload struct {
	expectedReportCount int
	expectedBatchCount  int
	nodeReports         map[nodeName]nodeReport
	lastReportAt        time.Time
	firstObservedAt     time.Time
	lastObservedAt      time.Time
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
	WorkloadUID     string
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
	return c.AddReportForWorkload(ns, workloadName, workloadName, reportText, c.now())
}

func (c *SlowNodeAggregator) AddReportForWorkload(ns, workloadUID, workloadName, reportText string, observedAt time.Time) (ready bool, slowNodes []string, err error) {
	if ns == "" || workloadName == "" {
		return false, nil, fmt.Errorf("namespace and workload name must not be empty")
	}
	if workloadUID == "" {
		return false, nil, fmt.Errorf("workload UID must not be empty")
	}
	if observedAt.IsZero() {
		return false, nil, fmt.Errorf("report observation time must not be empty")
	}

	report, plan, batchResults, err := extractNodeReport(reportText)
	if err != nil {
		return false, nil, fmt.Errorf("extract preflight report: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	key := workloadKey{namespace: ns, workloadUID: workloadUID, workloadName: workloadName}
	if expired, ok := c.expireWorkloadIfTimedOut(key, now); ok {
		return false, nil, expired
	}
	wkl := c.workloadForReport(key, plan, now, observedAt)
	wkl.lastReportAt = now
	if observedAt.Before(wkl.firstObservedAt) {
		wkl.firstObservedAt = observedAt
	}
	if observedAt.After(wkl.lastObservedAt) {
		wkl.lastObservedAt = observedAt
	}
	if wkl.lastObservedAt.Sub(wkl.firstObservedAt) > c.timeout {
		expired := c.buildTimeoutError(key, wkl)
		wkl = c.ensureWorkload(key, plan, now, observedAt)
		c.addNodeReport(key, wkl, report, batchResults)
		return false, nil, expired
	}

	c.addNodeReport(key, wkl, report, batchResults)

	if len(wkl.nodeReports) < wkl.expectedReportCount {
		return false, nil, nil
	}

	slowNodes = detectSlowNodes(ns, workloadName, wkl.nodeReports, wkl.expectedBatchCount)

	delete(c.workloads, key)
	return true, slowNodes, nil
}

func (c *SlowNodeAggregator) addNodeReport(key workloadKey, wkl *workload, report Report, batchResults []batchResult) {
	failFast := report.GPUCheck == CheckResultFail || report.StorageCheck == CheckResultFail
	if !failFast && len(batchResults) == 0 {
		klog.Warningf("preflight report has no batch results, falling back to fail-fast: namespace=%s workload=%s node=%s workloadSize=%d", key.namespace, key.workloadName, report.NodeName, report.WorkloadSize)
		failFast = true
	}
	np := nodeReport{
		nodeName:     nodeName(report.NodeName),
		selfIP:       nodeIP(report.NodeIP),
		failFast:     failFast,
		batchResults: batchResults}
	wkl.nodeReports[nodeName(report.NodeName)] = np
}

func (c *SlowNodeAggregator) workloadForReport(key workloadKey, plan workloadPlan, now, observedAt time.Time) *workload {
	wkl, ok := c.workloads[key]
	if !ok {
		return c.ensureWorkload(key, plan, now, observedAt)
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
	return c.ensureWorkload(key, plan, now, observedAt)
}

func (c *SlowNodeAggregator) ensureWorkload(key workloadKey, plan workloadPlan, now, observedAt time.Time) *workload {
	wkl := &workload{
		expectedReportCount: plan.reportCount,
		expectedBatchCount:  plan.batchCount,
		nodeReports:         make(map[nodeName]nodeReport, plan.reportCount),
		lastReportAt:        now,
		firstObservedAt:     observedAt,
		lastObservedAt:      observedAt,
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
		WorkloadUID:     key.workloadUID,
		WorkloadName:    key.workloadName,
		ReportedNodes:   reportedNodes,
		ReceivedReports: len(workload.nodeReports),
		ExpectedReports: workload.expectedReportCount,
		Timeout:         c.timeout,
	}
}
