package xapi

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"io"
	"reflect"
	"strconv"
	"strings"

	driver "github.com/Clickin/xapi-go/internal/conformance/codec"
)

// encodeXML writes the document in the original drivers' layout: a UTF-8
// declaration, tab indentation, one element per line, the drivers' escaping
// and default column sizes. Rows are written as the drivers write them for
// saveType ALL: row types are kept, an updated row carries its original row
// (an empty one when it has none), and deleted rows come last.
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
	x := &driverXMLWriter{w: bufio.NewWriter(w), xp: p == XPlatformXML5000 || p == XPlatformXML4000, o: o}
	x.raw(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	x.raw(`<Root xmlns="`)
	x.text(p.namespace())
	x.raw(`"`)
	if version := xmlRootVersion(p); version != "" {
		name := "ver"
		if o.xmlVersionAttr != "" {
			name = o.xmlVersionAttr
		}
		x.raw(" " + name + `="` + version + `"`)
	} else if o.xmlVersionAttr != "" {
		x.raw(" " + o.xmlVersionAttr + `="4000"`)
	}
	x.raw(">\n")
	params := make([]field, 0, len(s.params))
	for _, f := range s.params {
		if val := v.FieldByIndex(f.index); !((f.optional || optionalField(val)) && missingValue(val)) {
			params = append(params, f)
		}
	}
	if len(params) == 0 {
		x.raw("\t<Parameters/>\n")
	} else {
		x.raw("\t<Parameters>\n")
		for _, f := range params {
			x.parameter(f, v.FieldByIndex(f.index))
		}
		x.raw("\t</Parameters>\n")
	}
	for _, d := range s.datasets {
		x.dataset(d, v.FieldByIndex(d.index), p == NexacroXML4000)
	}
	x.raw("</Root>\n")
	if x.err != nil {
		return x.err
	}
	return x.w.Flush()
}

func xmlRootVersion(p Profile) string {
	switch p {
	case XPlatformXML5000:
		return "5000"
	case XPlatformXML4000:
		return "4000"
	default:
		return ""
	}
}

type driverXMLWriter struct {
	w   *bufio.Writer
	xp  bool
	o   options
	err error
}

func (x *driverXMLWriter) raw(s string) {
	if x.err == nil {
		_, x.err = x.w.WriteString(s)
	}
}

func (x *driverXMLWriter) text(s string) {
	if x.err != nil {
		return
	}
	escaped, err := driver.EscapeXML(s, x.xp)
	if err != nil {
		x.err = err
		return
	}
	x.raw(escaped)
}

func (x *driverXMLWriter) fail(err error) {
	if x.err == nil {
		x.err = err
	}
}

// xmlCell is a field value ready to write: its canonical state, its lexical,
// and for BLOB the source to stream.
type xmlCell struct {
	state, lexical string
	blob           reflect.Value
}

func (c xmlCell) absent() bool { return c.state == "missing" || c.state == "null" }

func xmlFieldCell(val reflect.Value, typ string, optional bool) (xmlCell, error) {
	if (optional || optionalField(val)) && missingValue(val) {
		return xmlCell{state: "missing"}, nil
	}
	if unwrapped, missing, null := optionalValueState(val); null {
		return xmlCell{state: "null"}, nil
	} else if !missing && unwrapped.IsValid() {
		val = unwrapped
	} else if val.Kind() == reflect.Pointer {
		val = reflect.Zero(val.Type().Elem())
	}
	if typ == "BLOB" {
		switch b := val.Interface().(type) {
		case []byte:
			if len(b) == 0 {
				return xmlCell{state: "empty"}, nil
			}
			return xmlCell{state: "value", blob: val}, nil
		case BlobSource:
			if b == nil {
				return xmlCell{state: "null"}, nil
			}
			return xmlCell{state: "value", blob: val}, nil
		default:
			return xmlCell{}, &TypeError{Want: "[]byte or BlobSource", Got: typeName(val.Interface())}
		}
	}
	lexical, err := encodeScalar(val.Interface(), typ)
	if err != nil {
		return xmlCell{}, err
	}
	if lexical == "" {
		return xmlCell{state: "empty"}, nil
	}
	state, lexical := driver.NormalizeScalar(typ, "value", lexical)
	return xmlCell{state: state, lexical: lexical}, nil
}

