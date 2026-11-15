package codec

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Clickin/xapi-go/internal/conformance/protocol"

	"github.com/go-xml-experiment/xml/xmltext"
)

type DecodeOptions struct {
	Strict             bool
	SSVUnitSeparator   string
	SSVRecordSeparator string
}

func Decode(b []byte) (protocol.Value, error) {
	return DecodeWithStrict(b, true)
}

func DecodeWithStrict(b []byte, strict bool) (protocol.Value, error) {
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 {
		return protocol.Value{}, fmt.Errorf("empty document")
	}
	var value protocol.Value
	var err error
	switch {
	case len(trimmed) >= 2 && ((trimmed[0] == 0xfe && trimmed[1] == 0x10) || (trimmed[0] == 0xfe && trimmed[1] == 0x01)):
		value, err = binaryValue(trimmed, strict)
	case bytes.HasPrefix(trimmed, []byte("SSV")):
		value, err = ssvValue(trimmed, nexacroSSVProfile, strict, "", "")
	case trimmed[0] == '<':
		value, err = xmlValue(b, strict, "")
	default:
		value, err = jsonValue(b, strict)
	}
	return validateDecodedValue(value, err, strict)
}

func DecodeProfile(b []byte, profile string, options DecodeOptions) (protocol.Value, error) {
	var value protocol.Value
	var err error
	switch profile {
	case "nexacro-json-1.0":
		value, err = jsonValue(b, options.Strict)
	case "xplatform-xml-5000", "nexacro-xml-4000":
		value, err = xmlValue(b, options.Strict, profile)
	case nexacroSSVProfile, xplatformSSVProfile:
		value, err = ssvValue(b, profile, options.Strict, options.SSVUnitSeparator, options.SSVRecordSeparator)
	case nexacroBinaryProfile, xplatformBinaryProfile:
		value, err = binaryValue(b, options.Strict)
	default:
		return protocol.Value{}, fmt.Errorf("unsupported profile %q", profile)
	}
	return validateDecodedValue(value, err, options.Strict)
}

func validateDecodedValue(value protocol.Value, decodeErr error, strict bool) (protocol.Value, error) {
	if decodeErr != nil {
		return value, decodeErr
	}
	return finishDecodedValue(value), nil
}

// finishDecodedValue applies what every driver decode shares. Deleted rows go
// to the driver's removed-row list, so they come after all live rows whatever
// their wire position. Every "empty" or "value" cell carries its lexical, even
// when that lexical is "".
func finishDecodedValue(value protocol.Value) protocol.Value {
	mark := func(cell protocol.Cell) protocol.Cell {
		if cell.State == "empty" || cell.State == "value" {
			cell.MarkLexicalPresent()
		}
		return cell
	}
	markAll := func(values map[string]protocol.Cell) {
		for id, cell := range values {
			values[id] = mark(cell)
		}
	}
	for i := range value.Parameters {
		if value.Parameters[i].State == "empty" || value.Parameters[i].State == "value" {
			value.Parameters[i].MarkLexicalPresent()
		}
	}
	for di := range value.Datasets {
		dataset := &value.Datasets[di]
		for ci := range dataset.ConstColumns {
			if dataset.ConstColumns[ci].Value.State != "" {
				dataset.ConstColumns[ci].Value = mark(dataset.ConstColumns[ci].Value)
			}
		}
		live := make([]protocol.Row, 0, len(dataset.Rows))
		var removed []protocol.Row
		for _, row := range dataset.Rows {
			markAll(row.Values)
			if row.OrgRow != nil {
				markAll(row.OrgRow.Values)
				// The drivers report a saved row holding only nulls as no
				// saved row.
				meaningful := false
				for _, cell := range row.OrgRow.Values {
					if cell.State != "null" && cell.State != "missing" {
						meaningful = true
					}
				}
				if !meaningful {
					row.OrgRow = nil
				}
			}
			if row.Type == "D" {
				removed = append(removed, row)
			} else {
				live = append(live, row)
			}
		}
		if len(dataset.Rows) > 0 {
			dataset.Rows = append(live, removed...)
		}
	}
	return value
}

