//go:build cgo

package codec

/*
#cgo LDFLAGS: -lz
#include <stdlib.h>
#include <zlib.h>
*/
import "C"

import (
	"fmt"
)

func DeflateZlib(b []byte) ([]byte, error) {
	input := C.CBytes(b)
	defer C.free(input)

	bound := C.compressBound(C.uLong(len(b)))
	output := C.malloc(C.size_t(bound))
	if output == nil {
		return nil, fmt.Errorf("zlib output allocation failed")
	}
	defer C.free(output)

	outputLen := bound
	result := C.compress2(
		(*C.Bytef)(output),
		&outputLen,
		(*C.Bytef)(input),
		C.uLong(len(b)),
		C.Z_DEFAULT_COMPRESSION,
	)
	if result != C.Z_OK {
		return nil, fmt.Errorf("zlib compression failed: %d", int(result))
	}

	compressed := C.GoBytes(output, C.int(outputLen))
	return append([]byte(zlibSignature), compressed...), nil
}
