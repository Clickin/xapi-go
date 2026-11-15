package protocol

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const Version = "1.0"

func decodeStrictJSONValue(data []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

type Cell struct {
	State          string `json:"state"`
	Lexical        string `json:"lexical,omitempty"`
	lexicalPresent bool
}

func (cell *Cell) MarkLexicalPresent() {
	cell.lexicalPresent = true
}

func (cell *Cell) UnmarshalJSON(data []byte) error {
	var in struct {
		State   string  `json:"state"`
		Lexical *string `json:"lexical"`
	}
	if err := decodeStrictJSONValue(data, &in); err != nil {
		return err
	}
	cell.State = in.State
	if in.Lexical != nil {
		cell.Lexical = *in.Lexical
		cell.lexicalPresent = true
	}
	return nil
}

func (cell Cell) MarshalJSON() ([]byte, error) {
	type cellJSON struct {
		State   string  `json:"state"`
		Lexical *string `json:"lexical,omitempty"`
	}
	out := cellJSON{State: cell.State}
	if cell.lexicalPresent || cell.Lexical != "" {
		out.Lexical = &cell.Lexical
	}
	return json.Marshal(out)
}

type Parameter struct {
	ID             string         `json:"id"`
	Type           string         `json:"type"`
	State          string         `json:"state"`
	Lexical        string         `json:"lexical,omitempty"`
	Index          int            `json:"index,omitempty"`
	Wire           map[string]any `json:"wire,omitempty"`
	lexicalPresent bool
}

func (parameter *Parameter) MarkLexicalPresent() {
	parameter.lexicalPresent = true
}

func (parameter *Parameter) UnmarshalJSON(data []byte) error {
	var in struct {
		ID      string         `json:"id"`
		Type    string         `json:"type"`
		State   string         `json:"state"`
		Lexical *string        `json:"lexical"`
		Index   int            `json:"index"`
		Wire    map[string]any `json:"wire"`
	}
	if err := decodeStrictJSONValue(data, &in); err != nil {
		return err
	}
	parameter.ID = in.ID
	parameter.Type = in.Type
	parameter.State = in.State
	parameter.Index = in.Index
	parameter.Wire = in.Wire
	if in.Lexical != nil {
		parameter.Lexical = *in.Lexical
		parameter.lexicalPresent = true
	}
	return nil
}

func (parameter Parameter) MarshalJSON() ([]byte, error) {
	type parameterJSON struct {
		ID      string         `json:"id"`
		Type    string         `json:"type"`
		State   string         `json:"state"`
		Lexical *string        `json:"lexical,omitempty"`
		Index   int            `json:"index,omitempty"`
		Wire    map[string]any `json:"wire,omitempty"`
	}
	out := parameterJSON{
		ID: parameter.ID, Type: parameter.Type, State: parameter.State,
		Index: parameter.Index, Wire: parameter.Wire,
	}
	if parameter.lexicalPresent || parameter.Lexical != "" {
		out.Lexical = &parameter.Lexical
	}
	return json.Marshal(out)
}

type Column struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Index    int    `json:"index"`
	Size     string `json:"size,omitempty"`
	Encoding string `json:"encoding,omitempty"`
	Prop     string `json:"prop,omitempty"`
	SumText  string `json:"sumtext,omitempty"`
}
type ConstColumn struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Index    int    `json:"index,omitempty"`
	Value    Cell   `json:"value,omitempty"`
	Size     string `json:"size,omitempty"`
	Encoding string `json:"encoding,omitempty"`
}

func (column ConstColumn) MarshalJSON() ([]byte, error) {
	type constColumnJSON struct {
		ID       string `json:"id"`
		Type     string `json:"type"`
		Index    *int   `json:"index,omitempty"`
		Value    *Cell  `json:"value,omitempty"`
		Size     string `json:"size,omitempty"`
		Encoding string `json:"encoding,omitempty"`
	}
	out := constColumnJSON{
		ID:       column.ID,
		Type:     column.Type,
		Size:     column.Size,
		Encoding: column.Encoding,
	}
	if column.Value.State != "" {
		out.Value = &column.Value
	} else {
		out.Index = &column.Index
	}
	return json.Marshal(out)
}

type Row struct {
	Type          string          `json:"type"`
	OrgRow        *Row            `json:"orgRow"`
	Values        map[string]Cell `json:"values"`
	orgRowPresent bool
}

func (row *Row) MarkOrgRowPresent() {
	row.orgRowPresent = true
}

