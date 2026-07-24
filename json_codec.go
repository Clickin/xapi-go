package xapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
)

func encodeJSON(w io.Writer, src any, o options) error {
	tw := &trackedWriter{w: w}
	w = tw
	v := reflect.ValueOf(src)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	s, e := compileSchema(v.Type())
	if e != nil {
		return e
	}
	if e = enforceSchemaLimits(s, o); e != nil {
		return e
	}
	if e = enforceValueLimits(v, s, o); e != nil {
		return e
	}
	if _, e = io.WriteString(w, "{\"version\":\"1.0\""); e != nil {
		return e
	}
	wrote := true
	if len(s.params) > 0 {
		if wrote {
			io.WriteString(w, ",")
		}
		io.WriteString(w, "\"Parameters\":[")
		first := true
		for _, f := range s.params {
			if (f.optional || optionalField(v.FieldByIndex(f.index))) && missingValue(v.FieldByIndex(f.index)) {
				continue
			}
			if !first {
				io.WriteString(w, ",")
			}
			first = false
			writeJSONParam(w, v, f, o)
		}
		io.WriteString(w, "]")
		wrote = true
	}
	if len(s.datasets) > 0 {
		if wrote {
			io.WriteString(w, ",")
		}
		io.WriteString(w, "\"Datasets\":[")
		for i, d := range s.datasets {
			if i > 0 {
				io.WriteString(w, ",")
			}
			if e := writeJSONDataset(w, v.FieldByIndex(d.index), d, o); e != nil {
				return e
			}
		}
		io.WriteString(w, "]")
	}
	_, e = io.WriteString(w, "}")
	if e == nil && tw.err != nil {
		e = tw.err
	}
	return e
}

type trackedWriter struct {
	w   io.Writer
	err error
}

