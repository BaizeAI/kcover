package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type controllerRunnerStub struct {
	name      string
	events    *[]string
	startErr  error
	startCall int
	stopCall  int
}

func (r *controllerRunnerStub) Start(context.Context) error {
	r.startCall++
	*r.events = append(*r.events, "start "+r.name)
	return r.startErr
}

func (r *controllerRunnerStub) Stop() {
	r.stopCall++
	*r.events = append(*r.events, "stop "+r.name)
}

func TestControllerAppLifecycle(t *testing.T) {
	var events []string
	first := &controllerRunnerStub{name: "first", events: &events}
	second := &controllerRunnerStub{name: "second", events: &events}
	app := &controllerApp{build: func() ([]controllerComponent, error) {
		return []controllerComponent{{name: first.name, runner: first}, {name: second.name, runner: second}}, nil
	}}

	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	app.Stop()
	app.Stop()

	want := []string{"start first", "start second", "stop second", "stop first"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("lifecycle events = %v, want %v", events, want)
	}
}

func TestControllerAppRollsBackStartedComponents(t *testing.T) {
	var events []string
	startErr := errors.New("start failed")
	first := &controllerRunnerStub{name: "first", events: &events}
	second := &controllerRunnerStub{name: "second", events: &events, startErr: startErr}
	app := &controllerApp{build: func() ([]controllerComponent, error) {
		return []controllerComponent{{name: first.name, runner: first}, {name: second.name, runner: second}}, nil
	}}

	if err := app.Start(context.Background()); !errors.Is(err, startErr) {
		t.Fatalf("Start() error = %v, want %v", err, startErr)
	}
	app.Stop()

	want := []string{"start first", "start second", "stop first"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("rollback events = %v, want %v", events, want)
	}
}

func TestControllerAppStopBeforeStart(t *testing.T) {
	built := false
	app := &controllerApp{build: func() ([]controllerComponent, error) {
		built = true
		return nil, nil
	}}

	app.Stop()
	if err := app.Start(context.Background()); err == nil {
		t.Fatal("Start() error = nil after Stop()")
	}
	if built {
		t.Fatal("Stop() before Start() built controller components")
	}
}
