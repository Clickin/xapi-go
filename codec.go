package xapi

import (
	"bytes"
	"fmt"
	"io"
	"reflect"
)

type Option func(*options)
type options struct {
	strict             bool
	compatibility      bool
	exactLimits        bool
	limits             Limits
	unknown            UnknownColumnPolicy
	missing            MissingDatasetPolicy
	duplicates         bool
	bigDecimal         BigDecimalEncoding
	jsonNull           JSONNullPolicy
	base64WS           bool
	xmlParamValueAttr  bool
	xmlVersionAttr     string
	ssvUnitSeparator   string
	ssvRecordSeparator string
	ssvCodePage        string
}

type UnknownColumnPolicy uint8

const (
	IgnoreUnknownColumns UnknownColumnPolicy = iota
	ErrorUnknownColumns
)

type MissingDatasetPolicy uint8

const (
	AllowMissingDatasets MissingDatasetPolicy = iota
	ErrorMissingDatasets
)

type BigDecimalEncoding uint8

const (
	BigDecimalAsNumber BigDecimalEncoding = iota
	BigDecimalAsString
)

type JSONNullPolicy uint8

const (
	JSONNullAsZero JSONNullPolicy = iota
	ErrorOnJSONNull
)

// Base64WhitespacePolicy controls whitespace in BLOB base64 text.
type Base64WhitespacePolicy uint8

const (
	RejectBase64Whitespace Base64WhitespacePolicy = iota
	AllowBase64Whitespace
)

func WithStrictMode() Option     { return func(o *options) { o.strict = true } }
func WithLimits(l Limits) Option { return func(o *options) { o.limits = l } }

// WithExactLimits applies zero-valued limits as literal zero caps instead of
// replacing them with defaults.
func WithExactLimits(l Limits) Option {
	return func(o *options) {
		o.exactLimits = true
		o.limits = l
	}
}
func WithUnknownColumnPolicy(p UnknownColumnPolicy) Option { return func(o *options) { o.unknown = p } }
func WithMissingDatasetPolicy(p MissingDatasetPolicy) Option {
	return func(o *options) { o.missing = p }
}
func WithCompatibility() Option {
	return func(o *options) {
		o.strict = false
		o.compatibility = true
		o.unknown = IgnoreUnknownColumns
		o.missing = AllowMissingDatasets
		o.duplicates = false
		o.base64WS = true
	}
}
func WithBigDecimalEncoding(p BigDecimalEncoding) Option {
	return func(o *options) { o.bigDecimal = p }
}
func WithJSONNullPolicy(p JSONNullPolicy) Option { return func(o *options) { o.jsonNull = p } }
func WithBase64Whitespace(p Base64WhitespacePolicy) Option {
	return func(o *options) { o.base64WS = p == AllowBase64Whitespace }
}

// WithXMLParameterValueAttribute emits XML Parameter values as a value
// attribute. The decoder accepts both attribute and element-text forms.
func WithXMLParameterValueAttribute() Option {
	return func(o *options) { o.xmlParamValueAttr = true }
}

// WithXMLVersionAttribute emits the root version using the "version"
// spelling instead of the default "ver" spelling.
func WithXMLVersionAttribute() Option {
	return func(o *options) { o.xmlVersionAttr = "version" }
}

func WithSSVSeparators(unit, record string) Option {
	return func(o *options) {
		o.ssvUnitSeparator = unit
		o.ssvRecordSeparator = record
	}
}
func WithSSVCodePage(codePage string) Option {
	return func(o *options) { o.ssvCodePage = codePage }
}

type Codec struct {
	profile Profile
	opts    options
}

