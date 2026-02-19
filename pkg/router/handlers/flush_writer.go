package handlers

import (
	"net/http"
)

type flushResponseWriter struct {
	http.ResponseWriter
	flusher http.Flusher
}

func newFlushResponseWriter(responseWriter http.ResponseWriter) *flushResponseWriter {
	f, ok := responseWriter.(http.Flusher)
	if !ok {
		panic("ResponseWriter does not support flushing")
	}

	return &flushResponseWriter{ResponseWriter: responseWriter, flusher: f}
}

func (f *flushResponseWriter) Write(p []byte) (int, error) {
	n, err := f.ResponseWriter.Write(p)

	f.flusher.Flush()

	return n, err //nolint:wrapcheck
}

func (f *flushResponseWriter) Flush() {
	f.flusher.Flush()
}
