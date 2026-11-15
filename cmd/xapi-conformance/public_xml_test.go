package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	xapi "github.com/Clickin/xapi-go"
)

type xmlEncodeVector struct {
	ID      string         `json:"id"`
	Profile string         `json:"profile"`
	Value   map[string]any `json:"value"`
	Expect  struct {
		Kind   string `json:"kind"`
		Output struct {
			Data string `json:"data"`
		} `json:"output"`
	} `json:"expect"`
}

// TestPublicXMLEncoderMatchesDriverVectors encodes the values of the driver
// XML encode vectors through the public API and compares the bytes with the
// original drivers' output. Values the public API cannot state are skipped:
// a saveType other than ALL (the public encoder keeps row types), and
// constant values a struct tag cannot carry.
func TestPublicXMLEncoderMatchesDriverVectors(t *testing.T) {
	dir := os.Getenv("XAPI_CONFORMANCE_DIR")
	if dir == "" {
		dir = "../../../xapi-conformance"
	}
	files, _ := filepath.Glob(filepath.Join(dir, "vectors/driver/*/*xml*.encode.json"))
	if len(files) == 0 {
		t.Skip("xapi-conformance checkout not found; set XAPI_CONFORMANCE_DIR")
	}
	checked := 0
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var v xmlEncodeVector
		if err := json.Unmarshal(b, &v); err != nil {
			t.Fatal(err)
		}
		if v.Expect.Kind != "wire" || !publicXMLCanState(v) {
			continue
		}
		profile, err := parseProfile(v.Profile)
		if err != nil {
			t.Fatal(err)
		}
		codec, err := xapi.NewCodec(profile)
		if err != nil {
			t.Fatal(err)
		}
		got, err := encodeValue(codec, v.Value, profile)
		want, _ := base64.StdEncoding.DecodeString(v.Expect.Output.Data)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s: err=%v\nwant %q\n got %q", v.ID, err, want, got)
		}
		checked++
	}
	t.Logf("%d vectors checked", checked)
	if checked < 80 {
		t.Fatalf("only %d vectors checked", checked)
	}
}

func publicXMLCanState(v xmlEncodeVector) bool {
	if strings.Contains(v.ID, ".savetype-") && !strings.HasSuffix(v.ID, "-1.encode") ||
		strings.Contains(v.ID, "unchanged-values-unset") {
		return false
	}
	for _, rawDataset := range array(v.Value["datasets"]) {
		dataset := rawDataset.(map[string]any)
		for _, rawConstant := range array(dataset["constColumns"]) {
			cell, _ := rawConstant.(map[string]any)["value"].(map[string]any)
			lexical := rawString(cell["lexical"])
			switch rawString(cell["state"]) {
			case "empty":
				return false
			case "value":
				if lexical != strings.TrimSpace(lexical) || strings.ContainsAny(lexical, ",\"\\\t\r\n") {
					return false
				}
			}
		}
	}
	return true
}