func (t *trackedWriter) Write(p []byte) (int, error) {
	if t.err != nil {
		return 0, t.err
	}
	n, e := t.w.Write(p)
	if e == nil && n != len(p) {
		e = io.ErrShortWrite
	}
	if e != nil {
		t.err = e
	}
	return n, e
}
func writeJSONParam(w io.Writer, v reflect.Value, f field, o options) error {
	io.WriteString(w, "{")
	writeJSONKey(w, "id", f.name)
	io.WriteString(w, ",")
	writeJSONKey(w, "type", strings.ToLower(f.typ))
	if f.size > 0 {
		io.WriteString(w, ",")
		writeJSONInt(w, "size", f.size)
	}
	io.WriteString(w, ",\"value\":")
	e := writeJSONScalar(w, v.FieldByIndex(f.index), f.typ, o)
	io.WriteString(w, "}")
	return e
}
func writeJSONDataset(w io.Writer, v reflect.Value, d dataset, o options) error {
	io.WriteString(w, "{")
	writeJSONKey(w, "id", d.name)
	io.WriteString(w, ",\"ColumnInfo\":")
	if e := writeJSONColumnInfo(w, d, o); e != nil {
		return e
	}
	io.WriteString(w, ",\"Rows\":[")
	for i := 0; i < v.Len(); i++ {
		if i > 0 {
			io.WriteString(w, ",")
		}
		rv := v.Index(i)
		if rv.Kind() == reflect.Pointer {
			rv = rv.Elem()
		}
		status := RowNormal
		var original reflect.Value
		if d.tracked {
			status = RowType(rv.FieldByName("Type").Uint())
			original = rv.FieldByName("Original")
			rv = rv.FieldByName("Current")
		} else if d.rowTypeIndex != nil {
			status = RowType(rv.FieldByIndex(d.rowTypeIndex).Uint())
			if d.orgRowIndex != nil {
				original = rv.FieldByIndex(d.orgRowIndex)
			}
		}
		io.WriteString(w, "{")
		writeJSONKey(w, "_RowType_", rowTypeJSON(status))
		for _, f := range d.fields {
			if f.constant {
				continue
			}
			if (f.optional || optionalField(rv.FieldByIndex(f.index))) && missingValue(rv.FieldByIndex(f.index)) {
				continue
			}
			io.WriteString(w, ",")
			writeJSONName(w, f.name)
			if e := writeJSONScalar(w, rv.FieldByIndex(f.index), f.typ, o); e != nil {
				return e
			}
		}
		io.WriteString(w, "}")
		if (d.tracked || d.orgRowIndex != nil) && status == RowUpdate && original.IsValid() && !original.IsNil() {
			io.WriteString(w, ",{")
			writeJSONKey(w, "_RowType_", "O")
			ov := original.Elem()
			for _, f := range d.fields {
				if f.constant {
					continue
				}
				if (f.optional || optionalField(ov.FieldByIndex(f.index))) && missingValue(ov.FieldByIndex(f.index)) {
					continue
				}
				io.WriteString(w, ",")
				writeJSONName(w, f.name)
				if e := writeJSONScalar(w, ov.FieldByIndex(f.index), f.typ, o); e != nil {
					return e
				}
			}
			io.WriteString(w, "}")
		}
	}
	io.WriteString(w, "]}")
	return nil
}
func writeJSONColumnInfo(w io.Writer, d dataset, o options) error {
	io.WriteString(w, "{\"Column\":[")
	first := true
	for _, f := range d.fields {
		if f.constant {
			continue
		}
		if !first {
			io.WriteString(w, ",")
		}
		first = false
		io.WriteString(w, "{")
		writeJSONKey(w, "id", f.name)
		io.WriteString(w, ",")
		writeJSONKey(w, "type", strings.ToLower(f.typ))
		if f.size > 0 {
			io.WriteString(w, ",")
			writeJSONInt(w, "size", f.size)
		}
		io.WriteString(w, "}")
	}
	io.WriteString(w, "]")
	var consts []field
	for _, f := range d.fields {
		if f.constant {
			consts = append(consts, f)
		}
	}
	if len(consts) > 0 {
		io.WriteString(w, ",\"ConstColumn\":[")
		for i, f := range consts {
			if i > 0 {
				io.WriteString(w, ",")
			}
			zero := reflect.New(d.elem).Elem()
			io.WriteString(w, "{")
			writeJSONKey(w, "id", f.name)
			io.WriteString(w, ",")
			writeJSONKey(w, "type", strings.ToLower(f.typ))
			if f.size > 0 {
				io.WriteString(w, ",")
				writeJSONInt(w, "size", f.size)
			}
			io.WriteString(w, ",\"value\":")
			if f.hasConstValue {
				if f.typ == "BLOB" {
					io.WriteString(w, `"`)
					io.WriteString(w, f.constValue)
					io.WriteString(w, `"`)
				} else if err := writeJSONScalar(w, reflect.ValueOf(f.constValue), f.typ, o); err != nil {
					return err
				}
			} else if e := writeJSONScalar(w, zero.FieldByIndex(f.index), f.typ, o); e != nil {
				return e
			}
			io.WriteString(w, "}")
		}
		io.WriteString(w, "]")
	}
	io.WriteString(w, "}")
	return nil
}
func writeJSONKey(w io.Writer, k, v string) error {
	b, _ := json.Marshal(k)
	w.Write(b)
	io.WriteString(w, ":")
	b, _ = json.Marshal(v)
	_, e := w.Write(b)
	return e
}
func writeJSONName(w io.Writer, k string) {
	b, _ := json.Marshal(k)
	w.Write(b)
	io.WriteString(w, ":")
}
func writeJSONInt(w io.Writer, k string, v int) error {
	b, _ := json.Marshal(k)
	if _, e := w.Write(b); e != nil {
		return e
	}
	if _, e := io.WriteString(w, ":"); e != nil {
		return e
	}
	_, e := io.WriteString(w, strconv.Itoa(v))
	return e
}
func writeJSONScalar(w io.Writer, val reflect.Value, typ string, o options) error {
	if unwrapped, _, null := optionalValueState(val); null {
		_, e := io.WriteString(w, "null")
		return e
	} else if unwrapped.IsValid() {
		val = unwrapped
	} else if val.Kind() == reflect.Pointer {
		val = reflect.Zero(val.Type().Elem())
	}
	if typ == "BLOB" {
		io.WriteString(w, "\"")
		var r io.ReadCloser
		switch v := val.Interface().(type) {
		case []byte:
			r = io.NopCloser(bytes.NewReader(v))
		case BlobSource:
			var e error
			r, e = v.Open()
			if e != nil {
				return e
			}
		default:
			return &TypeError{Want: "[]byte or BlobSource", Got: typeName(val.Interface())}
		}
		enc := base64.NewEncoder(base64.StdEncoding, w)
		_, e := io.Copy(enc, r)
		ce := enc.Close()
		re := r.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
		if re != nil {
			return re
		}
		_, e = io.WriteString(w, "\"")
		return e
	}
	ss, e := encodeScalar(val.Interface(), typ)
	if e != nil {
		return e
	}
	if typ != "DECIMAL" && typ != "BIGDECIMAL" && ((typ == "SHORT" || typ == "INT" || typ == "LONG" || typ == "UINT" || typ == "ULONG" || typ == "FLOAT" || typ == "DOUBLE" || typ == "BOOLEAN") && json.Valid([]byte(ss)) || ((typ == "DECIMAL" || typ == "BIGDECIMAL") && o.bigDecimal == BigDecimalAsNumber && json.Valid([]byte(ss)))) {
		_, e = io.WriteString(w, ss)
		return e
	}
	b, _ := json.Marshal(ss)
	_, e = w.Write(b)
	return e
}