func (x *driverXMLWriter) blob(c xmlCell) {
	if x.err != nil || !c.blob.IsValid() {
		return
	}
	var r io.ReadCloser
	switch v := c.blob.Interface().(type) {
	case []byte:
		r = io.NopCloser(bytes.NewReader(v))
	case BlobSource:
		var err error
		if r, err = v.Open(); err != nil {
			x.fail(err)
			return
		}
	}
	enc := base64.NewEncoder(base64.StdEncoding, x.w)
	_, copyErr := io.Copy(enc, r)
	closeErr := enc.Close()
	readCloseErr := r.Close()
	for _, err := range []error{copyErr, closeErr, readCloseErr} {
		if err != nil {
			x.fail(err)
			return
		}
	}
}

func (x *driverXMLWriter) parameter(f field, val reflect.Value) {
	cell, err := xmlFieldCell(val, f.typ, f.optional)
	if err != nil {
		x.fail(err)
		return
	}
	x.raw("\t\t<Parameter id=\"")
	x.text(f.name)
	x.raw(`" type="`)
	x.text(driver.WireType(f.typ))
	x.raw(`"`)
	if f.typ == "BLOB" {
		if cell.absent() {
			x.raw("/>\n")
			return
		}
		x.raw(` encrypt="base64">`)
		x.blob(cell)
		x.raw("</Parameter>\n")
		return
	}
	text, ok := driver.ParameterText(f.typ, cell.state, cell.lexical)
	if !ok {
		x.raw("/>\n")
		return
	}
	if x.o.xmlParamValueAttr {
		x.raw(` value="`)
		x.text(text)
		x.raw("\"/>\n")
		return
	}
	x.raw(">")
	x.text(text)
	x.raw("</Parameter>\n")
}

func xmlColumnSize(f field) string {
	if f.size > 0 {
		return strconv.Itoa(f.size)
	}
	return driver.DefaultWireSize(f.typ)
}

func (x *driverXMLWriter) dataset(d dataset, rows reflect.Value, constantsFirst bool) {
	x.raw("\t<Dataset id=\"")
	x.text(d.name)
	x.raw("\">\n\t\t<ColumnInfo>\n")
	var columns, constants []field
	for _, f := range d.fields {
		if f.constant {
			constants = append(constants, f)
		} else {
			columns = append(columns, f)
		}
	}
	if constantsFirst {
		x.constants(d, constants)
		x.columns(columns)
	} else {
		x.columns(columns)
		x.constants(d, constants)
	}
	x.raw("\t\t</ColumnInfo>\n\t\t<Rows>\n")
	var deleted []reflect.Value
	for i := 0; i < rows.Len(); i++ {
		if rowType, _, _ := xmlRowParts(d, rows.Index(i)); rowType == RowDelete {
			deleted = append(deleted, rows.Index(i))
			continue
		}
		x.row(d, columns, rows.Index(i))
	}
	for _, row := range deleted {
		x.row(d, columns, row)
	}
	x.raw("\t\t</Rows>\n\t</Dataset>\n")
}

func (x *driverXMLWriter) columns(columns []field) {
	for _, f := range columns {
		x.raw("\t\t\t<Column id=\"")
		x.text(f.name)
		x.raw(`" type="`)
		x.text(driver.WireType(f.typ))
		x.raw(`" size="`)
		x.text(xmlColumnSize(f))
		x.raw(`"`)
		if f.typ == "BLOB" {
			x.raw(` encrypt="base64"`)
		}
		x.raw("/>\n")
	}
}

