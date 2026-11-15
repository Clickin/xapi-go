package codec

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Clickin/xapi-go/internal/conformance/protocol"
)

const (
	defaultSSVRecordSeparator = "\x1e"
	defaultSSVUnitSeparator   = "\x1f"
	nexacroSSVProfile         = "nexacro-ssv"
	xplatformSSVProfile       = "xplatform-ssv"
)

func decodeSSVText(b []byte) (string, error) {
	if utf8.Valid(b) {
		return string(b), nil
	}
	end := bytes.IndexByte(b, 0x1e)
	if end < 0 {
		return "", fmt.Errorf("SSV input is not valid UTF-8")
	}
	header := string(b[:end])
	codePage := strings.ToLower(strings.TrimPrefix(header, "SSV:"))
	if codePage != "iso-8859-1" && codePage != "latin1" && codePage != "windows-1252" {
		return "", fmt.Errorf("unsupported SSV code page %q", codePage)
	}
	runes := make([]rune, len(b))
	for i, value := range b {
		runes[i] = rune(value)
	}
	return string(runes), nil
}

func ssvValue(b []byte, profile string, strict bool, unitSeparator, recordSeparator string) (protocol.Value, error) {
	text, err := decodeSSVText(b)
	if err != nil {
		return protocol.Value{}, err
	}
	// Vendor setProperty ignores custom separators; always parse its defaults.
	unitSeparator = defaultSSVUnitSeparator
	recordSeparator = defaultSSVRecordSeparator

	records, tail := splitSSVRecords(text, recordSeparator)
	if tail != "" {
		records = append(records, tail)
	}
	if len(records) == 0 {
		return protocol.Value{}, fmt.Errorf("missing SSV header")
	}

	header := records[0]
	if strings.HasPrefix(header, "SSV:") && strings.TrimPrefix(header, "SSV:") == "" {
		return protocol.Value{}, fmt.Errorf("empty SSV code page")
	}

	out := protocol.Value{Parameters: []protocol.Parameter{}, Datasets: []protocol.Dataset{}}

	for i := 1; i < len(records); {
		record := records[i]
		if record == "" {
			i++
			continue
		}
		if strings.HasPrefix(record, "Dataset:") {
			dataset, next, err := parseSSVDataset(records, i, profile, strict, unitSeparator)
			if err != nil {
				return protocol.Value{}, err
			}
			out.Datasets = append(out.Datasets, dataset)
			i = next
			continue
		}
		variableSeparator := unitSeparator
		if variableSeparator == "" {
			variableSeparator = "\x1f"
		}
		for _, variableRecord := range strings.Split(record, variableSeparator) {
			if variableRecord == "" {
				continue
			}
			parameter, valid, err := parseSSVParameter(variableRecord, profile)
			if err != nil {
				return protocol.Value{}, err
			}
			if valid {
				out.Parameters = replaceSSVParameter(out.Parameters, parameter)
			}
		}
		i++
	}
	return out, nil
}

func splitSSVRecords(input, separator string) ([]string, string) {
	records := []string{}
	for {
		index := strings.Index(input, separator)
		if index < 0 {
			return records, input
		}
		records = append(records, input[:index])
		input = input[index+len(separator):]
	}
}

func parseSSVParameter(record, profile string) (protocol.Parameter, bool, error) {
	header, value, hasValue := strings.Cut(record, "=")
	name, dataType, _, err := parseSSVTypedName(header)
	if err != nil || name == "" {
		return protocol.Parameter{}, false, fmt.Errorf("invalid SSV variable %q", record)
	}
	if !hasValue {
		return protocol.Parameter{}, false, nil
	}
	if dataType == "UNDEFINED" {
		dataType = "STRING"
	}
	cell := parsedSSVCell(value, profile)
	if cell.State == "null" && isSSVNumericParameter(dataType) {
		// Variable.set converts the null marker like an empty number: 0.
		cell = protocol.Cell{State: "empty"}
	}
	cell = ssvTypedCell(cell, dataType, profile, true)
	parameter := protocol.Parameter{ID: name, Type: dataType, State: cell.State, Lexical: cell.Lexical}
	if cell.State == "empty" {
		parameter.MarkLexicalPresent()
	}
	return parameter, true, nil
}

