package xapi

import (
	"encoding"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
)

type field struct {
	name, typ                           string
	index                               []int
	optional, rowtype, orgrow, constant bool
	constValue                          string
	hasConstValue                       bool
	size                                int
}
type dataset struct {
	name         string
	index        []int
	elem         reflect.Type
	fields       []field
	lookup       map[string]int
	tracked      bool
	rowTypeIndex []int
	orgRowIndex  []int
}
type schema struct {
	typ      reflect.Type
	params   []field
	datasets []dataset
}

var schemas sync.Map

func compileSchema(t reflect.Type) (*schema, error) {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, &TypeError{Want: "struct", Got: t.String()}
	}
	if x, ok := schemas.Load(t); ok {
		return x.(*schema), nil
	}
	s := &schema{typ: t}
	seen := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if sf.PkgPath != "" {
			continue
		}
		tag := sf.Tag.Get("xapi")
		if tag == "" {
			continue
		}
		kv := parseTag(tag)
		if err := validateTag(kv, sf.Name); err != nil {
			return nil, err
		}
		if id := kv["param"]; id != "" {
			if seen[id] {
				return nil, dup(id)
			}
			seen[id] = true
			f := newField(sf, []int{i}, kv, id)
			if f.typ == "" {
				f.typ = inferType(sf.Type)
			}
			if err := validateFieldType(sf.Type, f.typ, sf.Name); err != nil {
				return nil, err
			}
			s.params = append(s.params, f)
		}
		if id := kv["dataset"]; id != "" {
			if seen[id] {
				return nil, dup(id)
			}
			seen[id] = true
			et := sf.Type
			tracked := false
			if et.Kind() == reflect.Slice {
				et = et.Elem()
			}
			if et.Kind() == reflect.Struct && et.NumField() == 3 && et.Field(0).Name == "Type" && et.Field(1).Name == "Current" {
				tracked = true
				et = et.Field(1).Type
			}
			if sf.Type.Kind() != reflect.Slice {
				return nil, &TypeError{Want: "slice dataset", Got: sf.Type.String()}
			}
			if et.Kind() != reflect.Struct {
				return nil, &TypeError{Want: "struct dataset row", Got: et.String()}
			}
			fs, err := fields(et)
			if err != nil {
				return nil, err
			}
			lookup := make(map[string]int, len(fs))
			for j := range fs {
				lookup[fs[j].name] = j
			}
			var rowTypeIndex, orgRowIndex []int
			for j := 0; j < et.NumField(); j++ {
				sf := et.Field(j)
				kv := parseTag(sf.Tag.Get("xapi"))
				if _, ok := kv["rowtype"]; ok {
					if rowTypeIndex != nil || sf.Type.Kind() != reflect.Uint8 {
						return nil, &TypeError{Want: "single uint8 rowtype field", Got: sf.Name}
					}
					rowTypeIndex = []int{j}
				}
				if _, ok := kv["orgrow"]; ok {
					if orgRowIndex != nil || sf.Type.Kind() != reflect.Pointer || sf.Type.Elem() != et {
						return nil, &TypeError{Want: "single pointer orgrow field", Got: sf.Name}
					}
					orgRowIndex = []int{j}
				}
			}
			s.datasets = append(s.datasets, dataset{name: id, index: []int{i}, elem: et, fields: fs, lookup: lookup, tracked: tracked, rowTypeIndex: rowTypeIndex, orgRowIndex: orgRowIndex})
		}
	}
	schemas.Store(t, s)
	return s, nil
}
func fields(t reflect.Type) ([]field, error) {
	var out []field
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if sf.PkgPath != "" {
			continue
		}
		kv := parseTag(sf.Tag.Get("xapi"))
		if err := validateTag(kv, sf.Name); err != nil {
			return nil, err
		}
		id := kv["col"]
		constant := false
		if id == "" {
			id = kv["const"]
			constant = id != ""
		}
		if id == "" {
			continue
		}
		typ := normalizeWireType(kv["type"])
		if typ == "" {
			typ = inferType(sf.Type)
		}
		if typ == "" {
			return nil, &TypeError{Want: "explicit wire type", Got: sf.Name}
		}
		if err := validateFieldType(sf.Type, typ, sf.Name); err != nil {
			return nil, err
		}
		n := newField(sf, []int{i}, kv, id)
		n.constant = constant
		n.typ = typ
		if raw, ok := kv["size"]; ok {
			v, err := strconv.Atoi(raw)
			if err != nil || v <= 0 {
				return nil, &TypeError{Want: "positive column size", Got: raw}
			}
			n.size = v
		}
		for _, prior := range out {
			if prior.name == n.name {
				return nil, dup(n.name)
			}
		}
		out = append(out, n)
	}
	return out, nil
}

