package preflight

import (
	"fmt"
	"sync"
	"time"

	kcoverv1alpha1 "github.com/baizeai/kcover/pkg/apis/kcover/v1alpha1"
	"github.com/baizeai/kcover/pkg/events"
	"github.com/baizeai/kcover/pkg/runner"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
)

type ReportPublisher interface {
	runner.Runner
	ReportSubmitter
}

type ReportSubmitter interface {
	SubmitReport(*kcoverv1alpha1.PreflightReport) error
}

type publisherState int

const (
	publisherNew publisherState = iota
	publisherRunning
	publisherStopped
	reportWorkerCount = 3
	eventBufferSize   = 128
)

type reportPublisher struct {
	reportSink ReportSink
	eventSink  events.Sink
	queue      workqueue.TypedRateLimitingInterface[string]
	eventCh    chan events.Event

	mu       sync.Mutex
	state    publisherState
	pending  map[string]*kcoverv1alpha1.PreflightReport
	reportWG sync.WaitGroup
	eventWG  sync.WaitGroup
	doneCh   chan struct{}
}

func NewReportPublisher(reportSink ReportSink, eventSink events.Sink) (ReportPublisher, error) {
	if reportSink == nil {
		return nil, fmt.Errorf("preflight report sink cannot be nil")
	}
	if eventSink == nil {
		return nil, fmt.Errorf("preflight event sink cannot be nil")
	}

	return newReportPublisher(
		reportSink,
		eventSink,
		workqueue.NewTypedItemExponentialFailureRateLimiter[string](100*time.Millisecond, 30*time.Second),
	), nil
}

func newReportPublisher(reportSink ReportSink, eventSink events.Sink, rateLimiter workqueue.TypedRateLimiter[string]) *reportPublisher {
	return &reportPublisher{
		reportSink: reportSink,
		eventSink:  eventSink,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			rateLimiter,
			workqueue.TypedRateLimitingQueueConfig[string]{Name: "kcover-preflight-reports"},
		),
		pending: make(map[string]*kcoverv1alpha1.PreflightReport),
		eventCh: make(chan events.Event, eventBufferSize),
		doneCh:  make(chan struct{}),
	}
}

func (p *reportPublisher) SubmitReport(report *kcoverv1alpha1.PreflightReport) error {
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

func (p *reportPublisher) Start() error {
	p.mu.Lock()
	switch p.state {
	case publisherRunning:
		p.mu.Unlock()
		return nil
	case publisherStopped:
		p.mu.Unlock()
		return fmt.Errorf("preflight report publisher is stopped")
	}
	p.state = publisherRunning
	p.reportWG.Add(reportWorkerCount)
	p.eventWG.Add(1)
	p.mu.Unlock()

	for range reportWorkerCount {
		go p.runReportWorker()
	}
	go p.runEventWorker()
	go func() {
		p.reportWG.Wait()
		close(p.eventCh)
		p.eventWG.Wait()
		close(p.doneCh)
	}()
	klog.InfoS("preflight report publisher started")
	return nil
}

func (p *reportPublisher) runReportWorker() {
	defer p.reportWG.Done()
	for p.processNext() {
	}
}

func (p *reportPublisher) runEventWorker() {
	defer p.eventWG.Done()
	for event := range p.eventCh {
		if err := p.eventSink.RecordEvent(event); err != nil {
			klog.ErrorS(err, "failed to record PreflightReport observation", "namespace", event.Namespace, "node", event.Name)
		}
	}
}

func (p *reportPublisher) processNext() bool {
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
		select {
		case p.eventCh <- ObservationEvent(report):
		default:
			klog.ErrorS(nil, "drop PreflightReport observation because event queue is full", "namespace", report.Namespace, "name", report.Name)
		}
	}
	klog.V(3).InfoS("published PreflightReport", "namespace", report.Namespace, "name", report.Name, "created", created)
	return true
}

func (p *reportPublisher) finish(key string) {
	p.queue.Forget(key)
	p.mu.Lock()
	delete(p.pending, key)
	p.mu.Unlock()
}

func isPermanentReportWriteError(err error) bool {
	return apierrors.IsInvalid(err) || apierrors.IsBadRequest(err)
}

func (p *reportPublisher) Stop() {
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
		close(p.eventCh)
		close(p.doneCh)
		p.mu.Unlock()
		return
	}
	p.state = publisherStopped
	doneCh := p.doneCh
	p.mu.Unlock()

	p.queue.ShutDown()
	<-doneCh
	klog.InfoS("preflight report publisher stopped")
}

var _ ReportPublisher = (*reportPublisher)(nil)
