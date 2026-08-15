package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
)

type xmlNode struct {
	Name     xml.Name
	Attr     []xml.Attr
	Children []*xmlNode
	Text     string
}

func (n *xmlNode) UnmarshalXML(dec *xml.Decoder, start xml.StartElement) error {
	n.Name, n.Attr = start.Name, append([]xml.Attr(nil), start.Attr...)
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch v := tok.(type) {
		case xml.StartElement:
			child := new(xmlNode)
			if err := dec.DecodeElement(child, &v); err != nil {
				return err
			}
			n.Children = append(n.Children, child)
		case xml.CharData:
			n.Text += string(v)
		case xml.Directive:
			return fmt.Errorf("XML directives are not supported")
		case xml.EndElement:
			return nil
		}
	}
}

func parseWire(data []byte, profile string) (map[string]any, error) {
	if profile == "nexacro-json-1.0" {
		return parseJSONWire(data)
	}
	return parseXMLWire(data)
}

func parseXMLWire(data []byte) (map[string]any, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var root xmlNode
	if err := dec.Decode(&root); err != nil {
		return nil, err
	}
	if root.Name.Local != "Root" {
		return nil, fmt.Errorf("XML root must be Root")
	}
	value := map[string]any{"parameters": []any{}, "datasets": []any{}}
	if err := validateXMLStructure(&root); err != nil {
		return nil, err
	}
	wireRoot := map[string]any{}
	if root.Name.Space != "" {
		wireRoot["namespace"] = root.Name.Space
	}
	if v := xmlAttr(&root, "ver"); v != "" {
		wireRoot["ver"] = v
	}
	if v := xmlAttr(&root, "version"); v != "" {
		wireRoot["version"] = v
	}
	if len(wireRoot) > 0 {
		value["wire"] = map[string]any{"root": wireRoot}
	}
	for _, child := range root.Children {
		switch child.Name.Local {
		case "Parameters":
			for i, param := range children(child, "Parameter") {
				typ := strings.ToUpper(xmlAttr(param, "type"))
				if typ == "" {
					typ = "STRING"
				}
				p := map[string]any{"id": xmlAttr(param, "id"), "type": typ}
				lexical := strings.TrimSpace(param.Text)
				form := "text"
				if attr := xmlAttr(param, "value"); hasXMLAttr(param, "value") {
					lexical, form = attr, "attribute"
				}
				p["state"] = cellState(lexical, true)
				if lexical != "" {
					p["lexical"] = lexical
				}
				if i > 0 {
					p["index"] = i
				}
				p["wire"] = map[string]any{"valueForm": form}
				appendSlice(value, "parameters", p)
			}
		case "Dataset":
			appendSlice(value, "datasets", parseXMLDataset(child))
		}
	}
	if err := validateCanonicalColumns(value); err != nil {
		return nil, err
	}
	return value, nil
}

func parseXMLDataset(node *xmlNode) map[string]any {
	d := map[string]any{"id": xmlAttr(node, "id"), "columns": []any{}, "constColumns": []any{}, "rows": []any{}}
	columnIndex := 0
	for _, child := range node.Children {
		if child.Name.Local != "ColumnInfo" {
			continue
		}
		for _, col := range child.Children {
			if col.Name.Local != "Column" && col.Name.Local != "ConstColumn" {
				continue
			}
			m := map[string]any{"id": xmlAttr(col, "id"), "type": strings.ToUpper(xmlAttr(col, "type"))}
			if m["type"] == "" {
				m["type"] = "STRING"
			}
			if size := xmlAttr(col, "size"); size != "" {
				m["size"] = size
			}
			if encoding := xmlAttr(col, "enc"); encoding != "" {
				m["encoding"] = encoding
			}
			if col.Name.Local == "ConstColumn" {
				lexical := xmlAttr(col, "value")
				cell := map[string]any{"state": cellState(lexical, true)}
				if lexical != "" {
					cell["lexical"] = lexical
				}
				m["value"] = cell
				appendSlice(d, "constColumns", m)
				continue
			}
			m["index"] = columnIndex
			columnIndex++
			appendSlice(d, "columns", m)
		}
	}
	for _, child := range node.Children {
		if child.Name.Local != "Rows" {
			continue
		}
		for _, row := range children(child, "Row") {
			appendSlice(d, "rows", parseXMLRow(row, d))
		}
	}
	return d
}

