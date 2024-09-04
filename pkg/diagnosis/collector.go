package diagnosis

import (
	"github.com/baizeai/kcover/pkg/events"
	"github.com/baizeai/kcover/pkg/runner"
)

type Diagnostic interface {
	runner.Runner
	Events() <-chan events.CollectorEvent
}
