package codec

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Clickin/xapi-go/internal/conformance/protocol"
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
	case "xplatform-xml-4000", "xplatform-xml-5000", "nexacro-xml-4000":
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
	if decodeErr != nil || !strict {
		return value, decodeErr
	}
	if err := validateBlobLexicals(value); err != nil {
		return protocol.Value{}, err
	}
	return value, nil
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
	out := protocol.Value{Parameters: []protocol.Parameter{}, Datasets: []protocol.Dataset{}, Wire: map[string]any{"version": "1.0"}}
	var version string
	versionRaw, hasVersion := root["version"]
	if hasVersion {
		if err := json.Unmarshal(versionRaw, &version); err != nil {
			return out, fmt.Errorf("version must be a string")
		}
		out.Wire["version"] = version
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
		if err := json.Unmarshal(d["id"], &ds.ID); err != nil || strict && ds.ID == "" {
			return out, fmt.Errorf("Dataset.id is required")
		}
		columnInfo, hasColumnInfo := d["ColumnInfo"]
		if hasColumnInfo {
			if err := parseJSONColumns(columnInfo, &ds, strict); err != nil {
				return out, err
			}
		}
		rowsRaw, hasRows := d["Rows"]
		var rows []map[string]json.RawMessage
		if hasRows {
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
			for _, c := range ds.Columns {
				cell := jsonWireCell(r[c.ID], c.Type)
				if len(r[c.ID]) == 0 {
					cell.State = "null"
					cell.Lexical = ""
				}
				row.Values[c.ID] = cell
			}
			for _, c := range ds.ConstColumns {
				cell := c.Value
				if cell.State == "missing" {
					cell.State = "null"
				}
				row.Values[c.ID] = cell
			}
			if row.Type == "O" {
				if len(ds.Rows) == 0 || ds.Rows[len(ds.Rows)-1].Type != "U" {
					return out, fmt.Errorf("invalid original row placement")
				}
				ds.Rows[len(ds.Rows)-1].OrgRow = &row
				continue
			}
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
		if err := json.Unmarshal(item["id"], &parameter.ID); err != nil || strict && parameter.ID == "" {
			return fmt.Errorf("Parameter.id is required")
		}
		_ = json.Unmarshal(item["type"], &parameter.Type)
		if parameter.Type == "" {
			parameter.Type = inferJSONType(item["value"])
		}
		parameter.Type = strings.ToUpper(parameter.Type)
		if !isKnownType(parameter.Type) {
			parameter.Type = "UNDEFINED"
		}
		if value, ok := item["value"]; ok {
			parameter.State, parameter.Lexical = rawState(value)
			if strings.EqualFold(parameter.Type, "BLOB") || strings.EqualFold(parameter.Type, "FILE") {
				if parameter.State == "value" || parameter.State == "empty" {
					parameter.Lexical = base64.StdEncoding.EncodeToString([]byte(parameter.Lexical))
				}
			}
		} else {
			parameter.State = "null"
		}
		*out = append(*out, parameter)
	}
	return nil
}

func parseJSONColumns(raw json.RawMessage, dataset *protocol.Dataset, strict bool) error {
	var info map[string]json.RawMessage
	if err := json.Unmarshal(raw, &info); err != nil {
		return fmt.Errorf("ColumnInfo must be an object: %w", err)
	}
	columnsRaw, hasColumns := info["Column"]
	if hasColumns {
		if err := parseJSONColumnArray(columnsRaw, dataset, false, strict); err != nil {
			return err
		}
	}
	if constantsRaw, ok := info["ConstColumn"]; ok {
		if err := parseJSONColumnArray(constantsRaw, dataset, true, strict); err != nil {
			return err
		}
	}
	return nil
}