func NewCodec(p Profile, os ...Option) (*Codec, error) {
	if e := p.valid(); e != nil {
		return nil, e
	}
	o := options{limits: defaultLimits}
	for _, f := range os {
		f(&o)
	}
	if o.limits.MaxDocumentBytes < 0 || o.limits.MaxScalarBytes < 0 || o.limits.MaxBlobBytes < 0 ||
		o.limits.MaxDepth < 0 || o.limits.MaxDatasets < 0 || o.limits.MaxColumns < 0 || o.limits.MaxRows < 0 {
		return nil, fmt.Errorf("xapi: limits cannot be negative")
	}
	if !o.exactLimits {
		if o.limits.MaxDocumentBytes == 0 {
			o.limits.MaxDocumentBytes = defaultLimits.MaxDocumentBytes
		}
		if o.limits.MaxScalarBytes == 0 {
			o.limits.MaxScalarBytes = defaultLimits.MaxScalarBytes
		}
		if o.limits.MaxBlobBytes == 0 {
			o.limits.MaxBlobBytes = defaultLimits.MaxBlobBytes
		}
		if o.limits.MaxDepth == 0 {
			o.limits.MaxDepth = defaultLimits.MaxDepth
		}
		if o.limits.MaxDatasets == 0 {
			o.limits.MaxDatasets = defaultLimits.MaxDatasets
		}
		if o.limits.MaxColumns == 0 {
			o.limits.MaxColumns = defaultLimits.MaxColumns
		}
		if o.limits.MaxRows == 0 {
			o.limits.MaxRows = defaultLimits.MaxRows
		}
	}
	if o.strict {
		o.compatibility = false
		o.unknown = ErrorUnknownColumns
		o.missing = ErrorMissingDatasets
		o.duplicates = true
		o.base64WS = false
	}
	return &Codec{p, o}, nil
}
func (c *Codec) Decode(r io.Reader, dst any) error {
	if r == nil {
		return fmt.Errorf("xapi: nil reader")
	}
	if dst == nil {
		return fmt.Errorf("xapi: nil destination")
	}
	if c.profile.json() {
		return decodeJSON(r, dst, c.opts)
	}
	if c.profile.ssv() {
		return decodeSSV(r, dst, c.opts, c.profile)
	}
	if c.profile.binary() {
		return decodeBinary(r, dst, c.opts, c.profile)
	}
	return decodeXML(r, dst, c.opts, c.profile)
}
func (c *Codec) Encode(w io.Writer, src any) error {
	if w == nil {
		return fmt.Errorf("xapi: nil writer")
	}
	if src == nil {
		return fmt.Errorf("xapi: nil source")
	}
	if c.profile.json() {
		return encodeJSON(w, src, c.opts)
	}
	if c.profile.ssv() {
		return encodeSSV(w, src, c.opts, c.profile)
	}
	if c.profile.binary() {
		return encodeBinary(w, src, c.opts, c.profile)
	}
	return encodeXML(w, src, c.opts, c.profile)
}
func Decode(r io.Reader, dst any, p Profile, os ...Option) error {
	c, e := NewCodec(p, os...)
	if e != nil {
		return e
	}
	return c.Decode(r, dst)
}
func Encode(w io.Writer, src any, p Profile, os ...Option) error {
	c, e := NewCodec(p, os...)
	if e != nil {
		return e
	}
	return c.Encode(w, src)
}
func Marshal(src any, p Profile, os ...Option) ([]byte, error) {
	var b bytes.Buffer
	e := Encode(&b, src, p, os...)
	return b.Bytes(), e
}
func Unmarshal(b []byte, dst any, p Profile, os ...Option) error {
	return Decode(bytes.NewReader(b), dst, p, os...)
}
func Validate(dst any, _ ...Option) error {
	if dst == nil {
		return fmt.Errorf("xapi: nil schema value")
	}
	v := reflect.ValueOf(dst)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return fmt.Errorf("xapi: nil schema pointer")
		}
		v = v.Elem()
	}
	_, e := compileSchema(v.Type())
	return e
}
func MustValidate(dst any) {
	if e := Validate(dst); e != nil {
		panic(e)
	}
}
func stringValue(v any) string { return fmt.Sprint(v) }
func typeName(v any) string {
	if v == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%T", v)
}
