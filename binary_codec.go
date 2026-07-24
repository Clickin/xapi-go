package xapi

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
)

const (
	binaryVariableMark = uint16(0xfe10)
	binaryDatasetMark  = uint16(0xfe01)
	binaryVersion      = uint16(5000)
	binaryMaxPayload   = 10 << 20
	binaryMaxCount     = 100000
)

type binaryReader struct{ r *bytes.Reader }

func newBinaryReader(b []byte) *binaryReader { return &binaryReader{bytes.NewReader(b)} }
func (r *binaryReader) remaining() int       { return r.r.Len() }
func (r *binaryReader) u16() (uint16, error) {
	var b [2]byte
	_, e := io.ReadFull(r.r, b[:])
	return binary.BigEndian.Uint16(b[:]), e
}
func (r *binaryReader) i16() (int16, error) { v, e := r.u16(); return int16(v), e }
func (r *binaryReader) i32() (int32, error) {
	var b [4]byte
	_, e := io.ReadFull(r.r, b[:])
	return int32(binary.BigEndian.Uint32(b[:])), e
}
func (r *binaryReader) f64() (float64, error) {
	var b [8]byte
	_, e := io.ReadFull(r.r, b[:])
	return math.Float64frombits(binary.BigEndian.Uint64(b[:])), e
}
func (r *binaryReader) length() (int, error) {
	first, e := r.u16()
	if e != nil {
		return 0, e
	}
	if first&0x8000 == 0 {
		return int(first), nil
	}
	low, e := r.u16()
	if e != nil {
		return 0, e
	}
	n := (uint32(first&0x7fff) << 16) | uint32(low)
	if n > binaryMaxPayload {
		return 0, fmt.Errorf("xapi: binary length exceeds limit")
	}
	return int(n), nil
}
func (r *binaryReader) bytes(n int) ([]byte, error) {
	if n < 0 || n > binaryMaxPayload || n > r.remaining() {
		return nil, fmt.Errorf("xapi: invalid binary length %d", n)
	}
	b := make([]byte, n)
	_, e := io.ReadFull(r.r, b)
	return b, e
}
func (r *binaryReader) shortString() (string, error) {
	n, e := r.u16()
	if e != nil {
		return "", e
	}
	b, e := r.bytes(int(n))
	return string(b), e
}

type binaryValue struct {
	tag   uint16
	value any
}