func validateBlobLexicals(value protocol.Value) error {
	for i, parameter := range value.Parameters {
		if isBlobType(parameter.Type) {
			if err := validateBlobCell(protocol.Cell{State: parameter.State, Lexical: parameter.Lexical}); err != nil {
				return fmt.Errorf("Parameters[%d].value: %w", i, err)
			}
		}
	}
	for di, dataset := range value.Datasets {
		for ci, column := range dataset.ConstColumns {
			if isBlobType(column.Type) {
				if err := validateBlobCell(column.Value); err != nil {
					return fmt.Errorf("Datasets[%d].ConstColumns[%d].value: %w", di, ci, err)
				}
			}
		}
		columnTypes := make(map[string]string, len(dataset.Columns))
		for _, column := range dataset.Columns {
			columnTypes[column.ID] = column.Type
		}
		for ri, row := range dataset.Rows {
			for id, cell := range row.Values {
				if isBlobType(columnTypes[id]) {
					if err := validateBlobCell(cell); err != nil {
						return fmt.Errorf("Datasets[%d].Rows[%d].%s: %w", di, ri, id, err)
					}
				}
			}
			if row.OrgRow != nil {
				for id, cell := range row.OrgRow.Values {
					if isBlobType(columnTypes[id]) {
						if err := validateBlobCell(cell); err != nil {
							return fmt.Errorf("Datasets[%d].Rows[%d].OrgRow.%s: %w", di, ri, id, err)
						}
					}
				}
			}
		}
	}
	return nil
}

func isBlobType(dataType string) bool {
	return strings.EqualFold(dataType, "BLOB") || strings.EqualFold(dataType, "FILE")
}

func validateBlobCell(cell protocol.Cell) error {
	if cell.State != "value" && cell.State != "empty" {
		return nil
	}
	if _, err := base64.StdEncoding.DecodeString(cell.Lexical); err != nil {
		return fmt.Errorf("invalid base64 BLOB value")
	}
	return nil
}

func jsonValue(b []byte, strict bool) (protocol.Value, error) {
	if err := protocol.RejectDuplicateKeys(b); err != nil {
		return protocol.Value{}, err
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(b, &root); err != nil {
		return protocol.Value{}, err
	}
	out := protocol.Value{
		Parameters: []protocol.Parameter{},
		Datasets:   []protocol.Dataset{},
		Wire:       map[string]any{"version": "1.0"},
	}
	if versionRaw, ok := root["version"]; ok {
		var version string
		if err := json.Unmarshal(versionRaw, &version); err != nil {
			return out, fmt.Errorf("version must be a string")
		}
	}
	if raw, ok := root["Parameters"]; ok {
		if err := parseJSONParameters(raw, &out.Parameters, strict); err != nil {
			return out, err
		}
	}
	var datasets []map[string]json.RawMessage
	if raw, ok := root["Datasets"]; ok {
		if err := json.Unmarshal(raw, &datasets); err != nil {
			return out, fmt.Errorf("Datasets must be an array: %w", err)
		}
	}
	for _, d := range datasets {
		ds := protocol.Dataset{Columns: []protocol.Column{}, ConstColumns: []protocol.ConstColumn{}, Rows: []protocol.Row{}}
		if err := json.Unmarshal(d["id"], &ds.ID); err != nil || ds.ID == "" {
			return out, fmt.Errorf("Dataset.id is required")
		}
		constValues := map[string]protocol.Cell{}
		if columnInfo, ok := d["ColumnInfo"]; ok {
			var err error
			constValues, err = parseJSONColumns(columnInfo, &ds, strict)
			if err != nil {
				return out, err
			}
		}
		var rows []map[string]json.RawMessage
		if rowsRaw, ok := d["Rows"]; ok {
			if err := json.Unmarshal(rowsRaw, &rows); err != nil {
				return out, fmt.Errorf("Dataset.Rows must be an array: %w", err)
			}
		}
		for _, r := range rows {
			row := protocol.Row{Type: "N", Values: map[string]protocol.Cell{}}
			if raw := r["_RowType_"]; len(raw) > 0 {
				if err := json.Unmarshal(raw, &row.Type); err != nil {
					return out, fmt.Errorf("_RowType_ must be a string")
				}
			}
			if !strings.Contains("NIUDO", row.Type) || len(row.Type) != 1 {
				if strict {
					return out, fmt.Errorf("invalid _RowType_ %q", row.Type)
				}
				continue
			}
			if row.Type == "O" {
				if len(ds.Rows) == 0 || ds.Rows[len(ds.Rows)-1].Type != "U" {
					if strict {
						return out, fmt.Errorf("orphan original row")
					}
					continue
				}
				original := protocol.Row{Type: "O", Values: map[string]protocol.Cell{}}
				for _, column := range ds.Columns {
					original.Values[column.ID] = jsonCell(r[column.ID], column.Type)
				}
				for _, column := range ds.ConstColumns {
					original.Values[column.ID] = constValues[column.ID]
				}
				original.MarkOrgRowPresent()
				ds.Rows[len(ds.Rows)-1].OrgRow = &original
				ds.Rows[len(ds.Rows)-1].MarkOrgRowPresent()
				continue
			}
			for _, column := range ds.Columns {
				row.Values[column.ID] = jsonCell(r[column.ID], column.Type)
			}
			for _, column := range ds.ConstColumns {
				row.Values[column.ID] = constValues[column.ID]
			}
			row.MarkOrgRowPresent()
			ds.Rows = append(ds.Rows, row)
		}
		out.Datasets = append(out.Datasets, ds)
	}
	return out, nil
}

func parseJSONParameters(raw json.RawMessage, out *[]protocol.Parameter, strict bool) error {
	var parameters []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parameters); err != nil {
		return fmt.Errorf("Parameters must be an array: %w", err)
	}
	for _, item := range parameters {
		var parameter protocol.Parameter
		if err := json.Unmarshal(item["id"], &parameter.ID); err != nil || parameter.ID == "" {
			return fmt.Errorf("Parameter.id is required")
		}
		_ = json.Unmarshal(item["type"], &parameter.Type)
		if parameter.Type == "" {
			parameter.Type = inferJSONType(item["value"])
		}
		parameter.Type = normalizeDecodedType(parameter.Type, "STRING")
		if value, ok := item["value"]; ok {
			parameter.State, parameter.Lexical = rawState(value)
		} else {
			parameter.State = "null"
		}
		if parameter.Type == "UNDEFINED" && parameter.State != "null" {
			// Variable.set(String) gives an untyped variable the string type;
			// a null value is never set, so the type stays undefined.
			parameter.Type = "STRING"
		}
		cell := protocol.Cell{State: parameter.State, Lexical: parameter.Lexical}
		if isBlobType(parameter.Type) {
			cell = textBlobCell(cell)
		} else {
			cell = normalizeXMLCell(cell, parameter.Type)
		}
		parameter.State, parameter.Lexical = cell.State, cell.Lexical
		if parameter.State == "empty" {
			parameter.MarkLexicalPresent()
		}
		*out = append(*out, parameter)
	}
	return nil
}

