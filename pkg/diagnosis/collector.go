package diagnosis

import (
	"recovery.baizeai.io/pkg/events"
	"recovery.baizeai.io/pkg/runner"
)

type Diagnostic interface {
	runner.Runner
	Events() <-chan events.CollectorEvent
}