func decodeBinary(r io.Reader, dst any, o options, p Profile) error {
	b, e := io.ReadAll(&documentReader{r: r, max: o.limits.MaxDocumentBytes})
	if e != nil {
		return e
	}
	doc, e := parseBinary(b, o)
	if e != nil {
		return e
	}
	if e = enforceWireDocumentLimits(doc, o); e != nil {
		return e
	}
	return applyWireDocument(dst, doc, o)
}
func parseBinary(b []byte, o options) (wireDocument, error) {
	if len(b) == 0 {
		return wireDocument{}, fmt.Errorf("xapi: empty PlatformBinary payload")
	}
	if len(b) > binaryMaxPayload {
		return wireDocument{}, fmt.Errorf("xapi: binary payload exceeds limit")
	}
	r := newBinaryReader(b)
	doc := wireDocument{}
	mark, e := r.u16()
	if e != nil {
		return doc, e
	}
	if mark == binaryVariableMark {
		if e = readBinaryVariables(r, &doc, o); e != nil {
			return doc, e
		}
	} else if mark == binaryDatasetMark {
		if e = readBinaryDataset(r, &doc, o); e != nil {
			return doc, e
		}
	} else {
		return doc, fmt.Errorf("xapi: invalid PlatformBinary mark 0x%04x", mark)
	}
	for r.remaining() > 0 {
		mark, e = r.u16()
		if e != nil {
			return doc, e
		}
		if mark != binaryDatasetMark {
			return doc, fmt.Errorf("xapi: invalid PlatformBinary dataset mark 0x%04x", mark)
		}
		if e = readBinaryDataset(r, &doc, o); e != nil {
			return doc, e
		}
	}
	return doc, nil
}
func readBinaryVariables(r *binaryReader, doc *wireDocument, o options) error {
	v, e := r.u16()
	if e != nil {
		return e
	}
	if v != binaryVersion {
		return fmt.Errorf("xapi: invalid PlatformBinary version %d", v)
	}
	n, e := r.length()
	if e != nil {
		return e
	}
	block, e := r.bytes(n)
	if e != nil {
		return e
	}
	br := newBinaryReader(block)
	count, e := br.u16()
	if e != nil {
		return e
	}
	if int(count) > binaryMaxCount {
		return fmt.Errorf("xapi: too many binary variables")
	}
	for i := 0; i < int(count); i++ {
		id, e := br.shortString()
		if e != nil {
			return e
		}
		tag, e := br.u16()
		if e != nil {
			return e
		}
		v, e := readBinaryValue(br, tag)
		if e != nil {
			return e
		}
		state, lex := binaryCell(v, binaryVariantType(tag))
		doc.parameters = append(doc.parameters, wireParameter{id: id, typ: binaryVariantType(tag), cell: wireCell{state: state, lexical: lex}})
		_ = i
	}
	return ensureBinaryBlockEmpty(br, "variables")
}
func readBinaryDataset(r *binaryReader, doc *wireDocument, o options) error {
	v, e := r.u16()
	if e != nil {
		return e
	}
	if v != binaryVersion {
		return fmt.Errorf("xapi: invalid PlatformBinary version %d", v)
	}
	n, e := r.length()
	if e != nil {
		return e
	}
	header, e := r.bytes(n)
	if e != nil {
		return e
	}
	hr := newBinaryReader(header)
	alias, e := hr.shortString()
	if e != nil {
		return e
	}
	mark, e := hr.u16()
	if e != nil || mark != binaryVariableMark {
		return fmt.Errorf("xapi: invalid PlatformBinary constant mark")
	}
	v, e = hr.u16()
	if e != nil || v != binaryVersion {
		return fmt.Errorf("xapi: invalid PlatformBinary constant version")
	}
	n, e = hr.length()
	if e != nil {
		return e
	}
	cb, e := hr.bytes(n)
	if e != nil {
		return e
	}
	cr := newBinaryReader(cb)
	cc, e := cr.u16()
	if e != nil {
		return e
	}
	if int(cc) > binaryMaxCount {
		return fmt.Errorf("xapi: too many binary constants")
	}
	d := wireDataset{id: alias, alias: alias}
	for i := 0; i < int(cc); i++ {
		id, e := cr.shortString()
		if e != nil {
			return e
		}
		tag, e := cr.u16()
		if e != nil {
			return e
		}
		v, e := readBinaryValue(cr, tag)
		if e != nil {
			return e
		}
		st, lex := binaryCell(v, binaryVariantType(tag))
		d.constants = append(d.constants, wireColumn{id: id, typ: binaryVariantType(tag), constant: true, cell: wireCell{state: st, lexical: lex}})
	}
	if e = ensureBinaryBlockEmpty(cr, "constants"); e != nil {
		return e
	}
	count, e := hr.u16()
	if e != nil {
		return e
	}
	if int(count) > binaryMaxCount {
		return fmt.Errorf("xapi: too many binary columns")
	}
	for i := 0; i < int(count); i++ {
		id, e := hr.shortString()
		if e != nil {
			return e
		}
		code, e := hr.u16()
		if e != nil {
			return e
		}
		size, e := hr.u16()
		if e != nil {
			return e
		}
		attr, e := hr.u16()
		if e != nil {
			return e
		}
		c := wireColumn{id: id, typ: binaryColumnType(code), size: intString(int(size))}
		if attr&0xf000 == 0x6000 {
			n, e := hr.u16()
			if e != nil {
				return e
			}
			sum, e := hr.bytes(int(n))
			if e != nil {
				return e
			}
			c.prop = "SUM"
			c.sum = string(sum)
		}
		d.columns = append(d.columns, c)
	}
	if hr.remaining() != 0 {
		return fmt.Errorf("xapi: trailing PlatformBinary header")
	}
	for {
		rowLen, e := r.length()
		if e != nil {
			return e
		}
		rowCode, e := r.u16()
		if e != nil {
			return e
		}
		if rowLen == 0 && rowCode == 0 {
			break
		}
		if rowLen < 2 {
			return fmt.Errorf("xapi: invalid binary row length")
		}
		body, e := r.bytes(rowLen - 2)
		if e != nil {
			return e
		}
		rr := newBinaryReader(body)
		count, e := rr.u16()
		if e != nil {
			return e
		}
		if int(count) > binaryMaxCount {
			return fmt.Errorf("xapi: too many binary row values")
		}
		row := wireRow{typ: binaryRowType(rowCode), values: map[string]wireCell{}}
		for i := 0; i < int(count); i++ {
			tag, e := rr.u16()
			if e != nil {
				return e
			}
			v, e := readBinaryValue(rr, tag)
			if e != nil {
				return e
			}
			if i < len(d.columns) {
				st, lex := binaryCell(v, d.columns[i].typ)
				row.values[d.columns[i].id] = wireCell{state: st, lexical: lex}
			}
		}
		if row.typ == RowUpdate {
			saved, e := rr.u16()
			if e != nil {
				return e
			}
			org := &wireRow{typ: rowTypeOriginal, values: map[string]wireCell{}}
			for i := 0; i < int(saved); i++ {
				tag, e := rr.u16()
				if e != nil {
					return e
				}
				v, e := readBinaryValue(rr, tag)
				if e != nil {
					return e
				}
				if i < len(d.columns) {
					st, lex := binaryCell(v, d.columns[i].typ)
					org.values[d.columns[i].id] = wireCell{state: st, lexical: lex}
				}
			}
			row.original = org
		}
		if rr.remaining() != 0 {
			return fmt.Errorf("xapi: trailing binary row")
		}
		d.rows = append(d.rows, row)
		if len(d.rows) > binaryMaxCount {
			return fmt.Errorf("xapi: too many binary rows")
		}
	}
	doc.datasets = append(doc.datasets, d)
	return nil
}
func ensureBinaryBlockEmpty(r *binaryReader, kind string) error {
	if r.remaining() != 0 {
		return fmt.Errorf("xapi: trailing binary %s block", kind)
	}
	return nil
}
func readBinaryValue(r *binaryReader, tag uint16) (binaryValue, error) {
	switch tag {
	case 0, 1:
		return binaryValue{tag: tag}, nil
	case 2:
		v, e := r.i16()
		return binaryValue{tag: tag, value: v != 0}, e
	case 3:
		v, e := r.i32()
		return binaryValue{tag: tag, value: v}, e
	case 4:
		v, e := r.f64()
		return binaryValue{tag: tag, value: v}, e
	case 21, 40:
		n, e := r.length()
		if e != nil {
			return binaryValue{}, e
		}
		b, e := r.bytes(n)
		return binaryValue{tag: tag, value: string(b)}, e
	case 26:
		n, e := r.length()
		if e != nil {
			return binaryValue{}, e
		}
		b, e := r.bytes(n)
		return binaryValue{tag: tag, value: b}, e
	case 41:
		v, e := r.f64()
		return binaryValue{tag: tag, value: v}, e
	default:
		return binaryValue{}, fmt.Errorf("xapi: invalid PlatformBinary value type 0x%04x", tag)
	}
}
func binaryCell(v binaryValue, typ string) (string, string) {
	if v.tag == 0 || v.tag == 1 {
		return "empty", ""
	}
	switch x := v.value.(type) {
	case string:
		if x == "" {
			return "empty", ""
		}
		if strings.EqualFold(typ, "BLOB") || strings.EqualFold(typ, "FILE") {
			return "value", base64.StdEncoding.EncodeToString([]byte(x))
		}
		return "value", x
	case []byte:
		return "value", base64.StdEncoding.EncodeToString(x)
	case bool:
		if x {
			return "value", "true"
		}
		return "value", "false"
	case int32:
		return "value", strconv.FormatInt(int64(x), 10)
	case float64:
		if v.tag == 41 {
			return "value", strconv.FormatInt(int64(x), 10)
		}
		return "value", strconv.FormatFloat(x, 'g', -1, 64)
	default:
		return "value", fmt.Sprint(x)
	}
}
func binaryVariantType(tag uint16) string {
	switch tag {
	case 2:
		return "BOOLEAN"
	case 3:
		return "INT"
	case 4:
		return "DOUBLE"
	case 21:
		return "STRING"
	case 26:
		return "BLOB"
	case 40:
		return "BIGDECIMAL"
	case 41:
		return "DATETIME"
	default:
		return "UNDEFINED"
	}
}
func binaryColumnType(code uint16) string {
	switch code {
	case 1:
		return "STRING"
	case 2:
		return "INT"
	case 3:
		return "DOUBLE"
	case 4:
		return "BIGDECIMAL"
	case 5:
		return "DATE"
	case 6:
		return "TIME"
	case 7:
		return "DATETIME"
	case 8:
		return "BLOB"
	default:
		return "UNDEFINED"
	}
}
func binaryRowType(code uint16) RowType {
	switch code {
	case 2:
		return RowInsert
	case 4:
		return RowUpdate
	case 8:
		return RowDelete
	default:
		return RowNormal
	}
}

