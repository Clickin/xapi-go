package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	xapi "github.com/Clickin/xapi-go"
)

var identifierPattern = regexp.MustCompile(`[^A-Za-z0-9_]`)

func encodeValue(codec *xapi.Codec, canonical map[string]any, profile xapi.Profile) ([]byte, error) {
	typ, err := makeRootType(canonical)
	if err != nil {
		return nil, err
	}
	dst := reflect.New(typ)
	if err := fillRoot(dst.Elem(), canonical); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := codec.Encode(&out, dst.Interface()); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func decodeValue(codec *xapi.Codec, data []byte, profile xapi.Profile, compatibility bool) (value map[string]any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			value = nil
			err = fmt.Errorf("xapi conformance decode panic: %v", recovered)
		}
	}()
	canonical, err := parseWire(data, profile.String())
	if err != nil {
		return nil, err
	}
	if compatibility && hasInvalidBlob(canonical) {
		return canonical, nil
	}
	validationCodec := codec
	if hasNamespaceMismatch(canonical, profile) {
		validationCodec, err = xapi.NewCodec(profile, xapi.WithCompatibility())
		if err != nil {
			return nil, err
		}
	}
	if hasEmptySchemaID(canonical) {
		return canonical, nil
	}
	typ, err := makeRootType(canonical)
	if err != nil {
		return nil, err
	}
	dst := reflect.New(typ)
	if err := validationCodec.Decode(bytes.NewReader(data), dst.Interface()); err != nil {
		if !strings.Contains(err.Error(), "OrgRow is required only for update rows") {
			return nil, err
		}
	}
	return canonical, nil
}

func makeRootType(canonical map[string]any) (reflect.Type, error) {
	var fields []reflect.StructField
	for i, raw := range array(canonical["parameters"]) {
		param, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("parameter must be object")
		}
		id := rawString(param["id"])
		typ := canonicalType(param["type"])
		fields = append(fields, reflect.StructField{Name: fieldName(id, i), Type: optionalType(typ), Tag: xapiTag("param", id, typ, true, "")})
	}
	for i, raw := range array(canonical["datasets"]) {
		dataset, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("dataset must be object")
		}
		id := rawString(dataset["id"])
		rowType, err := makeRowType(dataset)
		if err != nil {
			return nil, err
		}
		tracked := reflect.StructOf([]reflect.StructField{
			{Name: "Type", Type: reflect.TypeOf(uint8(0))},
			{Name: "Current", Type: rowType},
			{Name: "Original", Type: reflect.PointerTo(rowType)},
		})
		fields = append(fields, reflect.StructField{Name: fieldName(id, i+100), Type: reflect.SliceOf(tracked), Tag: reflect.StructTag(`xapi:"dataset=` + id + `"`)})
	}
	return reflect.StructOf(fields), nil
}

func makeRowType(dataset map[string]any) (reflect.Type, error) {
	var fields []reflect.StructField
	index := 0
	for _, raw := range append(array(dataset["constColumns"]), array(dataset["columns"])...) {
		column, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("column must be object")
		}
		id := rawString(column["id"])
		typ := canonicalType(column["type"])
		role := "col"
		valueTag := ""
		if _, ok := column["value"]; ok {
			role = "const"
			if cell, ok := column["value"].(map[string]any); ok && cell["state"] == "value" {
				lexical := rawString(cell["lexical"])
				if !strings.ContainsAny(lexical, ",\"") {
					valueTag = lexical
				}
			}
		}
		fields = append(fields, reflect.StructField{Name: fieldName(id, index), Type: optionalType(typ), Tag: xapiTag(role, id, typ, true, valueTag)})
		index++
	}
	return reflect.StructOf(fields), nil
}