func parseJSONColumns(raw json.RawMessage, dataset *protocol.Dataset, strict bool) (map[string]protocol.Cell, error) {
	var info map[string]json.RawMessage
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, fmt.Errorf("ColumnInfo must be an object: %w", err)
	}
	constValues := map[string]protocol.Cell{}
	if columnsRaw, ok := info["Column"]; ok {
		if err := parseJSONColumnArray(columnsRaw, dataset, false, nil); err != nil {
			return nil, err
		}
	}
	if constantsRaw, ok := info["ConstColumn"]; ok {
		if err := parseJSONColumnArray(constantsRaw, dataset, true, constValues); err != nil {
			return nil, err
		}
	}
	return constValues, nil
}

func parseJSONColumnArray(raw json.RawMessage, dataset *protocol.Dataset, isConst bool, constValues map[string]protocol.Cell) error {
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return fmt.Errorf("column list must be an array: %w", err)
	}
	for _, item := range items {
		var id, dataType string
		_ = json.Unmarshal(item["id"], &id)
		if id == "" {
			return fmt.Errorf("column id is required")
		}
		_ = json.Unmarshal(item["type"], &dataType)
		if size, ok := item["size"]; ok {
			// The driver reads size as a JSON string and rejects a number.
			var text string
			if json.Unmarshal(size, &text) != nil {
				return fmt.Errorf("column %q size must be a string", id)
			}
		}
		if dataType == "" {
			if isConst {
				dataType = inferJSONType(item["value"])
			} else {
				dataType = "STRING"
			}
		}
		dataType = normalizeDecodedType(dataType, "STRING")
		if isConst {
			index := len(dataset.Columns) + len(dataset.ConstColumns)
			dataset.ConstColumns = append(dataset.ConstColumns, protocol.ConstColumn{ID: id, Type: dataType, Index: index})
			// The JSON reader stores the constant value string as is, unlike
			// the XML reader, which converts it to the column type.
			constValues[id] = rawCell(item["value"])
		} else {
			dataset.Columns = append(dataset.Columns, protocol.Column{ID: id, Type: dataType, Index: len(dataset.Columns)})
		}
	}
	return nil
}

func inferJSONType(raw json.RawMessage) string {
	return "STRING"
}
func normalizeDecodedType(dataType, missingDefault string) string {
	dataType = strings.ToUpper(dataType)
	if dataType == "" {
		dataType = missingDefault
	}
	if dataType == "FLOAT" {
		return "DOUBLE"
	}
	if dataType == "DECIMAL" || !isKnownType(dataType) {
		return "UNDEFINED"
	}
	return dataType
}

