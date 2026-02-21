package callgate_test

import (
	"errors"
	"testing"

	"github.com/dkarczmarski/webcmd/pkg/callgate"
)

func TestDefaultFactoryProvider_GetFactory(t *testing.T) {
	t.Parallel()

	provider := callgate.NewDefaultFactoryProvider()

	tests := []struct {
		name    string
		mode    string
		wantErr error
	}{
		{
			name:    "single mode",
			mode:    "single",
			wantErr: nil,
		},
		{
			name:    "sequence mode",
			mode:    "sequence",
			wantErr: nil,
		},
		{
			name:    "invalid mode",
			mode:    "unknown",
			wantErr: callgate.ErrInvalidCallGateMode,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			factory, err := provider.GetFactory(tt.mode)

			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("expected error, got nil")
				} else if !errors.Is(err, tt.wantErr) {
					t.Errorf("expected error %v, got %v", tt.wantErr, err)
				}

				if factory != nil {
					t.Errorf("expected nil factory, got non-nil")
				}

				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if factory == nil {
				t.Errorf("expected non-nil factory, got nil")
			}

			gate := factory()
			if gate == nil {
				t.Errorf("expected non-nil gate from factory")
			}
		})
	}
}
