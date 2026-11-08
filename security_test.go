package xapi

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStrictRejectsDuplicateJSONKeys(t *testing.T) {
	type r struct {
		Rows []struct {
			ID int `xapi:"col=id,type=INT"`
		} `xapi:"dataset=rows"`
	}
	x := `{"Datasets":[{"id":"rows","Rows":[{"id":1,"id":2}],"ColumnInfo":{"Column":[]}}]}`
	var out r
	if err := Unmarshal([]byte(x), &out, NexacroJSON10, WithStrictMode()); err == nil {
		t.Fatal("duplicate key accepted")
	}
	if err := Unmarshal([]byte(`{"Datasets":[],"Datasets":[]}`), &out, NexacroJSON10, WithStrictMode()); err == nil {
		t.Fatal("duplicate root key accepted")
	}
}
func TestXMLRejectsDTD(t *testing.T) {
	var out struct{}
	if err := Unmarshal([]byte(`<!DOCTYPE Root [<!ENTITY x "y">]><Root/>`), &out, XPlatformXML4000); err == nil {
		t.Fatal("DTD accepted")
	}
}

func TestXMLRejectsDuplicateAttributes(t *testing.T) {
	var out struct{}
	x := `<Root ver="4000" ver="4000"/>`
	if err := Unmarshal([]byte(x), &out, XPlatformXML4000); err == nil {
		t.Fatal("duplicate XML attribute accepted")
	}
}

func TestXMLProfileAcceptsAnyNamespace(t *testing.T) {
	var out struct{}
	x := `<Root xmlns="http://www.nexacroplatform.com/platform/dataset" ver="4000"/>`
	if err := Unmarshal([]byte(x), &out, XPlatformXML4000); err != nil {
		t.Fatalf("the root namespace must not be checked: %v", err)
	}
}

func TestInvalidFixturesReturnErrors(t *testing.T) {
	type response struct {
		Rows []struct {
			ID int `xapi:"col=id,type=INT"`
		} `xapi:"dataset=rows"`
	}
	for _, name := range []string{"malformed.xml", "dtd.xml"} {
		b, err := os.ReadFile(filepath.Join("testdata", "invalid", name))
		if err != nil {
			t.Fatal(err)
		}
		var out response
		if err := Unmarshal(b, &out, XPlatformXML4000); err == nil {
			t.Fatalf("invalid XML fixture accepted: %s", name)
		}
	}
	b, err := os.ReadFile(filepath.Join("testdata", "invalid", "malformed.json"))
	if err != nil {
		t.Fatal(err)
	}
	var out response
	if err := Unmarshal(b, &out, NexacroJSON10); err == nil {
		t.Fatal("invalid JSON fixture accepted")
	}
}
func TestContentTypeCharset(t *testing.T) {
	if _, e := ProfileFromContentType("application/json; charset=UTF-8"); e != nil {
		t.Fatal(e)
	}
	if _, e := ProfileFromContentType("application/json; charset=EUC-KR"); e == nil {
		t.Fatal("non-UTF-8 accepted")
	}
}
func TestXMLDeclarationCharset(t *testing.T) {
	var out struct{}
	const declared = `<?xml version="1.0" encoding="EUC-KR"?><Root/>`
	if e := Unmarshal([]byte(declared), &out, XPlatformXML4000, WithStrictMode()); e == nil {
		t.Fatal("non-UTF-8 declaration accepted in strict mode")
	}
	if e := Unmarshal([]byte(declared), &out, XPlatformXML4000); e != nil {
		t.Fatalf("default mode must ignore the declared encoding: %v", e)
	}
	// The bytes are still read as UTF-8: EUC-KR text must not pass as data.
	raw := []byte("<?xml version=\"1.0\" encoding=\"EUC-KR\"?><Root>\xc7\xd1</Root>")
	if e := Unmarshal(raw, &out, XPlatformXML4000); e == nil {
		t.Fatal("invalid UTF-8 accepted under a non-UTF-8 declaration")
	}
}
func TestDepthLimits(t *testing.T) {
	var out struct{}
	if e := Unmarshal([]byte(`<Root><A><B/></A></Root>`), &out, XPlatformXML4000, WithLimits(Limits{MaxDepth: 2})); e == nil {
		t.Fatal("XML depth accepted")
	}
	if e := Unmarshal([]byte(`{"x":{"y":{}}}`), &out, NexacroJSON10, WithLimits(Limits{MaxDepth: 1})); e == nil {
		t.Fatal("JSON depth accepted")
	}
}

