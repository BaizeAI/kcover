package recovery

import (
	"errors"
	"time"

	kcoverv1alpha1 "github.com/baizeai/kcover/pkg/apis/kcover/v1alpha1"
	"github.com/baizeai/kcover/pkg/preflight"

	"github.com/jellydator/ttlcache/v3"
)

type preflightReportKey string

type preflightTracker struct {
	processed    *ttlcache.Cache[preflightReportKey, time.Time]
	workloads    map[string]map[preflightReportKey]struct{}
	processedTTL time.Duration
	aggregator   *preflight.SlowNodeAggregator
}

type preflightReportResult struct {
	workloadName string
	slowNodes    []string
	skipped      bool
	waiting      bool
	duplicate    bool
}

func newPreflightTracker(reportCollectionTimeout time.Duration) *preflightTracker {
	if reportCollectionTimeout <= 0 {
		reportCollectionTimeout = preflight.DefaultReportCollectionTimeout
	}

	return &preflightTracker{
		processed:    ttlcache.New[preflightReportKey, time.Time](),
		workloads:    make(map[string]map[preflightReportKey]struct{}),
		processedTTL: reportCollectionTimeout,
		aggregator:   preflight.NewSlowNodeAggregator(reportCollectionTimeout),
	}
}

// markProcessed only deduplicates inside the current manager process lifetime.
// A controller restart rebuilds aggregation state by listing the durable reports.
func (s *preflightTracker) markProcessed(report *kcoverv1alpha1.PreflightReport) bool {
	key := preflightReportKey(report.Namespace + "/" + report.Name)
	if s.processed.Get(key, ttlcache.WithDisableTouchOnHit[preflightReportKey, time.Time]()) != nil {
		return true
	}
	s.processed.Set(key, time.Now().Add(s.processedTTL), s.processedTTL)
	workloadKey := report.Namespace + "/" + report.Spec.WorkloadUID
	if s.workloads[workloadKey] == nil {
		s.workloads[workloadKey] = make(map[preflightReportKey]struct{})
	}
	s.workloads[workloadKey][key] = struct{}{}
	return false
}

func (s *preflightTracker) cleanupProcessed() {
	s.processed.DeleteExpired()
	for workloadKey, keys := range s.workloads {
		for key := range keys {
			if s.processed.Get(key, ttlcache.WithDisableTouchOnHit[preflightReportKey, time.Time]()) == nil {
				delete(keys, key)
			}
		}
		if len(keys) == 0 {
			delete(s.workloads, workloadKey)
		}
	}
}

func (s *preflightTracker) cleanupProcessedReport(report *kcoverv1alpha1.PreflightReport) {
	key := preflightReportKey(report.Namespace + "/" + report.Name)
	s.processed.Delete(key)
	workloadKey := report.Namespace + "/" + report.Spec.WorkloadUID
	delete(s.workloads[workloadKey], key)
	if len(s.workloads[workloadKey]) == 0 {
		delete(s.workloads, workloadKey)
	}
}

func (s *preflightTracker) cleanupProcessedForWorkload(namespace, workloadUID string) {
	if s == nil || namespace == "" || workloadUID == "" {
		return
	}

	workloadKey := namespace + "/" + workloadUID
	for key := range s.workloads[workloadKey] {
		s.processed.Delete(key)
	}
	delete(s.workloads, workloadKey)
}

func (s *preflightTracker) handleReport(report *kcoverv1alpha1.PreflightReport) (preflightReportResult, error) {
	if s == nil || s.aggregator == nil {
		return preflightReportResult{skipped: true}, nil
	}

	namespace := report.Namespace
	workloadName := report.Spec.WorkloadName
	workloadUID := report.Spec.WorkloadUID
	if workloadName == "" || workloadUID == "" || report.Spec.ObservedAt.IsZero() {
		return preflightReportResult{skipped: true}, nil
	}

	duplicate := s.markProcessed(report)
	if duplicate {
		return preflightReportResult{workloadName: workloadName, skipped: true, duplicate: true}, nil
	}

	ready, slowNodes, err := s.aggregator.AddReportForWorkload(namespace, workloadUID, workloadName, report.Spec.Report, report.Spec.ObservedAt.Time)
	if err != nil {
		var timeoutErr preflight.WorkloadTimeoutError
		if errors.As(err, &timeoutErr) {
			s.cleanupProcessedForWorkload(timeoutErr.Namespace, timeoutErr.WorkloadUID)
		} else {
			s.cleanupProcessedReport(report)
		}
		return preflightReportResult{workloadName: workloadName}, err
	}
	if !ready {
		return preflightReportResult{workloadName: workloadName, waiting: true}, nil
	}

	s.cleanupProcessedForWorkload(namespace, workloadUID)

	return preflightReportResult{workloadName: workloadName, slowNodes: slowNodes}, nil
}

func (s *preflightTracker) sweepExpired() []preflight.WorkloadTimeoutError {
	if s == nil {
		return nil
	}

	s.cleanupProcessed()
	if s.aggregator == nil {
		return nil
	}

	errs := s.aggregator.ExpireTimedOutWorkloads()
	for _, err := range errs {
		s.cleanupProcessedForWorkload(err.Namespace, err.WorkloadUID)
	}

	return errs
}