func rawScalarString(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	if len(raw) > 0 && string(raw) != "null" {
		return string(raw)
	}
	return ""
}
func rawCell(raw json.RawMessage) protocol.Cell {
	if len(raw) == 0 {
		return protocol.Cell{State: "null"}
	}
	state, lexical := rawState(raw)
	cell := protocol.Cell{State: state, Lexical: lexical}
	if state == "empty" {
		cell.MarkLexicalPresent()
	}
	return cell
}

// jsonCell converts a column value as the driver's typed setter does; BLOB
// column text is base64-decoded (see normalizeXMLCell).
func jsonCell(raw json.RawMessage, dataType string) protocol.Cell {
	return normalizeXMLCell(rawCell(raw), strings.ToUpper(dataType))
}
func rawState(raw json.RawMessage) (string, string) {
	if string(raw) == "null" {
		return "null", ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if s == "" {
			return "empty", ""
		}
		return "value", s
	}
	return "value", string(raw)
}

type xroot struct {
	XMLName    xmltext.Name
	Attr       []xmltext.Attr
	Parameters struct {
		Items []xparam
	}
	Datasets struct {
		Items []xdataset
	}
	DirectDatasets []xdataset
}
type xparam struct {
	Attr []xmltext.Attr
	Text string
}
type xdataset struct {
	ID      string
	Columns struct {
		Items  []xcol
		Consts []xcol
	}
	Rows struct {
		Items []xrow
	}
}
type xcol struct {
	ID       string
	Type     string
	Size     string
	Encoding string
	Value    *string
	Encrypt  string
	Prop     string
	SumText  string
}
type xrow struct {
	Type          string
	Cols          []xcolvalue
	Org           *xorgrow
	OrgUnderscore *xorgrow
}
type xorgrow struct {
	Cols []xcolvalue
}
type xcolvalue struct {
	ID   string
	Text string
}

type xmlSyntax struct {
	ParameterSelfClosing []bool
	DatasetColumnIndexes []map[string]int
	DatasetIDs           []string
}