func TestDocumentLimitCountsTrailingBytes(t *testing.T) {
	var out struct{}
	jsonWire := []byte(`{"Datasets":[]}`)
	jsonWire = append(jsonWire, []byte(strings.Repeat(" ", 32))...)
	if err := Unmarshal(jsonWire, &out, NexacroJSON10, WithLimits(Limits{MaxDocumentBytes: int64(len(`{"Datasets":[]}`)) + 4})); err == nil {
		t.Fatal("JSON trailing bytes bypassed document limit")
	}
	xmlWire := append([]byte(`<Root/>`), []byte(strings.Repeat(" ", 32))...)
	if err := Unmarshal(xmlWire, &out, NexacroXML4000, WithLimits(Limits{MaxDocumentBytes: int64(len(`<Root/>`)) + 4})); err == nil {
		t.Fatal("XML trailing bytes bypassed document limit")
	}
}

func TestJSONRejectsTrailingDocument(t *testing.T) {
	var out struct{}
	if err := Unmarshal([]byte(`{"Datasets":[]} {"Datasets":[]}`), &out, NexacroJSON10); err == nil {
		t.Fatal("trailing JSON document accepted")
	}
}

func TestJSONRejectsMalformedNumber(t *testing.T) {
	var out struct {
		Value string `xapi:"param=value,type=STRING"`
	}
	for _, value := range []string{"12A", "01", "1.", "1e", "-"} {
		wire := `{"Parameters":[{"id":"value","value":` + value + `}]}`
		if err := Unmarshal([]byte(wire), &out, NexacroJSON10); err == nil {
			t.Fatalf("malformed JSON number accepted: %s", value)
		}
	}
}
func TestBlobLimit(t *testing.T) {
	type row struct {
		Content []byte `xapi:"col=content,type=BLOB"`
	}
	type response struct {
		Rows []row `xapi:"dataset=rows"`
	}
	x := `{"Datasets":[{"id":"rows","Rows":[{"content":"AAEC"}],"ColumnInfo":{"Column":[]}}]}`
	var out response
	if e := Unmarshal([]byte(x), &out, NexacroJSON10, WithLimits(Limits{MaxBlobBytes: 2})); e == nil {
		t.Fatal("BLOB limit ignored")
	}
}

func TestBlobBoundariesAndTrackedOriginal(t *testing.T) {
	type row struct {
		Blob []byte `xapi:"col=blob,type=BLOB"`
	}
	type response struct {
		Param []byte         `xapi:"param=blob,type=BLOB"`
		Rows  []Tracked[row] `xapi:"dataset=rows"`
	}
	for n := 0; n <= 3; n++ {
		payload := make([]byte, n)
		for i := range payload {
			payload[i] = byte(i + 1)
		}
		in := response{Param: payload, Rows: []Tracked[row]{{Type: RowUpdate, Current: row{Blob: payload}, Original: &row{Blob: []byte{9}}}}}
		for _, p := range []Profile{XPlatformXML4000, NexacroJSON10} {
			wire, err := Marshal(in, p)
			if err != nil {
				t.Fatal(err)
			}
			var out response
			if err := Unmarshal(wire, &out, p, WithStrictMode()); err != nil {
				t.Fatalf("%s/%d: %v", p, n, err)
			}
			if len(out.Rows) != 1 || !bytes.Equal(out.Param, payload) || !bytes.Equal(out.Rows[0].Current.Blob, payload) || out.Rows[0].Original == nil || !bytes.Equal(out.Rows[0].Original.Blob, []byte{9}) {
				t.Fatalf("%s/%d: %#v", p, n, out)
			}
		}
	}
	var out struct {
		Rows []struct {
			Blob []byte `xapi:"col=blob,type=BLOB"`
		} `xapi:"dataset=rows"`
	}
	for _, value := range []string{"AA?=", "A===", "AA=A"} {
		wire := `{"Datasets":[{"id":"rows","ColumnInfo":{"Column":[{"id":"blob","type":"BLOB"}]},"Rows":[{"blob":"` + value + `"}]}]}`
		if err := Unmarshal([]byte(wire), &out, NexacroJSON10); err == nil {
			t.Fatalf("invalid base64 accepted: %s", value)
		}
	}
}

