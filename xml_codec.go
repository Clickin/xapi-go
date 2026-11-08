package xapi

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-xml-experiment/xml/xmltext"
)

func encodeXML(w io.Writer, src any, o options, p Profile) error {
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
	out := &controlEntityWriter{w: w}
	x := newXMLWriter(out, p.namespace())
	if _, e = io.WriteString(out, `<?xml version="1.0" encoding="utf-8"?>`); e != nil {
		return e
	}
	versionAttr := o.xmlVersionAttr
	if versionAttr == "" {
		versionAttr = "ver"
	}
	version := "4000"
	if p == XPlatformXML5000 {
		version = "5000"
	}
	if e = x.begin("Root", xmlnsAttr(p.namespace()), xmlAttrOf(versionAttr, version)); e != nil {
		return e
	}
	if len(s.params) > 0 {
		if e = encodeParams(x, out, v, s.params, o); e != nil {
			return e
		}
	}
	for _, d := range s.datasets {
		if e = encodeDataset(x, out, v.FieldByIndex(d.index), d); e != nil {
			return e
		}
	}
	if e = x.end(); e != nil {
		return e
	}
	if e = x.Close(); e != nil {
		return e
	}
	return out.Flush()
}
func encodeParams(x *xmlWriter, out io.Writer, v reflect.Value, fs []field, o options) error {
	st := "Parameters"
	if e := x.begin(st); e != nil {
		return e
	}
	for _, f := range fs {
		val := v.FieldByIndex(f.index)
		if (f.optional || optionalField(val)) && missingValue(val) {
			continue
		}
		a := []xmltext.Attr{xmlAttrOf("id", f.name), xmlAttrOf("type", strings.ToLower(f.typ))}
		if f.size > 0 {
			a = append(a, xmlAttrOf("size", strconv.Itoa(f.size)))
		}
		if o.xmlParamValueAttr && f.typ != "BLOB" {
			ss, e := encodeScalar(val.Interface(), f.typ)
			if e != nil {
				return e
			}
			a = append(a, xmlAttrOf("value", ss))
			if e = x.begin("Parameter", a...); e != nil {
				return e
			}
			if e = x.end(); e != nil {
				return e
			}
			continue
		}
		if e := encodeXMLScalarElement(x, out, a, "Parameter", val, f.typ); e != nil {
			return e
		}
	}
	return x.end()
}
func encodeDataset(x *xmlWriter, out io.Writer, v reflect.Value, d dataset) error {
	a := []xmltext.Attr{xmlAttrOf("id", d.name)}
	if e := x.begin("Dataset", a...); e != nil {
		return e
	}
	ci := "ColumnInfo"
	if e := x.begin(ci); e != nil {
		return e
	}
	for _, f := range d.fields {
		name := "Column"
		attrs := []xmltext.Attr{xmlAttrOf("id", f.name), xmlAttrOf("type", strings.ToLower(f.typ))}
		if f.size > 0 {
			attrs = append(attrs, xmlAttrOf("size", strconv.Itoa(f.size)))
		}
		if f.typ == "BLOB" {
			attrs = append(attrs, xmlAttrOf("enc", "base64"))
		}
		if f.constant {
			name = "ConstColumn"
			if f.typ == "BLOB" {
				if e := writeXMLConstBlob(x, out, d, f, attrs); e != nil {
					return e
				}
				continue
			}
			ss := f.constValue
			if !f.hasConstValue {
				zero := reflect.New(d.elem).Elem()
				var e error
				ss, e = encodeScalar(zero.FieldByIndex(f.index).Interface(), f.typ)
				if e != nil {
					return e
				}
			}
			attrs = append(attrs, xmlAttrOf("value", ss))
		}
		if e := x.begin(name, attrs...); e != nil {
			return e
		}
		if e := x.end(); e != nil {
			return e
		}
	}
	if e := x.end(); e != nil {
		return e
	}
	rows := "Rows"
	if e := x.begin(rows); e != nil {
		return e
	}
	for i := 0; i < v.Len(); i++ {
		rv := v.Index(i)
		if rv.Kind() == reflect.Pointer {
			rv = rv.Elem()
		}
		rowType := RowNormal
		var original reflect.Value
		if d.tracked {
			rowType = RowType(rv.FieldByName("Type").Uint())
			original = rv.FieldByName("Original")
			rv = rv.FieldByName("Current")
		} else if d.rowTypeIndex != nil {
			rowType = RowType(rv.FieldByIndex(d.rowTypeIndex).Uint())
			if d.orgRowIndex != nil {
				original = rv.FieldByIndex(d.orgRowIndex)
			}
		}
		var rowAttrs []xmltext.Attr
		if rowType != RowNormal {
			rowAttrs = []xmltext.Attr{xmlAttrOf("type", rowTypeXML(rowType))}
		}
		if e := x.begin("Row", rowAttrs...); e != nil {
			return e
		}
		for _, f := range d.fields {
			if f.constant {
				continue
			}
			val := rv.FieldByIndex(f.index)
			if (f.optional || optionalField(val)) && missingValue(val) {
				continue
			}
			aa := []xmltext.Attr{xmlAttrOf("id", f.name)}
			if e := encodeXMLScalarElement(x, out, aa, "Col", val, f.typ); e != nil {
				return e
			}
		}
		if (d.tracked || d.orgRowIndex != nil) && rowType == RowUpdate && original.IsValid() && !original.IsNil() {
			if e := x.begin("OrgRow"); e != nil {
				return e
			}
			ov := original.Elem()
			for _, f := range d.fields {
				if f.constant {
					continue
				}
				if (f.optional || optionalField(ov.FieldByIndex(f.index))) && missingValue(ov.FieldByIndex(f.index)) {
					continue
				}
				if e := encodeXMLScalarElement(x, out, []xmltext.Attr{xmlAttrOf("id", f.name)}, "Col", ov.FieldByIndex(f.index), f.typ); e != nil {
					return e
				}
			}
			if e := x.end(); e != nil {
				return e
			}
		}
		if e := x.end(); e != nil {
			return e
		}
	}
	if e := x.end(); e != nil {
		return e
	}
	return x.end()
}

