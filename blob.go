package xapi

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
)

// BlobSource lets callers encode a large BLOB without first copying it into a
// second byte slice. Open must return a fresh reader for each encode.
type BlobSource interface {
	Open() (io.ReadCloser, error)
	Size() int64
}

type blobAccumulator struct {
	pending         []byte
	data            []byte
	max             int64
	allowWhitespace bool
	decode          []byte
}

func decodeBlobString(s string, max int64, allowWhitespace bool) ([]byte, error) {
	var err error
	s, err = normalizeBase64(s, allowWhitespace)
	if err != nil {
		return nil, err
	}
	if max > 0 && int64(base64.StdEncoding.DecodedLen(len(s))) > max {
		return nil, fmt.Errorf("blob exceeds limit")
	}
	b, e := base64.StdEncoding.DecodeString(s)
	if e != nil {
		return nil, e
	}
	if max > 0 && int64(len(b)) > max {
		return nil, fmt.Errorf("blob exceeds limit")
	}
	return b, nil
}

func normalizeBase64(s string, allowWhitespace bool) (string, error) {
	for i := 0; i < len(s); i++ {
		if !isBase64Whitespace(s[i]) {
			continue
		}
		if !allowWhitespace {
			return "", fmt.Errorf("base64 whitespace is not allowed")
		}
		var b bytes.Buffer
		b.Grow(len(s))
		b.WriteString(s[:i])
		for _, c := range s[i:] {
			if !isBase64Whitespace(byte(c)) {
				b.WriteByte(byte(c))
			}
		}
		return b.String(), nil
	}
	return s, nil
}

func isBase64Whitespace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}

func (b *blobAccumulator) Write(p []byte) error {
	for _, c := range p {
		if isBase64Whitespace(c) {
			if !b.allowWhitespace {
				return fmt.Errorf("base64 whitespace is not allowed")
			}
			continue
		}
		b.pending = append(b.pending, c)
	}
	n := len(b.pending) / 4 * 4
	if n == 0 {
		return nil
	}
	need := base64.StdEncoding.DecodedLen(n)
	if cap(b.decode) < need {
		b.decode = make([]byte, need)
	}
	out := b.decode[:need]
	m, e := base64.StdEncoding.Decode(out, b.pending[:n])
	if e != nil {
		return e
	}
	if b.max > 0 && int64(len(b.data)+m) > b.max {
		return fmt.Errorf("blob exceeds limit")
	}
	b.appendDecoded(out[:m])
	copy(b.pending, b.pending[n:])
	b.pending = b.pending[:len(b.pending)-n]
	return nil
}
func (b *blobAccumulator) Close() ([]byte, error) {
	if len(b.pending) > 0 {
		need := base64.StdEncoding.DecodedLen(len(b.pending))
		if cap(b.decode) < need {
			b.decode = make([]byte, need)
		}
		out := b.decode[:need]
		m, e := base64.StdEncoding.Decode(out, b.pending)
		if e != nil {
			return nil, e
		}
		if b.max > 0 && int64(len(b.data)+m) > b.max {
			return nil, fmt.Errorf("blob exceeds limit")
		}
		b.appendDecoded(out[:m])
		b.pending = nil
	}
	return b.data, nil
}

func (b *blobAccumulator) appendDecoded(p []byte) {
	if len(p) == 0 {
		return
	}
	if cap(b.data)-len(b.data) < len(p) {
		want := len(b.data) + len(p)
		capn := cap(b.data) * 2
		if capn < 32<<10 {
			capn = 32 << 10
		}
		if capn < want {
			capn = want
		}
		if b.max > 0 && int64(capn) > b.max {
			capn = int(b.max)
		}
		next := make([]byte, len(b.data), capn)
		copy(next, b.data)
		b.data = next
	}
	b.data = append(b.data, p...)
}