func TestWireColumnLimit(t *testing.T) {
	var out struct {
		Rows []struct {
			ID int `xapi:"col=id,type=INT"`
		} `xapi:"dataset=rows"`
	}
	jsonWire := `{"Datasets":[{"id":"rows","ColumnInfo":{"Column":[{"id":"id","type":"INT"},{"id":"extra","type":"STRING"}]},"Rows":[]}]}`
	if err := Unmarshal([]byte(jsonWire), &out, NexacroJSON10, WithLimits(Limits{MaxColumns: 1})); err == nil {
		t.Fatal("JSON wire column limit ignored")
	}
	xmlWire := `<Root><Dataset id="rows"><ColumnInfo><Column id="id" type="INT"/><Column id="extra" type="STRING"/></ColumnInfo><Rows/></Dataset></Root>`
	if err := Unmarshal([]byte(xmlWire), &out, NexacroXML4000, WithLimits(Limits{MaxColumns: 1})); err == nil {
		t.Fatal("XML wire column limit ignored")
	}
}

func TestStrictValidatesWireColumnMetadata(t *testing.T) {
	var out struct {
		Rows []struct {
			ID int `xapi:"col=id,type=INT"`
		} `xapi:"dataset=rows"`
	}
	jsonWire := `{"Datasets":[{"id":"rows","ColumnInfo":{"Column":[{"id":"id","type":"STRING"}]},"Rows":[]}]}`
	if err := Unmarshal([]byte(jsonWire), &out, NexacroJSON10, WithStrictMode()); err == nil {
		t.Fatal("JSON wire type mismatch accepted")
	}
	xmlWire := `<Root><Dataset id="rows"><ColumnInfo><Column id="id" type="STRING"/></ColumnInfo><Rows/></Dataset></Root>`
	if err := Unmarshal([]byte(xmlWire), &out, NexacroXML4000, WithStrictMode()); err == nil {
		t.Fatal("XML wire type mismatch accepted")
	}
}

func TestReferenceWireTypeAliases(t *testing.T) {
	type row struct {
		Amount Decimal  `xapi:"col=amount,type=BIG_DECIMAL"`
		When   DateTime `xapi:"col=when,type=DATE_TIME"`
	}
	type response struct {
		Rows []row `xapi:"dataset=rows"`
	}
	wire := `{"version":"1.0","Datasets":[{"id":"rows","ColumnInfo":{"Column":[{"id":"amount","type":"BIG_DECIMAL"},{"id":"when","type":"DATE_TIME"}]},"Rows":[{"amount":"1.2300","when":"20240101010203"}]}]}`
	var out response
	if err := Unmarshal([]byte(wire), &out, NexacroJSON10, WithStrictMode()); err != nil {
		t.Fatal(err)
	}
	if out.Rows[0].Amount != "1.2300" || out.Rows[0].When != "20240101010203" {
		t.Fatalf("wire aliases lost: %#v", out)
	}
}

func TestStrictRejectsAmbiguousReorderedDatasetRows(t *testing.T) {
	type row struct {
		ID int `xapi:"col=id,type=INT"`
	}
	type response struct {
		A []row `xapi:"dataset=a"`
		B []row `xapi:"dataset=b"`
	}
	wire := `{"Datasets":[{"Rows":[{"id":1}],"id":"a"},{"id":"b","Rows":[]}]}`
	if err := Unmarshal([]byte(wire), &response{}, NexacroJSON10, WithStrictMode()); err == nil {
		t.Fatal("ambiguous reordered rows accepted in strict mode")
	}
}

