package codec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Clickin/xapi-go/internal/conformance/protocol"
)

func validateValueTypes(value protocol.Value) error {
	if value.SaveType < 0 || value.SaveType > 5 {
		return fmt.Errorf("invalid root saveType %d", value.SaveType)
	}
	for _, parameter := range value.Parameters {
		if !isKnownType(defaultType(parameter.Type)) {
			return fmt.Errorf("unsupported parameter type %q", parameter.Type)
		}
		if isPublicUnsupportedType(parameter.Type) {
			return fmt.Errorf("invalid public XAPI type %q", parameter.Type)
		}
	}
	for _, dataset := range value.Datasets {
		if dataset.SaveType < 0 || dataset.SaveType > 5 {
			return fmt.Errorf("invalid Dataset %q saveType %d", dataset.ID, dataset.SaveType)
		}
		for _, column := range dataset.Columns {
			if !isKnownType(defaultType(column.Type)) {
				return fmt.Errorf("unsupported column type %q", column.Type)
			}
			if isPublicUnsupportedType(column.Type) {
				return fmt.Errorf("invalid public XAPI type %q", column.Type)
			}
		}
		for _, column := range dataset.ConstColumns {
			if !isKnownType(defaultType(column.Type)) {
				return fmt.Errorf("unsupported constant column type %q", column.Type)
			}
			if isPublicUnsupportedType(column.Type) {
				return fmt.Errorf("invalid public XAPI type %q", column.Type)
			}
		}
	}
	return nil
}

func isPublicUnsupportedType(dataType string) bool {
	switch strings.ToUpper(defaultType(dataType)) {
	case "DATASET", "INVALID":
		return true
	default:
		return false
	}
}

// applySaveTypes reproduces the original drivers' row selection. A Dataset
// without saveType inherits the root's; with neither, the drivers use NORMAL
// (2), which writes every live row as a plain row. Deleted rows live in the
// driver's removed-row list, so they are always written after the live rows,
// and only for ALL (1), DELETED (4) and CHANGED (5). An updated row written
// with its saved row but without a known original gets an empty one.
func applySaveTypes(value protocol.Value) protocol.Value {
	datasets := make([]protocol.Dataset, len(value.Datasets))
	copy(datasets, value.Datasets)
	value.Datasets = datasets
	for i := range value.Datasets {
		saveType := value.Datasets[i].SaveType
		if saveType == 0 {
			saveType = value.SaveType
		}
		if saveType == 0 {
			saveType = 2
		}
		var live, removed []protocol.Row
		for _, row := range value.Datasets[i].Rows {
			rowType := strings.ToUpper(row.Type)
			if rowType == "" {
				rowType = "N"
			}
			row.Type = rowType
			switch rowType {
			case "O":
				continue
			case "D":
				if saveType == 1 || saveType == 4 || saveType == 5 {
					row.OrgRow = nil
					removed = append(removed, row)
				}
				continue
			}
			include := saveType == 1 || saveType == 2 ||
				saveType == 3 && rowType != "N" ||
				saveType == 5 && rowType != "N"
			if !include {
				continue
			}
			// An updated row starts as a copy of its original row, so a
			// value the current row leaves out keeps the original value.
			if rowType == "U" && row.OrgRow != nil {
				values := make(map[string]protocol.Cell, len(row.Values))
				for id, cell := range row.OrgRow.Values {
					values[id] = cell
				}
				for id, cell := range row.Values {
					values[id] = cell
				}
				row.Values = values
			}
			switch {
			case saveType == 2:
				row.Type = "N"
				row.OrgRow = nil
			case rowType == "U" && row.OrgRow == nil:
				row.OrgRow = &protocol.Row{Type: "O", Values: map[string]protocol.Cell{}}
			case rowType != "U":
				row.OrgRow = nil
			}
			live = append(live, row)
		}
		value.Datasets[i].Rows = append(live, removed...)
		value.Datasets[i].SaveType = 0
	}
	value.SaveType = 0
	return value
}

func Encode(v protocol.Value, profile string) ([]byte, error) {
	if err := validateValueTypes(v); err != nil {
		return nil, err
	}
	if err := validateBlobLexicals(v); err != nil {
		return nil, err
	}
	v = applyScalarCompatibility(v)
	v = applySaveTypes(v)
	switch profile {
	case "nexacro-json-1.0":
		return marshalJSONValue(toJSON(v))
	case "xplatform-xml-5000", "nexacro-xml-4000":
		return encodeXML(v, profile)
	case nexacroSSVProfile, xplatformSSVProfile:
		return encodeSSV(v, profile)
	case nexacroBinaryProfile, xplatformBinaryProfile:
		return encodeBinary(v)
	default:
		return nil, fmt.Errorf("unsupported profile %q", profile)
	}
}

