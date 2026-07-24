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

type Cell struct {
	State   string `json:"state"`
	Lexical string `json:"lexical,omitempty"`
}
type Parameter struct {
	ID      string         `json:"id"`
	Type    string         `json:"type"`
	State   string         `json:"state"`
	Lexical string         `json:"lexical,omitempty"`
	Index   int            `json:"index,omitempty"`
	Wire    map[string]any `json:"wire,omitempty"`
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
	Index    int    `json:"index"`
	Value    Cell   `json:"value"`
	Size     string `json:"size,omitempty"`
	Encoding string `json:"encoding,omitempty"`
}
type Row struct {
	Type   string          `json:"type"`
	OrgRow *Row            `json:"orgRow"`
	Values map[string]Cell `json:"values"`
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