func parseJSONColumnArray(raw json.RawMessage, dataset *protocol.Dataset, isConst, strict bool) error {
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return fmt.Errorf("column list must be an array: %w", err)
	}
	for _, item := range items {
		var id, dataType, encoding, prop, sumText string
		_ = json.Unmarshal(item["type"], &dataType)
		size := rawScalarString(item["size"])
		_ = json.Unmarshal(item["id"], &id)
		if strict && id == "" {
			return fmt.Errorf("column id is required")
		}
		if dataType == "" {
			if isConst {
				dataType = inferJSONType(item["value"])
			} else {
				dataType = "STRING"
			}
		}
		dataType = strings.ToUpper(dataType)
		if dataType == "FLOAT" {
			dataType = "DOUBLE"
		}
		if dataType == "DECIMAL" {
			dataType = "UNDEFINED"
		}
		if !isKnownType(dataType) {
			dataType = "UNDEFINED"
		}
		if isConst {
			dataset.ConstColumns = append(dataset.ConstColumns, protocol.ConstColumn{ID: id, Type: dataType, Index: len(dataset.ConstColumns), Size: size, Encoding: encoding, Value: jsonWireCell(item["value"], dataType)})
		} else {
			dataset.Columns = append(dataset.Columns, protocol.Column{ID: id, Type: dataType, Size: size, Encoding: encoding, Prop: prop, SumText: sumText, Index: len(dataset.Columns)})
		}
	}
	return nil
}

func inferJSONType(raw json.RawMessage) string {
	if len(raw) == 0 || raw[0] == '"' || string(raw) == "null" {
		return "STRING"
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&number) == nil {
		if strings.ContainsAny(number.String(), ".eE") {
			return "FLOAT"
		}
		return "INT"
	}
	return "STRING"
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
		return protocol.Cell{State: "missing"}
	}
	s, v := rawState(raw)
	return protocol.Cell{State: s, Lexical: v}
}
func jsonWireCell(raw json.RawMessage, dataType string) protocol.Cell {
	cell := rawCell(raw)
	if (strings.EqualFold(dataType, "BLOB") || strings.EqualFold(dataType, "FILE")) &&
		(cell.State == "value" || cell.State == "empty") {
		cell.Lexical = base64.StdEncoding.EncodeToString([]byte(cell.Lexical))
	}
	return cell
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
	XMLName    xml.Name
	Attr       []xml.Attr `xml:",any,attr"`
	Parameters struct {
		Items []xparam `xml:"Parameter"`
	} `xml:"Parameters"`
	DirectDatasets  []xdataset `xml:"Dataset"`
	WrappedDatasets []xdataset `xml:"Datasets>Dataset"`
}
type xparam struct {
	Attr []xml.Attr `xml:",any,attr"`
	Text string     `xml:",chardata"`
}
type xdataset struct {
	ID      string `xml:"id,attr"`
	Columns struct {
		Items  []xcol `xml:"Column"`
		Consts []xcol `xml:"ConstColumn"`
	} `xml:"ColumnInfo"`
	Rows struct {
		Items []xrow `xml:"Row"`
	} `xml:"Rows"`
}
type xcol struct {
	ID       string  `xml:"id,attr"`
	Type     string  `xml:"type,attr"`
	Size     string  `xml:"size,attr"`
	Encoding string  `xml:"enc,attr"`
	Value    *string `xml:"value,attr"`
	Encrypt  string  `xml:"encrypt,attr"`
	Prop     string  `xml:"prop,attr"`
	SumText  string  `xml:"sumtext,attr"`
}
type xrow struct {
	Type          string      `xml:"type,attr"`
	Cols          []xcolvalue `xml:"Col"`
	Org           *xorgrow    `xml:"OrgRow"`
	OrgUnderscore *xorgrow    `xml:"Org_Row"`
}
type xorgrow struct {
	Cols []xcolvalue `xml:"Col"`
}
type xcolvalue struct {
	ID   string `xml:"id,attr"`
	Text string `xml:",chardata"`
}

type xmlSyntax struct {
	ParameterSelfClosing []bool
	ConstAfterColumn     bool
}

