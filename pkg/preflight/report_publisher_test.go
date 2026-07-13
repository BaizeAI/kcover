package preflight

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	kcoverv1alpha1 "github.com/baizeai/kcover/pkg/apis/kcover/v1alpha1"
	"github.com/baizeai/kcover/pkg/events"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/util/workqueue"
)

type flakyReportSink struct {
	mu       sync.Mutex
	failures int
	attempts int
	err      error
	created  bool
}

func (s *flakyReportSink) WriteReport(*kcoverv1alpha1.PreflightReport) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts++
	if s.attempts <= s.failures || s.failures < 0 {
		return false, s.err
	}
	return s.created, nil
}

func (s *flakyReportSink) attemptCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts
}

type recordingEventSink struct {
	events chan events.Event
}

func (s recordingEventSink) RecordEvent(event events.Event) error {
	s.events <- event
	return nil
}

func TestReportPublisherRetriesUntilReportIsCreated(t *testing.T) {
	t.Parallel()

	sink := &flakyReportSink{failures: 5, err: errors.New("temporary API failure"), created: true}
	eventSink := recordingEventSink{events: make(chan events.Event, 1)}
	publisher := newReportPublisher(
		sink,
		eventSink,
		workqueue.NewTypedItemExponentialFailureRateLimiter[string](time.Millisecond, 5*time.Millisecond),
	)
	if err := publisher.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer publisher.Stop()

	report := publisherTestReport(t)
	if err := publisher.SubmitReport(report); err != nil {
		t.Fatalf("SubmitReport() error = %v", err)
	}

	select {
	case event := <-eventSink.events:
		if event.Namespace != report.Namespace || event.Name != report.Spec.NodeName {
			t.Fatalf("observation event = %+v, want report node observation", event)
		}
	case <-time.After(time.Second):
		t.Fatal("publisher did not create report after transient failures")
	}
	if got := sink.attemptCount(); got != 6 {
		t.Fatalf("WriteReport() attempts = %d, want 6", got)
	}
}

func TestReportPublisherDropsPermanentError(t *testing.T) {
	t.Parallel()

	permanentErr := apierrors.NewBadRequest("invalid report")
	sink := &flakyReportSink{failures: -1, err: permanentErr}
	eventSink := recordingEventSink{events: make(chan events.Event, 1)}
	publisher := newReportPublisher(
		sink,
		eventSink,
		workqueue.NewTypedItemExponentialFailureRateLimiter[string](time.Millisecond, 5*time.Millisecond),
	)
	if err := publisher.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer publisher.Stop()

	if err := publisher.SubmitReport(publisherTestReport(t)); err != nil {
		t.Fatalf("SubmitReport() error = %v", err)
	}
	waitForPublisherAttempts(t, sink, 1)
	time.Sleep(20 * time.Millisecond)
	if got := sink.attemptCount(); got != 1 {
		t.Fatalf("WriteReport() attempts for permanent error = %d, want 1", got)
	}
	select {
	case event := <-eventSink.events:
		t.Fatalf("unexpected observation event after permanent failure: %+v", event)
	default:
	}
}

func TestReportPublisherRetriesAuthorizationError(t *testing.T) {
	t.Parallel()

	authorizationErr := apierrors.NewForbidden(schema.GroupResource{Group: kcoverv1alpha1.Group, Resource: "preflightreports"}, "report-a", errors.New("RBAC not ready"))
	sink := &flakyReportSink{failures: 2, err: authorizationErr, created: true}
	eventSink := recordingEventSink{events: make(chan events.Event, 1)}
	publisher := newReportPublisher(
		sink,
		eventSink,
		workqueue.NewTypedItemExponentialFailureRateLimiter[string](time.Millisecond, 5*time.Millisecond),
	)
	if err := publisher.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer publisher.Stop()

	if err := publisher.SubmitReport(publisherTestReport(t)); err != nil {
		t.Fatalf("SubmitReport() error = %v", err)
	}
	select {
	case <-eventSink.events:
	case <-time.After(time.Second):
		t.Fatal("publisher did not recover after authorization error")
	}
	if got := sink.attemptCount(); got != 3 {
		t.Fatalf("WriteReport() attempts after authorization errors = %d, want 3", got)
	}
}

func TestPermanentReportWriteErrorClassificationSupportsWrapping(t *testing.T) {
	t.Parallel()

	if !isPermanentReportWriteError(fmt.Errorf("write report: %w", apierrors.NewBadRequest("invalid report"))) {
		t.Fatal("wrapped BadRequest classified as retryable, want permanent")
	}
	forbidden := apierrors.NewForbidden(schema.GroupResource{Group: kcoverv1alpha1.Group, Resource: "preflightreports"}, "report-a", errors.New("RBAC not ready"))
	if isPermanentReportWriteError(fmt.Errorf("write report: %w", forbidden)) {
		t.Fatal("wrapped Forbidden classified as permanent, want retryable")
	}
}

func TestReportPublisherDoesNotObserveExistingReport(t *testing.T) {
	t.Parallel()

	sink := &flakyReportSink{created: false}
	eventSink := recordingEventSink{events: make(chan events.Event, 1)}
	publisher := newReportPublisher(
		sink,
		eventSink,
		workqueue.NewTypedItemExponentialFailureRateLimiter[string](time.Millisecond, 5*time.Millisecond),
	)
	if err := publisher.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer publisher.Stop()

	if err := publisher.SubmitReport(publisherTestReport(t)); err != nil {
		t.Fatalf("SubmitReport() error = %v", err)
	}
	waitForPublisherAttempts(t, sink, 1)
	select {
	case event := <-eventSink.events:
		t.Fatalf("unexpected observation event for existing report: %+v", event)
	default:
	}
}

