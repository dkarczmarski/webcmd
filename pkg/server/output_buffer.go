package server

import (
	"bytes"
	"fmt"
	"io"
	"os"
)

type outputBuffer interface {
	io.Writer
	io.WriterTo
	io.Closer
}

type MemoryOutputBuffer struct {
	bytes.Buffer
}

func NewMemoryOutputBuffer() *MemoryOutputBuffer {
	//nolint:exhaustruct
	return &MemoryOutputBuffer{}
}

func (b *MemoryOutputBuffer) Close() error {
	return nil
}

type ThresholdBuffer struct {
	threshold int
	mem       bytes.Buffer
	file      *os.File
}

func NewThresholdBuffer(threshold int) *ThresholdBuffer {
	if threshold < 0 {
		panic("negative threshold for ThresholdBuffer")
	}

	//nolint:exhaustruct
	return &ThresholdBuffer{
		threshold: threshold,
	}
}

func (b *ThresholdBuffer) Write(data []byte) (int, error) {
	if b.file == nil && b.mem.Len()+len(data) <= b.threshold {
		written, err := b.mem.Write(data)
		if err != nil {
			return written, fmt.Errorf("write to memory buffer: %w", err)
		}

		return written, nil
	}

	if b.file == nil {
		if err := b.spillToFile(); err != nil {
			return 0, err
		}
	}

	written, err := b.file.Write(data)
	if err != nil {
		return written, fmt.Errorf("write to temp file: %w", err)
	}

	return written, nil
}

func (b *ThresholdBuffer) WriteTo(writer io.Writer) (int64, error) {
	if b.file == nil {
		written, err := b.mem.WriteTo(writer)
		if err != nil {
			return written, fmt.Errorf("write memory buffer to writer: %w", err)
		}

		return written, nil
	}

	if _, err := b.file.Seek(0, io.SeekStart); err != nil {
		return 0, fmt.Errorf("seek temp file: %w", err)
	}

	written, err := io.Copy(writer, b.file)
	if err != nil {
		return written, fmt.Errorf("copy temp file to writer: %w", err)
	}

	return written, nil
}

func (b *ThresholdBuffer) Close() error {
	if b.file == nil {
		return nil
	}

	fileName := b.file.Name()

	closeErr := b.file.Close()
	removeErr := os.Remove(fileName)

	b.file = nil

	if closeErr != nil {
		return fmt.Errorf("close temp file: %w", closeErr)
	}

	if removeErr != nil && !os.IsNotExist(removeErr) {
		return fmt.Errorf("remove temp file: %w", removeErr)
	}

	return nil
}

func (b *ThresholdBuffer) spillToFile() error {
	tempFile, err := os.CreateTemp("", "webcmd-threshold-buffer-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	if b.mem.Len() > 0 {
		if _, err := tempFile.Write(b.mem.Bytes()); err != nil {
			tempFileName := tempFile.Name()
			_ = tempFile.Close()
			_ = os.Remove(tempFileName)

			return fmt.Errorf("write memory buffer to temp file: %w", err)
		}
	}

	b.mem.Reset()
	b.file = tempFile

	return nil
}
