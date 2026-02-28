package gateexec

import (
	"context"
	"errors"
	"fmt"

	"github.com/dkarczmarski/webcmd/pkg/callgate"
	"github.com/dkarczmarski/webcmd/pkg/config"
)

var ErrPreAction = errors.New("gate executor: pre-action")

type Action func(context.Context) (result int, done <-chan struct{}, err error)

type Executor struct {
	registry *callgate.Registry
}

func New(registry *callgate.Registry) *Executor {
	return &Executor{registry: registry}
}

func (e *Executor) Run(
	ctx context.Context,
	gateCfg *config.CallGateConfig,
	defaultGroup string,
	action Action,
) (int, error) {
	if gateCfg == nil || e.registry == nil {
		exit, _, err := action(ctx)

		return exit, err
	}

	group := defaultGroup
	if gateCfg.GroupName != nil {
		group = *gateCfg.GroupName
	}

	gate, err := e.registry.GetOrCreate(group, gateCfg.Mode)
	if err != nil {
		return -1, fmt.Errorf("%w: %w", ErrPreAction, err)
	}

	release, err := gate.Acquire(ctx)
	if err != nil {
		return -1, fmt.Errorf("%w: %w", ErrPreAction, err)
	}

	exit, done, runErr := action(ctx)

	if done != nil {
		go func() {
			<-done
			release()
		}()
	} else {
		release()
	}

	return exit, runErr
}