func parseXMLRow(node *xmlNode, dataset map[string]any) map[string]any {
	rowType := "N"
	switch strings.ToLower(xmlAttr(node, "type")) {
	case "insert":
		rowType = "I"
	case "update":
		rowType = "U"
	case "delete":
		rowType = "D"
	}
	row := map[string]any{"type": rowType, "orgRow": nil, "values": map[string]any{}}
	for _, rawColumn := range array(dataset["columns"]) {
		column := rawColumn.(map[string]any)
		row["values"].(map[string]any)[rawString(column["id"])] = map[string]any{"state": "missing"}
	}
	for _, child := range node.Children {
		if child.Name.Local == "Col" {
			id := xmlAttr(child, "id")
			if id == "" {
				continue
			}
			lexical := child.Text
			cell := map[string]any{"state": cellState(lexical, true)}
			if lexical != "" {
				cell["lexical"] = lexical
			}
			row["values"].(map[string]any)[id] = cell
			continue
		}
		if child.Name.Local == "OrgRow" {
			if rowType != "U" {
				continue
			}
			org := parseXMLRow(child, dataset)
			for _, rawColumn := range array(dataset["columns"]) {
				column := rawColumn.(map[string]any)
				id := rawString(column["id"])
				if _, present := org["values"].(map[string]any)[id]; !present {
					org["values"].(map[string]any)[id] = map[string]any{"state": "missing"}
				}
			}
			org["type"] = "O"
			row["orgRow"] = org
		}
	}
	return row
}

func parseJSONWire(data []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var root map[string]any
	if err := dec.Decode(&root); err != nil {
		return nil, err
	}
	value := map[string]any{"parameters": []any{}, "datasets": []any{}}
	if version, ok := root["version"]; ok {
		value["wire"] = map[string]any{"version": version}
	}
	params := firstArray(root, "Parameters", "parameters")
	for i, raw := range params {
		m, _ := raw.(map[string]any)
		id, _ := m["id"].(string)
		cell := jsonCell(m["value"])
		p := map[string]any{"id": id, "type": inferJSONType(m["value"]), "state": cell["state"]}
		if lexical, ok := cell["lexical"]; ok {
			p["lexical"] = lexical
		}
		if i > 0 {
			p["index"] = i
		}
		appendSlice(value, "parameters", p)
	}
	for _, raw := range firstArray(root, "Datasets", "datasets") {
		if m, ok := raw.(map[string]any); ok {
			appendSlice(value, "datasets", parseJSONDataset(m))
		}
	}
	return value, nil
}

func parseJSONDataset(raw map[string]any) map[string]any {
	d := map[string]any{"id": rawString(raw["id"]), "columns": []any{}, "constColumns": []any{}, "rows": []any{}}
	ci, _ := raw["ColumnInfo"].(map[string]any)
	columnIndex := 0
	for _, key := range []string{"ConstColumn", "Column"} {
		for _, rawCol := range array(ci[key]) {
			col, _ := rawCol.(map[string]any)
			m := map[string]any{"id": rawString(col["id"]), "type": strings.ToUpper(rawString(col["type"]))}
			if m["type"] == "" {
				m["type"] = "STRING"
			}
			if size := rawString(col["size"]); size != "" {
				m["size"] = size
			}
			if key == "ConstColumn" {
				m["value"] = jsonCell(col["value"])
				appendSlice(d, "constColumns", m)
			} else {
				m["index"] = columnIndex
				columnIndex++
				appendSlice(d, "columns", m)
			}
		}
	}
	rows := make([]any, 0, len(array(raw["Rows"])))
	for _, rawRow := range array(raw["Rows"]) {
		if row, ok := rawRow.(map[string]any); ok {
			parsed := parseJSONRow(row, d)
			if rawString(parsed["type"]) == "O" {
				continue
			}
			rows = append(rows, parsed)
		}
	}
	d["rows"] = rows
	return d
}

func parseJSONRow(raw map[string]any, dataset map[string]any) map[string]any {
	kind := "N"
	switch rawString(raw["_RowType_"]) {
	case "I", "U", "D", "O":
		kind = rawString(raw["_RowType_"])
	}
	row := map[string]any{"type": kind, "orgRow": nil, "values": map[string]any{}}
	known := map[string]bool{}
	for _, c := range append(array(dataset["constColumns"]), array(dataset["columns"])...) {
		if col, ok := c.(map[string]any); ok {
			known[rawString(col["id"])] = true
		}
	}
	for key, rawValue := range raw {
		if key == "_RowType_" || !known[key] {
			continue
		}
		row["values"].(map[string]any)[key] = jsonCell(rawValue)
	}
	return row
}

