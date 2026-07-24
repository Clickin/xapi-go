//go:build !cgo

package codec

func DeflateZlib(b []byte) ([]byte, error) {
	return deflateZlibStd(b)
}
