package xapi

import (
	"bytes"
	"testing"
)

type formatRow struct {
	ID   int    `xapi:"col=id,type=INT"`
	Name string `xapi:"col=name,type=STRING"`
}

type formatDocument struct {
	Rows []formatRow `xapi:"dataset=d"`
}

func TestSSVProfilesRoundTrip(t *testing.T) {
	input := formatDocument{Rows: []formatRow{{ID: 3, Name: "ok"}}}
	for _, profile := range []Profile{NexacroSSV, XPlatformSSV} {
		wire, err := Marshal(input, profile)
		if err != nil {
			t.Fatalf("%s encode: %v", profile, err)
		}
		var got formatDocument
		if err := Unmarshal(wire, &got, profile, WithStrictMode()); err != nil {
			t.Fatalf("%s decode: %v", profile, err)
		}
		if len(got.Rows) != 1 || got.Rows[0] != input.Rows[0] {
			t.Fatalf("%s roundtrip: %#v", profile, got)
		}
	}
}

func TestBinaryProfilesRoundTrip(t *testing.T) {
	input := formatDocument{Rows: []formatRow{{ID: 3, Name: "ok"}}}
	for _, profile := range []Profile{NexacroBinary5000, XPlatformBinary5000} {
		wire, err := Marshal(input, profile)
		if err != nil {
			t.Fatalf("%s encode: %v", profile, err)
		}
		var got formatDocument
		if err := Unmarshal(wire, &got, profile, WithStrictMode()); err != nil {
			t.Fatalf("%s decode: %v", profile, err)
		}
		if len(got.Rows) != 1 || got.Rows[0] != input.Rows[0] {
			t.Fatalf("%s roundtrip: %#v", profile, got)
		}
	}
}

func TestBinaryRejectsTruncatedPayload(t *testing.T) {
	var got formatDocument
	if err := Unmarshal([]byte{0xfe, 0x10, 0x13}, &got, NexacroBinary5000); err == nil {
		t.Fatal("truncated PlatformBinary accepted")
	}
}

func TestSSVCustomSeparators(t *testing.T) {
	input := formatDocument{Rows: []formatRow{{ID: 3, Name: "ok"}}}
	wire, err := Marshal(input, XPlatformSSV, WithSSVSeparators("|", "~"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(wire, []byte("SSV:utf-8~Dataset:d~")) {
		t.Fatalf("custom SSV framing missing: %q", wire)
	}
	var got formatDocument
	if err := Unmarshal(wire, &got, XPlatformSSV, WithSSVSeparators("|", "~")); err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 1 || got.Rows[0] != input.Rows[0] {
		t.Fatalf("custom SSV roundtrip: %#v", got)
	}
}