func wireType(dataType string) string {
	switch strings.ToUpper(dataType) {
	case "BOOLEAN":
		return "int"
	case "LONG", "ULONG", "DECIMAL", "BIGDECIMAL":
		return "bigdecimal"
	case "DOUBLE":
		return "float"
	case "FILE", "BLOB":
		return "blob"
	case "STRING", "CHAR":
		return "string"
	case "SHORT", "USHORT", "INT", "UINT":
		return "int"
	case "FLOAT":
		return "float"
	case "DATE":
		return "date"
	case "TIME":
		return "time"
	case "DATETIME":
		return "datetime"
	case "NULL":
		return "null"
	default:
		return strings.ToLower(dataType)
	}
}

type jsonField struct {
	key   string
	value any
}

type jsonObject []jsonField

func orderedJSON(fields ...jsonField) jsonObject {
	capacity := 16
	for len(fields) > capacity*3/4 {
		capacity *= 2
	}
	sort.SliceStable(fields, func(i, j int) bool {
		return javaJSONBucket(fields[i].key, capacity) < javaJSONBucket(fields[j].key, capacity)
	})
	return fields
}

func javaJSONBucket(key string, capacity int) uint32 {
	var hash uint32
	for _, r := range key {
		if r <= 0xffff {
			hash = 31*hash + uint32(r)
			continue
		}
		r -= 0x10000
		hash = 31*hash + uint32(0xd800+(r>>10))
		hash = 31*hash + uint32(0xdc00+(r&0x3ff))
	}
	hash ^= hash >> 16
	return hash & uint32(capacity-1)
}

func (object jsonObject) MarshalJSON() ([]byte, error) {
	var out strings.Builder
	out.WriteByte('{')
	for i, field := range object {
		if i > 0 {
			out.WriteByte(',')
		}
		key, err := marshalJSONString(field.key)
		if err != nil {
			return nil, err
		}
		value, err := marshalJSONValue(field.value)
		if err != nil {
			return nil, err
		}
		out.Write(key)
		out.WriteByte(':')
		out.Write(value)
	}
	out.WriteByte('}')
	return []byte(out.String()), nil
}

// marshalJSONValue never HTML-escapes: json.Marshal would rewrite '<', '>'
// and '&' inside jsonObject output, which the driver writes as they are.
func marshalJSONValue(value any) ([]byte, error) {
	if text, ok := value.(string); ok {
		return marshalJSONString(text)
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n")), nil
}

// marshalJSONString escapes like json-simple's JSONValue.escape, which the
// Nexacro driver uses: '/' is escaped, U+0000-U+001F, U+007F-U+009F and
// U+2000-U+20FF become \uXXXX with upper-case hex, and nothing else is.
func marshalJSONString(value string) ([]byte, error) {
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		case '/':
			out.WriteString(`\/`)
		default:
			if r <= 0x1f || r >= 0x7f && r <= 0x9f || r >= 0x2000 && r <= 0x20ff {
				fmt.Fprintf(&out, `\u%04X`, r)
			} else {
				out.WriteRune(r)
			}
		}
	}
	out.WriteByte('"')
	return []byte(out.String()), nil
}

func defaultWireSize(dataType string) string {
	switch strings.ToUpper(defaultType(dataType)) {
	case "STRING", "CHAR":
		return "32"
	case "SHORT", "USHORT", "INT", "UINT":
		return "4"
	case "LONG", "ULONG":
		return "8"
	case "DECIMAL", "BIGDECIMAL":
		return "16"
	case "FLOAT":
		return "4"
	case "DOUBLE":
		return "8"
	case "BOOLEAN":
		return "2"
	case "DATE":
		return "6"
	case "TIME":
		return "9"
	case "DATETIME":
		return "17"
	case "FILE", "BLOB":
		return "256"
	default:
		return "32"
	}
}