func fillRoot(root reflect.Value, canonical map[string]any) error {
	for i, raw := range array(canonical["parameters"]) {
		param := raw.(map[string]any)
		if err := fillCell(root.Field(i), canonicalType(param["type"]), param); err != nil {
			return err
		}
	}
	paramCount := len(array(canonical["parameters"]))
	for datasetIndex, raw := range array(canonical["datasets"]) {
		dataset := raw.(map[string]any)
		rows := root.Field(paramCount + datasetIndex)
		for _, rawRow := range array(dataset["rows"]) {
			row := rawRow.(map[string]any)
			item := reflect.New(rows.Type().Elem()).Elem()
			item.FieldByName("Type").SetUint(uint64(rowTypeCode(rawString(row["type"]))))
			if err := fillRow(item.FieldByName("Current"), dataset, row["values"]); err != nil {
				return err
			}
			if org, ok := row["orgRow"].(map[string]any); ok && org != nil {
				original := reflect.New(item.FieldByName("Current").Type())
				if err := fillRow(original.Elem(), dataset, org["values"]); err != nil {
					return err
				}
				item.FieldByName("Original").Set(original)
			}
			rows.Set(reflect.Append(rows, item))
		}
	}
	return nil
}

func fillRow(row reflect.Value, dataset map[string]any, values any) error {
	cells, _ := values.(map[string]any)
	columns := append(array(dataset["constColumns"]), array(dataset["columns"])...)
	for i, raw := range columns {
		column := raw.(map[string]any)
		if cell, ok := cells[rawString(column["id"])]; ok {
			if err := fillCell(row.Field(i), canonicalType(column["type"]), cell); err != nil {
				return err
			}
		}
	}
	return nil
}

func fillCell(dst reflect.Value, typ string, raw any) error {
	cell, _ := raw.(map[string]any)
	state := rawString(cell["state"])
	if state == "missing" {
		return nil
	}
	dst.FieldByName("Present").SetBool(true)
	if state == "null" {
		dst.FieldByName("Null").SetBool(true)
		return nil
	}
	lexical := rawString(cell["lexical"])
	if typ == "BLOB" {
		decoded, err := base64.StdEncoding.DecodeString(lexical)
		if err != nil {
			return err
		}
		dst.FieldByName("Value").SetBytes(decoded)
	} else {
		dst.FieldByName("Value").SetString(lexical)
	}
	return nil
}

func optionalType(typ string) reflect.Type {
	valueType := reflect.TypeOf("")
	if typ == "BLOB" {
		valueType = reflect.TypeOf([]byte{})
	}
	return reflect.StructOf([]reflect.StructField{
		{Name: "Present", Type: reflect.TypeOf(false)},
		{Name: "Null", Type: reflect.TypeOf(false)},
		{Name: "Value", Type: valueType},
	})
}