func TestBase64WhitespacePolicy(t *testing.T) {
	type row struct {
		Blob []byte `xapi:"col=blob,type=BLOB"`
	}
	type response struct {
		Param []byte `xapi:"param=blob,type=BLOB"`
		Rows  []row  `xapi:"dataset=rows"`
	}
	jsonWire := `{"Parameters":[{"id":"blob","type":"BLOB","value":"AA ECAw=="}],"Datasets":[{"id":"rows","ColumnInfo":{"Column":[{"id":"blob","type":"BLOB"}]},"Rows":[{"blob":"AA\nECAw=="}]}]}`
	var out response
	if err := Unmarshal([]byte(jsonWire), &out, NexacroJSON10); err == nil {
		t.Fatal("strict base64 whitespace accepted")
	}
	if err := Unmarshal([]byte(jsonWire), &out, NexacroJSON10, WithCompatibility()); err != nil {
		t.Fatal(err)
	}
	if string(out.Param) != "\x00\x01\x02\x03" || string(out.Rows[0].Blob) != "\x00\x01\x02\x03" {
		t.Fatalf("decoded BLOB = %#v", out)
	}

	xmlWire := `<Root><Parameters><Parameter id="blob" type="BLOB">AA ECAw==</Parameter></Parameters><Dataset id="rows"><ColumnInfo><Column id="blob" type="BLOB"/></ColumnInfo><Rows><Row><Col id="blob">AA
ECAw==</Col></Row></Rows></Dataset></Root>`
	out = response{}
	if err := Unmarshal([]byte(xmlWire), &out, NexacroXML4000); err == nil {
		t.Fatal("strict XML base64 whitespace accepted")
	}
	if err := Unmarshal([]byte(xmlWire), &out, NexacroXML4000, WithCompatibility()); err != nil {
		t.Fatal(err)
	}
	if string(out.Param) != "\x00\x01\x02\x03" || string(out.Rows[0].Blob) != "\x00\x01\x02\x03" {
		t.Fatalf("decoded XML BLOB = %#v", out)
	}
}
func TestUnsupportedDatePrecision(t *testing.T) {
	var out struct {
		When DateTime `xapi:"param=when,type=DATETIME"`
	}
	if e := Unmarshal([]byte(`<Root><Parameters><Parameter id="when">202401010102030000</Parameter></Parameters></Root>`), &out, XPlatformXML4000); e == nil {
		t.Fatal("unsupported DATETIME precision accepted")
	}
}
func TestEncodeValueLimits(t *testing.T) {
	type row struct {
		Name string `xapi:"col=name,type=STRING"`
		Blob []byte `xapi:"col=blob,type=BLOB"`
	}
	type response struct {
		Rows []row `xapi:"dataset=rows"`
	}
	in := response{Rows: []row{{Name: "long", Blob: []byte{1, 2}}}}
	if err := Encode(io.Discard, in, NexacroJSON10, WithLimits(Limits{MaxScalarBytes: 2, MaxBlobBytes: 1})); err == nil {
		t.Fatal("encode scalar/blob limits ignored")
	}
	if err := Encode(io.Discard, in, NexacroJSON10, WithExactLimits(Limits{MaxRows: 0})); err == nil {
		t.Fatal("exact zero row limit ignored")
	}
}

func TestNilPublicArguments(t *testing.T) {
	if e := Decode(nil, &struct{}{}, NexacroJSON10); e == nil {
		t.Fatal("nil reader accepted")
	}
	if e := Encode(io.Discard, nil, NexacroJSON10); e == nil {
		t.Fatal("nil source accepted")
	}
}

func TestNegativeLimitsRejected(t *testing.T) {
	if _, err := NewCodec(NexacroJSON10, WithLimits(Limits{MaxRows: -1})); err == nil {
		t.Fatal("negative limit accepted")
	}
}