func writeXMLConstBlob(x *xmlWriter, out io.Writer, d dataset, f field, attrs []xmltext.Attr) error {
	if err := x.sync(); err != nil {
		return err
	}
	if _, err := io.WriteString(out, "<ConstColumn"); err != nil {
		return err
	}
	for _, a := range attrs {
		if _, err := io.WriteString(out, " "+a.Name.Local+"=\""); err != nil {
			return err
		}
		if _, err := io.WriteString(out, xmlAttrEscaper.Replace(a.Value)+`"`); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(out, ` value="`); err != nil {
		return err
	}
	if f.hasConstValue {
		if _, err := io.WriteString(out, f.constValue); err != nil {
			return err
		}
		_, err := io.WriteString(out, `"></ConstColumn>`)
		return err
	}
	var r io.ReadCloser
	if f.hasConstValue {
		r = io.NopCloser(strings.NewReader(f.constValue))
	} else {
		zero := reflect.New(d.elem).Elem()
		val := zero.FieldByIndex(f.index)
		switch v := val.Interface().(type) {
		case []byte:
			r = io.NopCloser(bytes.NewReader(v))
		case BlobSource:
			var err error
			r, err = v.Open()
			if err != nil {
				return err
			}
		default:
			return &TypeError{Want: "[]byte or BlobSource", Got: typeName(val.Interface())}
		}
	}
	enc := base64.NewEncoder(base64.StdEncoding, out)
	_, copyErr := io.Copy(enc, r)
	closeErr := enc.Close()
	readCloseErr := r.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if readCloseErr != nil {
		return readCloseErr
	}
	_, err := io.WriteString(out, `"></ConstColumn>`)
	return err
}

func encodeXMLScalarElement(x *xmlWriter, out io.Writer, attrs []xmltext.Attr, name string, val reflect.Value, typ string) error {
	if unwrapped, _, _ := optionalValueState(val); unwrapped.IsValid() {
		val = unwrapped
	} else if val.Kind() == reflect.Pointer {
		val = reflect.Zero(val.Type().Elem())
	}
	if e := x.begin(name, attrs...); e != nil {
		return e
	}
	if typ == "BLOB" {
		if e := x.sync(); e != nil {
			return e
		}
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
		enc := base64.NewEncoder(base64.StdEncoding, out)
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
	} else {
		ss, e := encodeScalar(val.Interface(), typ)
		if e != nil {
			return e
		}
		if ss != "" {
			encoded, e := xmlText(ss)
			if e != nil {
				return e
			}
			if e = x.Text(encoded); e != nil {
				return e
			}
		}
	}
	if e := x.end(); e != nil {
		return e
	}
	return x.Flush()
}
func decodeXML(r io.Reader, dst any, o options, p Profile) error {
	opts := []xmltext.Options{xmltext.SplitText(true), xmltext.MaxTokenSize(0)}
	if o.compatibility {
		opts = append(opts, xmltext.Permissive(true))
	}
	if !o.strict {
		// The reference reader takes already decoded text, so the declared
		// encoding has no effect. Non-UTF-8 bytes still fail as invalid UTF-8.
		opts = append(opts, xmltext.WithCharsetReader(func(_ string, r io.Reader) (io.Reader, error) { return r, nil }))
	}
	dec := xmltext.NewDecoder(&controlEntityReader{r: &documentReader{r: r, max: o.limits.MaxDocumentBytes}}, opts...)
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
	var cur *dataset
	seenDatasets := map[string]bool{}
	datasetCount, rowCount := 0, 0
	wireColumnCount := 0
	var row reflect.Value
	var original reflect.Value
	rowType := RowNormal
	readingOriginal := false
	var col string
	var text strings.Builder
	constValues := map[string]string{}
	wireColumnIDs := map[string]bool{}
	constID, constValue := "", ""
	legacyStrict := false
	depth := 0
	declarationSeen := false
	sawColumn := false
	rootSeen := false
	valueFromAttr := false // the current scalar holds a Parameter's value attribute
	var blob *blobAccumulator
	var blobTarget reflect.Value
	var attrBuf []xmltext.Attr
	for {
		t, e := dec.ReadToken()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		switch t.Kind() {
		case xmltext.StartElement:
			attrBuf = attrBuf[:0]
			for it := t.Attrs(); it.Next(); {
				attrBuf = append(attrBuf, xmltext.Attr{Name: it.Name(), Value: string(it.Value())})
			}
			q := xmlStartTag{Name: t.Name(), Attr: attrBuf}
			attrs := map[string]bool{}
			for _, a := range q.Attr {
				key := a.Name.Space + "\x00" + a.Name.Local
				if attrs[key] {
					return fmt.Errorf("xapi: duplicate XML attribute %q", a.Name.Local)
				}
				attrs[key] = true
			}
			depth++
			if o.limits.MaxDepth > 0 && depth > o.limits.MaxDepth {
				return fmt.Errorf("xapi: XML depth limit exceeded")
			}
			if o.strict && depth == 1 {
				legacyStrict = q.Name.Space == "" && !declarationSeen
				if !hasAttr(q, "ver") && !hasAttr(q, "version") && !legacyStrict {
					return fmt.Errorf("xapi: XML version attribute is required")
				}
			}
			if depth == 1 {
				if rootSeen || q.Name.Local != "Root" {
					return fmt.Errorf("xapi: XML root must be Root")
				}
				// The root namespace is not checked in any mode, and the version
				// only in strict mode: XPLATFORM/Nexacro peers vary in both.
				if o.strict {
					expectedVersion := "4000"
					if p == XPlatformXML5000 {
						expectedVersion = "5000"
					}
					if ver := attr(q, "ver"); ver != "" && ver != expectedVersion {
						return fmt.Errorf("xapi: unsupported XML version %q", ver)
					}
					if ver := attr(q, "version"); ver != "" && ver != expectedVersion {
						return fmt.Errorf("xapi: unsupported XML version %q", ver)
					}
				}
				rootSeen = true
			}
			if o.strict && depth == 2 && q.Name.Local != "Parameters" && q.Name.Local != "Dataset" {
				return fmt.Errorf("xapi: unexpected XML root child %q", q.Name.Local)
			}
			if o.strict && q.Name.Local == "Datasets" {
				return fmt.Errorf("xapi: Datasets wrapper is not allowed")
			}
			switch q.Name.Local {
			case "Parameter":
				id := attr(q, "id")
				if o.strict && id == "" {
					return fmt.Errorf("xapi: Parameter id is required")
				}
				text.Reset()
				blob = nil
				valueFromAttr = false
				for _, f := range s.params {
					if f.name == id {
						cur = nil
						row = v.Elem()
						col = "@" + id
						if f.typ == "BLOB" {
							blob = &blobAccumulator{max: o.limits.MaxBlobBytes, allowWhitespace: o.base64WS}
							blobTarget = v.Elem().FieldByIndex(f.index)
						}
						if hasAttr(q, "value") {
							valueFromAttr = true
							value := attr(q, "value")
							if blob != nil {
								if e := blob.Write([]byte(value)); e != nil {
									return &PathError{Path: id, Err: e}
								}
							} else {
								text.WriteString(value)
							}
						}
					}
				}
			case "Dataset":
				datasetCount++
				if o.limits.MaxDatasets > 0 && datasetCount > o.limits.MaxDatasets {
					return fmt.Errorf("xapi: dataset limit exceeded")
				}
				id := attr(q, "id")
				if id == "" {
					return fmt.Errorf("xapi: Dataset id is required")
				}
				sawColumn = false
				row = reflect.Value{}
				for i := range s.datasets {
					if s.datasets[i].name == id {
						if seenDatasets[id] && o.strict {
							return fmt.Errorf("xapi: duplicate dataset %q", id)
						}
						cur = &s.datasets[i]
						constValues = map[string]string{}
						wireColumnIDs = map[string]bool{}
						wireColumnCount = 0
						seenDatasets[id] = true
					}
				}
			case "Row":
				if cur != nil {
					rowCount++
					if o.limits.MaxRows > 0 && rowCount > o.limits.MaxRows {
						return fmt.Errorf("xapi: row limit exceeded")
					}
					row = reflect.New(cur.elem).Elem()
					for _, f := range cur.fields {
						if f.constant {
							if value, ok := constValues[f.name]; ok {
								var e error
								if f.typ == "BLOB" {
									var b []byte
									b, e = decodeBlobString(value, o.limits.MaxBlobBytes, o.base64WS)
									if e == nil {
										e = assignBlobBytes(row.FieldByIndex(f.index), b)
									}
								} else {
									e = assignWithOptions(row.FieldByIndex(f.index), value, f.typ, o.limits.MaxBlobBytes, o)
								}
								if e != nil {
									return e
								}
							}
						}
					}
					rowTypeName := attr(q, "type")
					if o.strict && rowTypeName != "" && !validRowTypeXML(rowTypeName) {
						return fmt.Errorf("xapi: invalid row type %q", rowTypeName)
					}
					rowType = parseRowTypeXML(rowTypeName)
					original = reflect.Value{}
					readingOriginal = false
				}
			case "OrgRow", "Org_Row":
				if cur != nil {
					if q.Name.Local == "Org_Row" && o.strict {
						return fmt.Errorf("xapi: Org_Row is not allowed in strict mode")
					}
					if !row.IsValid() {
						return fmt.Errorf("xapi: OrgRow outside row in %s", cur.name)
					}
					if original.IsValid() && o.strict {
						return fmt.Errorf("xapi: duplicate OrgRow in %s", cur.name)
					}
					original = reflect.New(cur.elem).Elem()
					readingOriginal = true
				}
			case "ConstColumn":
				if cur != nil {
					wireColumnCount++
					if o.limits.MaxColumns > 0 && wireColumnCount > o.limits.MaxColumns {
						return fmt.Errorf("xapi: column limit exceeded")
					}
					if o.strict && sawColumn {
						return fmt.Errorf("xapi: ConstColumn must precede Column")
					}
					constID = attr(q, "id")
					constValue = attr(q, "value")
					if o.strict && constID == "" {
						return fmt.Errorf("xapi: ConstColumn id is required")
					}
					if o.strict && !legacyStrict && strings.EqualFold(attr(q, "type"), "BLOB") && !validXMLBlobEncoding(q, o) {
						return fmt.Errorf("xapi: BLOB encoding must be base64")
					}
					if wireColumnIDs[constID] && o.strict {
						return fmt.Errorf("xapi: duplicate ColumnInfo column %q", constID)
					}
					wireColumnIDs[constID] = true
					if e := validateXMLColumn(cur, attr(q, "id"), attr(q, "type"), o); e != nil {
						return e
					}
				}
			case "Column":
				if cur != nil {
					wireColumnCount++
					if o.limits.MaxColumns > 0 && wireColumnCount > o.limits.MaxColumns {
						return fmt.Errorf("xapi: column limit exceeded")
					}
					columnID := attr(q, "id")
					if o.strict && columnID == "" {
						return fmt.Errorf("xapi: Column id is required")
					}
					if o.strict && !legacyStrict && strings.EqualFold(attr(q, "type"), "BLOB") && !validXMLBlobEncoding(q, o) {
						return fmt.Errorf("xapi: BLOB encoding must be base64")
					}
					if wireColumnIDs[columnID] && o.strict {
						return fmt.Errorf("xapi: duplicate ColumnInfo column %q", columnID)
					}
					wireColumnIDs[columnID] = true
					sawColumn = true
					if e := validateXMLColumn(cur, attr(q, "id"), attr(q, "type"), o); e != nil {
						return e
					}
				}
			case "Col":
				if cur != nil && row.IsValid() { // a Col outside any Row is ignored
					col = attr(q, "id")
					if o.strict && col == "" {
						return fmt.Errorf("xapi: Col id is required")
					}
					if !wireColumnIDs[col] {
						return fmt.Errorf("xapi: column %q not found in ColumnInfo of %s", col, cur.name)
					}
					text.Reset()
					blob = nil
					valueFromAttr = false
					if i, ok := cur.lookup[col]; ok && cur.fields[i].typ == "BLOB" {
						blob = &blobAccumulator{max: o.limits.MaxBlobBytes, allowWhitespace: o.base64WS}
						blobTarget = row.FieldByIndex(cur.fields[i].index)
						if readingOriginal {
							blobTarget = original.FieldByIndex(cur.fields[i].index)
						}
					}
				}
			case "ColumnInfo", "Rows":
			default:
				if col == "" { // markup inside a Col keeps the text around it
					text.Reset()
				}
			}
		case xmltext.Text, xmltext.CData:
			q := t.Text()
			if o.strict && !rootSeen && !declarationSeen && len(q) > 0 {
				return fmt.Errorf("xapi: XML content precedes declaration")
			}
			if col != "" {
				if valueFromAttr && len(q) > 0 { // body text wins over the value attribute
					valueFromAttr = false
					text.Reset()
					if blob != nil {
						blob = &blobAccumulator{max: o.limits.MaxBlobBytes, allowWhitespace: o.base64WS}
					}
				}
				if blob != nil {
					if e := blob.Write(q); e != nil {
						return e
					}
				} else {
					text.WriteString(decodeControlMarkers(string(q)))
				}
				if o.limits.MaxScalarBytes > 0 && int64(text.Len()) > o.limits.MaxScalarBytes {
					return fmt.Errorf("xapi: scalar limit exceeded")
				}
			}
		case xmltext.Directive:
			d := strings.ToUpper(string(t.Text()))
			if strings.Contains(d, "DOCTYPE") || strings.Contains(d, "ENTITY") {
				return fmt.Errorf("xapi: DTD and external entities are not allowed")
			}
		case xmltext.ProcInst:
			if strings.EqualFold(t.Name().Local, "xml") {
				declarationSeen = true
				inst := strings.ToLower(string(t.Text()))
				if o.strict && strings.Contains(inst, "encoding") && !strings.Contains(inst, "utf-8") {
					return fmt.Errorf("xapi: XML declaration is not UTF-8")
				}
			}
		case xmltext.EndElement:
			switch t.Name().Local {
			case "Parameter":
				if blob != nil {
					b, e := blob.Close()
					if e != nil {
						return &PathError{Path: strings.TrimPrefix(col, "@"), Err: e}
					}
					if e = assignBlobBytes(blobTarget, b); e != nil {
						return e
					}
					blob = nil
					col = ""
					break
				}
				if strings.HasPrefix(col, "@") {
					id := strings.TrimPrefix(col, "@")
					for _, f := range s.params {
						if f.name == id {
							if e := assignWithOptions(v.Elem().FieldByIndex(f.index), text.String(), f.typ, o.limits.MaxBlobBytes, o); e != nil {
								return &PathError{Path: id, Err: e}
							}
						}
					}
				}
				col = ""
			case "Col":
				if cur != nil {
					if blob != nil {
						b, e := blob.Close()
						if e != nil {
							return &PathError{Path: cur.name + "." + col, Err: e}
						}
						if e = assignBlobBytes(blobTarget, b); e != nil {
							return e
						}
						blob = nil
						col = ""
						break
					}
					if i, ok := cur.lookup[col]; ok {
						f := cur.fields[i]
						target := row
						if readingOriginal {
							target = original
						}
						if e := assignWithOptions(target.FieldByIndex(f.index), text.String(), f.typ, o.limits.MaxBlobBytes, o); e != nil {
							return &PathError{Path: cur.name + "." + col, Err: e}
						}
					}
					if o.unknown == ErrorUnknownColumns {
						if _, ok := cur.lookup[col]; !ok {
							return &PathError{Path: cur.name + "." + col, Err: fmt.Errorf("unknown column")}
						}
					}
				}
				col = ""
			case "Row":
				if cur != nil {
					if cur.tracked {
						if rowType == RowUpdate && !original.IsValid() && o.strict {
							return fmt.Errorf("xapi: update row missing original in %s", cur.name)
						}
						if rowType != RowUpdate && original.IsValid() && o.strict {
							return fmt.Errorf("xapi: original row requires update in %s", cur.name)
						}
					} else if rowType != RowNormal && cur.rowTypeIndex == nil && o.strict {
						return fmt.Errorf("xapi: row status %s requires Tracked dataset", cur.name)
					}
					if cur.rowTypeIndex != nil {
						row.FieldByIndex(cur.rowTypeIndex).SetUint(uint64(rowType))
					}
					if !cur.tracked && cur.orgRowIndex != nil && original.IsValid() {
						p := reflect.New(cur.elem)
						p.Elem().Set(original)
						row.FieldByIndex(cur.orgRowIndex).Set(p)
					}
					target := v.Elem().FieldByIndex(cur.index)
					if cur.tracked {
						tracked := reflect.New(target.Type().Elem()).Elem()
						tracked.FieldByName("Current").Set(row)
						tracked.FieldByName("Type").SetUint(uint64(rowType))
						if rowType == RowUpdate && original.IsValid() {
							p := reflect.New(cur.elem)
							p.Elem().Set(original)
							tracked.FieldByName("Original").Set(p)
						}
						appendReflect(target, tracked)
					} else {
						appendReflect(target, row)
					}
					row = reflect.Value{}
				}
			case "ConstColumn":
				if constID != "" {
					constValues[constID] = constValue
					constID = ""
					constValue = ""
				}
			case "OrgRow", "Org_Row":
				readingOriginal = false
			case "Dataset":
				cur = nil
			}
			depth--
		}
	}
	if o.strict && !declarationSeen && !legacyStrict {
		return fmt.Errorf("xapi: XML declaration is required")
	}
	if !rootSeen {
		return fmt.Errorf("xapi: XML root is missing")
	}
	if o.missing == ErrorMissingDatasets {
		for _, d := range s.datasets {
			if !seenDatasets[d.name] {
				return fmt.Errorf("xapi: missing dataset %q", d.name)
			}
		}
	}
	return nil
}

func hasField(fs []field, name string) bool {
	for _, f := range fs {
		if f.name == name {
			return true
		}
	}
	return false
}

func validXMLBlobEncoding(s xmlStartTag, o options) bool {
	encoding := attr(s, "enc")
	if encoding == "" && o.compatibility {
		encoding = attr(s, "encrypt")
	}
	return strings.EqualFold(encoding, "base64")
}
func validateXMLColumn(d *dataset, id, typ string, o options) error {
	wire := map[string]string{id: typ}
	return validateWireColumns(d, wire, o)
}
func rowTypeXML(t RowType) string {
	switch t {
	case RowInsert:
		return "insert"
	case RowUpdate:
		return "update"
	case RowDelete:
		return "delete"
	default:
		return ""
	}
}

func xmlText(s string) (string, error) {
	var b strings.Builder
	for _, r := range s {
		if r == 0 {
			return "", fmt.Errorf("xapi: NUL is not allowed in XML text")
		}
		if r == '\n' || (r >= 1 && r <= 8) || (r >= 11 && r <= 12) || (r >= 14 && r <= 31) {
			b.WriteString(controlMarker(int(r)))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String(), nil
}

type controlEntityWriter struct {
	w       io.Writer
	pending []byte
}

func (w *controlEntityWriter) Write(p []byte) (int, error) {
	data := p
	if len(w.pending) > 0 {
		data = append(w.pending, p...)
		w.pending = nil
	}
	if n := controlMarkerSuffix(data); n > 0 {
		w.pending = append(w.pending, data[len(data)-n:]...)
		data = data[:len(data)-n]
	}
	if !utf8.Valid(data) {
		return 0, fmt.Errorf("xapi: invalid UTF-8 output")
	}
	if bytes.IndexByte(data, 0xee) < 0 {
		if len(data) == 0 {
			return len(p), nil
		}
		n, e := w.w.Write(data)
		if e != nil {
			return 0, e
		}
		if n != len(data) {
			return 0, io.ErrShortWrite
		}
		return len(p), nil
	}
	var b strings.Builder
	for _, r := range string(data) {
		if r >= 0xE001 && r <= 0xE01F {
			n := int(r - 0xE000)
			if n != 9 && n != 13 {
				b.WriteString("&#")
				b.WriteString(strconv.Itoa(n))
				b.WriteByte(';')
				continue
			}
		}
		b.WriteRune(r)
	}
	n, e := io.WriteString(w.w, b.String())
	if e != nil {
		return 0, e
	}
	if n != b.Len() {
		return 0, io.ErrShortWrite
	}
	return len(p), nil
}
func (w *controlEntityWriter) Flush() error {
	if len(w.pending) == 0 {
		return nil
	}
	p := append([]byte(nil), w.pending...)
	w.pending = nil
	if !utf8.Valid(p) {
		return fmt.Errorf("xapi: invalid UTF-8 output")
	}
	n, e := w.w.Write(p)
	if e == nil && n != len(p) {
		e = io.ErrShortWrite
	}
	return e
}
func controlMarkerSuffix(p []byte) int {
	if len(p) > 0 && p[len(p)-1] == 0xee {
		return 1
	}
	if len(p) > 1 && p[len(p)-2] == 0xee && p[len(p)-1] == 0x80 {
		return 2
	}
	return 0
}
func parseRowTypeXML(s string) RowType {
	switch strings.ToLower(s) {
	case "insert":
		return RowInsert
	case "update":
		return RowUpdate
	case "delete":
		return RowDelete
	default:
		return RowNormal
	}
}
func validRowTypeXML(s string) bool {
	switch strings.ToLower(s) {
	case "normal", "insert", "update", "delete":
		return true
	default:
		return false
	}
}

// XML 1.0 rejects these references before exposing them as tokens. The reader
// changes only numeric references to private markers; escaped ampersands are
// consequently left as the literal text "&#1;".
type controlEntityReader struct {
	r       io.Reader
	buf     []byte
	pending []byte
	carry   []byte
	done    bool
	cdata   bool
}

func (r *controlEntityReader) Read(p []byte) (int, error) {
	for len(r.buf) == 0 && !r.done {
		var b [32 * 1024]byte
		n, e := r.r.Read(b[:])
		if n > 0 {
			r.buf = append(r.buf, transformStreaming(b[:n], &r.pending, &r.cdata, &r.carry)...)
		}
		if e == io.EOF {
			r.done = true
			if len(r.carry) > 0 {
				if r.cdata {
					r.buf = append(r.buf, r.carry...)
				} else {
					r.buf = append(r.buf, transformControlChunk(r.carry, &r.pending, &r.cdata)...)
				}
				r.carry = nil
			}
			r.buf = append(r.buf, r.pending...)
			r.pending = nil
		}
		if e != nil && e != io.EOF {
			return 0, e
		}
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}

func transformStreaming(in []byte, pending *[]byte, cdata *bool, carry *[]byte) []byte {
	in = append(append([]byte(nil), (*carry)...), in...)
	*carry = nil
	out := make([]byte, 0, len(in))
	for len(in) > 0 {
		if *cdata {
			i := bytes.Index(in, []byte("]]>"))
			if i < 0 {
				k := suffixPrefixLen(in, "]]>")
				out = append(out, in[:len(in)-k]...)
				*carry = append(*carry, in[len(in)-k:]...)
				return out
			}
			out = append(out, in[:i+3]...)
			*cdata = false
			in = in[i+3:]
			continue
		}
		if i := bytes.Index(in, []byte("<![CDATA[")); i >= 0 {
			out = append(out, transformControlChunk(in[:i], pending, cdata)...)
			out = append(out, in[i:i+9]...)
			*cdata = true
			in = in[i+9:]
			continue
		}
		k := suffixPrefixLen(in, "<![CDATA[")
		out = append(out, transformControlChunk(in[:len(in)-k], pending, cdata)...)
		if k > 0 {
			*carry = append(*carry, in[len(in)-k:]...)
		}
		return out
	}
	return out
}

func suffixPrefixLen(in []byte, marker string) int {
	max := len(in)
	if max >= len(marker) {
		return 0
	}
	for n := max; n > 0; n-- {
		if bytes.Equal(in[len(in)-n:], []byte(marker[:n])) {
			return n
		}
	}
	return 0
}

func transformControlChunk(in []byte, pending *[]byte, cdata *bool) []byte {
	if *cdata {
		i := bytes.Index(in, []byte("]]>"))
		if i < 0 {
			return append([]byte(nil), in...)
		}
		out := append([]byte(nil), in[:i+3]...)
		*cdata = false
		return append(out, transformControlChunk(in[i+3:], pending, cdata)...)
	}
	if i := bytes.Index(in, []byte("<![CDATA[")); i >= 0 {
		out := transformControlChunk(in[:i], pending, cdata)
		out = append(out, in[i:i+9]...)
		*cdata = true
		return append(out, transformControlChunk(in[i+9:], pending, cdata)...)
	}
	out := make([]byte, 0, len(in))
	emit := func() { out = append(out, (*pending)...); *pending = nil }
	for _, b := range in {
		if len(*pending) == 0 {
			if b == '&' {
				*pending = append(*pending, b)
			} else {
				out = append(out, b)
			}
			continue
		}
		*pending = append(*pending, b)
		if len(*pending) == 2 && b != '#' {
			emit()
			continue
		}
		if len(*pending) <= 2 {
			continue
		}
		if b == ';' {
			if n, ok := parseControlRef((*pending)[2 : len(*pending)-1]); ok && n > 0 && n < 32 && n != 9 && n != 10 && n != 13 {
				out = append(out, []byte(controlMarker(n))...)
				*pending = nil
				continue
			}
		}
		if len(*pending) < 16 && b != ';' {
			continue
		}
		emit()
	}
	return out
}
func parseControlRef(s []byte) (int, bool) {
	base := 10
	if len(s) >= 2 && (s[0] == 'x' || s[0] == 'X') {
		base = 16
		s = s[1:]
	}
	if len(s) == 0 {
		return 0, false
	}
	n := 0
	for _, c := range s {
		v := -1
		switch {
		case c >= '0' && c <= '9':
			v = int(c - '0')
		case base == 16 && c >= 'a' && c <= 'f':
			v = int(c-'a') + 10
		case base == 16 && c >= 'A' && c <= 'F':
			v = int(c-'A') + 10
		}
		if v < 0 || v >= base {
			return 0, false
		}
		n = n*base + v
		if n > 0x10ffff {
			return 0, false
		}
	}
	return n, true
}
func controlMarker(n int) string { return string(rune(0xE000 + n)) }
func decodeControlMarkers(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r >= 0xE001 && r <= 0xE01F {
			n := int(r - 0xE000)
			if n != 9 && n != 10 && n != 13 {
				out = append(out, rune(n))
				continue
			}
		}
		out = append(out, r)
	}
	return string(out)
}
func attr(s xmlStartTag, k string) string {
	for _, a := range s.Attr {
		if a.Name.Local == k {
			return decodeControlMarkers(a.Value)
		}
	}
	return ""
}

func hasAttr(s xmlStartTag, k string) bool {
	for _, a := range s.Attr {
		if a.Name.Local == k {
			return true
		}
	}
	return false
}

// xmlWriter writes every element in the document's default namespace. An
// element is always written with an end tag, as older clients have received.
type xmlWriter struct {
	*xmltext.Encoder
	space string
}

func newXMLWriter(w io.Writer, space string) *xmlWriter {
	return &xmlWriter{Encoder: xmltext.NewEncoder(w), space: space}
}

func (x *xmlWriter) begin(local string, attrs ...xmltext.Attr) error {
	if e := x.BeginElement(xmltext.Name{Space: x.space, Local: local}); e != nil {
		return e
	}
	for _, a := range attrs {
		if e := x.Attr(a.Name, a.Value); e != nil {
			return e
		}
	}
	return nil
}

func (x *xmlWriter) end() error {
	if e := x.CloseStartTag(); e != nil {
		return e
	}
	return x.EndElement()
}

// sync writes everything so far, before bytes go directly to the output.
func (x *xmlWriter) sync() error {
	if e := x.CloseStartTag(); e != nil {
		return e
	}
	return x.Flush()
}

func xmlAttrOf(local, value string) xmltext.Attr {
	return xmltext.Attr{Name: xmltext.Name{Local: local}, Value: value}
}

func xmlnsAttr(space string) xmltext.Attr {
	return xmltext.Attr{Name: xmltext.Name{Space: xmltext.XMLNSNamespace, Local: "xmlns"}, Value: space}
}

var xmlAttrEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "\t", "&#x9;", "\n", "&#xA;", "\r", "&#xD;")

// xmlStartTag is a start element copied out of a decoder's token.
type xmlStartTag struct {
	Name xmltext.Name
	Attr []xmltext.Attr
}