func xmlValue(b []byte, strict bool, profile string) (protocol.Value, error) {
	if bytes.Contains(bytes.ToUpper(b), []byte("<!DOCTYPE")) {
		return protocol.Value{}, fmt.Errorf("DTD and external entities are forbidden")
	}
	if declaration := bytes.Index(b, []byte("<?xml")); declaration > 0 {
		return protocol.Value{}, fmt.Errorf("XML declaration must be the first bytes")
	}
	if err := rejectDuplicateXMLAttrs(b); err != nil {
		return protocol.Value{}, err
	}
	syntax, err := validateXMLStructure(b, strict, profile)
	if err != nil {
		return protocol.Value{}, err
	}
	root, err := parseXRoot(b)
	if err != nil {
		return protocol.Value{}, err
	}

	out := protocol.Value{Parameters: []protocol.Parameter{}, Datasets: []protocol.Dataset{}}
	rootWire := map[string]any{}
	if namespace := xmlNamespace(profile); namespace != "" {
		rootWire["namespace"] = namespace
		switch profile {
		case "nexacro-xml-4000":
			rootWire["version"] = "4000"
		case "xplatform-xml-5000":
			rootWire["ver"] = "5000"
		}
	} else {
		if root.XMLName.Space != "" {
			rootWire["namespace"] = root.XMLName.Space
		}
		for _, attribute := range root.Attr {
			if attribute.Name.Local == "version" || attribute.Name.Local == "ver" {
				rootWire[attribute.Name.Local] = attribute.Value
			}
		}
	}
	if len(rootWire) > 0 {
		out.Wire = map[string]any{"root": rootWire}
	}

	for _, item := range root.Parameters.Items {
		id, dataType := "", ""
		for _, attribute := range item.Attr {
			switch attribute.Name.Local {
			case "id":
				id = attribute.Value
			case "type":
				dataType = attribute.Value
			}
		}
		if id == "" {
			return protocol.Value{}, fmt.Errorf("Parameter.id is required")
		}
		dataType = normalizeDecodedParameterType(dataType)
		cell := normalizeXMLCell(xmlCell(item.Text), dataType)
		if isBlobType(dataType) {
			cell = textBlobCell(xmlCell(item.Text))
		}
		parameter := protocol.Parameter{
			ID: id, Type: dataType, State: cell.State, Lexical: cell.Lexical,
		}
		if cell.State == "empty" {
			parameter.MarkLexicalPresent()
		}
		out.Parameters = append(out.Parameters, parameter)
	}

	datasets := append(append([]xdataset{}, root.DirectDatasets...), root.Datasets.Items...)
	datasets = orderXMLDatasets(datasets, syntax.DatasetIDs)
	for datasetIndex, item := range datasets {
		if item.ID == "" {
			return protocol.Value{}, fmt.Errorf("Dataset.id is required")
		}
		dataset := protocol.Dataset{ID: item.ID, Columns: []protocol.Column{}, ConstColumns: []protocol.ConstColumn{}, Rows: []protocol.Row{}}
		constValues := map[string]protocol.Cell{}
		wireTypes := map[string]string{}
		for i, column := range item.Columns.Items {
			if column.ID == "" {
				return protocol.Value{}, fmt.Errorf("Column.id is required")
			}
			wireTypes[column.ID] = strings.ToUpper(column.Type)
			dataType := normalizeDecodedType(column.Type, "UNDEFINED")
			index := xmlColumnIndex(syntax, datasetIndex, "Column", column.ID, len(item.Columns.Consts)+i)
			dataset.Columns = append(dataset.Columns, protocol.Column{ID: column.ID, Type: dataType, Index: index})
		}
		for i, column := range item.Columns.Consts {
			if column.ID == "" {
				return protocol.Value{}, fmt.Errorf("ConstColumn.id is required")
			}
			dataType := normalizeDecodedType(column.Type, "UNDEFINED")
			index := xmlColumnIndex(syntax, datasetIndex, "ConstColumn", column.ID, i)
			dataset.ConstColumns = append(dataset.ConstColumns, protocol.ConstColumn{ID: column.ID, Type: dataType, Index: index})
			value := protocol.Cell{State: "null"}
			// The drivers fail on a BLOB constant without a value attribute,
			// which they write themselves for a null BLOB constant.
			if column.Value == nil && strings.EqualFold(dataType, "BLOB") {
				return protocol.Value{}, fmt.Errorf("BLOB ConstColumn %q has no value", column.ID)
			}
			if column.Value != nil {
				value = normalizeXMLCell(xmlCell(*column.Value), strings.ToUpper(column.Type))
			}
			constValues[column.ID] = value
			wireTypes[column.ID] = strings.ToUpper(column.Type)
		}
		known := map[string]bool{}
		for _, column := range dataset.Columns {
			known[column.ID] = true
		}
		for _, column := range dataset.ConstColumns {
			known[column.ID] = true
		}
		for _, itemRow := range item.Rows.Items {
			for _, cell := range itemRow.Cols {
				if !known[cell.ID] {
					return protocol.Value{}, fmt.Errorf("column %q is not declared", cell.ID)
				}
			}
			if itemRow.Org != nil {
				for _, cell := range itemRow.Org.Cols {
					if !known[cell.ID] {
						return protocol.Value{}, fmt.Errorf("org column %q is not declared", cell.ID)
					}
				}
			}
			if itemRow.OrgUnderscore != nil {
				for _, cell := range itemRow.OrgUnderscore.Cols {
					if !known[cell.ID] {
						return protocol.Value{}, fmt.Errorf("org column %q is not declared", cell.ID)
					}
				}
			}
			row := xmlRow(itemRow, dataset.Columns, constValues, wireTypes)
			row.MarkOrgRowPresent()
			dataset.Rows = append(dataset.Rows, row)
		}
		out.Datasets = append(out.Datasets, dataset)
	}
	return out, nil
}

func validateXMLStructure(b []byte, strict bool, profile string) (xmlSyntax, error) {
	decoder := xmltext.NewDecoderBytes(b, xmlDecodeOptions...)
	stack := []string{}
	rootSeen := false
	currentDataset := -1
	syntax := xmlSyntax{}
	for {
		before := decoder.InputOffset()
		token, err := readXMLToken(decoder)
		if err == io.EOF {
			if !rootSeen || len(stack) != 0 {
				return syntax, fmt.Errorf("XML root is incomplete")
			}
			return syntax, nil
		}
		if err != nil {
			return syntax, err
		}
		switch element := token.(type) {
		case xmlStartToken:
			if !rootSeen {
				if element.Name.Local != "Root" {
					return syntax, fmt.Errorf("root element must be Root")
				}
				rootSeen = true
			}
			if element.Name.Local == "OrgRow" && len(stack) > 0 && stack[len(stack)-1] == "Rows" {
				return syntax, fmt.Errorf("OrgRow must be inside Row")
			}
			switch element.Name.Local {
			case "Dataset":
				currentDataset = len(syntax.DatasetColumnIndexes)
				syntax.DatasetColumnIndexes = append(syntax.DatasetColumnIndexes, map[string]int{})
				id := ""
				for _, attribute := range element.Attr {
					if attribute.Name.Local == "id" {
						id = attribute.Value
						break
					}
				}
				syntax.DatasetIDs = append(syntax.DatasetIDs, id)
			case "Column", "ConstColumn":
				if currentDataset >= 0 {
					id := ""
					for _, attribute := range element.Attr {
						if attribute.Name.Local == "id" {
							id = attribute.Value
							break
						}
					}
					indexes := syntax.DatasetColumnIndexes[currentDataset]
					indexes[element.Name.Local+"\x00"+id] = len(indexes)
				}
			case "Parameter":
				segment := bytes.TrimSpace(b[before:decoder.InputOffset()])
				syntax.ParameterSelfClosing = append(syntax.ParameterSelfClosing, bytes.HasSuffix(segment, []byte("/>")))
			}
			stack = append(stack, element.Name.Local)
		case xmlEndToken:
			if len(stack) == 0 || stack[len(stack)-1] != element.Name.Local {
				return syntax, fmt.Errorf("unexpected closing element %s", element.Name.Local)
			}
			stack = stack[:len(stack)-1]
			if element.Name.Local == "Dataset" {
				currentDataset = -1
			}
		}
	}
}

