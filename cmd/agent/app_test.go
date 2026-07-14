package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type recordingRunner struct {
	name   string
	events *[]string
	err    error
}

func (r recordingRunner) Start(context.Context) error {
	*r.events = append(*r.events, "start "+r.name)
	return r.err
}

func (r recordingRunner) Stop() {
	*r.events = append(*r.events, "stop "+r.name)
}

func TestAgentAppLifecycle(t *testing.T) {
	var events []string
	app := &agentApp{
		reportPublisher: recordingRunner{name: "publisher", events: &events},
		nodeDetector:    recordingRunner{name: "detector", events: &events},
		reportCollector: recordingRunner{name: "collector", events: &events},
	}

	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	app.Stop()

	want := []string{
		"start publisher",
		"start detector",
		"start collector",
		"stop collector",
		"stop detector",
		"stop publisher",
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("lifecycle events = %v, want %v", events, want)
	}
}

func TestAgentAppStartRollsBackStartedComponents(t *testing.T) {
	startErr := errors.New("observer failed")
	var events []string
	app := &agentApp{
		reportPublisher: recordingRunner{name: "publisher", events: &events},
		nodeDetector:    recordingRunner{name: "detector", events: &events},
		reportCollector: recordingRunner{name: "collector", events: &events, err: startErr},
	}

	err := app.Start(context.Background())
	if !errors.Is(err, startErr) {
		t.Fatalf("Start() error = %v, want %v", err, startErr)
	}

	want := []string{
		"start publisher",
		"start detector",
		"start collector",
		"stop detector",
		"stop publisher",
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("rollback events = %v, want %v", events, want)
	}
}
