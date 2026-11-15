package codec

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

	"github.com/Clickin/xapi-go/internal/conformance/protocol"
)

const (
	nexacroBinaryProfile   = "nexacro-binary-5000"
	xplatformBinaryProfile = "xplatform-binary-5000"

	binaryVariableMark = uint16(0xfe10)
	binaryDatasetMark  = uint16(0xfe01)
	binaryVersion      = uint16(5000)

	binaryMaxPayload = 10 << 20
	binaryMaxCount   = 100000
)

type binaryReader struct {
	r *bytes.Reader
}

func newBinaryReader(b []byte) *binaryReader { return &binaryReader{r: bytes.NewReader(b)} }
func (r *binaryReader) remaining() int       { return r.r.Len() }
func (r *binaryReader) u16() (uint16, error) {
	var b [2]byte
	if _, err := io.ReadFull(r.r, b[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(b[:]), nil
}
func (r *binaryReader) i16() (int16, error) {
	v, err := r.u16()
	return int16(v), err
}
func (r *binaryReader) i32() (int32, error) {
	var b [4]byte
	if _, err := io.ReadFull(r.r, b[:]); err != nil {
		return 0, err
	}
	return int32(binary.BigEndian.Uint32(b[:])), nil
}
func (r *binaryReader) f64() (float64, error) {
	var b [8]byte
	if _, err := io.ReadFull(r.r, b[:]); err != nil {
		return 0, err
	}
	return math.Float64frombits(binary.BigEndian.Uint64(b[:])), nil
}
func (r *binaryReader) bytes(n int) ([]byte, error) {
	if n < 0 || n > binaryMaxPayload || n > r.remaining() {
		return nil, fmt.Errorf("binary length %d exceeds available limit", n)
	}
	out := make([]byte, n)
	if _, err := io.ReadFull(r.r, out); err != nil {
		return nil, err
	}
	return out, nil
}
func (r *binaryReader) length() (int, error) {
	first, err := r.u16()
	if err != nil {
		return 0, err
	}
	if first&0x8000 == 0 {
		return int(first), nil
	}
	low, err := r.u16()
	if err != nil {
		return 0, err
	}
	length := (uint32(first&0x7fff) << 16) | uint32(low)
	if length > binaryMaxPayload {
		return 0, fmt.Errorf("binary length %d exceeds payload limit", length)
	}
	return int(length), nil
}
func (r *binaryReader) shortString() (string, error) {
	n, err := r.u16()
	if err != nil {
		return "", err
	}
	b, err := r.bytes(int(n))
	return string(b), err
}

func isBinaryProfile(profile string) bool {
	return profile == nexacroBinaryProfile || profile == xplatformBinaryProfile
}

func binaryValue(b []byte, strict bool) (protocol.Value, error) {
	if len(b) == 0 {
		// The drivers cannot read an empty PlatformBinary body.
		return protocol.Value{}, fmt.Errorf("empty PlatformBinary payload")
	}
	if len(b) > binaryMaxPayload {
		return protocol.Value{}, fmt.Errorf("binary payload exceeds limit")
	}
	r := newBinaryReader(b)
	value := protocol.Value{Parameters: []protocol.Parameter{}, Datasets: []protocol.Dataset{}}
	mark, err := r.u16()
	if err != nil {
		return protocol.Value{}, err
	}
	switch mark {
	case binaryVariableMark:
		if err := readBinaryVariables(r, &value); err != nil {
			return protocol.Value{}, err
		}
	case binaryDatasetMark:
		if err := readBinaryDataset(r, &value); err != nil {
			return protocol.Value{}, err
		}
	default:
		return protocol.Value{}, fmt.Errorf("invalid PlatformBinary mark 0x%04x", mark)
	}
	for r.remaining() > 0 {
		mark, err = r.u16()
		if err != nil {
			return protocol.Value{}, err
		}
		if mark != binaryDatasetMark {
			return protocol.Value{}, fmt.Errorf("invalid PlatformBinary dataset mark 0x%04x", mark)
		}
		if err := readBinaryDataset(r, &value); err != nil {
			return protocol.Value{}, err
		}
	}
	if !strict {
		return value, nil
	}
	return value, nil
}

func readBinaryVariables(r *binaryReader, value *protocol.Value) error {
	version, err := r.u16()
	if err != nil {
		return err
	}
	if version != binaryVersion {
		return fmt.Errorf("invalid PlatformBinary variable version %d", version)
	}
	blockLen, err := r.length()
	if err != nil {
		return err
	}
	block, err := r.bytes(blockLen)
	if err != nil {
		return err
	}
	br := newBinaryReader(block)
	count, err := br.u16()
	if err != nil {
		return err
	}
	if int(count) > binaryMaxCount {
		return fmt.Errorf("too many binary variables")
	}
	for i := 0; i < int(count); i++ {
		id, err := br.shortString()
		if err != nil {
			return err
		}
		tag, err := br.u16()
		if err != nil {
			return err
		}
		v, err := readBinaryValue(br, tag)
		if err != nil {
			return err
		}
		state, lexical := binaryCell(v)
		value.Parameters = append(value.Parameters, protocol.Parameter{ID: id, Type: binaryVariantType(tag), State: state, Lexical: lexical})
	}
	if br.remaining() != 0 {
		return fmt.Errorf("trailing bytes in PlatformBinary variable block")
	}
	return nil
}

func readBinaryDataset(r *binaryReader, value *protocol.Value) error {
	version, err := r.u16()
	if err != nil {
		return err
	}
	if version != binaryVersion {
		return fmt.Errorf("invalid PlatformBinary dataset version %d", version)
	}
	headerLen, err := r.length()
	if err != nil {
		return err
	}
	header, err := r.bytes(headerLen)
	if err != nil {
		return err
	}
	hr := newBinaryReader(header)
	alias, err := hr.shortString()
	if err != nil {
		return err
	}
	mark, err := hr.u16()
	if err != nil {
		return err
	}
	if mark != binaryVariableMark {
		return fmt.Errorf("invalid PlatformBinary constant-column mark 0x%04x", mark)
	}
	constantVersion, err := hr.u16()
	if err != nil {
		return err
	}
	if constantVersion != binaryVersion {
		return fmt.Errorf("invalid PlatformBinary constant-column version %d", constantVersion)
	}
	constantLen, err := hr.length()
	if err != nil {
		return err
	}
	constantBlock, err := hr.bytes(constantLen)
	if err != nil {
		return err
	}
	cr := newBinaryReader(constantBlock)
	constantCount, err := cr.u16()
	if err != nil {
		return err
	}
	if int(constantCount) > binaryMaxCount {
		return fmt.Errorf("too many PlatformBinary constant columns")
	}
	constants := make([]protocol.ConstColumn, 0, constantCount)
	constantCells := make([]protocol.Cell, 0, constantCount)
	for i := 0; i < int(constantCount); i++ {
		id, err := cr.shortString()
		if err != nil {
			return err
		}
		tag, err := cr.u16()
		if err != nil {
			return err
		}
		v, err := readBinaryValue(cr, tag)
		if err != nil {
			return err
		}
		state, lexical := binaryCell(v)
		constantCells = append(constantCells, protocol.Cell{State: state, Lexical: lexical})
		constants = append(constants, protocol.ConstColumn{ID: id, Type: binaryVariantType(tag), Index: i})
	}
	if cr.remaining() != 0 {
		return fmt.Errorf("trailing bytes in PlatformBinary constant block")
	}
	columnCount, err := hr.u16()
	if err != nil {
		return err
	}
	if int(columnCount) > binaryMaxCount {
		return fmt.Errorf("too many PlatformBinary columns")
	}
	columns := make([]protocol.Column, 0, columnCount)
	for i := 0; i < int(columnCount); i++ {
		id, err := hr.shortString()
		if err != nil {
			return err
		}
		columnType, err := hr.u16()
		if err != nil {
			return err
		}
		_, err = hr.u16()
		if err != nil {
			return err
		}
		attr, err := hr.u16()
		if err != nil {
			return err
		}
		column := protocol.Column{ID: id, Type: binaryColumnType(columnType), Index: int(constantCount) + i}
		if attr&0xf000 == 0x6000 {
			sumLen, err := hr.u16()
			if err != nil {
				return err
			}
			if _, err := hr.bytes(int(sumLen)); err != nil {
				return err
			}
		}
		columns = append(columns, column)
	}
	if hr.remaining() != 0 {
		return fmt.Errorf("trailing bytes in PlatformBinary dataset header")
	}
	dataset := protocol.Dataset{ID: alias, Columns: columns, ConstColumns: constants, Rows: []protocol.Row{}}
	for {
		rowLen, err := r.length()
		if err != nil {
			return err
		}
		rowType, err := r.u16()
		if err != nil {
			return err
		}
		if rowLen == 0 && rowType == 0 {
			break
		}
		if rowLen < 2 {
			return fmt.Errorf("invalid PlatformBinary row length %d", rowLen)
		}
		rowBody, err := r.bytes(rowLen - 2)
		if err != nil {
			return err
		}
		rr := newBinaryReader(rowBody)
		count, err := rr.u16()
		if err != nil {
			return err
		}
		if int(count) > binaryMaxCount {
			return fmt.Errorf("too many PlatformBinary row values")
		}
		row := protocol.Row{Type: binaryRowType(rowType), OrgRow: nil, Values: map[string]protocol.Cell{}}
		row.MarkOrgRowPresent()
		for i := 0; i < int(count); i++ {
			tag, err := rr.u16()
			if err != nil {
				return err
			}
			v, err := readBinaryValue(rr, tag)
			if err != nil {
				return err
			}
			if i >= len(columns) {
				continue
			}
			state, lexical := binaryCellForType(v, columns[i].Type)
			row.Values[columns[i].ID] = protocol.Cell{State: state, Lexical: lexical}
		}
		if row.Type == "U" {
			savedCount, err := rr.u16()
			if err != nil {
				return err
			}
			org := &protocol.Row{Type: "O", Values: map[string]protocol.Cell{}}
			org.MarkOrgRowPresent()
			for i := 0; i < int(savedCount); i++ {
				tag, err := rr.u16()
				if err != nil {
					return err
				}
				v, err := readBinaryValue(rr, tag)
				if err != nil {
					return err
				}
				if i >= len(columns) {
					continue
				}
				state, lexical := binaryCellForType(v, columns[i].Type)
				org.Values[columns[i].ID] = protocol.Cell{State: state, Lexical: lexical}
			}
			row.OrgRow = org
		}
		if rr.remaining() != 0 {
			return fmt.Errorf("trailing bytes in PlatformBinary row")
		}
		// Constant values appear in every row, as in the other formats.
		for i, constant := range constants {
			row.Values[constant.ID] = constantCells[i]
		}
		dataset.Rows = append(dataset.Rows, row)
		if len(dataset.Rows) > binaryMaxCount {
			return fmt.Errorf("too many PlatformBinary rows")
		}
	}
	value.Datasets = append(value.Datasets, dataset)
	return nil
}

type binaryWireValue struct {
	tag uint16
	v   any
}

func readBinaryValue(r *binaryReader, tag uint16) (binaryWireValue, error) {
	switch tag {
	case 0, 1:
		return binaryWireValue{tag: tag}, nil
	case 2:
		v, err := r.i16()
		return binaryWireValue{tag: tag, v: v != 0}, err
	case 3:
		v, err := r.i32()
		return binaryWireValue{tag: tag, v: v}, err
	case 4:
		v, err := r.f64()
		return binaryWireValue{tag: tag, v: v}, err
	case 21, 40:
		n, err := r.length()
		if err != nil {
			return binaryWireValue{}, err
		}
		b, err := r.bytes(n)
		return binaryWireValue{tag: tag, v: string(b)}, err
	case 26:
		n, err := r.length()
		if err != nil {
			return binaryWireValue{}, err
		}
		b, err := r.bytes(n)
		return binaryWireValue{tag: tag, v: b}, err
	case 41:
		v, err := r.f64()
		return binaryWireValue{tag: tag, v: v}, err
	default:
		return binaryWireValue{}, fmt.Errorf("invalid PlatformBinary value type 0x%04x", tag)
	}
}

func binaryCell(v binaryWireValue) (string, string) {
	return binaryCellForType(v, binaryVariantType(v.tag))
}

func binaryCellForType(v binaryWireValue, dataType string) (string, string) {
	if v.tag == 0 || v.tag == 1 {
		return "null", ""
	}
	switch x := v.v.(type) {
	case string:
		if x == "" {
			return "empty", ""
		}
		if strings.EqualFold(dataType, "BLOB") || strings.EqualFold(dataType, "FILE") {
			return "value", base64.StdEncoding.EncodeToString([]byte(x))
		}
		return "value", x
	case []byte:
		if len(x) == 0 {
			return "empty", ""
		}
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
			return "value", formatBinaryEpoch(x, dataType)
		}
		return "value", javaDouble(x)
	default:
		return "value", fmt.Sprint(x)
	}
}

// javaDouble formats like Java's Double.toString for values in [1e-3, 1e7),
// which always keeps a fractional part.
func javaDouble(x float64) string {
	text := strconv.FormatFloat(x, 'g', -1, 64)
	if !strings.ContainsAny(text, ".eEN") {
		text += ".0"
	}
	return text
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

func encodeBinary(value protocol.Value) ([]byte, error) {
	if len(value.Parameters) == 0 && len(value.Datasets) == 0 {
		return []byte{}, nil
	}
	var out bytes.Buffer
	// The drivers write the variable block only when there are variables.
	if len(value.Parameters) > 0 {
		block := &bytes.Buffer{}
		writeU16(block, uint16(len(value.Parameters)))
		for _, p := range value.Parameters {
			if err := writeBinaryVariable(block, p); err != nil {
				return nil, err
			}
		}
		writeU16(&out, binaryVariableMark)
		writeU16(&out, binaryVersion)
		writeBinaryLength(&out, block.Len())
		out.Write(block.Bytes())
	}
	for _, dataset := range value.Datasets {
		if err := writeBinaryDataset(&out, dataset); err != nil {
			return nil, err
		}
	}
	return out.Bytes(), nil
}

func writeBinaryVariable(out *bytes.Buffer, p protocol.Parameter) error {
	if err := writeBinaryShortString(out, p.ID); err != nil {
		return err
	}
	cell := binaryPrimitiveNullAsZero(p.Type, protocol.Cell{State: p.State, Lexical: p.Lexical})
	return writeBinaryTypedValue(out, defaultType(p.Type), cell)
}

// binaryPrimitiveNullAsZero writes a null of a primitive Java type as that
// type's zero; the object types stay null.
func binaryPrimitiveNullAsZero(dataType string, cell protocol.Cell) protocol.Cell {
	if cell.State != "null" && cell.State != "missing" {
		return cell
	}
	switch strings.ToUpper(defaultType(dataType)) {
	case "SHORT", "USHORT", "INT", "UINT", "LONG", "ULONG", "BOOLEAN", "FLOAT", "DOUBLE":
		return protocol.Cell{State: "value", Lexical: "0"}
	}
	return cell
}

// binaryConstCell converts a constant value to the column type as the
// drivers do before writing it: an empty value converts like any other
// lexical, so it becomes zero or null for the non-string types.
func binaryConstCell(dataType string, cell protocol.Cell) protocol.Cell {
	cell = binaryPrimitiveNullAsZero(dataType, cell)
	switch strings.ToUpper(defaultType(dataType)) {
	case "STRING", "CHAR", "BLOB", "FILE", "UNDEFINED":
		return cell
	}
	if cell.State == "empty" {
		cell = protocol.Cell{State: "value", Lexical: ""}
	}
	return normalizeSourceCell(dataType, cell)
}

func writeBinaryDataset(out *bytes.Buffer, dataset protocol.Dataset) error {
	alias := dataset.ID
	if dataset.Wire != nil {
		if a, ok := dataset.Wire["alias"].(string); ok && a != "" {
			alias = a
		}
	}
	header := &bytes.Buffer{}
	if err := writeBinaryShortString(header, alias); err != nil {
		return err
	}
	constantBlock := &bytes.Buffer{}
	writeU16(constantBlock, uint16(len(dataset.ConstColumns)))
	for _, column := range dataset.ConstColumns {
		if err := writeBinaryShortString(constantBlock, column.ID); err != nil {
			return err
		}
		// Constant values stay as given in the text formats; PlatformBinary
		// writes them converted to the column type.
		value := binaryConstCell(column.Type, column.Value)
		if err := writeBinaryTypedValue(constantBlock, defaultType(column.Type), value); err != nil {
			return err
		}
	}
	writeU16(header, binaryVariableMark)
	writeU16(header, binaryVersion)
	writeBinaryLength(header, constantBlock.Len())
	header.Write(constantBlock.Bytes())
	normalCount := len(dataset.Columns)
	writeU16(header, uint16(normalCount))
	for _, column := range dataset.Columns {
		if err := writeBinaryShortString(header, column.ID); err != nil {
			return err
		}
		writeU16(header, binaryColumnCode(defaultType(column.Type)))
		size, _ := strconv.Atoi(columnSize(column.Size, column.Type))
		writeU16(header, uint16(size))
		attr := uint16(1)
		if strings.EqualFold(column.Prop, "SUM") {
			attr = 0x6000
		}
		writeU16(header, attr)
		if attr&0xf000 == 0x6000 {
			if err := writeBinaryShortString(header, column.SumText); err != nil {
				return err
			}
		}
	}
	writeU16(out, binaryDatasetMark)
	writeU16(out, binaryVersion)
	writeBinaryLength(out, header.Len())
	out.Write(header.Bytes())
	for _, row := range dataset.Rows {
		if err := writeBinaryRow(out, dataset, row); err != nil {
			return err
		}
	}
	writeU32(out, 0)
	return nil
}

func binaryRowType(code uint16) string {
	switch code {
	case 1:
		return "N"
	case 2:
		return "I"
	case 4:
		return "U"
	case 8:
		return "D"
	default:
		return "N"
	}
}

func writeBinaryRow(out *bytes.Buffer, dataset protocol.Dataset, row protocol.Row) error {
	body := &bytes.Buffer{}
	writeU16(body, binaryRowCode(row.Type))
	writeU16(body, uint16(len(dataset.Columns)))
	for _, column := range dataset.Columns {
		if err := writeBinaryTypedValue(body, defaultType(column.Type), row.Values[column.ID]); err != nil {
			return err
		}
	}
	if row.Type == "U" && row.OrgRow != nil {
		writeU16(body, uint16(len(dataset.Columns)))
		for _, column := range dataset.Columns {
			if err := writeBinaryTypedValue(body, defaultType(column.Type), row.OrgRow.Values[column.ID]); err != nil {
				return err
			}
		}
	} else if row.Type == "U" {
		writeU16(body, 0)
	}
	writeBinaryLength(out, body.Len())
	out.Write(body.Bytes())
	return nil
}

func writeBinaryTypedValue(out *bytes.Buffer, dataType string, cell protocol.Cell) error {
	if cell.State != "value" && cell.State != "empty" {
		writeU16(out, 0)
		return nil
	}
	t := strings.ToUpper(dataType)
	switch t {
	case "STRING", "CHAR":
		writeU16(out, 21)
		return writeBinaryLengthBytes(out, []byte(cell.Lexical))
	case "SHORT", "USHORT", "INT", "UINT", "BOOLEAN":
		value, err := parseBinaryInt(cell.Lexical)
		if err != nil {
			return err
		}
		writeU16(out, 3)
		writeI32(out, int32(value))
		return nil
	case "LONG", "ULONG", "DECIMAL", "BIGDECIMAL":
		writeU16(out, 40)
		return writeBinaryLengthBytes(out, []byte(cell.Lexical))
	case "FLOAT", "DOUBLE":
		value, err := strconv.ParseFloat(strings.TrimSpace(cell.Lexical), 64)
		if err != nil {
			return err
		}
		writeU16(out, 4)
		writeF64(out, value)
		return nil
	case "DATE", "TIME", "DATETIME":
		value, err := parseBinaryEpoch(cell.Lexical, t)
		if err != nil {
			return err
		}
		writeU16(out, 41)
		writeF64(out, float64(value))
		return nil
	case "BLOB", "FILE":
		value, err := base64.StdEncoding.DecodeString(cell.Lexical)
		if err != nil {
			return err
		}
		writeU16(out, 26)
		return writeBinaryLengthBytes(out, value)
	case "NULL":
		writeU16(out, 0)
		return nil
	case "UNDEFINED", "DATASET", "INVALID":
		writeU16(out, 21)
		return writeBinaryLengthBytes(out, []byte(cell.Lexical))
	default:
		return fmt.Errorf("unsupported PlatformBinary type %q", dataType)
	}
}

func binaryColumnCode(dataType string) uint16 {
	switch strings.ToUpper(dataType) {
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

func binaryRowCode(rowType string) uint16 {
	switch strings.ToUpper(rowType) {
	case "N", "":
		return 1
	case "I":
		return 2
	case "U":
		return 4
	case "D":
		return 8
	default:
		return 1
	}
}

func parseBinaryInt(s string) (int64, error) {
	s = strings.ReplaceAll(strings.TrimSpace(s), ",", "")
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		return strconv.ParseInt(s[2:], 16, 64)
	}
	return strconv.ParseInt(s, 10, 64)
}

// parseBinaryEpoch reads a canonical date lexical as the drivers do (see
// javaDate) and returns epoch milliseconds in UTC. PlatformBinary carries
// every date type as that number.
func parseBinaryEpoch(s, dataType string) (int64, error) {
	if t, ok := parseLongYearDate(s, dataType); ok {
		return t.UnixMilli(), nil
	}
	t, ok := javaDate(s, dataType)
	if !ok {
		return 0, fmt.Errorf("invalid PlatformBinary %s %q", strings.ToLower(dataType), s)
	}
	return t.UnixMilli(), nil
}

// parseLongYearDate reads the normalized lexical of a date whose year has
// more than four digits (formatJavaDate never cuts the year), which javaDate
// cannot tell apart by length.
func parseLongYearDate(s, dataType string) (time.Time, bool) {
	tail := 13
	switch strings.ToUpper(dataType) {
	case "DATE":
		tail = 4
	case "TIME":
		return time.Time{}, false
	}
	if len(s) <= tail+4 {
		return time.Time{}, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return time.Time{}, false
		}
	}
	year, err := strconv.Atoi(s[:len(s)-tail])
	if err != nil {
		return time.Time{}, false
	}
	rest := s[len(s)-tail:]
	n := func(i, j int) int { v, _ := strconv.Atoi(rest[i:j]); return v }
	if tail == 4 {
		return time.Date(year, time.Month(n(0, 2)), n(2, 4), 0, 0, 0, 0, time.UTC), true
	}
	return time.Date(year, time.Month(n(0, 2)), n(2, 4), n(4, 6), n(6, 8), n(8, 10), n(10, 13)*int(time.Millisecond), time.UTC), true
}

// formatBinaryEpoch is the canonical lexical of a PlatformBinary date for the
// type it is read as, in UTC.
func formatBinaryEpoch(ms float64, dataType string) string {
	t := time.UnixMilli(int64(ms)).UTC()
	switch strings.ToUpper(dataType) {
	case "DATE":
		return t.Format("20060102")
	case "TIME":
		return t.Format("150405") + fmt.Sprintf("%03d", t.Nanosecond()/1e6)
	default:
		return t.Format("20060102150405") + fmt.Sprintf("%03d", t.Nanosecond()/1e6)
	}
}

func writeU16(out *bytes.Buffer, value uint16) {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], value)
	out.Write(b[:])
}
func writeU32(out *bytes.Buffer, value uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], value)
	out.Write(b[:])
}
func writeI32(out *bytes.Buffer, value int32) { writeU32(out, uint32(value)) }
func writeF64(out *bytes.Buffer, value float64) {
	var b [8]byte
	bits := math.Float64bits(value)
	if math.IsNaN(value) {
		bits = 0x7ff8000000000000 // Double.doubleToLongBits canonical NaN
	}
	binary.BigEndian.PutUint64(b[:], bits)
	out.Write(b[:])
}
func writeBinaryLength(out *bytes.Buffer, n int) {
	if n < 0 || n > binaryMaxPayload {
		return
	}
	if n < 32768 {
		writeU16(out, uint16(n))
		return
	}
	writeU32(out, uint32(n)|0x80000000)
}
func writeBinaryLengthBytes(out *bytes.Buffer, b []byte) error {
	if len(b) > binaryMaxPayload {
		return fmt.Errorf("binary value exceeds payload limit")
	}
	writeBinaryLength(out, len(b))
	out.Write(b)
	return nil
}
func writeBinaryShortString(out *bytes.Buffer, s string) error {
	b := []byte(s)
	if len(b) > 32767 {
		return fmt.Errorf("binary string exceeds short length")
	}
	writeU16(out, uint16(len(b)))
	out.Write(b)
	return nil
}