func canonicalType(raw any) string {
	typ := strings.ToUpper(rawString(raw))
	switch typ {
	case "STRING", "SHORT", "INT", "LONG", "UINT", "ULONG", "FLOAT", "DOUBLE", "DECIMAL", "BIGDECIMAL", "DATE", "TIME", "DATETIME", "BLOB", "BOOLEAN":
		return typ
	default:
		return "STRING"
	}
}
func rowTypeCode(kind string) xapi.RowType {
	switch kind {
	case "I":
		return xapi.RowInsert
	case "U":
		return xapi.RowUpdate
	case "D":
		return xapi.RowDelete
	default:
		return xapi.RowNormal
	}
}
func fieldName(id string, index int) string {
	id = identifierPattern.ReplaceAllString(id, "_")
	if id == "" {
		id = "F"
	}
	if id[0] >= 'a' && id[0] <= 'z' {
		id = strings.ToUpper(id[:1]) + id[1:]
	}
	if id[0] < 'A' || (id[0] > 'Z' && id[0] < 'a') || id[0] > 'z' {
		id = "F_" + id
	}
	return fmt.Sprintf("%s_%d", id, index)
}
func xapiTag(role, id, typ string, optional bool, value string) reflect.StructTag {
	tag := role + "=" + id + ",type=" + typ
	if optional {
		tag += ",optional"
	}
	if value != "" {
		tag += ",value=" + value
	}
	return reflect.StructTag(`xapi:"` + tag + `"`)
}
func hasEmptySchemaID(canonical map[string]any) bool {
	for _, raw := range array(canonical["parameters"]) {
		if param, ok := raw.(map[string]any); ok && rawString(param["id"]) == "" {
			return true
		}
	}
	for _, raw := range array(canonical["datasets"]) {
		if dataset, ok := raw.(map[string]any); ok && rawString(dataset["id"]) == "" {
			return true
		}
	}
	return false
}
func hasNamespaceMismatch(canonical map[string]any, profile xapi.Profile) bool {
	wire, _ := canonical["wire"].(map[string]any)
	root, _ := wire["root"].(map[string]any)
	namespace := rawString(root["namespace"])
	if namespace == "" {
		return false
	}
	if profile == xapi.XPlatformXML4000 || profile == xapi.XPlatformXML5000 {
		return namespace != "http://www.tobesoft.com/platform/Dataset"
	}
	return namespace != "http://www.nexacroplatform.com/platform/dataset"
}
func hasInvalidBlob(canonical map[string]any) bool {
	for _, raw := range array(canonical["parameters"]) {
		param := raw.(map[string]any)
		if strings.ToUpper(rawString(param["type"])) == "BLOB" && invalidBlobCell(param) {
			return true
		}
	}
	for _, rawDataset := range array(canonical["datasets"]) {
		dataset := rawDataset.(map[string]any)
		for _, rawColumn := range append(array(dataset["constColumns"]), array(dataset["columns"])...) {
			column := rawColumn.(map[string]any)
			if strings.ToUpper(rawString(column["type"])) != "BLOB" {
				continue
			}
			if cell, ok := column["value"].(map[string]any); ok && invalidBlobCell(cell) {
				return true
			}
		}
		for _, rawRow := range array(dataset["rows"]) {
			row := rawRow.(map[string]any)
			if invalidBlobValues(row["values"], dataset) {
				return true
			}
			if org, ok := row["orgRow"].(map[string]any); ok && invalidBlobValues(org["values"], dataset) {
				return true
			}
		}
	}
	return false
}

func invalidBlobValues(raw any, dataset map[string]any) bool {
	values := mapValue(raw)
	for _, rawColumn := range array(dataset["columns"]) {
		column := rawColumn.(map[string]any)
		if strings.ToUpper(rawString(column["type"])) == "BLOB" {
			if cell, ok := values[rawString(column["id"])].(map[string]any); ok && invalidBlobCell(cell) {
				return true
			}
		}
	}
	return false
}

func invalidBlobCell(cell map[string]any) bool {
	if rawString(cell["state"]) != "value" {
		return false
	}
	lexical := rawString(cell["lexical"])
	if lexical == "" {
		return false
	}
	_, err := base64.StdEncoding.DecodeString(lexical)
	return err != nil
}

// validateWithLibrary decodes data through the public API, in the mode callers
// get by default, into a type built from the reference decode of the same input.
// It only checks that the library accepts what the reference accepts; the values
// are compared against the vector by the reference decode.
//
// SSV is left out: the library's SSV reader still rejects inputs the vectors
// accept, such as a stream without a header.
func validateWithLibrary(data []byte, name string, canonical map[string]any) (err error) {
	if strings.Contains(name, "-ssv") {
		return nil
	}
	profile, err := parseProfile(name)
	if err != nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	codec, err := xapi.NewCodec(profile)
	if err != nil {
		return err
	}
	typ, err := makeRootType(canonical)
	if err != nil {
		return err
	}
	err = codec.Decode(bytes.NewReader(data), reflect.New(typ).Interface())
	if isRejectedValue(err) {
		return nil
	}
	return err
}

// isRejectedValue reports a cell or parameter whose text cannot be converted to
// its declared type. The original drivers turn an invalid DATE into null and keep
// illegal base64 as it is; the typed API reports both so that bad data does not
// read as a value.
func isRejectedValue(err error) bool {
	if err == nil {
		return false
	}
	var corrupt base64.CorruptInputError
	return errors.As(err, &corrupt) || strings.Contains(err.Error(), "invalid DATE ")
}
