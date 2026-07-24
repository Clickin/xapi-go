package codec

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
)

// zlibSignature is the two-byte proprietary prefix the PlatformZlib protocol
// filter writes before a standard zlib stream: FF AD (decimal 65453).
const zlibSignature = "\xff\xad"

// IsZlib reports whether b begins with the PlatformZlib signature (FF AD). XML,
// JSON, and SSV documents never begin with these bytes, so the signature is an
// unambiguous transport marker and is auto-detected on decode exactly as the
// jars do (PlatformRequest peeks the first two bytes).
func IsZlib(b []byte) bool {
	return len(b) >= 2 && b[0] == 0xff && b[1] == 0xad
}

// InflateZlib decompresses a PlatformZlib payload. It requires the FF AD
// signature followed by a standard zlib (RFC 1950) stream, as produced by the
// jars' PlatformZlibByteDecoder, and returns the inner format bytes.
func InflateZlib(b []byte) ([]byte, error) {
	return InflateZlibLimit(b, 0)
}

// InflateZlibLimit is InflateZlib with an optional decompressed-size limit.
// A non-positive limit disables the limit. The reader consumes at most one
// byte beyond the limit, so oversized compressed payloads are rejected before
// unbounded output is allocated.
func InflateZlibLimit(b []byte, limit int64) ([]byte, error) {
	if !IsZlib(b) {
		return nil, fmt.Errorf("missing PlatformZlib signature")
	}
	reader, err := zlib.NewReader(bytes.NewReader(b[2:]))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	if limit <= 0 {
		return io.ReadAll(reader)
	}
	output, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(output)) > limit {
		return nil, fmt.Errorf("decompressed payload exceeds limit")
	}
	return output, nil
}

// FF AD signature followed by a zlib stream at the fastest level, mirroring the
// jars' PlatformZlibByteEncoder. The compressed bytes are deterministic for a
// given implementation but are not guaranteed byte-identical across zlib
// libraries, so wire-exact comparison of zlib output is reference-local.
func deflateZlibStd(b []byte) ([]byte, error) {
	var out bytes.Buffer
	out.WriteString(zlibSignature)
	writer := zlib.NewWriter(&out)
	if _, err := writer.Write(b); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