func xmlColumnIndex(syntax xmlSyntax, datasetIndex int, kind, id string, fallback int) int {
	if datasetIndex < len(syntax.DatasetColumnIndexes) {
		if index, ok := syntax.DatasetColumnIndexes[datasetIndex][kind+"\x00"+id]; ok {
			return index
		}
	}
	return fallback
}

func orderXMLDatasets(datasets []xdataset, ids []string) []xdataset {
	if len(ids) != len(datasets) {
		return datasets
	}
	ordered := make([]xdataset, 0, len(datasets))
	used := make([]bool, len(datasets))
	for _, id := range ids {
		for i, dataset := range datasets {
			if !used[i] && dataset.ID == id {
				ordered = append(ordered, dataset)
				used[i] = true
				break
			}
		}
	}
	if len(ordered) != len(datasets) {
		return datasets
	}
	return ordered
}

func xmlNamespace(profile string) string {
	switch profile {
	case "xplatform-xml-5000":
		return "http://www.tobesoft.com/platform/Dataset"
	case "nexacro-xml-4000":
		return "http://www.nexacroplatform.com/platform/dataset"
	default:
		return ""
	}
}

// xmlAcceptedNamespace reports whether a decoded root namespace is acceptable
// for the profile. Decode is intentionally permissive: it accepts both the
// canonical namespace emitted by Encode and the variant forms the commercial
// jars emit (tobesoft lowercase "dataset"; nexacro "nexacro.com"). Encode
// still emits only the canonical form returned by xmlNamespace.
func xmlAcceptedNamespace(profile, namespace string) bool {
	accepted := xmlAcceptedNamespaces(profile)
	if len(accepted) == 0 {
		return true
	}
	for _, value := range accepted {
		if namespace == value {
			return true
		}
	}
	return false
}

func xmlAcceptedNamespaces(profile string) []string {
	switch profile {
	case "xplatform-xml-5000":
		return []string{"http://www.tobesoft.com/platform/Dataset", "http://www.tobesoft.com/platform/dataset"}
	case "nexacro-xml-4000":
		return []string{"http://www.nexacroplatform.com/platform/dataset", "http://www.nexacro.com/platform/dataset"}
	default:
		return nil
	}
}

func rejectDuplicateXMLAttrs(b []byte) error {
	d := xmltext.NewDecoderBytes(b, xmlDecodeOptions...)
	for {
		tok, err := readXMLToken(d)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if start, ok := tok.(xmlStartToken); ok {
			seen := map[string]bool{}
			for _, a := range start.Attr {
				key := a.Name.Space + ":" + a.Name.Local
				if seen[key] {
					return fmt.Errorf("duplicate XML attribute %q", a.Name.Local)
				}
				seen[key] = true
			}
		}
	}
}