func replaceSSVParameter(parameters []protocol.Parameter, parameter protocol.Parameter) []protocol.Parameter {
	for i := range parameters {
		if parameters[i].ID == parameter.ID {
			copy(parameters[i:], parameters[i+1:])
			parameters = parameters[:len(parameters)-1]
			break
		}
	}
	return append(parameters, parameter)
}

func parseSSVDataset(records []string, start int, profile string, strict bool, unitSeparator string) (protocol.Dataset, int, error) {
	dataset := protocol.Dataset{
		ID:           strings.TrimPrefix(records[start], "Dataset:"),
		Columns:      []protocol.Column{},
		ConstColumns: []protocol.ConstColumn{},
		Rows:         []protocol.Row{},
	}
	if dataset.ID == "" {
		return protocol.Dataset{}, start, fmt.Errorf("empty SSV dataset id")
	}

	i := start + 1
	for ; i < len(records); i++ {
		record := records[i]
		if record == "" {
			i++
			break
		}
		if strings.HasPrefix(record, "_Const_") {
			constants, err := parseSSVConstants(record, profile, unitSeparator)
			if err != nil {
				return protocol.Dataset{}, i, err
			}
			dataset.ConstColumns = append(dataset.ConstColumns, constants...)
			continue
		}
		if strings.HasPrefix(record, "_RowType_") {
			columns, err := parseSSVColumns(record, unitSeparator)
			if err != nil {
				return protocol.Dataset{}, i, err
			}
			for j := range columns {
				columns[j].Index = len(dataset.Columns) + j
			}
			dataset.Columns = append(dataset.Columns, columns...)
			continue
		}
		row, valid, err := parseSSVRow(record, dataset.Columns, profile, strict, unitSeparator)
		if err != nil {
			return protocol.Dataset{}, i, err
		}
		if !valid {
			continue
		}
		if row.Type == "O" {
			if len(dataset.Rows) > 0 && dataset.Rows[len(dataset.Rows)-1].Type == "U" {
				for _, constant := range dataset.ConstColumns {
					row.Values[constant.ID] = constant.Value
				}
				original := row
				original.OrgRow = nil
				original.MarkOrgRowPresent()
				dataset.Rows[len(dataset.Rows)-1].OrgRow = &original
				dataset.Rows[len(dataset.Rows)-1].MarkOrgRowPresent()
			}
			continue
		}
		row.MarkOrgRowPresent()
		dataset.Rows = append(dataset.Rows, row)
	}
	for j := range dataset.ConstColumns {
		constant := &dataset.ConstColumns[j]
		constant.Index = len(dataset.Columns) + j
		for rowIndex := range dataset.Rows {
			dataset.Rows[rowIndex].Values[constant.ID] = constant.Value
		}
		constant.Value = protocol.Cell{}
	}
	return dataset, i, nil
}

func parseSSVConstants(record, profile, unitSeparator string) ([]protocol.ConstColumn, error) {
	fields := strings.Split(record, unitSeparator)
	if fields[0] != "_Const_" {
		return nil, fmt.Errorf("invalid SSV constant header")
	}
	constants := make([]protocol.ConstColumn, 0, len(fields)-1)
	for _, field := range fields[1:] {
		header, value, hasValue := strings.Cut(field, "=")
		if !hasValue {
			continue
		}
		name, dataType, _, err := parseSSVTypedName(header)
		if err != nil || name == "" {
			return nil, fmt.Errorf("invalid SSV constant column %q", field)
		}
		// A constant has no null marker: \x03 here is the character itself.
		cell := protocol.Cell{State: "value", Lexical: value}
		if value == "" {
			cell = protocol.Cell{State: "empty"}
			cell.MarkLexicalPresent()
		}
		constants = append(constants, protocol.ConstColumn{ID: name, Type: dataType, Value: cell})
	}
	return constants, nil
}

func parseSSVColumns(record, unitSeparator string) ([]protocol.Column, error) {
	fields := strings.Split(record, unitSeparator)
	if fields[0] != "_RowType_" {
		return nil, fmt.Errorf("invalid SSV column header")
	}
	columns := make([]protocol.Column, 0, len(fields)-1)
	for _, field := range fields[1:] {
		parts := strings.SplitN(field, ":", 4)
		typedName := strings.Join(parts[:min(2, len(parts))], ":")
		name, dataType, size, err := parseSSVTypedName(typedName)
		if err != nil || name == "" {
			return nil, fmt.Errorf("invalid SSV column %q", field)
		}
		if len(parts) > 2 && size == "" {
			dataType = "UNDEFINED"
		}
		columns = append(columns, protocol.Column{ID: name, Type: dataType})
	}
	return columns, nil
}

