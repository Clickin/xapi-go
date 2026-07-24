package xapi

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const (
	ssvRS  = "\x1e"
	ssvUS  = "\x1f"
	ssvETX = "\x03"
	ssvSTX = "\x02"
)

func encodeSSV(w io.Writer, src any, o options, p Profile) error {
	doc, err := wireDocumentFromValue(src, o)
	if err != nil {
		return err
	}
	unit, record := o.ssvUnitSeparator, o.ssvRecordSeparator
	if unit == "" {
		unit = ssvUS
	}
	if record == "" {
		record = ssvRS
	}
	if unit == record || !validSSVSeparator(unit) || !validSSVSeparator(record) {
		return fmt.Errorf("xapi: invalid SSV separators")
	}
	codePage := o.ssvCodePage
	if codePage == "" {
		codePage = "utf-8"
	}
	var b strings.Builder
	write := func(s string) { b.WriteString(s); b.WriteString(record) }
	write("SSV:" + codePage)
	params := make([]string, 0, len(doc.parameters))
	for _, param := range doc.parameters {
		field := param.id + ":" + defaultSSVType(param.typ)
		if param.size != "" {
			field += "(" + param.size + ")"
		}
		if param.cell.state != "missing" {
			v, e := encodeSSVCell(param.cell, p, param.typ, unit, record)
			if e != nil {
				return e
			}
			field += "=" + v
		}
		params = append(params, field)
	}
	if len(params) > 0 {
		write(strings.Join(params, unit))
	}
	for i, d := range doc.datasets {
		if e := validateSSVToken(d.id, unit, record); e != nil {
			return e
		}
		write("Dataset:" + d.id)
		if len(d.constants) > 0 {
			fields := []string{"_Const_"}
			for _, c := range d.constants {
				v := c.id + ":" + defaultSSVType(c.typ)
				if c.size != "" {
					v += "(" + c.size + ")"
				}
				if c.cell.state != "missing" {
					x, e := encodeSSVCell(c.cell, p, c.typ, unit, record)
					if e != nil {
						return e
					}
					v += "=" + x
				}
				fields = append(fields, v)
			}
			write(strings.Join(fields, unit))
		}
		if len(d.columns) == 0 {
			return fmt.Errorf("xapi: SSV dataset %q has no columns", d.id)
		}
		fields := []string{"_RowType_"}
		for _, c := range d.columns {
			v := c.id + ":" + defaultSSVType(c.typ)
			if c.size != "" {
				v += "(" + c.size + ")"
			}
			if c.prop != "" || c.sum != "" {
				v += ":" + c.prop
				if c.sum != "" {
					v += ":" + c.sum
				}
			}
			fields = append(fields, v)
		}
		write(strings.Join(fields, unit))
		for _, row := range d.rows {
			x, e := encodeSSVRow(row, d.columns, p, unit, record)
			if e != nil {
				return e
			}
			write(x)
			if row.original != nil {
				original := *row.original
				original.typ = rowTypeOriginal
				x, e = encodeSSVRow(original, d.columns, p, unit, record)
				if e != nil {
					return e
				}
				write(x)
			}
		}
		if p == NexacroSSV || i+1 < len(doc.datasets) {
			write("")
		}
	}
	if p == NexacroSSV && len(doc.datasets) == 0 {
		write("")
	}
	payload := b.String()
	_, err = io.WriteString(w, payload)
	return err
}

func decodeSSV(r io.Reader, dst any, o options, p Profile) error {
	b, err := io.ReadAll(&documentReader{r: r, max: o.limits.MaxDocumentBytes})
	if err != nil {
		return err
	}
	text, err := decodeSSVText(b)
	if err != nil {
		return err
	}
	unit, record := o.ssvUnitSeparator, o.ssvRecordSeparator
	if unit == "" {
		unit = ssvUS
	}
	if record == "" {
		record = ssvRS
	}
	if unit == record || !validSSVSeparator(unit) || !validSSVSeparator(record) {
		return fmt.Errorf("xapi: invalid SSV separators")
	}
	records, tail := splitSSV(text, record)
	if tail != "" {
		if o.strict {
			return fmt.Errorf("xapi: SSV record is not terminated")
		}
		records = append(records, tail)
	}
	if len(records) == 0 || (records[0] != "SSV" && !strings.HasPrefix(records[0], "SSV:")) {
		return fmt.Errorf("xapi: invalid SSV header")
	}
	if o.strict && records[0] == "SSV:" {
		return fmt.Errorf("xapi: empty SSV code page")
	}
	doc := wireDocument{}
	for i := 1; i < len(records); {
		rec := records[i]
		if rec == "" {
			i++
			continue
		}
		if strings.HasPrefix(rec, "Dataset:") {
			d, next, e := parseSSVDataset(records, i, p, o, unit)
			if e != nil {
				return e
			}
			doc.datasets = append(doc.datasets, d)
			i = next
			continue
		}
		for _, part := range strings.Split(rec, unit) {
			if part == "" {
				continue
			}
			param, e := parseSSVParameter(part, p)
			if e != nil {
				return e
			}
			doc.parameters = appendOrReplaceSSVParam(doc.parameters, param)
		}
		i++
	}
	if err := enforceWireDocumentLimits(doc, o); err != nil {
		return err
	}
	return applyWireDocument(dst, doc, o)
}

