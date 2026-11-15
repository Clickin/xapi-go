package main

import "testing"

func TestCapabilitiesAdvertiseXPlatformXML5000(t *testing.T) {
	profiles, ok := capabilities()["profiles"].([]map[string]any)
	if !ok {
		t.Fatalf("profiles = %#v", capabilities()["profiles"])
	}
	for _, profile := range profiles {
		if profile["name"] == "xplatform-xml-5000" {
			return
		}
	}
	t.Fatal("xplatform-xml-5000 profile missing")
}
