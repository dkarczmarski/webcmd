package gateexec

import (
	"context"
	"fmt"

	"github.com/dkarczmarski/webcmd/pkg/callgate"
	"github.com/dkarczmarski/webcmd/pkg/config"
)

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
		return -1, fmt.Errorf("callgate registry: %w", err)
	}

	release, err := gate.Acquire(ctx)
	if err != nil {
		return -1, fmt.Errorf("callgate acquire: %w", err)
	}

	exit, done, runErr := action(ctx)

	release()

	//nolint:godox
	// TODO: This channel will be used in the future to keep the gate held until async process finishes.
	// Currently, the lock is released immediately after StartProcess, which is a known bug for async mode.
	_ = done

	return exit, runErr
}
