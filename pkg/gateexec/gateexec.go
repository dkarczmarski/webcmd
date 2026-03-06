// Package gateexec provides a mechanism for executing actions under the control of call gates.
//
// It wraps an Action with gate-based concurrency control, handling gate acquisition,
// execution, and release (including asynchronous cleanup).
package gateexec

import (
	"context"
	"errors"
	"fmt"

	"github.com/dkarczmarski/webcmd/pkg/callgate"
	"github.com/dkarczmarski/webcmd/pkg/config"
)

// ErrPreAction is returned when an error occurs before the actual action starts,
// such as during gate acquisition or retrieval.
var ErrPreAction = errors.New("gate executor: pre-action")

// Action represents a function to be executed under gate control.
// It returns a result code (e.g. process exit code), an optional channel
// that indicates when the action is fully finished (for async cleanup),
// and any execution error.
type Action func(context.Context) (result int, done <-chan struct{}, err error)

// Registry provides access to execution gates identified
// by a group and mode name.
//
// Executor uses Registry to obtain or lazily create
// a Gate instance before running an action.
//
// Implementations are responsible for ensuring that
// repeated calls with the same group return the same Gate.
type Registry interface {
	GetOrCreate(group string, name string) (callgate.CallGate, error)
}

type Executor struct {
	registry Registry
}

func New(registry Registry) *Executor {
	return &Executor{registry: registry}
}

func (e *Executor) Run(
	ctx context.Context,
	gateCfg *config.CallGateConfig,
	defaultGroup string,
	action Action,
) (int, error) {
	if gateCfg == nil {
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
