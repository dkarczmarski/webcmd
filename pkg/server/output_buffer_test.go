//nolint:dupl
package server_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dkarczmarski/webcmd/pkg/server"
)

func TestNewThresholdBuffer_BelowThreshold_StaysReadable(t *testing.T) {
	t.Parallel()

	buf := server.NewThresholdBuffer(1024)
	defer func() {
		if err := buf.Close(); err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	}()

	input := "small payload"

	written, err := buf.Write([]byte(input))
	if err != nil {
		t.Fatalf("Write returned error: %v", err)
	}

	if written != len(input) {
		t.Fatalf("expected written=%d, got %d", len(input), written)
	}

	var out bytes.Buffer

	copied, err := buf.WriteTo(&out)
	if err != nil {
		t.Fatalf("WriteTo returned error: %v", err)
	}

	if copied != int64(len(input)) {
		t.Fatalf("expected copied=%d, got %d", len(input), copied)
	}

	if out.String() != input {
		t.Fatalf("expected output %q, got %q", input, out.String())
	}
}

func TestNewThresholdBuffer_ExceedThreshold_WritesFullContent(t *testing.T) {
	t.Parallel()

	buf := server.NewThresholdBuffer(10)
	defer func() {
		if err := buf.Close(); err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	}()

	input := "0123456789ABCDEF" // 16 bytes, exceeds threshold 10

	written, err := buf.Write([]byte(input))
	if err != nil {
		t.Fatalf("Write returned error: %v", err)
	}

	if written != len(input) {
		t.Fatalf("expected written=%d, got %d", len(input), written)
	}

	var out bytes.Buffer

	copied, err := buf.WriteTo(&out)
	if err != nil {
		t.Fatalf("WriteTo returned error: %v", err)
	}

	if copied != int64(len(input)) {
		t.Fatalf("expected copied=%d, got %d", len(input), copied)
	}

	if out.String() != input {
		t.Fatalf("expected output %q, got %q", input, out.String())
	}
}

func TestNewThresholdBuffer_MultipleWritesAcrossThreshold_WritesFullContent(t *testing.T) {
	t.Parallel()

	buf := server.NewThresholdBuffer(10)
	defer func() {
		if err := buf.Close(); err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	}()

	parts := []string{"1234", "5678", "90ABC", "DEF"}
	expected := strings.Join(parts, "")

	for _, part := range parts {
		written, err := buf.Write([]byte(part))
		if err != nil {
			t.Fatalf("Write(%q) returned error: %v", part, err)
		}

		if written != len(part) {
			t.Fatalf("expected written=%d for part %q, got %d", len(part), part, written)
		}
	}

	var out bytes.Buffer

	copied, err := buf.WriteTo(&out)
	if err != nil {
		t.Fatalf("WriteTo returned error: %v", err)
	}

	if copied != int64(len(expected)) {
		t.Fatalf("expected copied=%d, got %d", len(expected), copied)
	}

	if out.String() != expected {
		t.Fatalf("expected output %q, got %q", expected, out.String())
	}
}

func TestNewThresholdBuffer_WriteTo_CanBeCalledMultipleTimesAfterSpill(t *testing.T) {
	t.Parallel()

	buf := server.NewThresholdBuffer(8)
	defer func() {
		if err := buf.Close(); err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	}()

	input := "0123456789ABCDEFG" // definitely above threshold

	if _, err := buf.Write([]byte(input)); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}

	var out1 bytes.Buffer

	copied1, err := buf.WriteTo(&out1)
	if err != nil {
		t.Fatalf("first WriteTo returned error: %v", err)
	}

	if copied1 != int64(len(input)) {
		t.Fatalf("expected first copied=%d, got %d", len(input), copied1)
	}

	if out1.String() != input {
		t.Fatalf("expected first output %q, got %q", input, out1.String())
	}

	var out2 bytes.Buffer

	copied2, err := buf.WriteTo(&out2)
	if err != nil {
		t.Fatalf("second WriteTo returned error: %v", err)
	}

	if copied2 != int64(len(input)) {
		t.Fatalf("expected second copied=%d, got %d", len(input), copied2)
	}

	if out2.String() != input {
		t.Fatalf("expected second output %q, got %q", input, out2.String())
	}
}

func TestNewThresholdBuffer_ZeroThreshold_WritesDirectlyToFile(t *testing.T) {
	t.Parallel()

	buf := server.NewThresholdBuffer(0)
	defer func() {
		if err := buf.Close(); err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	}()

	input := "small payload"

	written, err := buf.Write([]byte(input))
	if err != nil {
		t.Fatalf("Write returned error: %v", err)
	}

	if written != len(input) {
		t.Fatalf("expected written=%d, got %d", len(input), written)
	}

	var out bytes.Buffer

	copied, err := buf.WriteTo(&out)
	if err != nil {
		t.Fatalf("WriteTo returned error: %v", err)
	}

	if copied != int64(len(input)) {
		t.Fatalf("expected copied=%d, got %d", len(input), copied)
	}

	if out.String() != input {
		t.Fatalf("expected output %q, got %q", input, out.String())
	}
}
