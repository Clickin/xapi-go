package xapi

import (
	"encoding"
	"encoding/base64"
	"fmt"
	"strings"
)

type Decimal string

// LexicalValue is an explicitly wire-preserving scalar for applications that
// need the original text without selecting a narrower Go primitive.
type LexicalValue string

func (v LexicalValue) String() string               { return string(v) }
func (v LexicalValue) MarshalText() ([]byte, error) { return []byte(v), nil }
func (v *LexicalValue) UnmarshalText(b []byte) error {
	*v = LexicalValue(string(b))
	return nil
}

func (d Decimal) String() string                { return string(d) }
func (d Decimal) MarshalText() ([]byte, error)  { return []byte(d), nil }
func (d *Decimal) UnmarshalText(b []byte) error { *d = Decimal(string(b)); return nil }

type Date string
type Time string
type DateTime string

var _ encoding.TextMarshaler = Decimal("")

func encodeScalar(v any, typ string) (string, error) {
	if v == nil {
		return "", nil
	}
	switch typ {
	case "BLOB":
		b, ok := v.([]byte)
		if !ok {
			return "", &TypeError{Want: "[]byte", Got: typeName(v)}
		}
		return base64.StdEncoding.EncodeToString(b), nil
	case "STRING", "DATE", "TIME", "DATETIME", "DECIMAL", "BIGDECIMAL":
		if t, ok := v.(encoding.TextMarshaler); ok {
			b, e := t.MarshalText()
			s := string(b)
			if strings.ContainsRune(s, 0) {
				return "", fmt.Errorf("xapi: NUL is not allowed")
			}
			return s, e
		}
		s := stringValue(v)
		if strings.ContainsRune(s, 0) {
			return "", fmt.Errorf("xapi: NUL is not allowed")
		}
		return s, nil
	default:
		s := stringValue(v)
		if strings.ContainsRune(s, 0) {
			return "", fmt.Errorf("xapi: NUL is not allowed")
		}
		return s, nil
	}
}
