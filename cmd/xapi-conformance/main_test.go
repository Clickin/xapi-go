package main

import "testing"

func TestNormalizeDecodedRows(t *testing.T) {
	value := map[string]any{"datasets": []any{map[string]any{"rows": []any{
		map[string]any{"type": "I", "orgRow": nil},
		map[string]any{"type": "U", "orgRow": map[string]any{"type": "O"}},
		map[string]any{"type": "D", "orgRow": nil},
	}}}}
	normalizeDecodedValue(value, "nexacro-xml-4000", false, true)
	rows := array(array(value["datasets"])[0].(map[string]any)["rows"])
	if len(rows) != 3 {
		t.Fatalf("rows = %#v", rows)
	}
	update := rows[1].(map[string]any)
	if update["type"] != "U" || update["orgRow"].(map[string]any)["type"] != "O" {
		t.Fatalf("normalized update = %#v", update)
	}
}

func TestCapabilitiesAdvertiseXPlatformXML(t *testing.T) {
	profiles, ok := capabilities()["profiles"].([]map[string]any)
	if !ok {
		t.Fatalf("profiles = %#v", capabilities()["profiles"])
	}
	for _, profile := range profiles {
		if profile["name"] == "xplatform-xml-4000" {
			return
		}
	}
	t.Fatal("xplatform-xml-4000 profile missing")
}

func TestNormalizeDecodedJSONOriginalRow(t *testing.T) {
	value := map[string]any{"datasets": []any{map[string]any{"rows": []any{
		map[string]any{"type": "U", "orgRow": map[string]any{"type": "O"}},
	}}}}
	normalizeDecodedValue(value, "nexacro-json-1.0", true, true)
	row := array(array(value["datasets"])[0].(map[string]any)["rows"])[0].(map[string]any)
	if row["orgRow"].(map[string]any)["type"] != "O" {
		t.Fatalf("normalized row = %#v", row)
	}
}
