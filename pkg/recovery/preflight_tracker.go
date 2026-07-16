package recovery

import (
	"time"

	kcoverv1alpha1 "github.com/baizeai/kcover/pkg/apis/kcover/v1alpha1"
	"github.com/baizeai/kcover/pkg/preflight"
)

type preflightTracker struct {
	aggregator *preflight.SlowNodeAggregator
}

type preflightReportResult struct {
	workloadName string
	slowNodes    []string
	skipped      bool
	waiting      bool
}

func newPreflightTracker(reportCollectionTimeout time.Duration) *preflightTracker {
	if reportCollectionTimeout <= 0 {
		reportCollectionTimeout = preflight.DefaultReportCollectionTimeout
	}

	return &preflightTracker{
		aggregator: preflight.NewSlowNodeAggregator(reportCollectionTimeout),
	}
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

	ready, slowNodes, err := s.aggregator.AddReportForWorkload(namespace, workloadUID, workloadName, report.Spec.Report, report.Spec.ObservedAt.Time)
	if err != nil {
		return preflightReportResult{workloadName: workloadName}, err
	}
	if !ready {
		return preflightReportResult{workloadName: workloadName, waiting: true}, nil
	}

	return preflightReportResult{workloadName: workloadName, slowNodes: slowNodes}, nil
}

func (s *preflightTracker) sweepExpired() []preflight.WorkloadTimeoutError {
	if s == nil || s.aggregator == nil {
		return nil
	}

	return s.aggregator.ExpireTimedOutWorkloads()
}