func xmlRow(r xrow, columns []protocol.Column, constValues map[string]protocol.Cell, wireTypes map[string]string) protocol.Row {
	typ := map[string]string{"insert": "I", "update": "U", "delete": "D", "normal": "N"}[strings.ToLower(r.Type)]
	if typ == "" {
		typ = "N"
	}
	row := protocol.Row{Type: typ, Values: map[string]protocol.Cell{}}
	for _, column := range columns {
		row.Values[column.ID] = protocol.Cell{State: "null"}
	}
	for id, value := range constValues {
		row.Values[id] = value
	}
	for _, cell := range r.Cols {
		row.Values[cell.ID] = normalizeXMLCell(xmlCell(cell.Text), wireTypes[cell.ID])
	}
	if r.Org == nil && r.OrgUnderscore != nil {
		for _, cell := range r.OrgUnderscore.Cols {
			row.Values[cell.ID] = normalizeXMLCell(xmlCell(cell.Text), wireTypes[cell.ID])
		}
		return row
	}
	original := r.Org
	if typ == "U" && original != nil {
		saved := protocol.Row{Type: "O", Values: map[string]protocol.Cell{}}
		for _, column := range columns {
			saved.Values[column.ID] = protocol.Cell{State: "null"}
		}
		for id, value := range constValues {
			saved.Values[id] = value
		}
		meaningful := false
		for _, cell := range original.Cols {
			value := normalizeXMLCell(xmlCell(cell.Text), wireTypes[cell.ID])
			saved.Values[cell.ID] = value
			if value.State != "null" {
				meaningful = true
			}
		}
		if meaningful {
			saved.MarkOrgRowPresent()
			row.OrgRow = &saved
		}
	}
	return row
}

func normalizeDecodedParameterType(dataType string) string {
	dataType = strings.ToUpper(dataType)
	switch dataType {
	case "":
		return "STRING"
	case "FLOAT":
		return "DOUBLE"
	case "DECIMAL":
		return "STRING"
	default:
		if !isKnownType(dataType) {
			return "STRING"
		}
		return dataType
	}
}

func normalizeXMLCell(cell protocol.Cell, dataType string) protocol.Cell {
	if cell.State == "null" {
		return cell
	}
	value := cell.Lexical
	switch strings.ToUpper(dataType) {
	case "CHAR", "SHORT", "USHORT", "INT", "UINT":
		value = javaInteger(value, 32)
	case "LONG", "ULONG":
		value = javaInteger(value, 64)
	case "FLOAT", "DOUBLE":
		// The drivers read a float column as a Java double.
		value = javaFloating(value, 64)
	case "BIGDECIMAL":
		decimal, ok := javaBigDecimal(value)
		if !ok {
			return protocol.Cell{State: "null"}
		}
		value = decimal
	case "DATE", "TIME", "DATETIME":
		date, ok := javaDateLexical(value, dataType)
		if !ok {
			return protocol.Cell{State: "null"}
		}
		value = date
	case "BOOLEAN":
		if sourceBoolean(value) {
			value = "true"
		} else {
			value = "false"
		}
	case "BLOB", "FILE":
		if value == "" {
			return protocol.Cell{State: "empty"}
		}
		if _, err := base64.StdEncoding.DecodeString(value); err != nil {
			return protocol.Cell{State: "null"}
		}
	}
	if cell.State == "empty" && value == "" {
		return cell
	}
	cell.State = "value"
	cell.Lexical = value
	return cell
}

// textBlobCell is how the drivers read a BLOB parameter in the text formats:
// Variable.set(String) converts the text to its bytes without decoding base64,
// so the parameter holds the bytes of the wire text itself.
func textBlobCell(cell protocol.Cell) protocol.Cell {
	if cell.State != "value" && cell.State != "empty" {
		return cell
	}
	if cell.Lexical == "" {
		return protocol.Cell{State: "empty"}
	}
	return protocol.Cell{State: "value", Lexical: base64.StdEncoding.EncodeToString([]byte(cell.Lexical))}
}

func xmlCell(s string) protocol.Cell {
	cell := protocol.Cell{State: "value", Lexical: s}
	if s == "" {
		cell.State = "empty"
		cell.MarkLexicalPresent()
	}
	return cell
}

// xmlDecodeOptions selects the decoder profile for wire documents.
//
// The declared encoding is not applied: wire documents are read as UTF-8
// whatever the declaration names, and other bytes fail as invalid UTF-8.
var xmlDecodeOptions = []xmltext.Options{
	xmltext.WithCharsetReader(func(_ string, r io.Reader) (io.Reader, error) { return r, nil }),
}

type xmlStartToken struct {
	Name xmltext.Name
	Attr []xmltext.Attr
}

type xmlEndToken struct{ Name xmltext.Name }

// readXMLToken returns the next element boundary, or nil for any other token.
func readXMLToken(d *xmltext.Decoder) (any, error) {
	t, err := d.ReadToken()
	if err != nil {
		return nil, err
	}
	switch t.Kind() {
	case xmltext.StartElement:
		return xmlStartToken{Name: t.Name(), Attr: copyXMLAttrs(t)}, nil
	case xmltext.EndElement:
		return xmlEndToken{Name: t.Name()}, nil
	}
	return nil, nil
}

func copyXMLAttrs(t xmltext.Token) []xmltext.Attr {
	var attrs []xmltext.Attr
	for it := t.Attrs(); it.Next(); {
		attrs = append(attrs, xmltext.Attr{Name: it.Name(), Value: string(it.Value())})
	}
	return attrs
}