func rowTypeJSON(t RowType) string {
	switch t {
	case RowInsert:
		return "I"
	case RowUpdate:
		return "U"
	case RowDelete:
		return "D"
	default:
		return "N"
	}
}

const rowOriginal RowType = 4

func parseRowType(s string) RowType {
	switch strings.ToUpper(s) {
	case "I":
		return RowInsert
	case "U":
		return RowUpdate
	case "D":
		return RowDelete
	case "O":
		return rowOriginal
	default:
		return RowNormal
	}
}

func validRowTypeJSON(s string) bool {
	switch strings.ToUpper(s) {
	case "N", "I", "U", "D", "O":
		return true
	default:
		return false
	}
}
func decodeJSON(r io.Reader, dst any, o options) error {
	v := reflect.ValueOf(dst)
	if v.Kind() != reflect.Pointer || v.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("xapi: destination must be pointer to struct")
	}
	s, e := compileSchema(v.Elem().Type())
	if e != nil {
		return e
	}
	if e = enforceSchemaLimits(s, o); e != nil {
		return e
	}
	return decodeJSONStreaming(r, dst, o, s)
}

func decodeParameters(dec *json.Decoder, root reflect.Value, s *schema, o options) error {
	if err := expectJSON(dec, '['); err != nil {
		return err
	}
	for dec.More() {
		if err := expectJSON(dec, '{'); err != nil {
			return err
		}
		var id string
		var value string
		var valueNull, valueSeen bool
		keys := map[string]bool{}
		for dec.More() {
			t, e := dec.Token()
			if e != nil {
				return e
			}
			k := t.(string)
			if keys[k] && o.duplicates {
				return fmt.Errorf("xapi: duplicate parameter key %q", k)
			}
			keys[k] = true
			switch k {
			case "id":
				if e := dec.Decode(&id); e != nil {
					return e
				}
			case "value":
				tok, e := dec.Token()
				if e != nil {
					return e
				}
				value, valueNull, e = jsonScalarToken(tok)
				if e != nil {
					return e
				}
				valueSeen = true
			default:
				if e := skipJSON(dec, o.limits.MaxDepth, 0); e != nil {
					return e
				}
			}
		}
		if _, e := dec.Token(); e != nil {
			return e
		}
		if o.strict && id == "" {
			return fmt.Errorf("xapi: parameter id is required")
		}
		for _, f := range s.params {
			if f.name == id {
				if !valueSeen {
					continue
				}
				var e error
				if !valueNull && f.typ != "BLOB" && o.limits.MaxScalarBytes > 0 && int64(len(value)) > o.limits.MaxScalarBytes {
					return &PathError{Path: id, Err: fmt.Errorf("scalar limit exceeded")}
				}
				if valueNull {
					e = assignJSONNull(root.FieldByIndex(f.index), o.jsonNull)
				} else if f.typ == "BLOB" {
					e = assignJSONBlobString(root.FieldByIndex(f.index), value, o.limits.MaxBlobBytes, o.base64WS)
				} else {
					e = assignWithOptions(root.FieldByIndex(f.index), value, f.typ, o.limits.MaxBlobBytes, o)
				}
				if e != nil {
					return &PathError{Path: id, Err: e}
				}
			}
		}
	}
	return expectJSON(dec, ']')
}
func decodeDatasets(dec *json.Decoder, root reflect.Value, s *schema, o options, seen map[string]bool) error {
	if err := expectJSON(dec, '['); err != nil {
		return err
	}
	count := 0
	for dec.More() {
		count++
		if o.limits.MaxDatasets > 0 && count > o.limits.MaxDatasets {
			return fmt.Errorf("xapi: dataset limit exceeded")
		}
		if err := decodeDataset(dec, root, s, o, seen); err != nil {
			return err
		}
	}
	return expectJSON(dec, ']')
}
func decodeDataset(dec *json.Decoder, root reflect.Value, s *schema, o options, seen map[string]bool) error {
	if err := expectJSON(dec, '{'); err != nil {
		return err
	}
	var id string
	var d *dataset
	consts := map[string]json.RawMessage{}
	wireTypes := map[string]string{}
	var pendingRows json.RawMessage
	keys := map[string]bool{}
	idSeen, columnInfoSeen, rowsSeen, columnListSeen := false, false, false, false
	for dec.More() {
		t, e := dec.Token()
		if e != nil {
			return e
		}
		k := t.(string)
		if keys[k] && o.duplicates {
			return fmt.Errorf("xapi: duplicate dataset key %q", k)
		}
		keys[k] = true
		switch k {
		case "id":
			if e := dec.Decode(&id); e != nil {
				return e
			}
			idSeen = true
			for i := range s.datasets {
				if s.datasets[i].name == id {
					if seen[id] && o.duplicates {
						return fmt.Errorf("xapi: duplicate dataset %q", id)
					}
					d = &s.datasets[i]
					seen[id] = true
				}
			}
		case "ColumnInfo":
			columnInfoSeen = true
			if e := decodeColumnInfo(dec, consts, wireTypes, o.limits.MaxDepth, o.limits.MaxColumns, o.duplicates, &columnListSeen); e != nil {
				return e
			}
		case "Rows":
			rowsSeen = true
			if d == nil {
				if e := dec.Decode(&pendingRows); e != nil {
					return e
				}
			} else if e := decodeRows(dec, root, d, o, consts); e != nil {
				return e
			}
		default:
			if e := skipJSON(dec, o.limits.MaxDepth, 0); e != nil {
				return e
			}
		}
	}
	if _, e := dec.Token(); e != nil {
		return e
	}
	if d != nil {
		if e := validateWireColumns(d, wireTypes, o); e != nil {
			return e
		}
	}
	if o.strict {
		if !idSeen || id == "" {
			return fmt.Errorf("xapi: dataset id is required")
		}
		if !columnInfoSeen || (!columnListSeen && len(consts) == 0) || !rowsSeen {
			return fmt.Errorf("xapi: dataset ColumnInfo/Column/Rows are required")
		}
		if d == nil {
			return fmt.Errorf("xapi: unknown dataset %q", id)
		}
	}
	if d != nil && len(pendingRows) > 0 {
		rd := json.NewDecoder(bytes.NewReader(pendingRows))
		rd.UseNumber()
		if e := decodeRows(rd, root, d, o, consts); e != nil {
			return e
		}
	}
	return nil
}
func decodeColumnInfo(dec *json.Decoder, consts map[string]json.RawMessage, wireTypes map[string]string, maxDepth, maxColumns int, duplicates bool, columnListSeen *bool) error {
	if err := expectJSON(dec, '{'); err != nil {
		return err
	}
	keys := map[string]bool{}
	columnCount := 0
	for dec.More() {
		t, e := dec.Token()
		if e != nil {
			return e
		}
		k := t.(string)
		if keys[k] && duplicates {
			return fmt.Errorf("xapi: duplicate ColumnInfo key %q", k)
		}
		keys[k] = true
		if k != "ConstColumn" {
			if k == "Column" {
				*columnListSeen = true
				if e := expectJSON(dec, '['); e != nil {
					return e
				}
				for dec.More() {
					columnCount++
					if maxColumns > 0 && columnCount > maxColumns {
						return fmt.Errorf("xapi: column limit exceeded")
					}
					if e := expectJSON(dec, '{'); e != nil {
						return e
					}
					var id, typ string
					for dec.More() {
						t, e := dec.Token()
						if e != nil {
							return e
						}
						switch t.(string) {
						case "id":
							e = dec.Decode(&id)
						case "type":
							e = dec.Decode(&typ)
						default:
							e = skipJSON(dec, maxDepth, 2)
						}
						if e != nil {
							return e
						}
					}
					if _, e := dec.Token(); e != nil {
						return e
					}
					if id != "" {
						if _, exists := wireTypes[id]; exists && duplicates {
							return fmt.Errorf("xapi: duplicate ColumnInfo column %q", id)
						}
						wireTypes[id] = strings.ToUpper(typ)
					}
				}
				if e := expectJSON(dec, ']'); e != nil {
					return e
				}
				continue
			}
			if e := skipJSON(dec, maxDepth, 0); e != nil {
				return e
			}
			continue
		}
		if e := expectJSON(dec, '['); e != nil {
			return e
		}
		for dec.More() {
			columnCount++
			if maxColumns > 0 && columnCount > maxColumns {
				return fmt.Errorf("xapi: column limit exceeded")
			}
			if e := expectJSON(dec, '{'); e != nil {
				return e
			}
			var id string
			var typ string
			var value json.RawMessage
			constKeys := map[string]bool{}
			for dec.More() {
				t, e := dec.Token()
				if e != nil {
					return e
				}
				key := t.(string)
				if constKeys[key] && duplicates {
					return fmt.Errorf("xapi: duplicate ConstColumn key %q", key)
				}
				constKeys[key] = true
				switch key {
				case "id":
					e = dec.Decode(&id)
				case "type":
					e = dec.Decode(&typ)
				case "value":
					e = dec.Decode(&value)
				default:
					e = skipJSON(dec, maxDepth, 0)
				}
				if e != nil {
					return e
				}
			}
			if _, e = dec.Token(); e != nil {
				return e
			}
			if id != "" {
				if _, exists := wireTypes[id]; exists && duplicates {
					return fmt.Errorf("xapi: duplicate ColumnInfo column %q", id)
				}
				consts[id] = value
				wireTypes[id] = strings.ToUpper(typ)
			}
		}
		if e := expectJSON(dec, ']'); e != nil {
			return e
		}
	}
	return expectJSON(dec, '}')
}
func decodeRows(dec *json.Decoder, root reflect.Value, d *dataset, o options, consts map[string]json.RawMessage) error {
	if err := expectJSON(dec, '['); err != nil {
		return err
	}
	target := root.FieldByIndex(d.index)
	var pending *reflect.Value
	rowTracking := d.tracked || d.orgRowIndex != nil
	for ri := 0; dec.More(); ri++ {
		if o.limits.MaxRows > 0 && ri >= o.limits.MaxRows {
			return fmt.Errorf("xapi: row limit exceeded for %s", d.name)
		}
		rv, status, e := decodeRow(dec, d, o, ri, consts)
		if e != nil {
			return e
		}
		if rowTracking && status == RowUpdate {
			pending = &rv
			continue
		}
		if rowTracking && status == rowOriginal {
			if pending == nil {
				if o.strict {
					return fmt.Errorf("xapi: original row without update in %s[%d]", d.name, ri)
				}
				continue
			}
			if d.tracked {
				tr := reflect.New(target.Type().Elem()).Elem()
				tr.FieldByName("Current").Set(*pending)
				p := reflect.New(d.elem)
				p.Elem().Set(rv)
				tr.FieldByName("Original").Set(p)
				tr.FieldByName("Type").SetUint(uint64(RowUpdate))
				appendReflect(target, tr)
			} else {
				current := *pending
				p := reflect.New(d.elem)
				p.Elem().Set(rv)
				current.FieldByIndex(d.orgRowIndex).Set(p)
				appendReflect(target, current)
			}
			pending = nil
			continue
		}
		if rowTracking && pending != nil {
			if o.strict {
				return fmt.Errorf("xapi: update row missing original in %s[%d]", d.name, ri)
			}
			if d.tracked {
				tr := reflect.New(target.Type().Elem()).Elem()
				tr.FieldByName("Current").Set(*pending)
				tr.FieldByName("Type").SetUint(uint64(RowUpdate))
				appendReflect(target, tr)
			} else {
				appendReflect(target, *pending)
			}
			pending = nil
		}
		if rowTracking {
			if status == rowOriginal {
				continue
			}
			tr := reflect.New(target.Type().Elem()).Elem()
			tr.FieldByName("Current").Set(rv)
			tr.FieldByName("Type").SetUint(uint64(status))
			appendReflect(target, tr)
		} else if status != RowNormal && d.rowTypeIndex == nil {
			if o.strict {
				return fmt.Errorf("xapi: row status %s requires Tracked dataset", d.name)
			}
		} else {
			appendReflect(target, rv)
		}
	}
	if pending != nil {
		if o.strict {
			return fmt.Errorf("xapi: update row missing original in %s", d.name)
		}
		if d.tracked {
			tr := reflect.New(target.Type().Elem()).Elem()
			tr.FieldByName("Current").Set(*pending)
			tr.FieldByName("Type").SetUint(uint64(RowUpdate))
			appendReflect(target, tr)
		} else {
			appendReflect(target, *pending)
		}
	}
	return expectJSON(dec, ']')
}
func decodeRow(dec *json.Decoder, d *dataset, o options, ri int, consts map[string]json.RawMessage) (reflect.Value, RowType, error) {
	if err := expectJSON(dec, '{'); err != nil {
		return reflect.Value{}, 0, err
	}
	rv := reflect.New(d.elem).Elem()
	status := RowNormal
	var seenFields []uint64
	if o.duplicates {
		seenFields = make([]uint64, (len(d.fields)+63)/64)
	}
	var seenUnknown []string
	seenRowType := false
	for _, f := range d.fields {
		if f.constant {
			if raw, ok := consts[f.name]; ok {
				if f.typ == "BLOB" && !bytes.Equal(raw, []byte("null")) {
					if e := assignJSONBlob(rv.FieldByIndex(f.index), raw, o.limits.MaxBlobBytes, o.base64WS); e != nil {
						return rv, 0, e
					}
					continue
				}
				ss, null, e := rawScalar(raw)
				if e != nil {
					return rv, 0, e
				}
				if null {
					e = assignJSONNull(rv.FieldByIndex(f.index), o.jsonNull)
				} else if f.typ == "BLOB" {
					e = assignJSONBlob(rv.FieldByIndex(f.index), raw, o.limits.MaxBlobBytes, o.base64WS)
				} else {
					e = assignWithOptions(rv.FieldByIndex(f.index), ss, f.typ, o.limits.MaxBlobBytes, o)
				}
				if e != nil {
					return rv, 0, e
				}
			}
		}
	}
	for dec.More() {
		t, e := dec.Token()
		if e != nil {
			return rv, 0, e
		}
		key := t.(string)
		if key == "_RowType_" {
			if seenRowType && o.duplicates {
				return rv, 0, &PathError{Path: fmt.Sprintf("%s[%d].%s", d.name, ri, key), Err: fmt.Errorf("duplicate JSON key")}
			}
			seenRowType = true
			tok, e := dec.Token()
			if e != nil {
				return rv, 0, e
			}
			ss, _, e := jsonScalarToken(tok)
			if e != nil {
				return rv, 0, e
			}
			if o.strict && !validRowTypeJSON(ss) {
				return rv, 0, fmt.Errorf("xapi: invalid row type %q", ss)
			}
			status = parseRowType(ss)
			continue
		}
		var f *field
		fieldIndex, known := d.lookup[key]
		if known {
			if o.duplicates {
				word, bit := fieldIndex/64, uint(fieldIndex%64)
				if seenFields[word]&(uint64(1)<<bit) != 0 {
					return rv, 0, &PathError{Path: fmt.Sprintf("%s[%d].%s", d.name, ri, key), Err: fmt.Errorf("duplicate JSON key")}
				}
				seenFields[word] |= uint64(1) << bit
			}
			f = &d.fields[fieldIndex]
		}
		if f == nil {
			if o.duplicates {
				for _, prior := range seenUnknown {
					if prior == key {
						return rv, 0, &PathError{Path: fmt.Sprintf("%s[%d].%s", d.name, ri, key), Err: fmt.Errorf("duplicate JSON key")}
					}
				}
				seenUnknown = append(seenUnknown, key)
			}
			if o.unknown == ErrorUnknownColumns {
				return rv, 0, &PathError{Path: fmt.Sprintf("%s[%d].%s", d.name, ri, key), Err: fmt.Errorf("unknown column")}
			}
			if e := skipJSON(dec, o.limits.MaxDepth, 0); e != nil {
				return rv, 0, e
			}
			continue
		}
		if f.typ == "BLOB" {
			tok, e := dec.Token()
			if e != nil {
				return rv, 0, e
			}
			if tok == nil {
				if e = assignJSONNull(rv.FieldByIndex(f.index), o.jsonNull); e != nil {
					return rv, 0, e
				}
			} else if ss, ok := tok.(string); ok {
				if e = assignJSONBlobString(rv.FieldByIndex(f.index), ss, o.limits.MaxBlobBytes, o.base64WS); e != nil {
					return rv, 0, &PathError{Path: fmt.Sprintf("%s[%d].%s", d.name, ri, key), Err: e}
				}
			} else {
				return rv, 0, fmt.Errorf("BLOB JSON value must be a string")
			}
			continue
		}
		tok, e := dec.Token()
		if e != nil {
			return rv, 0, e
		}
		ss, null, e := jsonScalarToken(tok)
		if e != nil {
			return rv, 0, e
		}
		if f.typ != "BLOB" && o.limits.MaxScalarBytes > 0 && int64(len(ss)) > o.limits.MaxScalarBytes {
			return rv, 0, fmt.Errorf("xapi: scalar limit exceeded")
		}
		if null {
			e = assignJSONNull(rv.FieldByIndex(f.index), o.jsonNull)
		} else {
			e = assignWithOptions(rv.FieldByIndex(f.index), ss, f.typ, o.limits.MaxBlobBytes, o)
		}
		if e != nil {
			return rv, 0, &PathError{Path: fmt.Sprintf("%s[%d].%s", d.name, ri, key), Err: e}
		}
	}
	_, e := dec.Token()
	if d.rowTypeIndex != nil {
		rv.FieldByIndex(d.rowTypeIndex).SetUint(uint64(status))
	}
	return rv, status, e
}
func expectJSON(dec *json.Decoder, want json.Delim) error {
	t, e := dec.Token()
	if e != nil {
		return e
	}
	if d, ok := t.(json.Delim); !ok || d != want {
		return fmt.Errorf("xapi: expected JSON %q", want)
	}
	return nil
}
func skipJSON(dec *json.Decoder, maxDepth, depth int) error {
	t, e := dec.Token()
	if e != nil {
		return e
	}
	if d, ok := t.(json.Delim); ok {
		if d == '{' || d == '[' {
			if maxDepth > 0 && depth+1 > maxDepth {
				return fmt.Errorf("xapi: JSON depth limit exceeded")
			}
			close := json.Delim('}')
			if d == '[' {
				close = ']'
			}
			for dec.More() {
				if d == '{' {
					if _, e = dec.Token(); e != nil {
						return e
					}
				}
				if e = skipJSON(dec, maxDepth, depth+1); e != nil {
					return e
				}
			}
			_, e = dec.Token()
			_ = close
		}
	}
	return e
}

