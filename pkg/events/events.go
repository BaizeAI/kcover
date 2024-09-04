package events

import "github.com/baizeai/kcover/pkg/runner"

type TargetType string

const (
	Pod    TargetType = "pod"
	Node   TargetType = "node"
	Device TargetType = "device"
)

type EventType int

const (
	_ EventType = iota
	Error
	Warning
)

type CollectorEvent struct {
	TargetType
	Namespace string
	Name      string
	EventType
	Message string
}

type Recorder interface {
	runner.Runner
	RecordEvent(e CollectorEvent) error
	EventChan() <-chan CollectorEvent
}