func toJSON(v protocol.Value) jsonObject {
	root := []jsonField{{key: "version", value: "1.0"}}
	if v.Wire != nil {
		if version, ok := v.Wire["version"].(string); ok {
			root[0].value = version
		}
	}

	parameters := make([]any, 0, len(v.Parameters))
	for _, parameter := range v.Parameters {
		fields := []jsonField{
			{key: "id", value: parameter.ID},
			{key: "type", value: wireType(parameter.Type)},
		}
		if scalar, ok := jsonScalar(parameter.Type, protocol.Cell{State: parameter.State, Lexical: parameter.Lexical}); ok {
			fields = append(fields, jsonField{key: "value", value: scalar})
		}
		parameters = append(parameters, orderedJSON(fields...))
	}
	if len(parameters) > 0 {
		root = append(root, jsonField{key: "Parameters", value: parameters})
	}

	datasets := make([]any, 0, len(v.Datasets))
	for _, dataset := range v.Datasets {
		columns := make([]any, 0, len(dataset.Columns))
		for _, column := range dataset.Columns {
			size := column.Size
			if size == "" {
				size = defaultWireSize(column.Type)
			}
			columns = append(columns, orderedJSON(
				jsonField{key: "id", value: column.ID},
				jsonField{key: "type", value: wireType(column.Type)},
				jsonField{key: "size", value: size},
			))
		}

		constants := make([]any, 0, len(dataset.ConstColumns))
		for _, column := range dataset.ConstColumns {
			size := column.Size
			if size == "" {
				size = defaultWireSize(column.Type)
			}
			fields := []jsonField{
				{key: "id", value: column.ID},
				{key: "type", value: wireType(column.Type)},
				{key: "size", value: size},
			}
			if column.Value.State == "value" || column.Value.State == "empty" {
				fields = append(fields, jsonField{key: "value", value: column.Value.Lexical})
			} else if strings.EqualFold(defaultType(column.Type), "BOOLEAN") {
				fields = append(fields, jsonField{key: "value", value: "0"})
			}
			constants = append(constants, orderedJSON(fields...))
		}

		rows := make([]any, 0, len(dataset.Rows))
		jsonRow := func(rowType string, values map[string]protocol.Cell, saved bool) jsonObject {
			fields := []jsonField{{key: "_RowType_", value: rowType}}
			for _, column := range dataset.Columns {
				cell, ok := values[column.ID]
				if !ok {
					if saved {
						continue
					}
					cell.State = "missing"
				}
				if scalar, include := jsonScalar(column.Type, cell); include {
					fields = append(fields, jsonField{key: column.ID, value: scalar})
				}
			}
			return orderedJSON(fields...)
		}
		for _, row := range dataset.Rows {
			rows = append(rows, jsonRow(row.Type, row.Values, false))
			if row.OrgRow != nil {
				rows = append(rows, jsonRow("O", row.OrgRow.Values, true))
			}
		}

		fields := []jsonField{{key: "id", value: dataset.ID}}
		if len(constants) > 0 || len(columns) > 0 {
			columnInfo := make([]jsonField, 0, 2)
			if len(constants) > 0 {
				columnInfo = append(columnInfo, jsonField{key: "ConstColumn", value: constants})
			}
			if len(columns) > 0 {
				columnInfo = append(columnInfo, jsonField{key: "Column", value: columns})
			}
			fields = append(fields, jsonField{key: "ColumnInfo", value: orderedJSON(columnInfo...)})
		}
		if len(rows) > 0 {
			fields = append(fields, jsonField{key: "Rows", value: rows})
		}
		datasets = append(datasets, orderedJSON(fields...))
	}
	if len(datasets) > 0 {
		root = append(root, jsonField{key: "Datasets", value: datasets})
	}
	return orderedJSON(root...)
}

func encodeXML(value protocol.Value, profile string) ([]byte, error) {
	var out strings.Builder
	namespace := "http://www.nexacroplatform.com/platform/dataset"
	if profile == "xplatform-xml-5000" {
		namespace = "http://www.tobesoft.com/platform/dataset"
	}
	out.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	out.WriteByte('\n')
	out.WriteString(`<Root xmlns="`)
	out.WriteString(namespace)
	out.WriteByte('"')
	if profile == "xplatform-xml-5000" {
		out.WriteString(` ver="5000"`)
	}
	out.WriteString(">\n")

	if len(value.Parameters) == 0 {
		out.WriteString("\t<Parameters/>\n")
	} else {
		out.WriteString("\t<Parameters>\n")
		for _, parameter := range value.Parameters {
			if err := writeXMLParameter(&out, parameter, profile == "xplatform-xml-5000"); err != nil {
				return nil, err
			}
		}
		out.WriteString("\t</Parameters>\n")
	}
	for _, dataset := range value.Datasets {
		if err := writeXMLDataset(&out, dataset, profile); err != nil {
			return nil, err
		}
	}
	out.WriteString("</Root>\n")
	return []byte(out.String()), nil
}