func encodeBinary(w io.Writer, src any, o options, p Profile) error {
	doc, e := wireDocumentFromValue(src, o)
	if e != nil {
		return e
	}
	var out bytes.Buffer
	vars := &bytes.Buffer{}
	writeU16(vars, uint16(len(doc.parameters)))
	for _, v := range doc.parameters {
		if e = writeBinaryShortString(vars, v.id); e != nil {
			return e
		}
		if e = writeBinaryTyped(vars, v.typ, v.cell); e != nil {
			return e
		}
	}
	writeU16(&out, binaryVariableMark)
	writeU16(&out, binaryVersion)
	writeBinaryLength(&out, vars.Len())
	out.Write(vars.Bytes())
	for _, d := range doc.datasets {
		if e = writeBinaryDataset(&out, d); e != nil {
			return e
		}
	}
	_, e = w.Write(out.Bytes())
	return e
}
func writeBinaryDataset(out *bytes.Buffer, d wireDataset) error {
	header := &bytes.Buffer{}
	if e := writeBinaryShortString(header, d.alias); e != nil {
		return e
	}
	cb := &bytes.Buffer{}
	writeU16(cb, uint16(len(d.constants)))
	for _, c := range d.constants {
		if e := writeBinaryShortString(cb, c.id); e != nil {
			return e
		}
		if e := writeBinaryTyped(cb, c.typ, c.cell); e != nil {
			return e
		}
	}
	writeU16(header, binaryVariableMark)
	writeU16(header, binaryVersion)
	writeBinaryLength(header, cb.Len())
	header.Write(cb.Bytes())
	writeU16(header, uint16(len(d.columns)))
	for _, c := range d.columns {
		if e := writeBinaryShortString(header, c.id); e != nil {
			return e
		}
		writeU16(header, binaryColumnCode(c.typ))
		n, _ := strconv.Atoi(c.size)
		writeU16(header, uint16(n))
		attr := uint16(1)
		if strings.EqualFold(c.prop, "SUM") {
			attr = 0x6000
		}
		writeU16(header, attr)
		if attr&0xf000 == 0x6000 {
			if e := writeBinaryShortString(header, c.sum); e != nil {
				return e
			}
		}
	}
	writeU16(out, binaryDatasetMark)
	writeU16(out, binaryVersion)
	writeBinaryLength(out, header.Len())
	out.Write(header.Bytes())
	for _, row := range d.rows {
		if e := writeBinaryRow(out, d, row); e != nil {
			return e
		}
	}
	writeU32(out, 0)
	return nil
}
func writeBinaryRow(out *bytes.Buffer, d wireDataset, row wireRow) error {
	body := &bytes.Buffer{}
	writeU16(body, binaryRowCode(row.typ))
	writeU16(body, uint16(len(d.columns)))
	for _, c := range d.columns {
		if e := writeBinaryTyped(body, c.typ, row.values[c.id]); e != nil {
			return e
		}
	}
	if row.typ == RowUpdate {
		if row.original == nil {
			writeU16(body, 0)
		} else {
			writeU16(body, uint16(len(d.columns)))
			for _, c := range d.columns {
				if e := writeBinaryTyped(body, c.typ, row.original.values[c.id]); e != nil {
					return e
				}
			}
		}
	}
	writeBinaryLength(out, body.Len())
	out.Write(body.Bytes())
	return nil
}
func writeBinaryTyped(out *bytes.Buffer, typ string, c wireCell) error {
	if c.state != "value" && c.state != "empty" {
		writeU16(out, 0)
		return nil
	}
	switch strings.ToUpper(typ) {
	case "STRING", "CHAR":
		writeU16(out, 21)
		return writeBinaryLengthBytes(out, []byte(c.lexical))
	case "SHORT", "USHORT", "INT", "UINT", "BOOLEAN":
		n, e := strconv.ParseInt(strings.ReplaceAll(strings.TrimSpace(c.lexical), ",", ""), 10, 32)
		if e != nil {
			return e
		}
		writeU16(out, 3)
		writeI32(out, int32(n))
	case "LONG", "ULONG", "DECIMAL", "BIGDECIMAL":
		writeU16(out, 40)
		return writeBinaryLengthBytes(out, []byte(c.lexical))
	case "FLOAT", "DOUBLE":
		n, e := strconv.ParseFloat(strings.TrimSpace(c.lexical), 64)
		if e != nil {
			return e
		}
		writeU16(out, 4)
		writeF64(out, n)
	case "DATE", "TIME", "DATETIME":
		n, e := parseBinaryEpoch(c.lexical)
		if e != nil {
			return e
		}
		writeU16(out, 41)
		writeF64(out, float64(n))
	case "BLOB", "FILE":
		b, e := base64.StdEncoding.DecodeString(c.lexical)
		if e != nil {
			return e
		}
		writeU16(out, 26)
		return writeBinaryLengthBytes(out, b)
	default:
		writeU16(out, 0)
	}
	return nil
}
func binaryColumnCode(t string) uint16 {
	switch strings.ToUpper(t) {
	case "STRING", "CHAR":
		return 1
	case "SHORT", "USHORT", "INT", "UINT", "BOOLEAN":
		return 2
	case "FLOAT", "DOUBLE":
		return 3
	case "LONG", "ULONG", "DECIMAL", "BIGDECIMAL":
		return 4
	case "DATE":
		return 5
	case "TIME":
		return 6
	case "DATETIME":
		return 7
	case "BLOB", "FILE":
		return 8
	default:
		return 0
	}
}
func binaryRowCode(t RowType) uint16 {
	switch t {
	case RowInsert:
		return 2
	case RowUpdate:
		return 4
	case RowDelete:
		return 8
	default:
		return 1
	}
}
func parseBinaryEpoch(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if n, e := strconv.ParseInt(s, 10, 64); e == nil {
		return n, nil
	}
	for _, layout := range []string{"20060102150405000", "20060102150405", "20060102", "150405000", "150405", time.RFC3339Nano} {
		if t, e := time.ParseInLocation(layout, s, time.UTC); e == nil {
			return t.UnixMilli(), nil
		}
	}
	return 0, fmt.Errorf("xapi: invalid PlatformBinary time %q", s)
}
func writeU16(b *bytes.Buffer, n uint16) {
	var x [2]byte
	binary.BigEndian.PutUint16(x[:], n)
	b.Write(x[:])
}
func writeU32(b *bytes.Buffer, n uint32) {
	var x [4]byte
	binary.BigEndian.PutUint32(x[:], n)
	b.Write(x[:])
}
func writeI32(b *bytes.Buffer, n int32) { writeU32(b, uint32(n)) }
func writeF64(b *bytes.Buffer, n float64) {
	var x [8]byte
	binary.BigEndian.PutUint64(x[:], math.Float64bits(n))
	b.Write(x[:])
}
func writeBinaryLength(b *bytes.Buffer, n int) {
	if n < 32768 {
		writeU16(b, uint16(n))
	} else {
		writeU32(b, uint32(n)|0x80000000)
	}
}
func writeBinaryLengthBytes(b *bytes.Buffer, p []byte) error {
	if len(p) > binaryMaxPayload {
		return fmt.Errorf("xapi: binary value exceeds limit")
	}
	writeBinaryLength(b, len(p))
	b.Write(p)
	return nil
}
func writeBinaryShortString(b *bytes.Buffer, s string) error {
	if len(s) > 32767 {
		return fmt.Errorf("xapi: binary string exceeds limit")
	}
	writeU16(b, uint16(len(s)))
	b.WriteString(s)
	return nil
}
