package preflight

import (
	"context"
	"fmt"
	"sync"
	"time"

	kcoverv1alpha1 "github.com/baizeai/kcover/pkg/apis/kcover/v1alpha1"
	"github.com/baizeai/kcover/pkg/events"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
)

type publisherState int

const (
	publisherNew publisherState = iota
	publisherRunning
	publisherStopped
)

// ReportPublisher accepts reports from collectors and delivers them
// asynchronously. It owns queuing, retries, and the observation event emitted
// after a report is first persisted.
type ReportPublisher struct {
	reportSink ReportSink
	eventSink  events.Sink
	queue      workqueue.TypedRateLimitingInterface[string]

	mu      sync.Mutex
	state   publisherState
	cancel  context.CancelFunc
	pending map[string]*kcoverv1alpha1.PreflightReport
	doneCh  chan struct{}
}

func NewReportPublisher(rSink ReportSink, eSink events.Sink) (*ReportPublisher, error) {
	if rSink == nil {
		return nil, fmt.Errorf("preflight report sink cannot be nil")
	}
	if eSink == nil {
		return nil, fmt.Errorf("preflight event sink cannot be nil")
	}

	return &ReportPublisher{
		reportSink: rSink,
		eventSink:  eSink,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.NewTypedItemExponentialFailureRateLimiter[string](100*time.Millisecond, 30*time.Second),
			workqueue.TypedRateLimitingQueueConfig[string]{Name: "kcover-preflight-reports"},
		),
		pending: make(map[string]*kcoverv1alpha1.PreflightReport),
		doneCh:  make(chan struct{}),
	}, nil
}

// Submit accepts a report for asynchronous delivery. A nil error means that the
// report was accepted, not that it has been persisted.
func (p *ReportPublisher) Submit(report *kcoverv1alpha1.PreflightReport) error {
	if report == nil || report.Namespace == "" || report.Name == "" {
		return fmt.Errorf("preflight report identity is empty")
	}
	key := report.Namespace + "/" + report.Name
	p.mu.Lock()
	if p.state != publisherRunning {
		p.mu.Unlock()
		return fmt.Errorf("preflight report publisher is not running")
	}
	p.pending[key] = report
	p.queue.Add(key)
	p.mu.Unlock()
	return nil
}

func (p *ReportPublisher) Start(parent context.Context) error {
	p.mu.Lock()
	switch p.state {
	case publisherRunning:
		p.mu.Unlock()
		return nil
	case publisherStopped:
		p.mu.Unlock()
		return fmt.Errorf("preflight report publisher is stopped")
	}
	ctx, cancel := context.WithCancel(parent)
	p.cancel = cancel
	p.state = publisherRunning
	p.mu.Unlock()

	go func() {
		defer close(p.doneCh)
		p.run()
	}()
	go func() {
		<-ctx.Done()
		p.queue.ShutDown()
	}()
	klog.InfoS("preflight report publisher started")
	return nil
}

func (p *ReportPublisher) run() {
	for p.processNext() {
	}
}

func (p *ReportPublisher) recordObservation(report *kcoverv1alpha1.PreflightReport) {
	event := ObservationEvent(report)
	if err := p.eventSink.RecordEvent(event); err != nil {
		klog.ErrorS(err, "failed to record PreflightReport observation", "namespace", event.Namespace, "node", event.Name)
	}
}

func (p *ReportPublisher) processNext() bool {
	key, shutdown := p.queue.Get()
	if shutdown {
		return false
	}
	defer p.queue.Done(key)

	p.mu.Lock()
	report := p.pending[key]
	p.mu.Unlock()
	if report == nil {
		p.queue.Forget(key)
		return true
	}

	created, err := p.reportSink.WriteReport(report)
	if err != nil {
		if isPermanentReportWriteError(err) {
			p.finish(key)
			klog.ErrorS(err, "drop invalid PreflightReport", "namespace", report.Namespace, "name", report.Name)
			return true
		}

		p.queue.AddRateLimited(key)
		klog.ErrorS(err, "retry PreflightReport", "namespace", report.Namespace, "name", report.Name, "retry", p.queue.NumRequeues(key))
		return true
	}

	p.finish(key)
	if created {
		p.recordObservation(report)
	}
	klog.V(3).InfoS("published PreflightReport", "namespace", report.Namespace, "name", report.Name, "created", created)
	return true
}

func (p *ReportPublisher) finish(key string) {
	p.queue.Forget(key)
	p.mu.Lock()
	delete(p.pending, key)
	p.mu.Unlock()
}

func isPermanentReportWriteError(err error) bool {
	return apierrors.IsInvalid(err) || apierrors.IsBadRequest(err)
}

func (p *ReportPublisher) Stop() {
	p.mu.Lock()
	if p.state == publisherStopped {
		doneCh := p.doneCh
		p.mu.Unlock()
		<-doneCh
		return
	}
	if p.state == publisherNew {
		p.state = publisherStopped
		p.queue.ShutDown()
		close(p.doneCh)
		p.mu.Unlock()
		return
	}
	p.state = publisherStopped
	cancel := p.cancel
	doneCh := p.doneCh
	p.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	p.queue.ShutDown()
	<-doneCh
	klog.InfoS("preflight report publisher stopped")
}