func parseSSVTypedName(header string) (string, string, string, error) {
	colon := strings.IndexByte(header, ':')
	if colon <= 0 {
		return header, "STRING", "", nil
	}
	name, typeAndSize := header[:colon], header[colon+1:]
	dataType := typeAndSize
	size := ""
	if open := strings.IndexByte(typeAndSize, '('); open >= 0 {
		if !strings.HasSuffix(typeAndSize, ")") || open == 0 {
			return "", "", "", fmt.Errorf("invalid SSV type")
		}
		dataType = typeAndSize[:open]
		size = typeAndSize[open+1 : len(typeAndSize)-1]
		if size == "" {
			return "", "", "", fmt.Errorf("empty SSV length")
		}
	}
	switch strings.ToUpper(dataType) {
	case "":
		dataType = "STRING"
	case "FLOAT":
		dataType = "DOUBLE"
	case "DECIMAL":
		dataType = "UNDEFINED"
	default:
		dataType = strings.ToUpper(dataType)
		if !isKnownType(dataType) {
			dataType = "UNDEFINED"
		}
	}
	return name, dataType, size, nil
}

func parseSSVRow(record string, columns []protocol.Column, profile string, strict bool, unitSeparator string) (protocol.Row, bool, error) {
	fields := strings.Split(record, unitSeparator)
	if len(fields[0]) != 1 || !strings.Contains("NIUDO", fields[0]) || len(columns) == 0 {
		return protocol.Row{}, false, nil
	}
	row := protocol.Row{Type: fields[0], Values: map[string]protocol.Cell{}}
	for i, column := range columns {
		if i+1 >= len(fields) {
			row.Values[column.ID] = protocol.Cell{State: "missing"}
			continue
		}
		row.Values[column.ID] = ssvTypedCell(parsedSSVCell(fields[i+1], profile), column.Type, profile, false)
	}
	return row, true, nil
}

func isSSVNumericParameter(dataType string) bool {
	switch strings.ToUpper(dataType) {
	case "SHORT", "USHORT", "INT", "UINT", "LONG", "ULONG", "FLOAT", "DOUBLE":
		return true
	}
	return false
}

// ssvTypedCell converts a cell the way the drivers' typed setters do: an
// empty number reads as 0, an empty date or bigdecimal as null (see
// normalizeXMLCell). BLOB text is base64-decoded only for Nexacro SSV
// columns; parameters, and every XPLATFORM SSV BLOB, keep the bytes of the
// text itself.
func ssvTypedCell(cell protocol.Cell, dataType, profile string, parameter bool) protocol.Cell {
	if isBlobType(dataType) && (parameter || profile == xplatformSSVProfile) {
		return textBlobCell(cell)
	}
	return normalizeXMLCell(cell, strings.ToUpper(dataType))
}

func ssvCell(value, profile string) (string, string) {
	if profile == xplatformSSVProfile {
		if value == "\x02" {
			return "null", ""
		}
		if value == "" {
			return "empty", ""
		}
		return "value", value
	}
	if value == "\x03" {
		return "null", ""
	}
	if value == "" {
		return "empty", ""
	}
	return "value", value
}

func parsedSSVCell(value, profile string) protocol.Cell {
	state, lexical := ssvCell(value, profile)
	cell := protocol.Cell{State: state, Lexical: lexical}
	if state == "empty" {
		cell.MarkLexicalPresent()
	}
	return cell
}

