package main

import (
	"encoding/base64"
	"fmt"
	"strings"
)

func preflightCanonicalLimits(value map[string]any, options map[string]any) (string, error) {
	limits, _ := options["limits"].(map[string]any)
	if limits == nil {
		return "", nil
	}
	max := func(key string) (int64, bool) {
		n, ok := limits[key].(float64)
		return int64(n), ok
	}
	if cap, ok := max("datasets"); ok && int64(len(array(value["datasets"]))) > cap {
		return "value.datasets", fmt.Errorf("dataset limit exceeded")
	}
	for di, rawDataset := range array(value["datasets"]) {
		dataset, _ := rawDataset.(map[string]any)
		if cap, ok := max("columns"); ok && int64(len(array(dataset["columns"]))) > cap {
			return fmt.Sprintf("value.datasets[%d].columns", di), fmt.Errorf("column limit exceeded")
		}
		if cap, ok := max("rows"); ok && int64(len(array(dataset["rows"]))) > cap {
			return fmt.Sprintf("value.datasets[%d].rows", di), fmt.Errorf("row limit exceeded")
		}
		for ri, rawRow := range array(dataset["rows"]) {
			row, _ := rawRow.(map[string]any)
			if cap, ok := max("depth"); ok && row["orgRow"] != nil && cap <= 1 {
				return fmt.Sprintf("value.datasets[%d].rows[%d].orgRow", di, ri), fmt.Errorf("depth limit exceeded")
			}
			for id, rawCell := range mapValue(row["values"]) {
				cell, _ := rawCell.(map[string]any)
				if rawString(cell["state"]) == "missing" || rawString(cell["state"]) == "null" {
					continue
				}
				lexical := rawString(cell["lexical"])
				typ := columnType(dataset, id)
				if typ == "BLOB" {
					decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lexical))
					if err == nil {
						if cap, ok := max("blobBytes"); ok && int64(len(decoded)) > cap {
							return fmt.Sprintf("value.datasets[%d].rows[%d].values.%s", di, ri, id), fmt.Errorf("blob limit exceeded")
						}
					}
				} else if cap, ok := max("scalarBytes"); ok && int64(len(lexical)) > cap {
					return fmt.Sprintf("value.datasets[%d].rows[%d].values.%s", di, ri, id), fmt.Errorf("scalar limit exceeded")
				}
			}
		}
	}
	return "", nil
}

func columnType(dataset map[string]any, id string) string {
	for _, raw := range append(array(dataset["columns"]), array(dataset["constColumns"])...) {
		column, _ := raw.(map[string]any)
		if rawString(column["id"]) == id {
			return strings.ToUpper(rawString(column["type"]))
		}
	}
	return "STRING"
}
func mapValue(value any) map[string]any {
	m, _ := value.(map[string]any)
	return m
}