func (x *driverXMLWriter) constants(d dataset, constants []field) {
	for _, f := range constants {
		cell := xmlCell{state: "value", lexical: f.constValue}
		if !f.hasConstValue {
			var err error
			cell, err = xmlFieldCell(reflect.New(d.elem).Elem().FieldByIndex(f.index), f.typ, f.optional)
			if err != nil {
				x.fail(err)
				return
			}
		}
		if cell.state == "missing" {
			cell.state = "null"
		}
		if f.typ != "BLOB" {
			cell.state, cell.lexical = driver.NormalizeConstScalar(f.typ, cell.state, cell.lexical)
		}
		x.raw("\t\t\t<ConstColumn id=\"")
		x.text(f.name)
		x.raw(`" type="`)
		x.text(driver.WireType(f.typ))
		x.raw(`" size="`)
		x.text(xmlColumnSize(f))
		x.raw(`"`)
		if cell.absent() {
			x.raw("/>\n")
			continue
		}
		if f.typ == "BLOB" {
			x.raw(` encrypt="base64" value="`)
			if cell.blob.IsValid() {
				x.blob(cell)
			} else {
				x.text(cell.lexical)
			}
			x.raw("\"></ConstColumn>\n")
			continue
		}
		x.raw(` value="`)
		x.text(cell.lexical)
		x.raw("\"/>\n")
	}
}

// xmlRowParts returns a row's type, its current values and its original row
// (invalid when there is none).
func xmlRowParts(d dataset, rv reflect.Value) (RowType, reflect.Value, reflect.Value) {
	if rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	var original reflect.Value
	rowType := RowNormal
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
	if original.IsValid() && (original.IsNil() || rowType != RowUpdate) {
		original = reflect.Value{}
	}
	if original.IsValid() {
		original = original.Elem()
	}
	return rowType, rv, original
}

func (x *driverXMLWriter) row(d dataset, columns []field, rv reflect.Value) {
	rowType, current, original := xmlRowParts(d, rv)
	x.raw("\t\t\t<Row")
	if rowType != RowNormal {
		x.raw(` type="` + rowTypeXML(rowType) + `"`)
	}
	x.raw(">\n")
	for _, f := range columns {
		cell, err := xmlFieldCell(current.FieldByIndex(f.index), f.typ, f.optional)
		if err != nil {
			x.fail(err)
			return
		}
		// An updated row starts as a copy of its original row.
		if cell.state == "missing" && original.IsValid() {
			if cell, err = xmlFieldCell(original.FieldByIndex(f.index), f.typ, f.optional); err != nil {
				x.fail(err)
				return
			}
		}
		x.cell(f, cell, "\t\t\t\t", false)
	}
	if rowType == RowUpdate {
		x.raw("\t\t\t\t<OrgRow>\n")
		if original.IsValid() {
			for _, f := range columns {
				cell, err := xmlFieldCell(original.FieldByIndex(f.index), f.typ, f.optional)
				if err != nil {
					x.fail(err)
					return
				}
				x.cell(f, cell, "\t\t\t\t\t", true)
			}
		}
		x.raw("\t\t\t\t</OrgRow>\n")
	}
	x.raw("\t\t\t</Row>\n")
}

func (x *driverXMLWriter) cell(f field, cell xmlCell, indent string, saved bool) {
	if cell.absent() {
		// The drivers hold an unset BOOLEAN as false and write it as 0; a
		// saved row they never filled has no cells.
		if saved || !strings.EqualFold(f.typ, "BOOLEAN") {
			return
		}
		cell = xmlCell{state: "value", lexical: "0"}
	}
	x.raw(indent + `<Col id="`)
	x.text(f.name)
	x.raw(`">`)
	if cell.blob.IsValid() {
		x.blob(cell)
	} else {
		x.text(cell.lexical)
	}
	x.raw("</Col>\n")
}
