package codec

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/Clickin/xapi-go/internal/conformance/protocol"
)

func TestConformanceEncodingRegressions(t *testing.T) {
	t.Run("json defaults and empty cells", func(t *testing.T) {
		wire, err := Encode(protocol.Value{
			Datasets: []protocol.Dataset{{
				ID:      "d",
				Columns: []protocol.Column{{ID: "s", Type: "STRING"}},
				Rows: []protocol.Row{{Type: "N", Values: map[string]protocol.Cell{
					"s": {State: "empty"},
				}}},
			}},
		}, "nexacro-json-1.0")
		if err != nil {
			t.Fatal(err)
		}
		const expected = `{"Datasets":[{"ColumnInfo":{"Column":[{"size":"32","id":"s","type":"string"}]},"id":"d","Rows":[{"s":"","_RowType_":"N"}]}],"version":"1.0"}`
		if string(wire) != expected {
			t.Fatalf("wire = %s", wire)
		}
	})

	t.Run("nexacro SSV empty and missing markers", func(t *testing.T) {
		wire, err := Encode(protocol.Value{Datasets: []protocol.Dataset{{
			ID:      "d",
			Columns: []protocol.Column{{ID: "a", Type: "STRING"}, {ID: "b", Type: "STRING"}},
			Rows: []protocol.Row{{Type: "N", Values: map[string]protocol.Cell{
				"a": {State: "empty"}, "b": {State: "missing"},
			}}},
		}}}, nexacroSSVProfile)
		if err != nil {
			t.Fatal(err)
		}
		const expected = "U1NWOlVURi04HkRhdGFzZXQ6ZB5fUm93VHlwZV8fYTpzdHJpbmcoMzIpH2I6c3RyaW5nKDMyKR5OHx8DHh4="
		if got := base64.StdEncoding.EncodeToString(wire); got != expected {
			t.Fatalf("wire = %s", got)
		}
	})

	t.Run("binary internal parameter types are rejected", func(t *testing.T) {
		_, err := Encode(protocol.Value{Parameters: []protocol.Parameter{
			{ID: "u", Type: "UNDEFINED", State: "empty"},
			{ID: "n", Type: "NULL", State: "empty"},
			{ID: "d", Type: "DATASET", State: "empty"},
			{ID: "i", Type: "INVALID", State: "empty"},
		}}, nexacroBinaryProfile)
		if err == nil {
			t.Fatal("Encode accepted internal binary parameter types")
		}
	})
	t.Run("xplatform XML 5000 profile", func(t *testing.T) {
		wire, err := Encode(protocol.Value{Parameters: []protocol.Parameter{}, Datasets: []protocol.Dataset{}}, "xplatform-xml-5000")
		if err != nil {
			t.Fatal(err)
		}
		const expected = "PD94bWwgdmVyc2lvbj0iMS4wIiBlbmNvZGluZz0iVVRGLTgiPz4KPFJvb3QgeG1sbnM9Imh0dHA6Ly93d3cudG9iZXNvZnQuY29tL3BsYXRmb3JtL2RhdGFzZXQiIHZlcj0iNTAwMCI+Cgk8UGFyYW1ldGVycy8+CjwvUm9vdD4K"
		if got := base64.StdEncoding.EncodeToString(wire); got != expected {
			t.Fatalf("wire = %s", got)
		}
	})
}

func TestConformanceJSONPreservesExplicitColumnSize(t *testing.T) {
	wire, err := Encode(protocol.Value{Datasets: []protocol.Dataset{{
		ID:      "d",
		Columns: []protocol.Column{{ID: "s", Type: "STRING", Size: "50"}},
	}}}, "nexacro-json-1.0")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), `"size":"50"`) {
		t.Fatalf("wire = %s", wire)
	}
}

func TestConformanceJSONOmitsNullAndMissingTypedCells(t *testing.T) {
	wire, err := Encode(protocol.Value{Datasets: []protocol.Dataset{{
		ID:      "d",
		Columns: []protocol.Column{{ID: "i", Type: "INT"}, {ID: "f", Type: "FLOAT"}},
		Rows: []protocol.Row{{Type: "N", Values: map[string]protocol.Cell{
			"i": {State: "null"}, "f": {State: "missing"},
		}}},
	}}}, "nexacro-json-1.0")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), `"i":`) || strings.Contains(string(wire), `"f":`) {
		t.Fatalf("wire = %s", wire)
	}
}

func TestConformanceCustomSSVSeparatorsDecodeEmpty(t *testing.T) {
	wire, err := base64.StdEncoding.DecodeString("U1NWOnV0Zi04fkRhdGFzZXQ6ZH5fUm93VHlwZV98YTpTVFJJTkd+Tnx4fn4=")
	if err != nil {
		t.Fatal(err)
	}
	value, err := DecodeProfile(wire, nexacroSSVProfile, DecodeOptions{
		Strict: true, SSVUnitSeparator: "|", SSVRecordSeparator: "~",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(value.Parameters) != 0 || len(value.Datasets) != 0 {
		t.Fatalf("value = %#v", value)
	}
}

func TestConformanceSSVPreservesOriginalRows(t *testing.T) {
	wire, err := base64.StdEncoding.DecodeString("U1NWOnV0Zi04HnA6SU5UPTEeRGF0YXNldDpkHl9Db25zdF8fYzpTVFJJTkcoNSk9Zml4ZWQeX1Jvd1R5cGVfH2E6U1RSSU5HKDEwKTpTVU06dG90YWwfYjpJTlQeVR9uZXcfMR5PH29sZB8xHk4fHwMeHg==")
	if err != nil {
		t.Fatal(err)
	}
	value, err := DecodeProfile(wire, nexacroSSVProfile, DecodeOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(value.Datasets) != 1 || len(value.Datasets[0].Rows) != 2 {
		t.Fatalf("datasets = %#v", value.Datasets)
	}
	row := value.Datasets[0].Rows[0]
	if row.Type != "U" || row.OrgRow == nil || row.OrgRow.Type != "O" || row.OrgRow.Values["a"].Lexical != "old" {
		t.Fatalf("update = %#v", row)
	}
}