func decodeSSVText(b []byte) (string, error) {
	if utf8.Valid(b) {
		return string(b), nil
	}
	end := bytes.IndexByte(b, 0x1e)
	if end < 0 {
		return "", fmt.Errorf("xapi: SSV input is not valid UTF-8")
	}
	header := strings.ToLower(strings.TrimPrefix(string(b[:end]), "SSV:"))
	if header != "iso-8859-1" && header != "latin1" && header != "windows-1252" {
		return "", fmt.Errorf("xapi: unsupported SSV code page %q", header)
	}
	runes := make([]rune, len(b))
	for i, c := range b {
		runes[i] = rune(c)
	}
	return string(runes), nil
}
func splitSSV(s, sep string) ([]string, string) {
	var out []string
	for {
		i := strings.Index(s, sep)
		if i < 0 {
			return out, s
		}
		out = append(out, s[:i])
		s = s[i+len(sep):]
	}
}
func parseSSVParameter(record string, p Profile) (wireParameter, error) {
	header, value, has := strings.Cut(record, "=")
	id, typ, size, err := parseSSVName(header)
	if err != nil || id == "" {
		return wireParameter{}, fmt.Errorf("xapi: invalid SSV parameter %q", record)
	}
	cell := wireCell{state: "missing"}
	if has {
		cell.state, cell.lexical = decodeSSVCell(value, p)
	}
	return wireParameter{id: id, typ: typ, size: size, cell: cell}, nil
}
func appendOrReplaceSSVParam(ps []wireParameter, p wireParameter) []wireParameter {
	for i := range ps {
		if ps[i].id == p.id {
			ps[i] = p
			return ps
		}
	}
	return append(ps, p)
}
func parseSSVDataset(records []string, start int, p Profile, o options, unit string) (wireDataset, int, error) {
	d := wireDataset{id: strings.TrimPrefix(records[start], "Dataset:")}
	if o.strict && d.id == "" {
		return d, start, fmt.Errorf("xapi: empty SSV dataset id")
	}
	sawColumns, terminated := false, false
	i := start + 1
	for ; i < len(records); i++ {
		rec := records[i]
		if rec == "" {
			terminated = true
			i++
			break
		}
		if strings.HasPrefix(rec, "_Const_") {
			if o.strict && sawColumns {
				return d, i, fmt.Errorf("xapi: SSV constants must precede columns")
			}
			cs, e := parseSSVConstants(rec, p, unit)
			if e != nil {
				return d, i, e
			}
			d.constants = append(d.constants, cs...)
			continue
		}
		if strings.HasPrefix(rec, "_RowType_") {
			cs, e := parseSSVColumns(rec, unit)
			if e != nil {
				return d, i, e
			}
			d.columns = cs
			sawColumns = true
			continue
		}
		if o.strict && !sawColumns {
			return d, i, fmt.Errorf("xapi: SSV rows require column info")
		}
		row, e := parseSSVRow(rec, d.columns, p, o.strict, unit)
		if e != nil {
			return d, i, e
		}
		if row.typ == rowTypeOriginal {
			if len(d.rows) > 0 {
				d.rows[len(d.rows)-1].original = &row
			}
			continue
		}
		d.rows = append(d.rows, row)
	}
	if o.strict && !sawColumns {
		return d, i, fmt.Errorf("xapi: SSV dataset requires column info")
	}
	if o.strict && p == NexacroSSV && !terminated {
		return d, i, fmt.Errorf("xapi: SSV dataset requires a null record")
	}
	return d, i, nil
}
func parseSSVConstants(record string, p Profile, unit string) ([]wireColumn, error) {
	fields := strings.Split(record, unit)
	if len(fields) == 0 || fields[0] != "_Const_" {
		return nil, fmt.Errorf("xapi: invalid SSV constant header")
	}
	out := make([]wireColumn, 0, len(fields)-1)
	for _, f := range fields[1:] {
		h, v, has := strings.Cut(f, "=")
		id, t, size, e := parseSSVName(h)
		if e != nil || id == "" {
			return nil, fmt.Errorf("xapi: invalid SSV constant column")
		}
		c := wireColumn{id: id, typ: t, size: size, constant: true, cell: wireCell{state: "missing"}}
		if has {
			c.cell.state, c.cell.lexical = decodeSSVCell(v, p)
		}
		out = append(out, c)
	}
	return out, nil
}
func parseSSVColumns(record, unit string) ([]wireColumn, error) {
	fields := strings.Split(record, unit)
	if len(fields) < 2 || fields[0] != "_RowType_" {
		return nil, fmt.Errorf("xapi: invalid SSV column header")
	}
	out := make([]wireColumn, 0, len(fields)-1)
	for _, f := range fields[1:] {
		parts := strings.SplitN(f, ":", 4)
		id, t, size, e := parseSSVName(strings.Join(parts[:minInt(2, len(parts))], ":"))
		if e != nil || id == "" {
			return nil, fmt.Errorf("xapi: invalid SSV column")
		}
		c := wireColumn{id: id, typ: t, size: size}
		if len(parts) > 2 {
			c.prop = parts[2]
		}
		if len(parts) > 3 {
			c.sum = parts[3]
		}
		out = append(out, c)
	}
	return out, nil
}
func parseSSVName(header string) (string, string, string, error) {
	id, rest, has := strings.Cut(header, ":")
	if !has {
		return id, "STRING", "", nil
	}
	typ, size := rest, ""
	if i := strings.IndexByte(rest, '('); i >= 0 {
		if !strings.HasSuffix(rest, ")") || i == 0 {
			return "", "", "", fmt.Errorf("xapi: invalid SSV type")
		}
		typ, size = rest[:i], rest[i+1:len(rest)-1]
		if size == "" {
			return "", "", "", fmt.Errorf("xapi: empty SSV size")
		}
	}
	if typ == "" {
		typ = "STRING"
	}
	typ = strings.ToUpper(typ)
	if !knownWireType(typ) {
		return "", "", "", fmt.Errorf("xapi: unsupported SSV type %q", typ)
	}
	return id, typ, size, nil
}
func parseSSVRow(record string, cols []wireColumn, p Profile, strict bool, unit string) (wireRow, error) {
	fields := strings.Split(record, unit)
	if len(fields[0]) != 1 || !strings.Contains("NIUDO", fields[0]) {
		if strict {
			return wireRow{}, fmt.Errorf("xapi: invalid SSV row type %q", fields[0])
		}
		return wireRow{}, nil
	}
	row := wireRow{typ: parseSSVRowType(fields[0]), values: map[string]wireCell{}}
	for i, c := range cols {
		if i+1 >= len(fields) {
			row.values[c.id] = wireCell{state: "missing"}
			continue
		}
		state, lexical := decodeSSVCell(fields[i+1], p)
		row.values[c.id] = wireCell{state: state, lexical: lexical}
	}
	return row, nil
}
func parseSSVRowType(s string) RowType {
	switch s {
	case "I":
		return RowInsert
	case "U":
		return RowUpdate
	case "D":
		return RowDelete
	case "O":
		return rowTypeOriginal
	default:
		return RowNormal
	}
}