func encodeSSV(value protocol.Value, profile string) ([]byte, error) {
	unitSeparator, recordSeparator := defaultSSVUnitSeparator, defaultSSVRecordSeparator

	var out strings.Builder
	writeRecord := func(record string) { out.WriteString(record); out.WriteString(recordSeparator) }
	writeRecord("SSV:UTF-8")

	for _, parameter := range value.Parameters {
		record, err := encodeSSVParameter(parameter, profile, unitSeparator, recordSeparator)
		if err != nil {
			return nil, err
		}
		writeRecord(record)
	}
	for i, dataset := range value.Datasets {
		if err := validateSSVToken(dataset.ID, unitSeparator, recordSeparator); err != nil {
			return nil, fmt.Errorf("dataset id: %w", err)
		}
		writeRecord("Dataset:" + dataset.ID)
		if len(dataset.ConstColumns) > 0 {
			fields := []string{"_Const_"}
			for _, column := range dataset.ConstColumns {
				field, err := encodeSSVConstant(column, profile, unitSeparator, recordSeparator)
				if err != nil {
					return nil, err
				}
				fields = append(fields, field)
			}
			writeRecord(strings.Join(fields, unitSeparator))
		}
		if len(dataset.Columns) == 0 {
			return nil, fmt.Errorf("SSV dataset %q has no columns", dataset.ID)
		}
		columnFields := []string{"_RowType_"}
		for _, column := range dataset.Columns {
			field, err := encodeSSVColumn(column, unitSeparator, recordSeparator)
			if err != nil {
				return nil, err
			}
			columnFields = append(columnFields, field)
		}
		writeRecord(strings.Join(columnFields, unitSeparator))
		for _, row := range dataset.Rows {
			record, err := encodeSSVRow(row, dataset.Columns, profile, unitSeparator, recordSeparator)
			if err != nil {
				return nil, err
			}
			writeRecord(record)
			if row.OrgRow != nil {
				org := *row.OrgRow
				org.Type = "O"
				record, err := encodeSSVRow(org, dataset.Columns, profile, unitSeparator, recordSeparator)
				if err != nil {
					return nil, err
				}
				writeRecord(record)
			}
		}
		if ssvProfileUsesNexacroFraming(profile) || i+1 < len(value.Datasets) {
			writeRecord("")
		}
	}
	if ssvProfileUsesNexacroFraming(profile) && len(value.Datasets) == 0 {
		writeRecord("")
	}
	return []byte(out.String()), nil
}

func encodeSSVParameter(parameter protocol.Parameter, profile, unitSeparator, recordSeparator string) (string, error) {
	if err := validateSSVToken(parameter.ID, unitSeparator, recordSeparator); err != nil {
		return "", fmt.Errorf("parameter id: %w", err)
	}
	header := parameter.ID + ":" + ssvWireType(parameter.Type)
	if parameter.State == "missing" {
		return header, nil
	}
	cell := protocol.Cell{State: parameter.State, Lexical: parameter.Lexical}
	if cell.State == "null" && strings.EqualFold(defaultType(parameter.Type), "BLOB") {
		// The drivers write a null BLOB variable as an empty value.
		cell = protocol.Cell{State: "empty"}
	}
	encoded, err := encodeSSVTypedCell(parameter.Type, cell, profile, unitSeparator, recordSeparator)
	if err != nil {
		return "", err
	}
	return header + "=" + encoded, nil
}

func encodeSSVConstant(column protocol.ConstColumn, profile, unitSeparator, recordSeparator string) (string, error) {
	if err := validateSSVToken(column.ID, unitSeparator, recordSeparator); err != nil {
		return "", fmt.Errorf("constant column id: %w", err)
	}
	size := column.Size
	if size == "" {
		size = ssvDefaultSize(column.Type)
	}
	field := column.ID + ":" + ssvWireType(column.Type) + "(" + size + ")"
	if column.Value.State == "missing" {
		return field, nil
	}
	// A constant has no null marker: null is written as an empty value.
	if column.Value.State == "null" {
		return field + "=", nil
	}
	encoded, err := encodeSSVCell(column.Value, profile, unitSeparator, recordSeparator)
	if err != nil {
		return "", err
	}
	return field + "=" + encoded, nil
}

func encodeSSVColumn(column protocol.Column, unitSeparator, recordSeparator string) (string, error) {
	if err := validateSSVToken(column.ID, unitSeparator, recordSeparator); err != nil {
		return "", fmt.Errorf("column id: %w", err)
	}
	size := column.Size
	if size == "" {
		size = ssvDefaultSize(column.Type)
	}
	field := column.ID + ":" + ssvWireType(column.Type) + "(" + size + ")"
	if err := validateSSVToken(field, unitSeparator, recordSeparator); err != nil {
		return "", err
	}
	return field, nil
}