// xnode is an element of a wire document. Elements are matched by local name
// because the wire namespace depends on the profile, and documents without one
// are accepted too.
type xnode struct {
	name     xmltext.Name
	attrs    []xmltext.Attr
	text     strings.Builder
	children []*xnode
	inCol    bool // nested inside a Col
}

func parseXMLTree(b []byte) (*xnode, error) {
	d := xmltext.NewDecoderBytes(b, xmlDecodeOptions...)
	var stack []*xnode
	for {
		t, err := d.ReadToken()
		if err != nil {
			return nil, err
		}
		switch t.Kind() {
		case xmltext.StartElement:
			n := &xnode{name: t.Name(), attrs: copyXMLAttrs(t)}
			if len(stack) > 0 {
				top := stack[len(stack)-1]
				n.inCol = top.name.Local == "Col" || top.inCol
				top.children = append(top.children, n)
			}
			stack = append(stack, n)
		case xmltext.EndElement:
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				return n, nil
			}
			// Markup inside a Col keeps its text in the Col's value.
			if top := stack[len(stack)-1]; top.name.Local == "Col" || top.inCol {
				top.text.WriteString(n.text.String())
			}
		case xmltext.Text, xmltext.CData:
			if len(stack) > 0 {
				stack[len(stack)-1].text.Write(t.Text())
			}
		}
	}
}

func (n *xnode) attr(local string) (value string, ok bool) {
	for _, a := range n.attrs {
		if a.Name.Local == local {
			value, ok = a.Value, true
		}
	}
	return value, ok
}

func (n *xnode) attrText(local string) string {
	value, _ := n.attr(local)
	return value
}

func parseXRoot(b []byte) (xroot, error) {
	n, err := parseXMLTree(b)
	if err != nil {
		return xroot{}, err
	}
	root := xroot{XMLName: n.name, Attr: n.attrs}
	for _, c := range n.children {
		switch c.name.Local {
		case "Parameters":
			for _, p := range c.children {
				if p.name.Local == "Parameter" {
					root.Parameters.Items = append(root.Parameters.Items, xparam{Attr: p.attrs, Text: p.text.String()})
				}
			}
		case "Dataset":
			root.DirectDatasets = append(root.DirectDatasets, c.dataset())
		case "Datasets":
			for _, d := range c.children {
				if d.name.Local == "Dataset" {
					root.Datasets.Items = append(root.Datasets.Items, d.dataset())
				}
			}
		}
	}
	return root, nil
}

func (n *xnode) dataset() xdataset {
	d := xdataset{ID: n.attrText("id")}
	for _, c := range n.children {
		switch c.name.Local {
		case "ColumnInfo":
			for _, col := range c.children {
				switch col.name.Local {
				case "Column":
					d.Columns.Items = append(d.Columns.Items, col.column())
				case "ConstColumn":
					d.Columns.Consts = append(d.Columns.Consts, col.column())
				}
			}
		case "Rows":
			for _, row := range c.children {
				if row.name.Local == "Row" {
					d.Rows.Items = append(d.Rows.Items, row.row())
				}
			}
		}
	}
	return d
}

func (n *xnode) column() xcol {
	c := xcol{
		ID:       n.attrText("id"),
		Type:     n.attrText("type"),
		Size:     n.attrText("size"),
		Encoding: n.attrText("enc"),
		Encrypt:  n.attrText("encrypt"),
		Prop:     n.attrText("prop"),
		SumText:  n.attrText("sumtext"),
	}
	if value, ok := n.attr("value"); ok {
		c.Value = &value
	}
	return c
}

func (n *xnode) row() xrow {
	r := xrow{Type: n.attrText("type")}
	for _, c := range n.children {
		switch c.name.Local {
		case "Col":
			r.Cols = append(r.Cols, c.colValue())
		case "OrgRow":
			if r.Org == nil {
				r.Org = &xorgrow{}
			}
			r.Org.Cols = append(r.Org.Cols, c.colValues()...)
		case "Org_Row":
			if r.OrgUnderscore == nil {
				r.OrgUnderscore = &xorgrow{}
			}
			r.OrgUnderscore.Cols = append(r.OrgUnderscore.Cols, c.colValues()...)
		}
	}
	return r
}

func (n *xnode) colValues() []xcolvalue {
	var cols []xcolvalue
	for _, c := range n.children {
		if c.name.Local == "Col" {
			cols = append(cols, c.colValue())
		}
	}
	return cols
}

func (n *xnode) colValue() xcolvalue {
	return xcolvalue{ID: n.attrText("id"), Text: n.text.String()}
}
