package middleware

import "net/http"

func NewInMemoryWriter(w http.ResponseWriter) *InMemoryWriter {
	return &InMemoryWriter{
		w: w,
		h: make(http.Header),
	}
}

type InMemoryWriter struct {
	w      http.ResponseWriter
	Buf    []byte
	Status int
	h      http.Header
}

func (w *InMemoryWriter) Header() http.Header {
	return w.h
}

func (w *InMemoryWriter) Write(b []byte) (int, error) {
	w.Buf = b
	return len(b), nil
}

func (w *InMemoryWriter) WriteHeader(statusCode int) {
	w.Status = statusCode
}
