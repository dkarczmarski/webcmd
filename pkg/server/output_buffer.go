package server

import (
	"bytes"
	"io"
)

type outputBuffer interface {
	io.Writer
	WriteTo(w io.Writer) (int64, error)
	Close() error
}

type memoryOutputBuffer struct {
	bytes.Buffer
}

func newMemoryOutputBuffer() *memoryOutputBuffer {
	//nolint:exhaustruct
	return &memoryOutputBuffer{}
}

func (b *memoryOutputBuffer) Close() error {
	return nil
}