func (row *Row) UnmarshalJSON(data []byte) error {
	var in struct {
		Type   string          `json:"type"`
		OrgRow json.RawMessage `json:"orgRow"`
		Values map[string]Cell `json:"values"`
	}
	if err := decodeStrictJSONValue(data, &in); err != nil {
		return err
	}
	row.Type = in.Type
	row.Values = in.Values
	if len(in.OrgRow) > 0 {
		row.orgRowPresent = true
		if string(in.OrgRow) != "null" {
			var org Row
			if err := decodeStrictJSONValue(in.OrgRow, &org); err != nil {
				return err
			}
			row.OrgRow = &org
		}
	}
	return nil
}

func (row Row) MarshalJSON() ([]byte, error) {
	if row.orgRowPresent || row.OrgRow != nil {
		return json.Marshal(struct {
			Type   string          `json:"type"`
			OrgRow *Row            `json:"orgRow"`
			Values map[string]Cell `json:"values"`
		}{Type: row.Type, OrgRow: row.OrgRow, Values: row.Values})
	}
	return json.Marshal(struct {
		Type   string          `json:"type"`
		Values map[string]Cell `json:"values"`
	}{Type: row.Type, Values: row.Values})
}

type Dataset struct {
	ID           string         `json:"id"`
	Columns      []Column       `json:"columns"`
	ConstColumns []ConstColumn  `json:"constColumns"`
	Rows         []Row          `json:"rows"`
	SaveType     int            `json:"saveType,omitempty"`
	Wire         map[string]any `json:"wire,omitempty"`
}
type Value struct {
	Parameters []Parameter    `json:"parameters"`
	Datasets   []Dataset      `json:"datasets"`
	SaveType   int            `json:"saveType,omitempty"`
	Wire       map[string]any `json:"wire,omitempty"`
}

type Envelope struct {
	Case      string         `json:"case"`
	Operation string         `json:"operation"`
	Profile   string         `json:"profile"`
	Input     *Input         `json:"input,omitempty"`
	Value     *Value         `json:"value,omitempty"`
	Options   map[string]any `json:"options,omitempty"`
}
type Input struct {
	Encoding string `json:"encoding"`
	Data     string `json:"data"`
}
type Output struct {
	Encoding string `json:"encoding"`
	Data     string `json:"data"`
}
type ErrorBody struct {
	Class   string `json:"class"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message,omitempty"`
}
type Response struct {
	OK     bool       `json:"ok"`
	Value  *Value     `json:"value,omitempty"`
	Output *Output    `json:"output,omitempty"`
	Error  *ErrorBody `json:"error,omitempty"`
}

func DecodeJSON(r io.Reader, dst any, limit int64) error {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return err
	}
	if int64(len(b)) > limit {
		return errors.New("payload exceeds limit")
	}
	if err := RejectDuplicateKeys(b); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(dst); err != nil {
		return err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errors.New("multiple JSON values")
	}
	return nil
}

// RejectDuplicateKeys closes a security and interoperability gap in the
// standard JSON decoder: duplicate names otherwise silently use the last
// value, which can make adapters disagree about the same wire payload.
func RejectDuplicateKeys(b []byte) error {
	d := json.NewDecoder(bytes.NewReader(b))
	if err := walkJSON(d); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}
func walkJSON(d *json.Decoder) error {
	t, err := d.Token()
	if err != nil {
		return err
	}
	switch x := t.(type) {
	case json.Delim:
		switch x {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				kt, err := d.Token()
				if err != nil {
					return err
				}
				key, ok := kt.(string)
				if !ok {
					return errors.New("invalid object key")
				}
				if seen[key] {
					return fmt.Errorf("duplicate JSON key %q", key)
				}
				seen[key] = true
				if err := walkJSON(d); err != nil {
					return err
				}
			}
			_, err = d.Token()
			return err
		case '[':
			for d.More() {
				if err := walkJSON(d); err != nil {
					return err
				}
			}
			_, err = d.Token()
			return err
		}
	}
	return nil
}
func DecodeInput(in Input, whitespace bool) ([]byte, error) {
	if in.Encoding != "base64" {
		return nil, fmt.Errorf("unsupported encoding %q", in.Encoding)
	}
	s := in.Data
	if !whitespace && bytes.IndexFunc([]byte(s), func(r rune) bool { return r == ' ' || r == '\n' || r == '\r' || r == '\t' }) >= 0 {
		return nil, errors.New("base64 whitespace is not allowed")
	}
	if whitespace {
		s = string(bytes.Map(func(r rune) rune {
			if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
				return -1
			}
			return r
		}, []byte(s)))
	}
	return base64.StdEncoding.DecodeString(s)
}
func EncodeOutput(b []byte) *Output {
	return &Output{Encoding: "base64", Data: base64.StdEncoding.EncodeToString(b)}
}