const rowTypeOriginal RowType = 4

func decodeSSVCell(v string, p Profile) (string, string) {
	if p == XPlatformSSV {
		if v == ssvSTX {
			return "empty", ""
		}
		if v == "" {
			return "null", ""
		}
		return "value", v
	}
	if v == ssvETX {
		return "missing", ""
	}
	if v == "" {
		return "empty", ""
	}
	return "value", v
}
func encodeSSVRow(row wireRow, cols []wireColumn, p Profile, unit, record string) (string, error) {
	fields := make([]string, 1, len(cols)+1)
	fields[0] = rowTypeString(row.typ)
	for _, c := range cols {
		cell := row.values[c.id]
		v, e := encodeSSVCell(cell, p, c.typ, unit, record)
		if e != nil {
			return "", e
		}
		fields = append(fields, v)
	}
	return strings.Join(fields, unit), nil
}
func encodeSSVCell(c wireCell, p Profile, typ, unit, record string) (string, error) {
	switch c.state {
	case "value":
		if e := validateSSVToken(c.lexical, unit, record); e != nil {
			return "", e
		}
		return c.lexical, nil
	case "empty":
		if p == XPlatformSSV {
			return ssvSTX, nil
		}
		return "", nil
	case "missing", "null":
		if p == XPlatformSSV {
			return "", nil
		}
		return ssvETX, nil
	default:
		return "", fmt.Errorf("xapi: invalid SSV cell state %q", c.state)
	}
}
func rowTypeString(t RowType) string {
	switch t {
	case RowInsert:
		return "I"
	case RowUpdate:
		return "U"
	case RowDelete:
		return "D"
	case rowTypeOriginal:
		return "O"
	default:
		return "N"
	}
}
func defaultSSVType(s string) string {
	if s == "" {
		return "STRING"
	}
	return strings.ToUpper(s)
}
func validateSSVToken(s, unit, record string) error {
	if strings.Contains(s, unit) || strings.Contains(s, record) {
		return fmt.Errorf("xapi: value contains an SSV separator")
	}
	return nil
}
func validSSVSeparator(s string) bool { return utf8.RuneCountInString(s) == 1 }
func knownWireType(s string) bool {
	switch strings.ToUpper(s) {
	case "STRING", "CHAR", "SHORT", "USHORT", "INT", "UINT", "LONG", "ULONG", "FLOAT", "DOUBLE", "DECIMAL", "BIGDECIMAL", "BOOLEAN", "DATE", "TIME", "DATETIME", "BLOB", "FILE", "NULL", "UNDEFINED", "DATASET", "INVALID":
		return true
	default:
		return false
	}
}
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
