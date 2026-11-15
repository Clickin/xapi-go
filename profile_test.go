package xapi

import (
	"strings"
	"testing"
)

func TestZeroProfileWritesDriverXPlatformRoot(t *testing.T) {
	var zero Profile
	if zero != XPlatformXML5000 {
		t.Fatalf("zero profile = %s", zero)
	}
	codec, err := NewCodec(zero)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := codec.Encode(&out, fixtureResponse{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `<Root xmlns="http://www.tobesoft.com/platform/dataset" ver="5000">`) {
		t.Fatalf("zero profile root: %s", out.String())
	}
}
