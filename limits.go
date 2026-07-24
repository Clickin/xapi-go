package xapi

import (
	"errors"
	"io"
)

type Limits struct {
	MaxDocumentBytes, MaxScalarBytes, MaxBlobBytes int64
	MaxDepth, MaxDatasets, MaxColumns, MaxRows     int
}

var defaultLimits = Limits{MaxDocumentBytes: 128 << 20, MaxScalarBytes: 16 << 20, MaxBlobBytes: 64 << 20, MaxDepth: 64, MaxDatasets: 1000, MaxColumns: 10000, MaxRows: 10_000_000}

var errDocumentLimit = errors.New("xapi: document limit exceeded")

type documentReader struct {
	r   io.Reader
	max int64
	n   int64
}

func (r *documentReader) Read(p []byte) (int, error) {
	if r.max <= 0 {
		return r.r.Read(p)
	}
	if r.n >= r.max {
		var one [1]byte
		n, err := r.r.Read(one[:])
		if n > 0 {
			r.n += int64(n)
			return 0, errDocumentLimit
		}
		return 0, err
	}
	remaining := r.max - r.n
	if int64(len(p)) > remaining+1 {
		p = p[:remaining+1]
	}
	n, err := r.r.Read(p)
	r.n += int64(n)
	if r.n > r.max {
		return n, errDocumentLimit
	}
	return n, err
}
