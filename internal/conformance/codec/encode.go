package codec

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Clickin/xapi-go/internal/conformance/protocol"
)

func validateValueTypes(value protocol.Value, allowBinaryTypes bool) error {
	if value.SaveType == 5 {
		return fmt.Errorf("saveType 5 is not supported")
	}
	if value.SaveType < 0 || value.SaveType > 5 {
		return fmt.Errorf("invalid root saveType %d", value.SaveType)
	}
	for _, parameter := range value.Parameters {
		if allowBinaryTypes && isInternalType(parameter.Type) || !allowBinaryTypes && (strings.EqualFold(parameter.Type, "DATASET") || strings.EqualFold(parameter.Type, "INVALID")) {
			return fmt.Errorf("unsupported XAPI type %q", parameter.Type)
		}
		if !isKnownType(defaultType(parameter.Type)) {
			return fmt.Errorf("unsupported parameter type %q", parameter.Type)
		}
	}
	for _, dataset := range value.Datasets {
		if dataset.SaveType < 0 || dataset.SaveType > 5 {
			return fmt.Errorf("invalid Dataset %q saveType %d", dataset.ID, dataset.SaveType)
		}
		for _, column := range dataset.Columns {
			if allowBinaryTypes && isInternalType(column.Type) || !allowBinaryTypes && (strings.EqualFold(column.Type, "DATASET") || strings.EqualFold(column.Type, "INVALID")) {
				return fmt.Errorf("unsupported XAPI type %q", column.Type)
			}
			if !isKnownType(defaultType(column.Type)) {
				return fmt.Errorf("unsupported column type %q", column.Type)
			}
		}
		for _, column := range dataset.ConstColumns {
			if allowBinaryTypes && isInternalType(column.Type) || !allowBinaryTypes && (strings.EqualFold(column.Type, "DATASET") || strings.EqualFold(column.Type, "INVALID")) {
				return fmt.Errorf("unsupported XAPI type %q", column.Type)
			}
			if !isKnownType(defaultType(column.Type)) {
				return fmt.Errorf("unsupported constant column type %q", column.Type)
			}
		}
	}
	return nil
}

func isInternalType(dataType string) bool {
	switch strings.ToUpper(dataType) {
	case "UNDEFINED", "NULL", "DATASET", "INVALID":
		return true
	default:
		return false
	}
}
func applySaveTypes(value protocol.Value) protocol.Value {
	needsFiltering := value.SaveType != 0
	if !needsFiltering {
		for _, dataset := range value.Datasets {
			if dataset.SaveType != 0 {
				needsFiltering = true
				break
			}
		}
	}
	if !needsFiltering {
		return value
	}

	datasets := make([]protocol.Dataset, len(value.Datasets))
	copy(datasets, value.Datasets)
	value.Datasets = datasets
	for i := range value.Datasets {
		saveType := value.Datasets[i].SaveType
		if saveType == 0 {
			saveType = value.SaveType
		}
		if saveType == 0 || saveType == 1 {
			continue
		}
		rows := make([]protocol.Row, 0, len(value.Datasets[i].Rows))
		for _, row := range value.Datasets[i].Rows {
			rowType := strings.ToUpper(row.Type)
			include := saveType == 2 && rowType != "D" ||
				saveType == 3 && (rowType == "I" || rowType == "U") ||
				saveType == 4 && rowType == "D" ||
				saveType == 5 && rowType != "N" && rowType != ""
			if !include {
				continue
			}
			if saveType == 2 {
				row.Type = "N"
				row.OrgRow = nil
			}
			rows = append(rows, row)
		}
		value.Datasets[i].Rows = rows
	}
	return value
}