func TestSchemaValidationRejectsInvalidTags(t *testing.T) {
	tests := []struct {
		name string
		v    any
	}{
		{"wire type", struct {
			Rows []struct {
				Value string `xapi:"col=value,type=UNKNOWN"`
			} `xapi:"dataset=rows"`
		}{}},
		{"size", struct {
			Rows []struct {
				Value string `xapi:"col=value,type=STRING,size=bad"`
			} `xapi:"dataset=rows"`
		}{}},
		{"blob target", struct {
			Rows []struct {
				Value string `xapi:"col=value,type=BLOB"`
			} `xapi:"dataset=rows"`
		}{}},
		{"duplicate columns", struct {
			Rows []struct {
				A string `xapi:"col=value,type=STRING"`
				B string `xapi:"col=value,type=STRING"`
			} `xapi:"dataset=rows"`
		}{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := Validate(tt.v); err == nil {
				t.Fatal("invalid schema accepted")
			}
		})
	}
}

func TestStrictRejectsDecimalFloatBinding(t *testing.T) {
	type response struct {
		Amount float64 `xapi:"param=amount,type=BIGDECIMAL"`
	}
	if err := Validate(response{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Marshal(response{Amount: 1.25}, NexacroJSON10, WithStrictMode()); err == nil {
		t.Fatal("strict decimal float binding accepted")
	}
}

func TestDecodeErrorIncludesDatasetPath(t *testing.T) {
	type row struct {
		When Date `xapi:"col=when,type=DATE"`
	}
	type response struct {
		Rows []row `xapi:"dataset=rows"`
	}
	x := `{"Datasets":[{"id":"rows","ColumnInfo":{"Column":[{"id":"when","type":"DATE"}]},"Rows":[{"when":"not-a-date"}]}]}`
	err := Unmarshal([]byte(x), &response{}, NexacroJSON10)
	if err == nil || !strings.Contains(err.Error(), "rows[0].when") {
		t.Fatalf("missing path in error: %v", err)
	}
}

func TestStrictRejectsInvalidXMLOriginalRows(t *testing.T) {
	type row struct {
		ID int `xapi:"col=id,type=INT"`
	}
	type trackedResponse struct {
		Rows []Tracked[row] `xapi:"dataset=rows"`
	}
	base := `<Root><Dataset id="rows"><ColumnInfo><Column id="id" type="INT"/></ColumnInfo><Rows>%s</Rows></Dataset></Root>`
	for _, rows := range []string{
		`<Row type="update"><Col id="id">1</Col></Row>`,
		`<Row><Col id="id">1</Col><OrgRow><Col id="id">0</Col></OrgRow></Row>`,
	} {
		var out trackedResponse
		if err := Unmarshal([]byte(fmt.Sprintf(base, rows)), &out, NexacroXML4000, WithStrictMode()); err == nil {
			t.Fatalf("invalid original-row sequence accepted: %s", rows)
		}
	}
	var plain struct {
		Rows []row `xapi:"dataset=rows"`
	}
	if err := Unmarshal([]byte(fmt.Sprintf(base, `<Row type="update"><Col id="id">1</Col></Row>`)), &plain, NexacroXML4000, WithStrictMode()); err == nil {
		t.Fatal("row status accepted for untracked dataset")
	}
}
func TestJSONPolicies(t *testing.T) {
	type row struct {
		Amount Decimal `xapi:"col=amount,type=BIGDECIMAL"`
		Name   string  `xapi:"col=name,type=STRING"`
	}
	type response struct {
		Rows []row `xapi:"dataset=rows"`
	}
	b, e := Marshal(response{Rows: []row{{Amount: "1.2300"}}}, NexacroJSON10, WithBigDecimalEncoding(BigDecimalAsString))
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(b), `"amount":"1.2300"`) {
		t.Fatalf("decimal string policy ignored: %s", b)
	}
	var out response
	if e = Unmarshal([]byte(`{"Datasets":[{"id":"rows","Rows":[{"name":null}],"ColumnInfo":{"Column":[]}}]}`), &out, NexacroJSON10, WithJSONNullPolicy(ErrorOnJSONNull)); e == nil {
		t.Fatal("null policy ignored")
	}
}
