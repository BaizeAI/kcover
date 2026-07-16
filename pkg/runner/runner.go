package runner

import "context"

type Runner interface {
	Start(context.Context) error
	Stop()
}