func writeXMLParameter(out *strings.Builder, parameter protocol.Parameter, xp bool) error {
	id, err := escapeXML(parameter.ID, xp)
	if err != nil {
		return err
	}
	dataType, err := escapeXML(wireType(parameter.Type), xp)
	if err != nil {
		return err
	}
	scalar, hasValue := jsonScalar(parameter.Type, protocol.Cell{State: parameter.State, Lexical: parameter.Lexical})
	out.WriteString(`		<Parameter id="`)
	out.WriteString(id)
	out.WriteString(`" type="`)
	out.WriteString(dataType)
	out.WriteByte('"')
	if strings.EqualFold(defaultType(parameter.Type), "BLOB") && hasValue {
		out.WriteString(` encrypt="base64"`)
	}
	if !hasValue {
		out.WriteString("/>\n")
		return nil
	}
	out.WriteByte('>')
	escaped, err := escapeXML(scalar, xp)
	if err != nil {
		return err
	}
	out.WriteString(escaped)
	out.WriteString("</Parameter>\n")
	return nil
}

func columnSize(size, dataType string) string {
	if size != "" {
		return size
	}
	return defaultWireSize(dataType)
}

func writeXMLDataset(out *strings.Builder, dataset protocol.Dataset, profile string) error {
	xp := profile == "xplatform-xml-5000"
	id, err := escapeXML(dataset.ID, xp)
	if err != nil {
		return err
	}
	out.WriteString(`	<Dataset id="`)
	out.WriteString(id)
	out.WriteString(`">` + "\n")
	out.WriteString("\t\t<ColumnInfo>\n")
	if profile == "nexacro-xml-4000" {
		for _, column := range dataset.ConstColumns {
			if err := writeXMLConstColumn(out, column, xp); err != nil {
				return err
			}
		}
		for _, column := range dataset.Columns {
			if err := writeXMLColumn(out, column, xp); err != nil {
				return err
			}
		}
	} else {
		for _, column := range dataset.Columns {
			if err := writeXMLColumn(out, column, xp); err != nil {
				return err
			}
		}
		for _, column := range dataset.ConstColumns {
			if err := writeXMLConstColumn(out, column, xp); err != nil {
				return err
			}
		}
	}
	out.WriteString("\t\t</ColumnInfo>\n")
	out.WriteString("\t\t<Rows>\n")
	for _, row := range dataset.Rows {
		if strings.EqualFold(row.Type, "O") {
			continue
		}
		if err := writeXMLRow(out, row, dataset.Columns, profile == "xplatform-xml-5000"); err != nil {
			return err
		}
	}
	out.WriteString("\t\t</Rows>\n")
	out.WriteString("\t</Dataset>\n")
	return nil
}

func writeXMLConstColumn(out *strings.Builder, column protocol.ConstColumn, xp bool) error {
	id, err := escapeXML(column.ID, xp)
	if err != nil {
		return err
	}
	dataType, err := escapeXML(wireType(column.Type), xp)
	if err != nil {
		return err
	}
	size, err := escapeXML(columnSize(column.Size, column.Type), xp)
	if err != nil {
		return err
	}
	out.WriteString(`			<ConstColumn id="`)
	out.WriteString(id)
	out.WriteString(`" type="`)
	out.WriteString(dataType)
	out.WriteString(`" size="`)
	out.WriteString(size)
	out.WriteByte('"')
	if column.Value.State != "value" && column.Value.State != "empty" {
		out.WriteString("/>\n")
		return nil
	}
	if strings.EqualFold(defaultType(column.Type), "BLOB") {
		out.WriteString(` encrypt="base64"`)
	}
	value, err := escapeXML(column.Value.Lexical, xp)
	if err != nil {
		return err
	}
	out.WriteString(` value="`)
	out.WriteString(value)
	out.WriteByte('"')
	if strings.EqualFold(defaultType(column.Type), "BLOB") {
		out.WriteString("></ConstColumn>\n")
	} else {
		out.WriteString("/>\n")
	}
	return nil
}