var validWireTypes = map[string]bool{
	"STRING": true, "SHORT": true, "INT": true, "LONG": true,
	"UINT": true, "ULONG": true, "FLOAT": true,
	"DOUBLE": true, "DECIMAL": true, "BIGDECIMAL": true,
	"DATE": true, "TIME": true, "DATETIME": true,
	"BLOB": true, "BOOLEAN": true,
}

func validateTag(kv map[string]string, fieldName string) error {
	roles := 0
	for _, key := range []string{"param", "dataset", "col", "const"} {
		if _, ok := kv[key]; ok {
			roles++
			if kv[key] == "" {
				return &TypeError{Want: "non-empty tag id", Got: fieldName}
			}
		}
	}
	if roles > 1 {
		return &TypeError{Want: "one xapi tag role", Got: fieldName}
	}
	if raw, ok := kv["type"]; ok && !validWireTypes[normalizeWireType(raw)] {
		return &TypeError{Want: "supported wire type", Got: raw}
	}
	return nil
}

var (
	textUnmarshalerType = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()
	blobSourceType      = reflect.TypeOf((*BlobSource)(nil)).Elem()
)

func validateFieldType(t reflect.Type, typ, fieldName string) error {
	if !validWireTypes[typ] {
		return &TypeError{Want: "supported wire type", Got: typ}
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
		if t.Kind() == reflect.Pointer {
			return &TypeError{Want: "non-nested pointer field", Got: fieldName}
		}
	}
	if isOptionalType(t) {
		v, _ := t.FieldByName("Value")
		t = v.Type
		for t.Kind() == reflect.Pointer {
			return &TypeError{Want: "non-nested pointer field", Got: fieldName}
		}
	}
	if typ == "BLOB" {
		if (t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8) || t.Implements(blobSourceType) || reflect.PointerTo(t).Implements(blobSourceType) {
			return nil
		}
		return &TypeError{Want: "[]byte or BlobSource", Got: fieldName}
	}
	if t.Kind() == reflect.Struct && !reflect.PointerTo(t).Implements(textUnmarshalerType) {
		return &TypeError{Want: "scalar field", Got: fieldName}
	}
	return nil
}

func isOptionalType(t reflect.Type) bool {
	if t.Kind() != reflect.Struct || t.NumField() != 3 {
		return false
	}
	p, pok := t.FieldByName("Present")
	n, nok := t.FieldByName("Null")
	_, vok := t.FieldByName("Value")
	return pok && nok && vok && p.Type.Kind() == reflect.Bool && n.Type.Kind() == reflect.Bool
}
func newField(sf reflect.StructField, idx []int, kv map[string]string, id string) field {
	value, hasValue := kv["value"]
	return field{name: id, typ: normalizeWireType(kv["type"]), index: idx, optional: kv["optional"] != "", rowtype: kv["rowtype"] != "", orgrow: kv["orgrow"] != "", constValue: value, hasConstValue: hasValue}
}

func normalizeWireType(s string) string {
	normalized := strings.NewReplacer("-", "_", " ", "_").Replace(strings.ToUpper(strings.TrimSpace(s)))
	switch normalized {
	case "BIG_DECIMAL":
		return "BIGDECIMAL"
	case "DATE_TIME":
		return "DATETIME"
	case "BOOL":
		return "BOOLEAN"
	default:
		return normalized
	}
}
func parseTag(s string) map[string]string {
	m := map[string]string{}
	for _, x := range strings.Split(s, ",") {
		p := strings.SplitN(strings.TrimSpace(x), "=", 2)
		if len(p) == 2 {
			m[p[0]] = p[1]
		} else if len(p) == 1 && p[0] != "" {
			m[p[0]] = p[0]
		}
	}
	return m
}
func inferType(t reflect.Type) string {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Name() {
	case "Decimal", "Date", "Time", "DateTime":
		return ""
	}
	switch t.Kind() {
	case reflect.String:
		return "STRING"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "LONG"
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "LONG"
	case reflect.Float32:
		return "FLOAT"
	case reflect.Float64:
		return "DOUBLE"
	case reflect.Bool:
		return "BOOLEAN"
	case reflect.Slice:
		// BLOB is deliberately explicit so a byte slice cannot silently use
		// the wrong wire representation.
	}
	return ""
}
func dup(s string) error { return &TypeError{Want: "unique tag id", Got: s} }

func validateWireColumns(d *dataset, wire map[string]string, o options) error {
	for id, typ := range wire {
		var f *field
		for i := range d.fields {
			if d.fields[i].name == id {
				f = &d.fields[i]
				break
			}
		}
		if f == nil {
			if o.unknown == ErrorUnknownColumns {
				return fmt.Errorf("xapi: unknown ColumnInfo column %s.%s", d.name, id)
			}
			continue
		}
		if typ != "" && normalizeWireType(typ) != f.typ && o.strict {
			return fmt.Errorf("xapi: %s.%s wire type %s does not match DTO type %s", d.name, id, typ, f.typ)
		}
	}
	return nil
}
