package gateexec_test

import (
	"errors"
	"testing"
	"time"
)

func strPtr(s string) *string {
	return &s
}

func assertIs(t *testing.T, err error, target error) {
	t.Helper()

	if !errors.Is(err, target) {
		t.Fatalf("expected errors.Is(err, %v) == true, got false; err=%v", target, err)
	}
}

func assertEqual[T comparable](t *testing.T, got, want T, msg string) {
	t.Helper()

	if got != want {
		t.Fatalf("%s: got %v, want %v", msg, got, want)
	}
}

func eventually(t *testing.T, timeout time.Duration, check func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Fatalf("condition not satisfied within %s", timeout)
}