func writeXMLColumn(out *strings.Builder, column protocol.Column, xp bool) error {
	id, err := escapeXML(column.ID, xp)
	if err != nil {
		return err
	}
	dataType, err := escapeXML(wireType(column.Type), xp)
	if err != nil {
		return err
	}
	size, err := escapeXML(columnSize(column.Size, column.Type), xp)
	if err != nil {
		return err
	}
	out.WriteString(`			<Column id="`)
	out.WriteString(id)
	out.WriteString(`" type="`)
	out.WriteString(dataType)
	out.WriteString(`" size="`)
	out.WriteString(size)
	out.WriteByte('"')
	if strings.EqualFold(defaultType(column.Type), "BLOB") {
		out.WriteString(` encrypt="base64"`)
	}
	out.WriteString("/>\n")
	return nil
}

func writeXMLRow(out *strings.Builder, row protocol.Row, columns []protocol.Column, xp bool) error {
	rowType := map[string]string{"N": "", "I": "insert", "U": "update", "D": "delete"}[strings.ToUpper(row.Type)]
	if rowType == "" && !strings.EqualFold(row.Type, "N") {
		return fmt.Errorf("invalid XML row type %q", row.Type)
	}
	out.WriteString("\t\t\t<Row")
	if rowType != "" {
		out.WriteString(` type="`)
		out.WriteString(rowType)
		out.WriteByte('"')
	}
	out.WriteString(">\n")
	if err := writeXMLCells(out, row.Values, columns, "\t\t\t\t", xp, false); err != nil {
		return err
	}
	if row.OrgRow != nil {
		out.WriteString("\t\t\t\t<OrgRow>\n")
		if err := writeXMLCells(out, row.OrgRow.Values, columns, "\t\t\t\t\t", xp, true); err != nil {
			return err
		}
		out.WriteString("\t\t\t\t</OrgRow>\n")
	}
	out.WriteString("\t\t\t</Row>\n")
	return nil
}

func writeXMLCells(out *strings.Builder, cells map[string]protocol.Cell, columns []protocol.Column, indent string, xp, saved bool) error {
	for _, column := range columns {
		cell, ok := cells[column.ID]
		if !ok || cell.State == "missing" || cell.State == "null" {
			// The drivers hold an unset BOOLEAN as false and write it as 0;
			// a saved row the driver never filled has no cells at all.
			if saved || !strings.EqualFold(defaultType(column.Type), "BOOLEAN") {
				continue
			}
			cell = protocol.Cell{State: "value", Lexical: "0"}
		}
		id, err := escapeXML(column.ID, xp)
		if err != nil {
			return err
		}
		value, err := escapeXML(cell.Lexical, xp)
		if err != nil {
			return err
		}
		out.WriteString(indent)
		out.WriteString(`<Col id="`)
		out.WriteString(id)
		out.WriteString(`">`)
		out.WriteString(value)
		out.WriteString("</Col>\n")
	}
	return nil
}

// escapeXML follows the drivers' XmlCodec. XPLATFORM writes every character
// below U+0020 as a decimal reference and a space as &#32;; Nexacro escapes
// only the five predefined entities and writes tab, LF and CR as they are.
// Nexacro would also write other control characters raw, which no XML parser
// (the driver's included) reads back, so they are rejected here.
func escapeXML(value string, xplatform bool) (string, error) {
	var out strings.Builder
	for _, r := range value {
		switch r {
		case '&':
			out.WriteString("&amp;")
		case '<':
			out.WriteString("&lt;")
		case '>':
			out.WriteString("&gt;")
		case '"':
			out.WriteString("&quot;")
		case '\'':
			out.WriteString("&apos;")
		case ' ':
			if xplatform {
				out.WriteString("&#32;")
			} else {
				out.WriteByte(' ')
			}
		default:
			switch {
			case r < 0x20 && xplatform:
				fmt.Fprintf(&out, "&#%d;", r)
			case r < 0x20 && r != '\t' && r != '\n' && r != '\r':
				return "", fmt.Errorf("character U+%04X is not valid XML 1.0", r)
			default:
				out.WriteRune(r)
			}
		}
	}
	return out.String(), nil
}

func sameRow(a, b protocol.Row) bool {
	if a.Type != b.Type || len(a.Values) != len(b.Values) {
		return false
	}
	for id, x := range a.Values {
		y, ok := b.Values[id]
		if !ok || x.State != y.State || x.Lexical != y.Lexical {
			return false
		}
	}
	return true
}