func encodeSSVRow(row protocol.Row, columns []protocol.Column, profile, unitSeparator, recordSeparator string) (string, error) {
	if len(row.Type) != 1 || !strings.Contains("NIUDO", row.Type) {
		return "", fmt.Errorf("invalid SSV row type %q", row.Type)
	}
	fields := make([]string, 1, len(columns)+1)
	fields[0] = row.Type
	for _, column := range columns {
		cell, ok := row.Values[column.ID]
		if !ok {
			cell = protocol.Cell{State: "missing"}
		}
		dataType := column.Type
		if row.Type == "O" {
			// A saved row keeps no BOOLEAN default.
			dataType = "STRING"
		}
		encoded, err := encodeSSVTypedCell(dataType, cell, profile, unitSeparator, recordSeparator)
		if err != nil {
			return "", fmt.Errorf("column %q: %w", column.ID, err)
		}
		fields = append(fields, encoded)
	}
	return strings.Join(fields, unitSeparator), nil
}

// encodeSSVTypedCell writes an unset BOOLEAN as 0, as the drivers hold it as
// false.
func encodeSSVTypedCell(dataType string, cell protocol.Cell, profile, unitSeparator, recordSeparator string) (string, error) {
	if (cell.State == "null" || cell.State == "missing") && strings.EqualFold(defaultType(dataType), "BOOLEAN") {
		return "0", nil
	}
	return encodeSSVCell(cell, profile, unitSeparator, recordSeparator)
}

func encodeSSVCell(cell protocol.Cell, profile, unitSeparator, recordSeparator string) (string, error) {
	switch cell.State {
	case "value":
		if err := validateSSVToken(cell.Lexical, unitSeparator, recordSeparator); err != nil {
			return "", err
		}
		return cell.Lexical, nil
	case "empty":
		return "", nil
	case "null":
		if profile == xplatformSSVProfile {
			return "", nil
		}
		return "\x03", nil
	case "missing":
		if profile == xplatformSSVProfile {
			return "", nil
		}
		return "\x03", nil
	default:
		return "", fmt.Errorf("invalid cell state %q", cell.State)
	}
}

func ssvWireType(dataType string) string {
	switch strings.ToUpper(defaultType(dataType)) {
	case "CHAR", "STRING":
		return "string"
	case "SHORT", "USHORT", "INT", "UINT", "BOOLEAN":
		return "int"
	case "FLOAT", "DOUBLE":
		return "float"
	case "LONG", "ULONG", "DECIMAL", "BIGDECIMAL":
		return "bigdecimal"
	case "DATE":
		return "date"
	case "TIME":
		return "time"
	case "DATETIME":
		return "datetime"
	case "BLOB", "FILE":
		return "blob"
	default:
		return strings.ToLower(defaultType(dataType))
	}
}

func ssvDefaultSize(dataType string) string {
	switch strings.ToUpper(defaultType(dataType)) {
	case "CHAR":
		return "1"
	case "STRING":
		return "32"
	case "SHORT", "USHORT", "BOOLEAN":
		return "2"
	case "INT", "UINT", "FLOAT":
		return "4"
	case "LONG", "ULONG", "DOUBLE":
		return "8"
	case "DECIMAL", "BIGDECIMAL":
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
		return "0"
	}
}

// ssvProfileUsesNexacroFraming reports whether a profile follows the Nexacro
// SSV framing rules (per-dataset null record and terminal null record).
func ssvProfileUsesNexacroFraming(profile string) bool {
	return profile == nexacroSSVProfile
}

func validateSSVToken(value, unitSeparator, recordSeparator string) error {
	if strings.Contains(value, unitSeparator) || strings.Contains(value, recordSeparator) {
		return fmt.Errorf("value contains an SSV separator")
	}
	return nil
}

func isKnownType(dataType string) bool {
	switch strings.ToUpper(dataType) {
	case "STRING", "CHAR", "SHORT", "USHORT", "INT", "UINT", "LONG", "ULONG",
		"FLOAT", "DOUBLE", "DECIMAL", "BIGDECIMAL", "BOOLEAN", "DATE", "TIME",
		"DATETIME", "BLOB", "FILE", "NULL", "UNDEFINED", "DATASET", "INVALID":
		return true
	default:
		return false
	}
}

func defaultType(dataType string) string {
	if dataType == "" {
		return "STRING"
	}
	return strings.ToUpper(dataType)
}