func rawScalar(raw json.RawMessage) (string, bool, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", true, nil
	}
	if raw[0] == '"' {
		var s string
		e := json.Unmarshal(raw, &s)
		return s, false, e
	}
	return string(raw), false, nil
}

func jsonScalarToken(tok json.Token) (string, bool, error) {
	switch v := tok.(type) {
	case nil:
		return "", true, nil
	case string:
		return v, false, nil
	case json.Number:
		return v.String(), false, nil
	case bool:
		return strconv.FormatBool(v), false, nil
	default:
		return "", false, fmt.Errorf("xapi: JSON value must be scalar")
	}
}
func assignJSONBlob(dst reflect.Value, raw json.RawMessage, max int64, allowWhitespace bool) error {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return fmt.Errorf("BLOB JSON value must be a string")
	}
	payload := raw[1 : len(raw)-1]
	if bytes.IndexByte(payload, '\\') >= 0 {
		ss, _, e := rawScalar(raw)
		if e != nil {
			return e
		}
		return assignLimitedWithWhitespace(dst, ss, "BLOB", max, allowWhitespace)
	}
	a := blobAccumulator{max: max, allowWhitespace: allowWhitespace}
	if e := a.Write(payload); e != nil {
		return e
	}
	b, e := a.Close()
	if e != nil {
		return e
	}
	return assignBlobBytes(dst, b)
}
func assignJSONBlobString(dst reflect.Value, s string, max int64, allowWhitespace bool) error {
	b, e := decodeBlobString(s, max, allowWhitespace)
	if e != nil {
		return e
	}
	return assignBlobBytes(dst, b)
}