func TestReportPublisherRunsMultipleReportWorkers(t *testing.T) {
	t.Parallel()

	sink := &blockingReportSink{
		started: make(chan string, reportWorkerCount),
		release: make(chan struct{}),
	}
	publisher := newReportPublisher(
		sink,
		recordingEventSink{events: make(chan events.Event, 1)},
		workqueue.NewTypedItemExponentialFailureRateLimiter[string](time.Millisecond, 5*time.Millisecond),
	)
	if err := publisher.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer publisher.Stop()

	for idx := range reportWorkerCount {
		report := publisherTestReport(t)
		report.Name += fmt.Sprintf("-%d", idx)
		if err := publisher.SubmitReport(report); err != nil {
			t.Fatalf("SubmitReport(%d) error = %v", idx, err)
		}
	}
	for range reportWorkerCount {
		select {
		case <-sink.started:
		case <-time.After(time.Second):
			t.Fatal("report writes did not run concurrently")
		}
	}
	close(sink.release)
}

func TestReportPublisherEventDoesNotBlockReports(t *testing.T) {
	t.Parallel()

	sink := &countingReportSink{attempts: make(chan struct{}, reportWorkerCount+1)}
	eventSink := &blockingEventSink{release: make(chan struct{})}
	publisher := newReportPublisher(
		sink,
		eventSink,
		workqueue.NewTypedItemExponentialFailureRateLimiter[string](time.Millisecond, 5*time.Millisecond),
	)
	if err := publisher.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer publisher.Stop()

	for idx := 0; idx < reportWorkerCount+1; idx++ {
		report := publisherTestReport(t)
		report.Name += fmt.Sprintf("-%d", idx)
		if err := publisher.SubmitReport(report); err != nil {
			t.Fatalf("SubmitReport(%d) error = %v", idx, err)
		}
	}
	for idx := 0; idx < reportWorkerCount+1; idx++ {
		select {
		case <-sink.attempts:
		case <-time.After(time.Second):
			t.Fatal("blocking observation Event stalled report creation")
		}
	}
	close(eventSink.release)
}

func TestReportPublisherLifecycleIsIdempotent(t *testing.T) {
	t.Parallel()

	publisher := newReportPublisher(
		&flakyReportSink{},
		recordingEventSink{events: make(chan events.Event, 1)},
		workqueue.NewTypedItemExponentialFailureRateLimiter[string](time.Millisecond, 5*time.Millisecond),
	)
	if err := publisher.SubmitReport(publisherTestReport(t)); err == nil {
		t.Fatal("SubmitReport() before Start error = nil, want non-nil")
	}
	if err := publisher.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := publisher.Start(context.Background()); err != nil {
		t.Fatalf("second Start() error = %v, want nil", err)
	}
	publisher.Stop()
	publisher.Stop()
	if err := publisher.SubmitReport(publisherTestReport(t)); err == nil {
		t.Fatal("SubmitReport() after Stop error = nil, want non-nil")
	}
	if err := publisher.Start(context.Background()); err == nil {
		t.Fatal("Start() after Stop error = nil, want non-nil")
	}
}

func TestReportPublisherStopBeforeStartReturns(t *testing.T) {
	t.Parallel()

	publisher := newReportPublisher(
		&flakyReportSink{},
		recordingEventSink{events: make(chan events.Event, 1)},
		workqueue.NewTypedItemExponentialFailureRateLimiter[string](time.Millisecond, 5*time.Millisecond),
	)
	publisher.Stop()
	publisher.Stop()
}

func TestReportPublisherStopsAfterParentContextCancellation(t *testing.T) {
	t.Parallel()

	publisher := newReportPublisher(
		&flakyReportSink{},
		recordingEventSink{events: make(chan events.Event, 1)},
		workqueue.NewTypedItemExponentialFailureRateLimiter[string](time.Millisecond, 5*time.Millisecond),
	)
	ctx, cancel := context.WithCancel(context.Background())
	if err := publisher.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	cancel()

	select {
	case <-publisher.doneCh:
	case <-time.After(time.Second):
		t.Fatal("publisher did not stop after parent context cancellation")
	}
	publisher.Stop()
}

type blockingReportSink struct {
	started chan string
	release chan struct{}
}

func (s *blockingReportSink) WriteReport(report *kcoverv1alpha1.PreflightReport) (bool, error) {
	s.started <- report.Name
	<-s.release
	return false, nil
}

type countingReportSink struct {
	attempts chan struct{}
}

func (s *countingReportSink) WriteReport(*kcoverv1alpha1.PreflightReport) (bool, error) {
	s.attempts <- struct{}{}
	return true, nil
}

type blockingEventSink struct {
	release chan struct{}
}

func (s *blockingEventSink) RecordEvent(events.Event) error {
	<-s.release
	return nil
}

func publisherTestReport(t *testing.T) *kcoverv1alpha1.PreflightReport {
	t.Helper()
	report, err := BuildPreflightReport("train-ns", "node-a", "job-a", "job-uid", transportTestPayload, time.Unix(100, 0))
	if err != nil {
		t.Fatalf("BuildPreflightReport() error = %v", err)
	}
	return report
}

func waitForPublisherAttempts(t *testing.T, sink *flakyReportSink, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if sink.attemptCount() >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("WriteReport() attempts = %d, want at least %d", sink.attemptCount(), want)
}