func Encode(v protocol.Value, profile string) ([]byte, error) {
	allowBinaryTypes := profile == nexacroBinaryProfile || profile == xplatformBinaryProfile
	if err := validateValueTypes(v, allowBinaryTypes); err != nil {
		return nil, err
	}
	if err := validateBlobLexicals(v); err != nil {
		return nil, err
	}
	v = applyScalarCompatibility(v)
	v = applySaveTypes(v)
	switch profile {
	case "nexacro-json-1.0":
		return json.Marshal(toJSON(v))
	case "xplatform-xml-4000", "xplatform-xml-5000", "nexacro-xml-4000":
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

func jsonSize(size, dataType string) string {
	if size != "" {
		return size
	}
	return jsonDefaultSize(dataType)
}
func jsonDefaultSize(dataType string) string {
	switch strings.ToUpper(dataType) {
	case "STRING", "CHAR":
		return "32"
	case "SHORT", "USHORT", "INT", "UINT":
		return "4"
	case "BOOLEAN":
		return "2"
	case "FLOAT":
		return "4"
	case "DOUBLE":
		return "8"
	case "LONG", "ULONG", "DECIMAL", "BIGDECIMAL":
		return "16"
	case "DATE":
		return "6"
	case "TIME":
		return "9"
	case "DATETIME":
		return "17"
	case "BLOB", "FILE":
		return "256"
	default:
		return "32"
	}
}

type jsonColumn struct {
	Size    string `json:"size,omitempty"`
	ID      string `json:"id"`
	Type    string `json:"type"`
	Value   any    `json:"value,omitempty"`
	Prop    string `json:"prop,omitempty"`
	SumText string `json:"sumtext,omitempty"`
}

func (c jsonColumn) MarshalJSON() ([]byte, error) {
	fields := jsonObject{{key: "size", value: c.Size}, {key: "id", value: c.ID}, {key: "type", value: c.Type}}
	if c.Value != nil {
		fields = append(fields, jsonField{key: "value", value: c.Value})
	}
	if c.Prop != "" {
		fields = append(fields, jsonField{key: "prop", value: c.Prop})
	}
	if c.SumText != "" {
		fields = append(fields, jsonField{key: "sumtext", value: c.SumText})
	}
	return fields.MarshalJSON()
}

type jsonField struct {
	key   string
	value any
}

type jsonObject []jsonField

func (o jsonObject) MarshalJSON() ([]byte, error) {
	var out strings.Builder
	out.WriteByte('{')
	for i, field := range o {
		if i > 0 {
			out.WriteByte(',')
		}
		key, err := json.Marshal(field.key)
		if err != nil {
			return nil, err
		}
		value, err := json.Marshal(field.value)
		if err != nil {
			return nil, err
		}
		out.Write(key)
		out.WriteByte(':')
		out.WriteString(strings.ReplaceAll(string(value), "/", `\/`))
	}
	out.WriteByte('}')
	return []byte(out.String()), nil
}

func javaHashBucket(key string) int {
	hash := 0
	for _, r := range key {
		hash = 31*hash + int(r)
	}
	return (hash ^ (hash >> 16)) & 15
}

func orderedJSONRow(rowType string, values map[string]any, columns []protocol.Column) jsonObject {
	fields := make([]jsonField, 0, len(values)+1)
	for _, column := range columns {
		if value, ok := values[column.ID]; ok {
			fields = append(fields, jsonField{key: column.ID, value: value})
		}
	}
	fields = append(fields, jsonField{key: "_RowType_", value: rowType})
	sort.SliceStable(fields, func(i, j int) bool {
		return javaHashBucket(fields[i].key) < javaHashBucket(fields[j].key)
	})
	return jsonObject(fields)
}

func jsonRowCell(dataType string, cell protocol.Cell) (any, bool) {
	if cell.State == "value" {
		return jsonScalar(dataType, cell), true
	}
	if cell.State == "empty" && (strings.EqualFold(dataType, "STRING") || strings.EqualFold(dataType, "CHAR")) {
		return "", true
	}
	return nil, false
}

func toJSON(v protocol.Value) map[string]any {
	root := map[string]any{"version": "1.0"}
	if v.Wire != nil {
		if s, ok := v.Wire["version"].(string); ok {
			root["version"] = s
		}
	}
	params := make([]any, 0, len(v.Parameters))
	for _, p := range v.Parameters {
		fields := jsonObject{{key: "id", value: p.ID}, {key: "type", value: wireType(p.Type)}}
		if p.State != "missing" {
			if p.State == "null" {
				fields = append(fields, jsonField{key: "value", value: nil})
			} else {
				fields = append(fields, jsonField{key: "value", value: jsonScalar(p.Type, protocol.Cell{State: p.State, Lexical: p.Lexical})})
			}
		}
		params = append(params, fields)
	}
	if len(params) > 0 {
		root["Parameters"] = params
	}
	datasets := make([]any, 0, len(v.Datasets))
	for _, d := range v.Datasets {
		cols := make([]any, 0, len(d.Columns))
		for _, c := range d.Columns {
			column := jsonColumn{Size: jsonSize(c.Size, c.Type), ID: c.ID, Type: wireType(c.Type), Prop: c.Prop, SumText: c.SumText}
			cols = append(cols, column)
		}
		consts := make([]any, 0, len(d.ConstColumns))
		for _, c := range d.ConstColumns {
			column := jsonColumn{Size: jsonSize(c.Size, c.Type), ID: c.ID, Type: wireType(c.Type)}
			if c.Value.State != "missing" {
				column.Value = jsonScalar(c.Type, c.Value)
			}
			consts = append(consts, column)
		}
		columnTypes := make(map[string]string, len(d.Columns))
		for _, column := range d.Columns {
			columnTypes[column.ID] = column.Type
		}
		rows := []any{}
		for ri, r := range d.Rows {
			if r.Type == "O" && ri > 0 && d.Rows[ri-1].OrgRow != nil && sameRow(*d.Rows[ri-1].OrgRow, r) {
				continue
			}
			rowType := r.Type
			if len(d.Columns) == 0 && rowType == "U" {
				rowType = "N"
			}
			values := map[string]any{}
			for id, c := range r.Values {
				if value, include := jsonRowCell(columnTypes[id], c); include {
					values[id] = value
				}
			}
			if r.Type == "I" && len(values) == 0 {
				rowType = "N"
				for _, column := range d.Columns {
					if strings.EqualFold(column.Type, "BOOLEAN") {
						values[column.ID] = "0"
						break
					}
				}
			}
			rows = append(rows, orderedJSONRow(rowType, values, d.Columns))
			if r.OrgRow != nil && len(d.Columns) > 0 {
				originalValues := map[string]any{}
				for id, c := range r.OrgRow.Values {
					if value, include := jsonRowCell(columnTypes[id], c); include {
						originalValues[id] = value
					}
				}
				rows = append(rows, orderedJSONRow("O", originalValues, d.Columns))
			}
		}
		columnInfo := jsonObject{}
		if len(consts) > 0 {
			columnInfo = append(columnInfo, jsonField{key: "ConstColumn", value: consts})
		}
		if len(cols) > 0 {
			columnInfo = append(columnInfo, jsonField{key: "Column", value: cols})
		}
		dataset := jsonObject{{key: "ColumnInfo", value: columnInfo}, {key: "id", value: d.ID}}
		if len(consts) == 0 && len(cols) == 0 {
			dataset = jsonObject{{key: "id", value: d.ID}}
		}
		if len(rows) > 0 {
			dataset = append(dataset, jsonField{key: "Rows", value: rows})
		}
		datasets = append(datasets, dataset)
	}
	if len(datasets) > 0 {
		root["Datasets"] = datasets
	}
	return root

}
func encodeXML(value protocol.Value, profile string) ([]byte, error) {
	namespace := xmlNamespace(profile)
	versionName, version := "ver", "4000"
	if profile == "xplatform-xml-4000" || profile == "xplatform-xml-5000" {
		version = "5000"
	}
	if root, ok := value.Wire["root"].(map[string]any); ok {
		if configured, ok := root["namespace"].(string); ok && configured != "" {
			namespace = configured
		}
		if configured, ok := root["version"].(string); ok {
			versionName, version = "version", configured
		}
		if configured, ok := root["ver"].(string); ok {
			versionName, version = "ver", configured
		}
	}
	if profile == "xplatform-xml-4000" || profile == "xplatform-xml-5000" {
		versionName, version = "ver", "5000"
		namespace = xmlNamespace(profile)
	}

	var out strings.Builder
	out.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	out.WriteString(`<Root xmlns="`)
	escaped, err := escapeXMLScalar(namespace)
	if err != nil {
		return nil, err
	}
	out.WriteString(escaped)
	if profile != "nexacro-xml-4000" {
		out.WriteString(`" `)
		out.WriteString(versionName)
		out.WriteString(`="`)
		escaped, err = escapeXMLScalar(version)
		if err != nil {
			return nil, err
		}
		out.WriteString(escaped)
	}
	out.WriteString(`">`)
	if len(value.Parameters) == 0 && len(value.Datasets) == 0 {
		out.WriteString("\n\t<Parameters/>\n</Root>\n")
		return []byte(out.String()), nil
	}

	if len(value.Parameters) == 0 {
		out.WriteString("\n\t<Parameters/>")
	} else {
		out.WriteString("\n\t<Parameters>")
		for _, parameter := range value.Parameters {
			out.WriteString("\n\t\t")
			if err := writeXMLParameter(&out, parameter); err != nil {
				return nil, err
			}
		}
		out.WriteString("\n\t</Parameters>")
	}
	for _, dataset := range value.Datasets {
		out.WriteString("\n\t")
		if err := writeXMLDataset(&out, dataset, profile); err != nil {
			return nil, err
		}
	}
	out.WriteString("\n</Root>\n")
	result := out.String()
	if profile == "nexacro-xml-4000" {
		result = strings.NewReplacer("&#9;", "\t", "&#10;", "\n", "&#13;", "\r").Replace(result)
	}
	return []byte(result), nil
}

func writeXMLParameter(out *strings.Builder, parameter protocol.Parameter) error {
	id, err := escapeXMLScalar(parameter.ID)
	if err != nil {
		return err
	}
	dataType, err := escapeXMLScalar(wireType(parameter.Type))
	if err != nil {
		return err
	}
	out.WriteString(`<Parameter id="`)
	out.WriteString(id)
	out.WriteString(`" type="`)
	out.WriteString(dataType)
	out.WriteByte('"')
	if form, ok := parameter.Wire["valueForm"].(string); ok && form == "attribute" && parameter.State != "missing" && parameter.State != "null" {
		value, err := escapeXMLScalar(parameter.Lexical)
		if err != nil {
			return err
		}
		out.WriteString(` value="`)
		out.WriteString(value)
		out.WriteString(`"/>`)
		return nil
	}
	if parameter.State == "missing" || parameter.State == "null" {
		out.WriteString("/>")
		return nil
	}
	out.WriteByte('>')
	value, err := escapeXMLScalar(parameter.Lexical)
	if err != nil {
		return err
	}
	out.WriteString(value)
	out.WriteString("</Parameter>")
	return nil
}
func writeXMLDataset(out *strings.Builder, dataset protocol.Dataset, profile string) error {
	id, err := escapeXMLScalar(dataset.ID)
	if err != nil {
		return err
	}
	out.WriteString(`<Dataset id="`)
	out.WriteString(id)
	out.WriteString(`">`)
	out.WriteString("\n\t\t<ColumnInfo>")
	if profile == "xplatform-xml-4000" || profile == "xplatform-xml-5000" {
		for _, column := range dataset.Columns {
			out.WriteString("\n\t\t\t")
			if err := writeXMLColumn(out, column); err != nil {
				return err
			}
		}
		for _, column := range dataset.ConstColumns {
			out.WriteString("\n\t\t\t")
			if err := writeXMLConstColumn(out, column); err != nil {
				return err
			}
		}
	} else {
		for _, column := range dataset.ConstColumns {
			out.WriteString("\n\t\t\t")
			if err := writeXMLConstColumn(out, column); err != nil {
				return err
			}
		}
		for _, column := range dataset.Columns {
			out.WriteString("\n\t\t\t")
			if err := writeXMLColumn(out, column); err != nil {
				return err
			}
		}
	}
	out.WriteString("\n\t\t</ColumnInfo>")
	out.WriteString("\n\t\t<Rows>")
	for rowIndex, row := range dataset.Rows {
		if row.Type == "O" && rowIndex > 0 && dataset.Rows[rowIndex-1].OrgRow != nil && sameRow(*dataset.Rows[rowIndex-1].OrgRow, row) {
			continue
		}
		if row.Type == "O" {
			continue
		}
		out.WriteString("\n\t\t\t")
		if err := writeXMLRow(out, row, dataset.Columns); err != nil {
			return err
		}
	}
	out.WriteString("\n\t\t</Rows>")
	out.WriteString("\n\t</Dataset>")
	return nil
}

func writeXMLConstColumn(out *strings.Builder, column protocol.ConstColumn) error {
	id, err := escapeXMLScalar(column.ID)
	if err != nil {
		return err
	}
	dataType, err := escapeXMLScalar(wireType(column.Type))
	if err != nil {
		return err
	}
	out.WriteString(`<ConstColumn id="`)
	out.WriteString(id)
	out.WriteString(`" type="`)
	out.WriteString(dataType)
	if column.Size == "" {
		if strings.EqualFold(dataType, "BLOB") {
			column.Size = "256"
		} else {
			column.Size = "32"
		}
	}
	out.WriteByte('"')
	if column.Size != "" {
		size, err := escapeXMLScalar(column.Size)
		if err != nil {
			return err
		}
		out.WriteString(` size="`)
		out.WriteString(size)
		out.WriteByte('"')
	}
	encoding := column.Encoding
	if strings.EqualFold(dataType, "BLOB") && encoding == "" {
		encoding = "base64"
	}
	if strings.EqualFold(dataType, "BLOB") && !strings.EqualFold(encoding, "base64") {
		return fmt.Errorf("BLOB ConstColumn requires base64 encoding")
	}
	if encoding != "" {
		escapedEncoding, err := escapeXMLScalar(strings.ToLower(encoding))
		if err != nil {
			return err
		}
		out.WriteString(` encrypt="`)
		out.WriteString(escapedEncoding)
		out.WriteByte('"')
	}
	if column.Value.State != "missing" && column.Value.State != "null" {
		value, err := escapeXMLScalar(column.Value.Lexical)
		if err != nil {
			return err
		}
		out.WriteString(` value="`)
		out.WriteString(value)
		out.WriteByte('"')
	}
	if column.Value.State != "missing" && column.Value.State != "null" && strings.EqualFold(dataType, "BLOB") {
		out.WriteString(`></ConstColumn>`)
	} else {
		out.WriteString("/>")
	}
	return nil
}

func writeXMLColumn(out *strings.Builder, column protocol.Column) error {
	id, err := escapeXMLScalar(column.ID)
	if err != nil {
		return err
	}
	dataType, err := escapeXMLScalar(wireType(column.Type))
	if err != nil {
		return err
	}
	out.WriteString(`<Column id="`)
	out.WriteString(id)
	out.WriteString(`" type="`)
	out.WriteString(dataType)
	out.WriteByte('"')
	encoding := column.Encoding
	if strings.EqualFold(dataType, "BLOB") && encoding == "" {
		encoding = "base64"
	}
	if column.Size == "" {
		if strings.EqualFold(dataType, "BLOB") {
			column.Size = "256"
		} else {
			column.Size = "32"
		}
	}
	if strings.EqualFold(dataType, "BLOB") && !strings.EqualFold(encoding, "base64") {
		return fmt.Errorf("BLOB Column requires base64 encoding")
	}
	attributes := [][2]string{{"size", column.Size}, {"encrypt", strings.ToLower(encoding)}, {"prop", column.Prop}, {"sumtext", column.SumText}}
	for _, attribute := range attributes {
		if attribute[1] == "" {
			continue
		}
		value, err := escapeXMLScalar(attribute[1])
		if err != nil {
			return err
		}
		out.WriteByte(' ')
		out.WriteString(attribute[0])
		out.WriteString(`="`)
		out.WriteString(value)
		out.WriteByte('"')
	}
	out.WriteString("/>")
	return nil
}

func writeXMLRow(out *strings.Builder, row protocol.Row, columns []protocol.Column) error {
	rowType := map[string]string{"N": "", "I": "insert", "U": "update", "D": "delete"}[row.Type]
	if rowType == "" && row.Type != "N" {
		return fmt.Errorf("invalid XML row type %q", row.Type)
	}
	out.WriteString("<Row")
	if rowType != "" {
		out.WriteString(` type="`)
		out.WriteString(rowType)
		out.WriteByte('"')
	}
	out.WriteByte('>')
	out.WriteString("\n\t\t\t\t")
	if err := writeXMLCells(out, row.Values, columns); err != nil {
		return err
	}
	if row.OrgRow != nil {
		out.WriteString("\n\t\t\t\t<OrgRow>")
		if err := writeXMLCells(out, row.OrgRow.Values, columns); err != nil {
			return err
		}
		out.WriteString("</OrgRow>")
	}
	out.WriteString("\n\t\t\t</Row>")
	return nil
}

func writeXMLCells(out *strings.Builder, cells map[string]protocol.Cell, columns []protocol.Column) error {
	for _, column := range columns {
		cell, ok := cells[column.ID]
		if !ok || cell.State == "missing" || cell.State == "null" {
			continue
		}
		id, err := escapeXMLScalar(column.ID)
		if err != nil {
			return err
		}
		value, err := escapeXMLScalar(cell.Lexical)
		if err != nil {
			return err
		}
		out.WriteString(`<Col id="`)
		out.WriteString(id)
		out.WriteString(`">`)
		out.WriteString(value)
		out.WriteString("</Col>")
	}
	return nil
}

func escapeXMLScalar(value string) (string, error) {
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
		case '\t':
			out.WriteString("&#9;")
		case '\n':
			out.WriteString("&#10;")
		case '\r':
			out.WriteString("&#13;")
		default:
			if r < 0x20 {
				return "", fmt.Errorf("character U+%04X is not valid XML 1.0", r)
			}
			out.WriteRune(r)
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
