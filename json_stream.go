package xapi

import (
	"bufio"
	"fmt"
	"io"
	"reflect"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// jsonStream is the small lexical reader used when a schema contains BLOBs.
// Unlike encoding/json.Decoder.Token, it can consume a JSON string one decoded
// byte at a time and feed base64 directly into the destination accumulator.
type jsonStream struct {
	r   *bufio.Reader
	n   int64
	max int64
}

func newJSONStream(r io.Reader, max int64) *jsonStream {
	return &jsonStream{r: bufio.NewReader(r), max: max}
}

func (s *jsonStream) readByte() (byte, error) {
	if s.max > 0 && s.n >= s.max {
		_, err := s.r.ReadByte()
		if err == nil {
			return 0, errDocumentLimit
		}
		return 0, err
	}
	b, err := s.r.ReadByte()
	if err != nil {
		return 0, err
	}
	s.n++
	if s.max > 0 && s.n > s.max {
		return 0, errDocumentLimit
	}
	return b, nil
}

func (s *jsonStream) peekByte() (byte, error) {
	b, err := s.readByte()
	if err != nil {
		return 0, err
	}
	if err := s.r.UnreadByte(); err != nil {
		return 0, err
	}
	s.n--
	return b, nil
}

func (s *jsonStream) skipSpace() error {
	for {
		b, err := s.peekByte()
		if err != nil {
			return err
		}
		if b != ' ' && b != '\t' && b != '\r' && b != '\n' {
			return nil
		}
		if _, err := s.readByte(); err != nil {
			return err
		}
	}
}

func (s *jsonStream) expect(want byte) error {
	if err := s.skipSpace(); err != nil {
		return err
	}
	b, err := s.readByte()
	if err != nil {
		return err
	}
	if b != want {
		return fmt.Errorf("xapi: expected JSON %q, got %q", want, b)
	}
	return nil
}

func (s *jsonStream) readStringBytes(put func([]byte) error) error {
	return s.readStringBytesMode(put, true, 256)
}

func (s *jsonStream) readStringBytesUnchecked(put func([]byte) error) error {
	return s.readStringBytesMode(put, false, 32<<10)
}

func (s *jsonStream) readStringBytesMode(put func([]byte) error, validateUTF8 bool, chunkSize int) error {
	if err := s.expect('"'); err != nil {
		return err
	}
	// Keep scalar strings cheap; BLOBs receive larger chunks through the
	// unchecked path while avoiding a large frame per ordinary JSON string.
	chunk := make([]byte, chunkSize)
	used := 0
	var utf8Pending []byte
	flush := func() error {
		if !validateUTF8 {
			if used > 0 {
				if err := put(chunk[:used]); err != nil {
					return err
				}
			}
			used = 0
			return nil
		}
		if used == 0 && len(utf8Pending) == 0 {
			return nil
		}
		data := chunk[:used]
		if len(utf8Pending) > 0 {
			data = append(utf8Pending, data...)
			utf8Pending = nil
		}
		complete := len(data)
		for i := 0; i < len(data); {
			if !utf8.FullRune(data[i:]) {
				complete = i
				utf8Pending = append(utf8Pending, data[i:]...)
				break
			}
			r, n := utf8.DecodeRune(data[i:])
			if r == utf8.RuneError && n == 1 {
				return fmt.Errorf("xapi: invalid UTF-8 in JSON string")
			}
			i += n
		}
		if complete > 0 {
			if err := put(data[:complete]); err != nil {
				return err
			}
		}
		used = 0
		return nil
	}
	emit := func(c byte) error {
		chunk[used] = c
		used++
		if used == len(chunk) {
			return flush()
		}
		return nil
	}
	for {
		b, err := s.readByte()
		if err != nil {
			return err
		}
		switch b {
		case '"':
			if err := flush(); err != nil {
				return err
			}
			if len(utf8Pending) > 0 {
				return fmt.Errorf("xapi: incomplete UTF-8 in JSON string")
			}
			return nil
		case '\\':
			r, err := s.readEscape()
			if err != nil {
				return err
			}
			var encoded [utf8.UTFMax]byte
			n := utf8.EncodeRune(encoded[:], r)
			for _, c := range encoded[:n] {
				if err := emit(c); err != nil {
					return err
				}
			}
		default:
			if b < 0x20 {
				return fmt.Errorf("xapi: unescaped JSON control byte")
			}
			if err := emit(b); err != nil {
				return err
			}
		}
	}
}

func (s *jsonStream) readStringTo(put func(byte) error) error {
	return s.readStringBytes(func(p []byte) error {
		for _, c := range p {
			if err := put(c); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *jsonStream) readEscape() (rune, error) {
	b, err := s.readByte()
	if err != nil {
		return 0, err
	}
	switch b {
	case '"', '\\', '/':
		return rune(b), nil
	case 'b':
		return '\b', nil
	case 'f':
		return '\f', nil
	case 'n':
		return '\n', nil
	case 'r':
		return '\r', nil
	case 't':
		return '\t', nil
	case 'u':
		first, err := s.readHex4()
		if err != nil {
			return 0, err
		}
		if utf16.IsSurrogate(rune(first)) {
			if first < 0xD800 || first > 0xDBFF {
				return utf8.RuneError, nil
			}
			if b, err := s.readByte(); err != nil || b != '\\' {
				if err != nil {
					return 0, err
				}
				return 0, fmt.Errorf("xapi: invalid JSON surrogate escape")
			}
			if b, err := s.readByte(); err != nil || b != 'u' {
				if err != nil {
					return 0, err
				}
				return 0, fmt.Errorf("xapi: invalid JSON surrogate escape")
			}
			second, err := s.readHex4()
			if err != nil {
				return 0, err
			}
			return utf16.DecodeRune(rune(first), rune(second)), nil
		}
		return rune(first), nil
	default:
		return 0, fmt.Errorf("xapi: invalid JSON escape %q", b)
	}
}

func (s *jsonStream) readHex4() (uint16, error) {
	var n uint16
	for i := 0; i < 4; i++ {
		b, err := s.readByte()
		if err != nil {
			return 0, err
		}
		v := byte(0xff)
		switch {
		case b >= '0' && b <= '9':
			v = b - '0'
		case b >= 'a' && b <= 'f':
			v = b - 'a' + 10
		case b >= 'A' && b <= 'F':
			v = b - 'A' + 10
		}
		if v == 0xff {
			return 0, fmt.Errorf("xapi: invalid JSON unicode escape")
		}
		n = n*16 + uint16(v)
	}
	return n, nil
}

func (s *jsonStream) readString(max int64) (string, error) {
	var b strings.Builder
	var n int64
	err := s.readStringTo(func(c byte) error {
		n++
		if max > 0 && n > max {
			return fmt.Errorf("xapi: scalar limit exceeded")
		}
		b.WriteByte(c)
		return nil
	})
	if err != nil {
		return "", err
	}
	return b.String(), nil
}

type streamScalar struct {
	text string
	null bool
}

func (s *jsonStream) scalar(max int64) (streamScalar, error) {
	if err := s.skipSpace(); err != nil {
		return streamScalar{}, err
	}
	b, err := s.peekByte()
	if err != nil {
		return streamScalar{}, err
	}
	if b == '"' {
		v, err := s.readString(max)
		return streamScalar{text: v}, err
	}
	var want string
	switch b {
	case 'n':
		want = "null"
	case 't':
		want = "true"
	case 'f':
		want = "false"
	default:
		if b != '-' && (b < '0' || b > '9') {
			return streamScalar{}, fmt.Errorf("xapi: JSON value must be scalar")
		}
		out, err := s.number(max)
		return streamScalar{text: out}, err
	}
	var out strings.Builder
	for i := 0; i < len(want); i++ {
		b, err := s.readByte()
		if err != nil {
			return streamScalar{}, err
		}
		if b != want[i] {
			return streamScalar{}, fmt.Errorf("xapi: invalid JSON literal")
		}
	}
	if want == "null" {
		return streamScalar{null: true}, nil
	}
	out.WriteString(want)
	return streamScalar{text: out.String()}, nil
}

func (s *jsonStream) number(max int64) (string, error) {
	var out strings.Builder
	read := func() (byte, error) {
		b, err := s.readByte()
		if err == nil {
			if max > 0 && int64(out.Len()+1) > max {
				return 0, fmt.Errorf("xapi: scalar limit exceeded")
			}
			out.WriteByte(b)
		}
		return b, err
	}
	peek := func() (byte, error) { return s.peekByte() }

	if b, _ := peek(); b == '-' {
		if _, err := read(); err != nil {
			return "", err
		}
	}
	b, err := peek()
	if err != nil {
		return "", fmt.Errorf("xapi: invalid JSON number")
	}
	switch {
	case b == '0':
		if _, err := read(); err != nil {
			return "", err
		}
		if next, err := peek(); err == nil && next >= '0' && next <= '9' {
			return "", fmt.Errorf("xapi: invalid JSON number")
		}
	case b >= '1' && b <= '9':
		for {
			next, err := peek()
			if err != nil || next < '0' || next > '9' {
				break
			}
			if _, err := read(); err != nil {
				return "", err
			}
		}
	default:
		return "", fmt.Errorf("xapi: invalid JSON number")
	}
	if next, err := peek(); err == nil && next == '.' {
		if _, err := read(); err != nil {
			return "", err
		}
		next, err := peek()
		if err != nil || next < '0' || next > '9' {
			return "", fmt.Errorf("xapi: invalid JSON number")
		}
		for {
			next, err := peek()
			if err != nil || next < '0' || next > '9' {
				break
			}
			if _, err := read(); err != nil {
				return "", err
			}
		}
	}
	if next, err := peek(); err == nil && (next == 'e' || next == 'E') {
		if _, err := read(); err != nil {
			return "", err
		}
		if next, err := peek(); err == nil && (next == '+' || next == '-') {
			if _, err := read(); err != nil {
				return "", err
			}
		}
		next, err := peek()
		if err != nil || next < '0' || next > '9' {
			return "", fmt.Errorf("xapi: invalid JSON number")
		}
		for {
			next, err := peek()
			if err != nil || next < '0' || next > '9' {
				break
			}
			if _, err := read(); err != nil {
				return "", err
			}
		}
	}
	next, err := peek()
	if err != nil && err != io.EOF {
		return "", err
	}
	if err == nil && !strings.ContainsRune(" \t\r\n,]}", rune(next)) {
		return "", fmt.Errorf("xapi: invalid JSON number")
	}
	return out.String(), nil
}

func (s *jsonStream) skipValue(depth, maxDepth int) error {
	if maxDepth > 0 && depth > maxDepth {
		return fmt.Errorf("xapi: JSON depth limit exceeded")
	}
	if err := s.skipSpace(); err != nil {
		return err
	}
	b, err := s.peekByte()
	if err != nil {
		return err
	}
	switch b {
	case '"':
		_, err = s.readString(0)
		return err
	case '{':
		if err = s.expect('{'); err != nil {
			return err
		}
		if err = s.skipSpace(); err != nil {
			return err
		}
		if b, _ = s.peekByte(); b == '}' {
			_, err = s.readByte()
			return err
		}
		for {
			if _, err = s.readString(0); err != nil {
				return err
			}
			if err = s.expect(':'); err != nil {
				return err
			}
			if err = s.skipValue(depth+1, maxDepth); err != nil {
				return err
			}
			if err = s.skipSpace(); err != nil {
				return err
			}
			b, err = s.readByte()
			if err != nil {
				return err
			}
			if b == '}' {
				return nil
			}
			if b != ',' {
				return fmt.Errorf("xapi: expected JSON object separator")
			}
		}
	case '[':
		if err = s.expect('['); err != nil {
			return err
		}
		if err = s.skipSpace(); err != nil {
			return err
		}
		if b, _ = s.peekByte(); b == ']' {
			_, err = s.readByte()
			return err
		}
		for {
			if err = s.skipValue(depth+1, maxDepth); err != nil {
				return err
			}
			if err = s.skipSpace(); err != nil {
				return err
			}
			b, err = s.readByte()
			if err != nil {
				return err
			}
			if b == ']' {
				return nil
			}
			if b != ',' {
				return fmt.Errorf("xapi: expected JSON array separator")
			}
		}
	default:
		_, err = s.scalar(0)
		return err
	}
}

func (s *jsonStream) readBlob(max int64, allowWhitespace bool) ([]byte, bool, error) {
	if err := s.skipSpace(); err != nil {
		return nil, false, err
	}
	if b, err := s.peekByte(); err != nil {
		return nil, false, err
	} else if b == 'n' {
		v, err := s.scalar(0)
		if err != nil {
			return nil, false, err
		}
		if !v.null {
			return nil, false, fmt.Errorf("xapi: invalid JSON null")
		}
		return nil, true, nil
	}
	acc := &blobAccumulator{max: max, allowWhitespace: allowWhitespace}
	if err := s.readStringBytesUnchecked(acc.Write); err != nil {
		return nil, false, err
	}
	b, err := acc.Close()
	return b, false, err
}

type streamValue struct {
	text   string
	blob   []byte
	isBlob bool
	null   bool
}

type streamGenericRow struct {
	status RowType
	values map[string]streamValue
}

func streamObjectKey(s *jsonStream, first *bool, max int64) (string, bool, error) {
	if err := s.skipSpace(); err != nil {
		return "", false, err
	}
	b, err := s.peekByte()
	if err != nil {
		return "", false, err
	}
	if !*first {
		if b == '}' {
			_, err = s.readByte()
			return "", true, err
		}
		if err := s.expect(','); err != nil {
			return "", false, err
		}
		if err := s.skipSpace(); err != nil {
			return "", false, err
		}
		if b, err = s.peekByte(); err != nil {
			return "", false, err
		} else if b == '}' {
			return "", false, fmt.Errorf("xapi: trailing JSON object comma")
		}
	} else if b == '}' {
		_, err = s.readByte()
		return "", true, err
	}
	*first = false
	key, err := s.readString(max)
	if err != nil {
		return "", false, err
	}
	if err := s.expect(':'); err != nil {
		return "", false, err
	}
	return key, false, nil
}

func streamArrayNext(s *jsonStream, first *bool) (bool, error) {
	if err := s.skipSpace(); err != nil {
		return false, err
	}
	b, err := s.peekByte()
	if err != nil {
		return false, err
	}
	if !*first {
		if b == ']' {
			_, err = s.readByte()
			return true, err
		}
		if err := s.expect(','); err != nil {
			return false, err
		}
		if err := s.skipSpace(); err != nil {
			return false, err
		}
		if b, err = s.peekByte(); err != nil {
			return false, err
		} else if b == ']' {
			return false, fmt.Errorf("xapi: trailing JSON array comma")
		}
	} else if b == ']' {
		_, err = s.readByte()
		return true, err
	}
	*first = false
	return false, nil
}

func (s *jsonStream) streamValue(f *field, o options) (streamValue, error) {
	if f != nil && f.typ == "BLOB" {
		b, null, err := s.readBlob(o.limits.MaxBlobBytes, o.base64WS)
		return streamValue{blob: b, isBlob: true, null: null}, err
	}
	v, err := s.scalar(o.limits.MaxScalarBytes)
	return streamValue{text: v.text, null: v.null}, err
}

func applyStreamValue(dst reflect.Value, f field, v streamValue, o options) error {
	if v.null {
		return assignJSONNull(dst, o.jsonNull)
	}
	if f.typ == "BLOB" {
		if !v.isBlob {
			b, err := decodeBlobString(v.text, o.limits.MaxBlobBytes, o.base64WS)
			if err != nil {
				return err
			}
			v.blob = b
		}
		return assignBlobBytes(dst, v.blob)
	}
	return assignWithOptions(dst, v.text, f.typ, o.limits.MaxBlobBytes, o)
}

func decodeJSONStreaming(r io.Reader, dst any, o options, s *schema) error {
	js := newJSONStream(r, o.limits.MaxDocumentBytes)
	if err := js.expect('{'); err != nil {
		return err
	}
	root := reflect.ValueOf(dst).Elem()
	seenDatasets := map[string]bool{}
	rootKeys := map[string]bool{}
	versionSeen := false
	first := true
	for {
		key, done, err := streamObjectKey(js, &first, o.limits.MaxScalarBytes)
		if err != nil {
			return err
		}
		if done {
			break
		}
		if rootKeys[key] && o.duplicates {
			return fmt.Errorf("xapi: duplicate JSON key %q", key)
		}
		rootKeys[key] = true
		switch key {
		case "version":
			v, err := js.scalar(o.limits.MaxScalarBytes)
			if err != nil || v.null || v.text != "1.0" {
				return fmt.Errorf("xapi: unsupported JSON version %q", v.text)
			}
			versionSeen = true
		case "Parameters":
			if err := decodeStreamParameters(js, root, s, o); err != nil {
				return err
			}
		case "Datasets":
			if err := decodeStreamDatasets(js, root, s, o, seenDatasets); err != nil {
				return err
			}
		default:
			if err := js.skipValue(1, o.limits.MaxDepth); err != nil {
				return err
			}
		}
		if versionSeen {
			// Keep the variable meaningful for strict/static analysis; a missing
			// version remains compatible with existing legacy fixtures.
			versionSeen = true
		}
	}
	if err := js.skipSpace(); err != nil && err != io.EOF {
		return err
	}
	if _, err := js.readByte(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("xapi: trailing JSON data after root")
		}
		return err
	}
	if o.strict && !versionSeen && rootKeys["Parameters"] {
		return fmt.Errorf("xapi: JSON version is required")
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

func decodeStreamParameters(js *jsonStream, root reflect.Value, s *schema, o options) error {
	if err := js.expect('['); err != nil {
		return err
	}
	first := true
	for {
		done, err := streamArrayNext(js, &first)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		if err := js.expect('{'); err != nil {
			return err
		}
		var id string
		var value streamValue
		valueSeen := false
		keys := map[string]bool{}
		objFirst := true
		for {
			key, done, err := streamObjectKey(js, &objFirst, o.limits.MaxScalarBytes)
			if err != nil {
				return err
			}
			if done {
				break
			}
			if keys[key] && o.duplicates {
				return fmt.Errorf("xapi: duplicate parameter key %q", key)
			}
			keys[key] = true
			switch key {
			case "id":
				v, err := js.scalar(o.limits.MaxScalarBytes)
				if err != nil || v.null {
					return fmt.Errorf("xapi: parameter id must be a string")
				}
				id = v.text
			case "value":
				var f *field
				for i := range s.params {
					if s.params[i].name == id {
						f = &s.params[i]
					}
				}
				var err error
				value, err = js.streamValue(f, o)
				if err != nil {
					return err
				}
				valueSeen = true
			default:
				if err := js.skipValue(1, o.limits.MaxDepth); err != nil {
					return err
				}
			}
		}
		if valueSeen {
			for _, f := range s.params {
				if f.name == id {
					if err := applyStreamValue(root.FieldByIndex(f.index), f, value, o); err != nil {
						return &PathError{Path: id, Err: err}
					}
				}
			}
		}
	}
}

func decodeStreamDatasets(js *jsonStream, root reflect.Value, s *schema, o options, seen map[string]bool) error {
	if err := js.expect('['); err != nil {
		return err
	}
	first := true
	count := 0
	for {
		done, err := streamArrayNext(js, &first)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		count++
		if o.limits.MaxDatasets > 0 && count > o.limits.MaxDatasets {
			return fmt.Errorf("xapi: dataset limit exceeded")
		}
		if err := decodeStreamDataset(js, root, s, o, seen); err != nil {
			return err
		}
	}
}

func decodeStreamDataset(js *jsonStream, root reflect.Value, s *schema, o options, seen map[string]bool) error {
	if err := js.expect('{'); err != nil {
		return err
	}
	var id string
	var d *dataset
	consts := map[string]streamValue{}
	wireTypes := map[string]string{}
	var pending []streamGenericRow
	rowsBeforeID := false
	idSeen, columnInfoSeen, rowsSeen, columnListSeen := false, false, false, false
	keys := map[string]bool{}
	first := true
	for {
		key, done, err := streamObjectKey(js, &first, o.limits.MaxScalarBytes)
		if err != nil {
			return err
		}
		if done {
			break
		}
		if keys[key] && o.duplicates {
			return fmt.Errorf("xapi: duplicate dataset key %q", key)
		}
		keys[key] = true
		switch key {
		case "id":
			v, err := js.scalar(o.limits.MaxScalarBytes)
			if err != nil || v.null {
				return fmt.Errorf("xapi: dataset id must be a string")
			}
			id = v.text
			if rowsBeforeID && len(s.datasets) == 1 && id != s.datasets[0].name {
				return fmt.Errorf("xapi: dataset id %q does not match rows received before id", id)
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
			if err := decodeStreamColumnInfo(js, consts, wireTypes, o, &columnListSeen); err != nil {
				return err
			}
		case "Rows":
			rowsSeen = true
			if d != nil {
				if err := decodeStreamRows(js, root, d, o, consts); err != nil {
					return err
				}
			} else if len(s.datasets) == 1 {
				d = &s.datasets[0]
				rowsBeforeID = true
				if err := decodeStreamRows(js, root, d, o, consts); err != nil {
					return err
				}
			} else {
				if o.strict {
					return fmt.Errorf("xapi: Rows must follow dataset id when multiple datasets are defined")
				}
				var err error
				pending, err = readStreamGenericRows(js, o)
				if err != nil {
					return err
				}
			}
		default:
			if err := js.skipValue(1, o.limits.MaxDepth); err != nil {
				return err
			}
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
	if d != nil {
		if err := applyStreamConstants(root, d, o, consts); err != nil {
			return err
		}
		if err := validateWireColumns(d, wireTypes, o); err != nil {
			return err
		}
		if len(pending) > 0 {
			if err := appendStreamGenericRows(root, d, o, consts, pending); err != nil {
				return err
			}
		}
	}
	return nil
}

func applyStreamConstants(root reflect.Value, d *dataset, o options, consts map[string]streamValue) error {
	if len(consts) == 0 {
		return nil
	}
	target := root.FieldByIndex(d.index)
	for i := 0; i < target.Len(); i++ {
		row := target.Index(i)
		if d.tracked {
			current := row.FieldByName("Current")
			if err := applyStreamConstantsToRow(current, d, o, consts); err != nil {
				return err
			}
			original := row.FieldByName("Original")
			if !original.IsNil() {
				if err := applyStreamConstantsToRow(original.Elem(), d, o, consts); err != nil {
					return err
				}
			}
			continue
		}
		if err := applyStreamConstantsToRow(row, d, o, consts); err != nil {
			return err
		}
		if d.orgRowIndex != nil {
			original := row.FieldByIndex(d.orgRowIndex)
			if !original.IsNil() {
				if err := applyStreamConstantsToRow(original.Elem(), d, o, consts); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func applyStreamConstantsToRow(row reflect.Value, d *dataset, o options, consts map[string]streamValue) error {
	for _, f := range d.fields {
		if !f.constant {
			continue
		}
		if value, ok := consts[f.name]; ok {
			if err := applyStreamValue(row.FieldByIndex(f.index), f, value, o); err != nil {
				return err
			}
		}

	}
	return nil
}
func decodeStreamColumnInfo(js *jsonStream, consts map[string]streamValue, wireTypes map[string]string, o options, columnListSeen *bool) error {
	if err := js.expect('{'); err != nil {
		return err
	}
	keys := map[string]bool{}
	columnCount := 0
	first := true
	for {
		key, done, err := streamObjectKey(js, &first, o.limits.MaxScalarBytes)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		if keys[key] && o.duplicates {
			return fmt.Errorf("xapi: duplicate ColumnInfo key %q", key)
		}
		keys[key] = true
		if key != "Column" && key != "ConstColumn" {
			if err := js.skipValue(1, o.limits.MaxDepth); err != nil {
				return err
			}
			continue
		}
		if key == "Column" {
			*columnListSeen = true
		}
		if err := js.expect('['); err != nil {
			return err
		}
		arrayFirst := true
		for {
			done, err := streamArrayNext(js, &arrayFirst)
			if err != nil {
				return err
			}
			if done {
				break
			}
			columnCount++
			if o.limits.MaxColumns > 0 && columnCount > o.limits.MaxColumns {
				return fmt.Errorf("xapi: column limit exceeded")
			}
			if err := js.expect('{'); err != nil {
				return err
			}
			id, typ, value := "", "", streamValue{}
			valueSeen := false
			entryKeys := map[string]bool{}
			entryFirst := true
			for {
				entryKey, entryDone, err := streamObjectKey(js, &entryFirst, o.limits.MaxScalarBytes)
				if err != nil {
					return err
				}
				if entryDone {
					break
				}
				if entryKeys[entryKey] && o.duplicates {
					return fmt.Errorf("xapi: duplicate ColumnInfo entry key %q", entryKey)
				}
				entryKeys[entryKey] = true
				switch entryKey {
				case "id":
					v, err := js.scalar(o.limits.MaxScalarBytes)
					if err != nil || v.null {
						return fmt.Errorf("xapi: ColumnInfo id must be a string")
					}
					id = v.text
				case "type":
					v, err := js.scalar(o.limits.MaxScalarBytes)
					if err != nil || v.null {
						return fmt.Errorf("xapi: ColumnInfo type must be a string")
					}
					typ = strings.ToUpper(v.text)
				case "value":
					f := &field{name: id, typ: typ}
					value, err = js.streamValue(f, o)
					if err != nil {
						return err
					}
					valueSeen = true
				default:
					if err := js.skipValue(2, o.limits.MaxDepth); err != nil {
						return err
					}
				}
			}
			if id == "" {
				continue
			}
			if _, exists := wireTypes[id]; exists && o.duplicates {
				return fmt.Errorf("xapi: duplicate ColumnInfo column %q", id)
			}
			wireTypes[id] = typ
			if key == "ConstColumn" && valueSeen {
				consts[id] = value
			}
		}
	}
}

func decodeStreamRows(js *jsonStream, root reflect.Value, d *dataset, o options, consts map[string]streamValue) error {
	if err := js.expect('['); err != nil {
		return err
	}
	target := root.FieldByIndex(d.index)
	rowTracking := d.tracked || d.orgRowIndex != nil
	var pending *reflect.Value
	first := true
	rowIndex := 0
	for {
		done, err := streamArrayNext(js, &first)
		if err != nil {
			return err
		}
		if done {
			break
		}
		if o.limits.MaxRows > 0 && rowIndex >= o.limits.MaxRows {
			return fmt.Errorf("xapi: row limit exceeded for %s", d.name)
		}
		rv, status, err := decodeStreamRow(js, d, o, consts, rowIndex)
		if err != nil {
			return err
		}
		if rowTracking && status == RowUpdate {
			pending = &rv
			rowIndex++
			continue
		}
		if rowTracking && status == rowOriginal {
			if pending == nil {
				if o.strict {
					return fmt.Errorf("xapi: original row without update in %s[%d]", d.name, rowIndex)
				}
				rowIndex++
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
				p := reflect.New(d.elem)
				p.Elem().Set(rv)
				pendingRow := *pending
				pendingRow.FieldByIndex(d.orgRowIndex).Set(p)
				appendReflect(target, pendingRow)
			}
			pending = nil
			rowIndex++
			continue
		}
		if rowTracking && pending != nil {
			if o.strict {
				return fmt.Errorf("xapi: update row missing original in %s[%d]", d.name, rowIndex)
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
			if status != rowOriginal {
				if d.tracked {
					tr := reflect.New(target.Type().Elem()).Elem()
					tr.FieldByName("Current").Set(rv)
					tr.FieldByName("Type").SetUint(uint64(status))
					appendReflect(target, tr)
				} else {
					appendReflect(target, rv)
				}
			}
		} else if status != RowNormal && d.rowTypeIndex == nil {
			if o.strict {
				return fmt.Errorf("xapi: row status %s requires Tracked dataset", d.name)
			}
		} else {
			appendReflect(target, rv)
		}
		rowIndex++
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
	return nil
}

func decodeStreamRow(js *jsonStream, d *dataset, o options, consts map[string]streamValue, ri int) (reflect.Value, RowType, error) {
	if err := js.expect('{'); err != nil {
		return reflect.Value{}, 0, err
	}
	rv := reflect.New(d.elem).Elem()
	for _, f := range d.fields {
		if f.constant {
			if v, ok := consts[f.name]; ok {
				if err := applyStreamValue(rv.FieldByIndex(f.index), f, v, o); err != nil {
					return rv, 0, err
				}
			}
		}
	}
	status := RowNormal
	var seenFields []uint64
	if o.duplicates {
		seenFields = make([]uint64, (len(d.fields)+63)/64)
	}
	var seenUnknown []string
	seenRowType := false
	first := true
	for {
		key, done, err := streamObjectKey(js, &first, o.limits.MaxScalarBytes)
		if err != nil {
			return rv, 0, err
		}
		if done {
			break
		}
		if key == "_RowType_" {
			if seenRowType && o.duplicates {
				return rv, 0, &PathError{Path: fmt.Sprintf("%s[%d].%s", d.name, ri, key), Err: fmt.Errorf("duplicate JSON key")}
			}
			seenRowType = true
			v, err := js.scalar(o.limits.MaxScalarBytes)
			if err != nil || v.null || (o.strict && !validRowTypeJSON(v.text)) {
				if err != nil {
					return rv, 0, err
				}
				return rv, 0, fmt.Errorf("xapi: invalid row type %q", v.text)
			}
			status = parseRowType(v.text)
			continue
		}
		fieldIndex, known := d.lookup[key]
		if known {
			if o.duplicates {
				word, bit := fieldIndex/64, uint(fieldIndex%64)
				if seenFields[word]&(uint64(1)<<bit) != 0 {
					return rv, 0, &PathError{Path: fmt.Sprintf("%s[%d].%s", d.name, ri, key), Err: fmt.Errorf("duplicate JSON key")}
				}
				seenFields[word] |= uint64(1) << bit
			}
			f := d.fields[fieldIndex]
			v, err := js.streamValue(&f, o)
			if err != nil {
				return rv, 0, err
			}
			if err := applyStreamValue(rv.FieldByIndex(f.index), f, v, o); err != nil {
				return rv, 0, &PathError{Path: fmt.Sprintf("%s[%d].%s", d.name, ri, key), Err: err}
			}
			continue
		}
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
		if err := js.skipValue(1, o.limits.MaxDepth); err != nil {
			return rv, 0, err
		}
	}
	if d.rowTypeIndex != nil {
		rv.FieldByIndex(d.rowTypeIndex).SetUint(uint64(status))
	}
	return rv, status, nil
}

func readStreamGenericRows(js *jsonStream, o options) ([]streamGenericRow, error) {
	if err := js.expect('['); err != nil {
		return nil, err
	}
	var rows []streamGenericRow
	first := true
	for ri := 0; ; ri++ {
		done, err := streamArrayNext(js, &first)
		if err != nil {
			return nil, err
		}
		if done {
			return rows, nil
		}
		if o.limits.MaxRows > 0 && ri >= o.limits.MaxRows {
			return nil, fmt.Errorf("xapi: row limit exceeded")
		}
		if err := js.expect('{'); err != nil {
			return nil, err
		}
		row := streamGenericRow{values: map[string]streamValue{}}
		keys := map[string]bool{}
		objFirst := true
		for {
			key, done, err := streamObjectKey(js, &objFirst, o.limits.MaxScalarBytes)
			if err != nil {
				return nil, err
			}
			if done {
				break
			}
			if keys[key] && o.duplicates {
				return nil, fmt.Errorf("xapi: duplicate JSON key %q", key)
			}
			keys[key] = true
			if key == "_RowType_" {
				v, err := js.scalar(o.limits.MaxScalarBytes)
				if err != nil {
					return nil, err
				}
				if v.null || (o.strict && !validRowTypeJSON(v.text)) {
					return nil, fmt.Errorf("xapi: invalid row type %q", v.text)
				}
				row.status = parseRowType(v.text)
				continue
			}
			v, err := js.streamValue(nil, o)
			if err != nil {
				return nil, err
			}
			row.values[key] = v
		}
		rows = append(rows, row)
	}
}

func appendStreamGenericRows(root reflect.Value, d *dataset, o options, consts map[string]streamValue, rows []streamGenericRow) error {
	target := root.FieldByIndex(d.index)
	rowTracking := d.tracked || d.orgRowIndex != nil
	var pending *reflect.Value
	for ri, generic := range rows {
		rv := reflect.New(d.elem).Elem()
		for _, f := range d.fields {
			if f.constant {
				if v, ok := consts[f.name]; ok {
					if err := applyStreamValue(rv.FieldByIndex(f.index), f, v, o); err != nil {
						return err
					}
				}
			}
		}
		for key, v := range generic.values {
			fieldIndex, ok := d.lookup[key]
			if !ok {
				if o.unknown == ErrorUnknownColumns {
					return &PathError{Path: fmt.Sprintf("%s[%d].%s", d.name, ri, key), Err: fmt.Errorf("unknown column")}
				}
				continue
			}
			f := d.fields[fieldIndex]
			if err := applyStreamValue(rv.FieldByIndex(f.index), f, v, o); err != nil {
				return &PathError{Path: fmt.Sprintf("%s[%d].%s", d.name, ri, key), Err: err}
			}
		}
		if d.rowTypeIndex != nil {
			rv.FieldByIndex(d.rowTypeIndex).SetUint(uint64(generic.status))
		}
		if rowTracking && generic.status == RowUpdate {
			pending = &rv
			continue
		}
		if rowTracking && generic.status == rowOriginal {
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
				p := reflect.New(d.elem)
				p.Elem().Set(rv)
				current := *pending
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
			if generic.status == rowOriginal {
				continue
			}
			if d.tracked {
				tr := reflect.New(target.Type().Elem()).Elem()
				tr.FieldByName("Current").Set(rv)
				tr.FieldByName("Type").SetUint(uint64(generic.status))
				appendReflect(target, tr)
			} else {
				appendReflect(target, rv)
			}
		} else if generic.status != RowNormal && d.rowTypeIndex == nil {
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
	return nil
}