func jsonCell(raw any) map[string]any {
	if raw == nil {
		return map[string]any{"state": "null"}
	}
	if s, ok := raw.(string); ok {
		cell := map[string]any{"state": cellState(s, true)}
		if s != "" {
			cell["lexical"] = s
		}
		return cell
	}
	return map[string]any{"state": "value", "lexical": jsonLexical(raw)}
}
func inferJSONType(raw any) string {
	switch v := raw.(type) {
	case json.Number:
		if strings.ContainsAny(string(v), ".eE") {
			return "DOUBLE"
		}
		return "INT"
	case bool:
		return "BOOLEAN"
	default:
		return "STRING"
	}
}
func jsonLexical(v any) string {
	switch x := v.(type) {
	case json.Number:
		return string(x)
	case bool:
		return strconv.FormatBool(x)
	default:
		return fmt.Sprint(x)
	}
}
func cellState(lexical string, present bool) string {
	if !present {
		return "missing"
	}
	if lexical == "" {
		return "empty"
	}
	return "value"
}
func children(n *xmlNode, name string) []*xmlNode {
	var out []*xmlNode
	for _, child := range n.Children {
		if child.Name.Local == name {
			out = append(out, child)
		}
	}
	return out
}
func xmlAttr(n *xmlNode, name string) string {
	for _, attr := range n.Attr {
		if attr.Name.Local == name {
			return attr.Value
		}
	}
	return ""
}
func hasXMLAttr(n *xmlNode, name string) bool {
	for _, attr := range n.Attr {
		if attr.Name.Local == name {
			return true
		}
	}
	return false
}
func firstArray(root map[string]any, names ...string) []any {
	for _, name := range names {
		if value, ok := root[name]; ok {
			return array(value)
		}
	}
	return nil
}
func array(value any) []any {
	values, _ := value.([]any)
	return values
}
func rawString(value any) string {
	s, _ := value.(string)
	return s
}

func appendSlice(m map[string]any, key string, value any) {
	current, _ := m[key].([]any)
	m[key] = append(current, value)
}
func validateXMLStructure(root *xmlNode) error {
	var datasets []*xmlNode
	for _, child := range root.Children {
		if child.Name.Local == "Datasets" {
			return fmt.Errorf("Datasets wrapper is not allowed")
		}
		if child.Name.Local == "Dataset" {
			datasets = append(datasets, child)
		}
	}
	for _, dataset := range datasets {
		columnInfoSeen := false
		rowsSeen := false
		hasRows := false
		for _, child := range dataset.Children {
			switch child.Name.Local {
			case "ColumnInfo":
				if rowsSeen {
					return fmt.Errorf("ColumnInfo must precede Rows")
				}
				columnInfoSeen = true
			case "Rows":
				rowsSeen = true
				for _, row := range child.Children {
					if row.Name.Local != "Row" {
						if row.Name.Local == "Col" || row.Name.Local == "OrgRow" {
							return fmt.Errorf("Rows may contain only Row")
						}
						continue
					}
					hasRows = true
					if err := validateXMLRow(row); err != nil {
						return err
					}
				}
			case "Row", "Col", "OrgRow":
				return fmt.Errorf("row element is outside Rows")
			}
		}
		if hasRows && !columnInfoSeen {
			return fmt.Errorf("Rows requires ColumnInfo")
		}
	}
	return nil
}

func validateXMLRow(row *xmlNode) error {
	for _, child := range row.Children {
		switch child.Name.Local {
		case "Col":
		case "OrgRow":
			for _, orgChild := range child.Children {
				if orgChild.Name.Local == "Col" {
					continue
				}
			}
		}
	}
	return nil
}

func validateCanonicalColumns(value map[string]any) error {
	for _, rawDataset := range array(value["datasets"]) {
		dataset := rawDataset.(map[string]any)
		known := map[string]bool{}
		for _, rawColumn := range array(dataset["columns"]) {
			known[rawString(rawColumn.(map[string]any)["id"])] = true
		}
		for _, rawRow := range array(dataset["rows"]) {
			row := rawRow.(map[string]any)
			for id := range mapValue(row["values"]) {
				if !known[id] {
					return fmt.Errorf("unknown XML column %q", id)
				}
			}
			if org, ok := row["orgRow"].(map[string]any); ok && org != nil {
				for id := range mapValue(org["values"]) {
					if !known[id] {
						return fmt.Errorf("unknown XML column %q", id)
					}
				}
			}
		}
	}
	return nil
}
