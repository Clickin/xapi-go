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
	if strict && (unitSeparator != "" || recordSeparator != "") {
		return protocol.Value{}, fmt.Errorf("custom SSV separators are not supported in strict mode")
	}
	if unitSeparator == "" {
		unitSeparator = defaultSSVUnitSeparator
	}
	if recordSeparator == "" {
		recordSeparator = defaultSSVRecordSeparator
	}
	if unitSeparator == recordSeparator {
		return protocol.Value{}, fmt.Errorf("SSV separators must differ")
	}

	records, tail := splitSSVRecords(text, recordSeparator)
	if tail != "" {
		if strict {
			return protocol.Value{}, fmt.Errorf("SSV record is not terminated")
		}
		records = append(records, tail)
	}
	if len(records) == 0 {
		return protocol.Value{}, fmt.Errorf("missing SSV header")
	}

	header := records[0]
	recordStart := 1
	codePage := ""
	if header == "SSV" || strings.HasPrefix(header, "SSV:") {
		if header == "SSV" {
			codePage = ""
		} else {
			codePage = strings.TrimPrefix(header, "SSV:")
		}
		if strict && strings.HasPrefix(header, "SSV:") && codePage == "" {
			return protocol.Value{}, fmt.Errorf("empty SSV code page")
		}
		if strings.EqualFold(codePage, "utf-8") {
			codePage = ""
		}
	} else {
		recordStart = 0
	}

	out := protocol.Value{Parameters: []protocol.Parameter{}, Datasets: []protocol.Dataset{}}
	ssvWire := map[string]any{}
	if codePage != "" {
		ssvWire["codePage"] = codePage
	}
	if unitSeparator != defaultSSVUnitSeparator {
		ssvWire["unitSeparator"] = unitSeparator
	}
	if recordSeparator != defaultSSVRecordSeparator {
		ssvWire["recordSeparator"] = recordSeparator
	}
	if len(ssvWire) > 0 {
		out.Wire = map[string]any{"ssv": ssvWire}
	}
	for i := recordStart; i < len(records); {
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
			parameter, err := parseSSVParameter(variableRecord, profile)
			if err != nil {
				return protocol.Value{}, err
			}
			if parameter.State == "missing" {
				continue
			}
			out.Parameters = replaceSSVParameter(out.Parameters, parameter)
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

func parseSSVParameter(record, profile string) (protocol.Parameter, error) {
	header, value, hasValue := strings.Cut(record, "=")
	name, dataType, size, err := parseSSVTypedName(header)
	if err != nil || name == "" {
		return protocol.Parameter{}, fmt.Errorf("invalid SSV variable %q", record)
	}
	parameter := protocol.Parameter{ID: name, Type: dataType, State: "missing"}
	if size != "" {
		parameter.Wire = map[string]any{"length": size}
	}
	if hasValue {
		parameter.State, parameter.Lexical = ssvCell(value, profile)
	}
	return parameter, nil
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
	if strict && dataset.ID == "" {
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
			for j := range constants {
				constants[j].Index = len(dataset.Columns) + len(dataset.ConstColumns) + j
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
		for _, constant := range dataset.ConstColumns {
			cell := constant.Value
			if cell.State == "missing" {
				cell.State = "null"
			}
			row.Values[constant.ID] = cell
		}
		if row.Type == "O" {
			continue
		}
		dataset.Rows = append(dataset.Rows, row)
	}
	return dataset, i, nil
}

func parseSSVConstants(record, profile, unitSeparator string) ([]protocol.ConstColumn, error) {
	fields := strings.Split(record, unitSeparator)
	if fields[0] != "_Const_" {
		return nil, fmt.Errorf("invalid SSV constant header")
	}
	constants := make([]protocol.ConstColumn, 0, len(fields)-1)
	for i, field := range fields[1:] {
		header, value, hasValue := strings.Cut(field, "=")
		name, dataType, size, err := parseSSVTypedName(header)
		if err != nil || name == "" {
			return nil, fmt.Errorf("invalid SSV constant column %q", field)
		}
		if !hasValue {
			continue
		}
		if strings.HasSuffix(header, ":") {
			dataType = "UNDEFINED"
		}
		constant := protocol.ConstColumn{ID: name, Type: dataType, Index: i, Size: size, Value: protocol.Cell{State: "missing"}}
		if hasValue {
			constant.Value.State, constant.Value.Lexical = ssvCell(value, profile)
		}
		constants = append(constants, constant)
	}
	return constants, nil
}

func parseSSVColumns(record, unitSeparator string) ([]protocol.Column, error) {
	fields := strings.Split(record, unitSeparator)
	if fields[0] != "_RowType_" || len(fields) < 2 {
		return nil, fmt.Errorf("invalid SSV column header")
	}
	columns := make([]protocol.Column, 0, len(fields)-1)
	for _, field := range fields[1:] {
		parts := strings.SplitN(field, ":", 4)
		name, dataType, size, err := parseSSVTypedName(strings.Join(parts[:min(2, len(parts))], ":"))
		if err != nil {
			return nil, fmt.Errorf("invalid SSV column %q", field)
		}
		if len(parts) > 1 && parts[1] == "" {
			dataType = "UNDEFINED"
		}
		if name == "" {
			name = field
		}
		if strings.EqualFold(dataType, "FLOAT") {
			dataType = "DOUBLE"
		}
		if strings.EqualFold(dataType, "DECIMAL") {
			dataType = "UNDEFINED"
		}
		column := protocol.Column{ID: name, Type: dataType, Size: size}
		if len(parts) > 2 {
			column.Prop = parts[2]
		}
		if size == "" && dataType == "STRING" && column.Prop != "" {
			column.Type = "UNDEFINED"
		}
		if len(parts) > 3 {
			column.SumText = parts[3]
		}
		columns = append(columns, column)
	}
	return columns, nil
}

func parseSSVTypedName(header string) (string, string, string, error) {
	name, typeAndSize, hasType := strings.Cut(header, ":")
	if !hasType {
		return name, "STRING", "", nil
	}
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
	if dataType == "" {
		dataType = "STRING"
	}
	dataType = strings.ToUpper(dataType)
	if !isKnownType(dataType) {
		dataType = "UNDEFINED"
	}
	return name, dataType, size, nil
}

func parseSSVRow(record string, columns []protocol.Column, profile string, strict bool, unitSeparator string) (protocol.Row, bool, error) {
	fields := strings.Split(record, unitSeparator)
	if len(fields[0]) != 1 || !strings.Contains("NIUDO", fields[0]) {
		return protocol.Row{}, false, nil
	}
	if strict && len(fields)-1 > len(columns) {
		return protocol.Row{}, false, fmt.Errorf("SSV row has too many values")
	}
	row := protocol.Row{Type: fields[0], Values: map[string]protocol.Cell{}}
	for i, column := range columns {
		if i+1 >= len(fields) {
			row.Values[column.ID] = protocol.Cell{State: "missing"}
			continue
		}
		state, lexical := ssvCell(fields[i+1], profile)
		row.Values[column.ID] = protocol.Cell{State: state, Lexical: lexical}
	}
	return row, true, nil
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

func encodeSSV(value protocol.Value, profile string) ([]byte, error) {
	unitSeparator, recordSeparator := defaultSSVUnitSeparator, defaultSSVRecordSeparator
	codePage := "UTF-8"
	if wire, ok := value.Wire["ssv"].(map[string]any); ok {
		if configured, ok := wire["unitSeparator"].(string); ok && configured != "" {
			unitSeparator = configured
		}
		if configured, ok := wire["recordSeparator"].(string); ok && configured != "" {
			recordSeparator = configured
		}
		if configured, ok := wire["codePage"].(string); ok {
			codePage = configured
		}
	}
	if unitSeparator == recordSeparator {
		return nil, fmt.Errorf("SSV separators must differ")
	}

	var out strings.Builder
	writeRecord := func(record string) { out.WriteString(record); out.WriteString(recordSeparator) }
	header := "SSV"
	if codePage != "" {
		header += ":" + codePage
	}
	writeRecord(header)

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
			field, err := encodeSSVColumn(column, profile, unitSeparator, recordSeparator)
			if err != nil {
				return nil, err
			}
			columnFields = append(columnFields, field)
		}
		writeRecord(strings.Join(columnFields, unitSeparator))
		for rowIndex, row := range dataset.Rows {
			if row.Type == "O" && rowIndex > 0 && dataset.Rows[rowIndex-1].OrgRow != nil && sameRow(*dataset.Rows[rowIndex-1].OrgRow, row) {
				continue
			}
			record, err := encodeSSVRow(row, dataset.Columns, profile, unitSeparator, recordSeparator)
			if err != nil {
				return nil, err
			}
			writeRecord(record)
			if row.OrgRow != nil {
				original := *row.OrgRow
				original.Type = "O"
				record, err = encodeSSVRow(original, dataset.Columns, profile, unitSeparator, recordSeparator)
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
	typeName := defaultType(parameter.Type)
	if profile == nexacroSSVProfile || profile == xplatformSSVProfile {
		typeName = strings.ToLower(typeName)
	}
	header := parameter.ID + ":" + typeName
	if parameter.Wire != nil {
		if length, ok := parameter.Wire["length"].(string); ok && length != "" {
			header += "(" + length + ")"
		}
	}
	if parameter.State == "missing" {
		return header, nil
	}
	encoded, err := encodeSSVCell(protocol.Cell{State: parameter.State, Lexical: parameter.Lexical}, profile, unitSeparator, recordSeparator)
	if err != nil {
		return "", err
	}
	return header + "=" + encoded, nil
}

func ssvColumnType(dataType, size, profile string) (string, string) {
	typeName := defaultType(dataType)
	if profile == nexacroSSVProfile || profile == xplatformSSVProfile {
		typeName = strings.ToLower(typeName)
		if size == "" && typeName == "string" {
			size = "32"
		}
	}
	return typeName, size
}

func encodeSSVConstant(column protocol.ConstColumn, profile, unitSeparator, recordSeparator string) (string, error) {
	if err := validateSSVToken(column.ID, unitSeparator, recordSeparator); err != nil {
		return "", fmt.Errorf("constant column id: %w", err)
	}
	typeName, size := ssvColumnType(column.Type, column.Size, profile)
	field := column.ID + ":" + typeName
	if size != "" {
		field += "(" + size + ")"
	}
	if column.Value.State == "missing" {
		return field, nil
	}
	encoded, err := encodeSSVCell(column.Value, profile, unitSeparator, recordSeparator)
	if err != nil {
		return "", err
	}
	return field + "=" + encoded, nil
}

func encodeSSVColumn(column protocol.Column, profile, unitSeparator, recordSeparator string) (string, error) {
	if err := validateSSVToken(column.ID, unitSeparator, recordSeparator); err != nil {
		return "", fmt.Errorf("column id: %w", err)
	}
	typeName, size := ssvColumnType(column.Type, column.Size, profile)
	field := column.ID + ":" + typeName
	if size != "" {
		field += "(" + size + ")"
	}
	if column.Prop != "" || column.SumText != "" {
		field += ":" + column.Prop
		if column.SumText != "" {
			field += ":" + column.SumText
		}
	}
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
		encoded, err := encodeSSVCell(cell, profile, unitSeparator, recordSeparator)
		if err != nil {
			return "", fmt.Errorf("column %q: %w", column.ID, err)
		}
		fields = append(fields, encoded)
	}
	return strings.Join(fields, unitSeparator), nil
}

func encodeSSVCell(cell protocol.Cell, profile, unitSeparator, recordSeparator string) (string, error) {
	switch cell.State {
	case "value":
		if err := validateSSVToken(cell.Lexical, unitSeparator, recordSeparator); err != nil {
			return "", err
		}
		return cell.Lexical, nil
	case "empty", "missing":
		if profile == xplatformSSVProfile {
			return "", nil
		}
		return "\x03", nil
	case "null":
		if profile == xplatformSSVProfile {
			return "", nil
		}
		return "\x03", nil
	default:
		return "", fmt.Errorf("invalid cell state %q", cell.State)
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