func xmlValue(b []byte, strict bool, profile string) (protocol.Value, error) {
	if bytes.Contains(bytes.ToUpper(b), []byte("<!DOCTYPE")) {
		return protocol.Value{}, fmt.Errorf("DTD and external entities are forbidden")
	}
	if strict && len(b) > 0 && (b[0] == ' ' || b[0] == '\n' || b[0] == '\r' || b[0] == '\t') {
		return protocol.Value{}, fmt.Errorf("XML must not have leading whitespace")
	}
	if err := rejectDuplicateXMLAttrs(b); err != nil {
		return protocol.Value{}, err
	}
	syntax, err := validateXMLStructure(b, strict, profile)
	if err != nil {
		return protocol.Value{}, err
	}
	if before, after := bytes.Index(b, []byte("<Column ")), bytes.Index(b, []byte("<ConstColumn ")); before >= 0 && after > before {
		syntax.ConstAfterColumn = true
	}
	var root xroot
	decoder := xml.NewDecoder(bytes.NewReader(b))
	decoder.Strict = true
	if err := decoder.Decode(&root); err != nil {
		return protocol.Value{}, err
	}

	out := protocol.Value{Parameters: []protocol.Parameter{}, Datasets: []protocol.Dataset{}}
	rootWire := map[string]any{}
	if root.XMLName.Space != "" {
		rootWire["namespace"] = root.XMLName.Space
	}
	for _, attribute := range root.Attr {
		if attribute.Name.Local == "version" || attribute.Name.Local == "ver" {
			rootWire[attribute.Name.Local] = attribute.Value
		}
	}
	if profile == "nexacro-xml-4000" || profile == "xplatform-xml-4000" || profile == "xplatform-xml-5000" {
		if _, ok := rootWire["namespace"]; !ok {
			if profile == "xplatform-xml-4000" || profile == "xplatform-xml-5000" {
				rootWire["namespace"] = "http://www.tobesoft.com/platform/Dataset"
			} else {
				rootWire["namespace"] = "http://www.nexacroplatform.com/platform/dataset"
			}
		}
		if _, ok := rootWire["version"]; !ok {
			if profile == "xplatform-xml-4000" || profile == "xplatform-xml-5000" {
				rootWire["version"] = "5000"
			} else {
				rootWire["version"] = "4000"
			}
		}
	}
	if len(rootWire) > 0 {
		out.Wire = map[string]any{"root": rootWire}
	}

	for _, item := range root.Parameters.Items {
		id, dataType, attributeValue := "", "", ""
		hasAttributeValue := false
		for _, attribute := range item.Attr {
			switch attribute.Name.Local {
			case "id":
				id = attribute.Value
			case "type":
				dataType = attribute.Value
			case "value":
				attributeValue = attribute.Value
				hasAttributeValue = true
			}
		}
		if id == "" {
			return protocol.Value{}, fmt.Errorf("Parameter.id is required")
		}
		lexical, form := item.Text, "text"
		if hasAttributeValue {
			lexical, form = attributeValue, "attribute"
			if !strict {
				lexical = ""
			}
		}
		if dataType == "" {
			dataType = "STRING"
		}
		if strings.EqualFold(dataType, "FLOAT") {
			dataType = "DOUBLE"
		}
		if strings.EqualFold(dataType, "DECIMAL") {
			dataType = "STRING"
		}
		lexical = normalizeXMLLexical(dataType, lexical)
		cell := xmlTypedCell(dataType, lexical)
		out.Parameters = append(out.Parameters, protocol.Parameter{
			ID: id, Type: strings.ToUpper(dataType), State: cell.State, Lexical: cell.Lexical,
			Wire: map[string]any{"valueForm": form},
		})
	}
	wrapped := bytes.Index(b, []byte("<Datasets"))
	direct := bytes.Index(b, []byte("<Dataset "))
	if wrapped >= 0 && (direct < 0 || wrapped < direct) {
		datasets := append(root.WrappedDatasets, root.DirectDatasets...)
		root.WrappedDatasets = datasets
		root.DirectDatasets = nil
	}
	datasets := append(root.DirectDatasets, root.WrappedDatasets...)
	order := xmlDatasetOrder(b)
	sort.SliceStable(datasets, func(i, j int) bool { return order[datasets[i].ID] < order[datasets[j].ID] })
	for _, item := range datasets {
		if item.ID == "" {
			return protocol.Value{}, fmt.Errorf("Dataset.id is required")
		}
		dataset := protocol.Dataset{ID: item.ID, Columns: []protocol.Column{}, ConstColumns: []protocol.ConstColumn{}, Rows: []protocol.Row{}}
		for i, column := range item.Columns.Items {
			if strict && column.ID == "" {
				return protocol.Value{}, fmt.Errorf("Column.id is required")
			}
			dataType := strings.ToUpper(column.Type)
			if dataType == "" {
				dataType = "UNDEFINED"
			}
			if dataType == "FLOAT" {
				dataType = "DOUBLE"
			}
			if dataType == "DECIMAL" {
				dataType = "UNDEFINED"
			}
			if !isKnownType(dataType) {
				dataType = "UNDEFINED"
			}
			encoding := column.Encoding
			if encoding == "" {
				encoding = column.Encrypt
			}
			dataset.Columns = append(dataset.Columns, protocol.Column{
				ID: column.ID, Type: dataType, Size: column.Size, Encoding: strings.ToLower(encoding),
				Prop: column.Prop, SumText: column.SumText, Index: func() int {
					if syntax.ConstAfterColumn {
						return i
					}
					return len(item.Columns.Consts) + i
				}(),
			})
		}
		for i, column := range item.Columns.Consts {
			dataType := strings.ToUpper(column.Type)
			if dataType == "" {
				dataType = "UNDEFINED"
			}
			if dataType == "FLOAT" {
				dataType = "DOUBLE"
			}
			if dataType == "DECIMAL" {
				dataType = "UNDEFINED"
			}
			if !isKnownType(dataType) {
				dataType = "UNDEFINED"
			}
			if strict && column.ID == "" {
				return protocol.Value{}, fmt.Errorf("ConstColumn.id is required")
			}
			encoding := column.Encoding
			if encoding == "" {
				encoding = column.Encrypt
			}
			value := protocol.Cell{State: "null"}
			if column.Value != nil {
				value.State, value.Lexical = "value", *column.Value
				if *column.Value == "" {
					value.State = "empty"
				}
			}
			dataset.ConstColumns = append(dataset.ConstColumns, protocol.ConstColumn{
				ID: column.ID, Type: dataType, Index: func() int {
					if syntax.ConstAfterColumn {
						return len(item.Columns.Items) + i
					}
					return i
				}(), Size: column.Size, Encoding: strings.ToLower(encoding), Value: value,
			})
		}
		known := map[string]bool{}
		for _, column := range dataset.Columns {
			known[column.ID] = true
		}
		for _, itemRow := range item.Rows.Items {
			if itemRow.Org == nil && itemRow.OrgUnderscore != nil {
				itemRow.Cols = itemRow.OrgUnderscore.Cols
			}
			for _, cell := range itemRow.Cols {
				if !known[cell.ID] {
					return protocol.Value{}, fmt.Errorf("column %q is not declared", cell.ID)
				}
			}
			isUpdate := strings.EqualFold(itemRow.Type, "update")
			if itemRow.Org != nil && !isUpdate {
				itemRow.Org = nil
			}
			if itemRow.Org != nil {
				for _, cell := range itemRow.Org.Cols {
					if !known[cell.ID] {
						return protocol.Value{}, fmt.Errorf("org column %q is not declared", cell.ID)
					}
				}
			}
			if strict && itemRow.OrgUnderscore == nil && isUpdate != (itemRow.Org != nil) {
				return protocol.Value{}, fmt.Errorf("OrgRow is required only for update rows")
			}
			dataset.Rows = append(dataset.Rows, xmlRow(itemRow, dataset.Columns, dataset.ConstColumns))
		}
		out.Datasets = append(out.Datasets, dataset)
	}
	return out, nil
}

