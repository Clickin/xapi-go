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
		return protocol.Value{}, nil
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
		value.Parameters = append(value.Parameters, protocol.Parameter{ID: id, Type: binaryVariantType(tag), State: state, Lexical: lexical, Index: i})
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
		constants = append(constants, protocol.ConstColumn{ID: id, Type: binaryVariantType(tag), Index: i, Value: protocol.Cell{State: state, Lexical: lexical}})
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
		size, err := hr.u16()
		if err != nil {
			return err
		}
		attr, err := hr.u16()
		if err != nil {
			return err
		}
		column := protocol.Column{ID: id, Type: binaryColumnType(columnType), Index: i}
		if size != 0 {
			column.Size = strconv.Itoa(int(size))
		}
		if attr&0xf000 == 0x6000 {
			sumLen, err := hr.u16()
			if err != nil {
				return err
			}
			sum, err := hr.bytes(int(sumLen))
			if err != nil {
				return err
			}
			column.Prop, column.SumText = "SUM", string(sum)
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
	if v.tag == 0 || v.tag == 1 {
		return "empty", ""
	}
	return binaryCellForType(v, binaryVariantType(v.tag))
}

func binaryCellForType(v binaryWireValue, dataType string) (string, string) {
	if v.tag == 0 || v.tag == 1 {
		return "empty", ""
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

func encodeBinary(value protocol.Value) ([]byte, error) {
	if len(value.Parameters) == 0 && len(value.Datasets) == 0 {
		return nil, nil
	}
	var out bytes.Buffer
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
	return writeBinaryTypedValue(out, defaultType(p.Type), protocol.Cell{State: p.State, Lexical: p.Lexical})
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
		if err := writeBinaryTypedValue(constantBlock, defaultType(column.Type), column.Value); err != nil {
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
		size := 0
		if column.Size != "" {
			size, _ = strconv.Atoi(column.Size)
		}
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
	t := strings.ToUpper(dataType)
	if cell.State == "empty" && (t == "UNDEFINED" || t == "DATASET" || t == "INVALID") {
		writeU16(out, 21)
		return writeBinaryLengthBytes(out, nil)
	}
	if cell.State != "value" && cell.State != "empty" {
		writeU16(out, 0)
		return nil
	}
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
		value, err := parseBinaryEpoch(cell.Lexical)
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
	case "NULL", "UNDEFINED", "DATASET", "INVALID":
		writeU16(out, 0)
		return nil
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

func parseBinaryEpoch(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n, nil
	}
	layouts := []string{"20060102150405000", "20060102150405", "20060102", "150405000", "150405", time.RFC3339Nano}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t.UnixMilli(), nil
		}
	}
	return 0, fmt.Errorf("invalid PlatformBinary epoch %q", s)
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
	binary.BigEndian.PutUint64(b[:], math.Float64bits(value))
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