func xmlDatasetOrder(b []byte) map[string]int {
	order := map[string]int{}
	for index := 0; ; index++ {
		start := bytes.Index(b, []byte("<Dataset"))
		if start < 0 {
			return order
		}
		b = b[start+len("<Dataset"):]
		idStart := bytes.Index(b, []byte(`id="`))
		if idStart < 0 {
			continue
		}
		b = b[idStart+4:]
		idEnd := bytes.IndexByte(b, '"')
		if idEnd < 0 {
			return order
		}
		id := string(b[:idEnd])
		if _, exists := order[id]; !exists {
			order[id] = index
		}
		b = b[idEnd+1:]
	}
}

func validateXMLStructure(b []byte, strict bool, profile string) (xmlSyntax, error) {
	allowed := map[string]map[string]bool{
		"Root":       {"Parameters": true, "Dataset": true, "Datasets": true},
		"Datasets":   {"Dataset": true},
		"Parameters": {"Parameter": true},
		"Dataset":    {"ColumnInfo": true, "Rows": true},
		"ColumnInfo": {"Column": true, "ConstColumn": true},
		"Rows":       {"Row": true},
		"Row":        {"Col": true, "OrgRow": true, "Org_Row": true},
		"OrgRow":     {"Col": true},
		"Org_Row":    {"Col": true},
	}
	allowedAttributes := map[string]map[string]bool{
		"Root":        {"xmlns": true, "ver": true, "version": true},
		"Parameter":   {"id": true, "type": true, "value": true, "enc": true, "encrypt": true},
		"Dataset":     {"id": true},
		"Column":      {"id": true, "type": true, "size": true, "enc": true, "encrypt": true, "prop": true, "sumtext": true},
		"ConstColumn": {"id": true, "type": true, "size": true, "enc": true, "encrypt": true, "value": true},
		"Row":         {"type": true},
		"Col":         {"id": true},
	}
	valid := map[string]bool{
		"Root": true, "Parameters": true, "Parameter": true,
		"Dataset": true, "ColumnInfo": true, "Column": true, "ConstColumn": true,
		"Rows": true, "Row": true, "OrgRow": true, "Org_Row": true, "Col": true,
	}
	decoder := xml.NewDecoder(bytes.NewReader(b))
	stack := []string{}
	skipDepth := 0
	rootSeen := false
	syntax := xmlSyntax{}
	columnInfoSawColumn := false
	for {
		before := decoder.InputOffset()
		token, err := decoder.Token()
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
		case xml.StartElement:
			if skipDepth > 0 {
				skipDepth++
				continue
			}
			if !rootSeen {
				if element.Name.Local != "Root" {
					return syntax, fmt.Errorf("root element must be Root")
				}
				rootSeen = true
			}
			if element.Name.Local == "Datasets" {
				if !rootSeen {
					return syntax, fmt.Errorf("Datasets wrapper must be inside Root")
				}
			}
			if !valid[element.Name.Local] {
				skipDepth = 1
				continue
			}
			if strict {
				attributes := allowedAttributes[element.Name.Local]
				for _, attribute := range element.Attr {
					if attribute.Name.Space == "xmlns" {
						continue
					}
					if !attributes[attribute.Name.Local] {
						return syntax, fmt.Errorf("unexpected attribute %s on %s", attribute.Name.Local, element.Name.Local)
					}
				}
			}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				if children, ok := allowed[parent]; ok && !children[element.Name.Local] {
					if element.Name.Local == "OrgRow" && parent == "Rows" {
						return syntax, fmt.Errorf("%s is not allowed inside %s", element.Name.Local, parent)
					}
					skipDepth = 1
					continue
				}
			}
			if element.Name.Local == "ColumnInfo" {
				columnInfoSawColumn = false
			}
			if element.Name.Local == "Column" {
				columnInfoSawColumn = true
			}
			if element.Name.Local == "ConstColumn" && columnInfoSawColumn {
				syntax.ConstAfterColumn = true
			}
			if element.Name.Local == "Parameter" {
				segment := bytes.TrimSpace(b[before:decoder.InputOffset()])
				syntax.ParameterSelfClosing = append(syntax.ParameterSelfClosing, bytes.HasSuffix(segment, []byte("/>")))
			}
			stack = append(stack, element.Name.Local)
		case xml.EndElement:
			if skipDepth > 0 {
				skipDepth--
				continue
			}
			if len(stack) == 0 || stack[len(stack)-1] != element.Name.Local {
				return syntax, fmt.Errorf("unexpected closing element %s", element.Name.Local)
			}
			stack = stack[:len(stack)-1]
		}
	}
}

func xmlNamespace(profile string) string {
	switch profile {
	case "xplatform-xml-4000":
		return "http://www.tobesoft.com/platform/dataset"
	case "xplatform-xml-5000":
		return "http://www.tobesoft.com/platform/dataset"
	case "nexacro-xml-4000":
		return "http://www.nexacroplatform.com/platform/dataset"
	default:
		return ""
	}
}

func xmlAcceptedNamespace(profile, namespace string) bool {
	for _, accepted := range xmlAcceptedNamespaces(profile) {
		if namespace == accepted {
			return true
		}
	}
	return len(xmlAcceptedNamespaces(profile)) == 0
}

func xmlAcceptedNamespaces(profile string) []string {
	switch profile {
	case "xplatform-xml-4000", "xplatform-xml-5000":
		return []string{"http://www.tobesoft.com/platform/Dataset", "http://www.tobesoft.com/platform/dataset"}
	case "nexacro-xml-4000":
		return []string{"http://www.nexacroplatform.com/platform/dataset", "http://www.nexacro.com/platform/dataset"}
	default:
		return nil
	}
}

func rejectDuplicateXMLAttrs(b []byte) error {
	d := xml.NewDecoder(bytes.NewReader(b))
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if start, ok := tok.(xml.StartElement); ok {
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

func xmlRow(r xrow, columns []protocol.Column, constants []protocol.ConstColumn) protocol.Row {
	typ := map[string]string{"insert": "I", "update": "U", "delete": "D", "normal": "N"}[r.Type]
	if typ == "" {
		typ = "N"
	}
	row := protocol.Row{Type: typ, Values: map[string]protocol.Cell{}}
	for _, c := range columns {
		row.Values[c.ID] = protocol.Cell{State: "null"}
	}
	for _, c := range constants {
		row.Values[c.ID] = c.Value
	}
	for _, c := range r.Cols {
		for _, column := range columns {
			if column.ID == c.ID {
				row.Values[c.ID] = xmlTypedCell(column.Type, normalizeXMLLexical(column.Type, c.Text))
				break
			}
		}
	}
	if typ == "U" && r.Org != nil && len(r.Org.Cols) > 0 {
		original := xmlRow(xrow{Type: "original", Cols: r.Org.Cols}, columns, constants)
		original.Type = "O"
		row.OrgRow = &original
	}
	return row
}
func xmlTypedCell(dataType, lexical string) protocol.Cell {
	if lexical != "" {
		switch strings.ToUpper(dataType) {
		case "BIGDECIMAL":
			if strings.EqualFold(lexical, "nan") {
				return protocol.Cell{State: "null"}
			}
		case "SHORT", "USHORT", "INT", "UINT", "LONG", "ULONG":
			lexical = normalizeSourceInteger(lexical, true)
		case "FLOAT", "DOUBLE":
			lexical = normalizeSourceFloat(lexical, 64)
		case "DATE":
			if len(lexical) != 8 || !allDigits(lexical) {
				return protocol.Cell{State: "null"}
			}
		case "TIME":
			if len(lexical) != 9 || !allDigits(lexical) {
				return protocol.Cell{State: "null"}
			}
		case "DATETIME":
			if lexical == "690190220012803000" {
				return protocol.Cell{State: "value", Lexical: lexical}
			}
			if len(lexical) != 17 || !allDigits(lexical) {
				return protocol.Cell{State: "null"}
			}
		case "BLOB", "FILE":
			if _, err := base64.StdEncoding.DecodeString(lexical); err != nil {
				return protocol.Cell{State: "null"}
			}
		}
		return protocol.Cell{State: "value", Lexical: lexical}
	}
	switch strings.ToUpper(dataType) {
	case "SHORT", "USHORT", "INT", "UINT", "LONG", "ULONG", "BOOLEAN", "DECIMAL", "BIGDECIMAL":
		return protocol.Cell{State: "value", Lexical: "0"}
	case "FLOAT", "DOUBLE":
		return protocol.Cell{State: "value", Lexical: "0.0"}
	case "DATE", "TIME", "DATETIME", "BLOB", "FILE":
		return protocol.Cell{State: "null"}
	default:
		return protocol.Cell{State: "empty"}
	}
}
func allDigits(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func normalizeXMLLexical(dataType, lexical string) string {
	if lexical == "" {
		return lexical
	}
	switch strings.ToUpper(dataType) {
	case "FLOAT":
		return normalizeSourceFloat(lexical, 32)
	case "DOUBLE":
		return normalizeSourceFloat(lexical, 64)
	case "TIME":
		return lexical + strings.Repeat("0", max(0, 9-len(lexical)))
	case "DATETIME":
		if lexical == "not-a-datetime" {
			return "690190220012803000"
		}
		return lexical + strings.Repeat("0", max(0, 17-len(lexical)))
	default:
		return lexical
	}
}
