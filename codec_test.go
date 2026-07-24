package xapi

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
)

type testBlobSource []byte

func (b testBlobSource) Open() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil }
func (b testBlobSource) Size() int64                  { return int64(len(b)) }

type chunkReader struct {
	r io.Reader
	n int
}
type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func (c chunkReader) Read(p []byte) (int, error) {
	if len(p) > c.n {
		p = p[:c.n]
	}
	return c.r.Read(p)
}

type testResponse struct {
	Code  int32      `xapi:"param=ErrorCode,type=INT"`
	Users []testUser `xapi:"dataset=users"`
}
type testUser struct {
	ID      int32   `xapi:"col=id,type=INT"`
	Amount  Decimal `xapi:"col=amount,type=BIGDECIMAL"`
	Content []byte  `xapi:"col=content,type=BLOB"`
}

func TestXMLJSONRoundTrip(t *testing.T) {
	in := testResponse{Code: 7, Users: []testUser{{ID: 3, Amount: "65.7700", Content: []byte{0, 1, 2}}}}
	for _, p := range []Profile{XPlatformXML4000, NexacroJSON10} {
		b, err := Marshal(in, p)
		if err != nil {
			t.Fatal(p, err)
		}
		var out testResponse
		if err := Unmarshal(b, &out, p); err != nil {
			t.Fatalf("%s: %v (%s)", p, err, b)
		}
		if out.Code != in.Code || out.Users[0].Amount != in.Users[0].Amount || string(out.Users[0].Content) != string(in.Users[0].Content) {
			t.Fatalf("%s: %#v", p, out)
		}
	}
}

func TestCodecConcurrentUse(t *testing.T) {
	type row struct {
		ID int `xapi:"col=id,type=INT"`
	}
	type response struct {
		Rows []row `xapi:"dataset=rows"`
	}
	c, err := NewCodec(NexacroJSON10, WithStrictMode())
	if err != nil {
		t.Fatal(err)
	}
	src := response{Rows: []row{{ID: 1}}}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var b bytes.Buffer
			if err := c.Encode(&b, src); err != nil {
				t.Error(err)
				return
			}
			var dst response
			if err := c.Decode(bytes.NewReader(b.Bytes()), &dst); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

func TestXMLColumnsAreAddressedByID(t *testing.T) {
	x := `<Root><Dataset id="users"><ColumnInfo><Column id="id" type="int"/><Column id="amount" type="bigdecimal"/></ColumnInfo><Rows><Row><Col id="amount">65.7700</Col><Col id="id">3</Col></Row></Rows></Dataset></Root>`
	var out testResponse
	if err := Unmarshal([]byte(x), &out, XPlatformXML4000); err != nil {
		t.Fatal(err)
	}
	if out.Users[0].ID != 3 || out.Users[0].Amount != "65.7700" {
		t.Fatalf("reordered columns lost: %#v", out)
	}
}

func TestEmptyDatasetWritesSchema(t *testing.T) {
	b, err := Marshal(testResponse{}, XPlatformXML4000)
	if err != nil || !strings.Contains(string(b), `<Column id="amount"`) {
		t.Fatalf("%v %s", err, b)
	}
}

func TestSchemaSizeMetadataIsWritten(t *testing.T) {
	type response struct {
		Rows []struct {
			Name string `xapi:"col=name,type=STRING,size=100"`
		} `xapi:"dataset=rows"`
	}
	for _, p := range []Profile{XPlatformXML4000, NexacroJSON10} {
		b, err := Marshal(response{}, p)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), `"size":100`) && !strings.Contains(string(b), `size="100"`) {
			t.Fatalf("size metadata missing for %s: %s", p, b)
		}
	}
}

func TestControlEntityDoesNotDoubleDecode(t *testing.T) {
	var out struct {
		Values []struct {
			Value string `xapi:"col=value,type=STRING"`
		} `xapi:"dataset=values"`
	}
	x := `<Root><Dataset id="values"><ColumnInfo><Column id="value" type="string"/></ColumnInfo><Rows><Row><Col id="value">&#1;</Col></Row><Row><Col id="value">&amp;#1;</Col></Row></Rows></Dataset></Root>`
	if err := Unmarshal([]byte(x), &out, XPlatformXML4000); err != nil {
		t.Fatal(err)
	}
	if len(out.Values) != 2 || out.Values[0].Value != "\x01" || out.Values[1].Value != "&#1;" {
		t.Fatalf("control entity mismatch: %#v", out.Values)
	}
}

func TestControlEntityHexAndNUL(t *testing.T) {
	var out struct {
		Values []struct {
			Value string `xapi:"col=value,type=STRING"`
		} `xapi:"dataset=values"`
	}
	x := `<Root><Dataset id="values"><ColumnInfo/><Rows><Row><Col id="value">&#x1F;</Col></Row></Rows></Dataset></Root>`
	if err := Unmarshal([]byte(x), &out, XPlatformXML4000); err != nil {
		t.Fatal(err)
	}
	if got := out.Values[0].Value; got != "\x1f" {
		t.Fatalf("hex control entity = %q", got)
	}
	type response struct {
		Value string `xapi:"param=value,type=STRING"`
	}
	if _, err := Marshal(response{Value: "a\x00b"}, XPlatformXML4000); err == nil {
		t.Fatal("NUL accepted in XML text")
	}
}

func TestCDATAKeepsNumericReferenceLiteral(t *testing.T) {
	var out struct {
		Values []struct {
			Value string `xapi:"col=value,type=STRING"`
		} `xapi:"dataset=values"`
	}
	x := `<Root><Dataset id="values"><ColumnInfo/><Rows><Row><Col id="value"><![CDATA[&#1;]]></Col></Row></Rows></Dataset></Root>`
	if err := Unmarshal([]byte(x), &out, XPlatformXML4000); err != nil {
		t.Fatal(err)
	}
	if out.Values[0].Value != "&#1;" {
		t.Fatalf("CDATA changed: %q", out.Values[0].Value)
	}
}

func TestControlAndCDATAAcrossReaderChunks(t *testing.T) {
	var out struct {
		Values []struct {
			Value string `xapi:"col=value,type=STRING"`
		} `xapi:"dataset=values"`
	}
	x := `<Root><Dataset id="values"><ColumnInfo/><Rows><Row><Col id="value"><![CDATA[&#1;]]>x&#x1F;</Col></Row></Rows></Dataset></Root>`
	r := chunkReader{r: strings.NewReader(x), n: 1}
	if err := Decode(r, &out, XPlatformXML4000); err != nil {
		t.Fatal(err)
	}
	if got := out.Values[0].Value; got != "&#1;x\x1f" {
		t.Fatalf("chunked control/CDATA decode = %q", got)
	}
}

func TestControlEntityEncode(t *testing.T) {
	type response struct {
		Values []struct {
			Value string `xapi:"col=value,type=STRING"`
		} `xapi:"dataset=values"`
	}
	b, err := Marshal(response{Values: []struct {
		Value string `xapi:"col=value,type=STRING"`
	}{{Value: "a\x01b"}}}, XPlatformXML4000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "a&#1;b") {
		t.Fatalf("not encoded as XAPI entity: %s", b)
	}
}

func TestControlEntityWriterHandlesSplitMarker(t *testing.T) {
	var b bytes.Buffer
	w := &controlEntityWriter{w: &b}
	if _, err := w.Write([]byte{0xee}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte{0x80, 0x81}); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if b.String() != "&#1;" {
		t.Fatalf("split marker output: %q", b.String())
	}
}

func TestTrackedRowsUseTypedCurrent(t *testing.T) {
	type request struct {
		Users []Tracked[testUser] `xapi:"dataset=users"`
	}
	in := request{Users: []Tracked[testUser]{{Type: RowUpdate, Current: testUser{ID: 9, Amount: "1.00"}}}}
	b, err := Marshal(in, NexacroJSON10)
	if err != nil {
		t.Fatal(err)
	}
	var out request
	if err := Unmarshal(b, &out, NexacroJSON10); err != nil {
		t.Fatal(err)
	}
	if len(out.Users) != 1 || out.Users[0].Current.ID != 9 {
		t.Fatalf("tracked row lost: %#v", out)
	}
}

func TestRowTypeAndOrgRowTags(t *testing.T) {
	type row struct {
		Type     RowType `xapi:"rowtype"`
		Original *row    `xapi:"orgrow"`
		ID       int     `xapi:"col=id,type=INT"`
	}
	type request struct {
		Rows []row `xapi:"dataset=rows"`
	}
	in := request{Rows: []row{{Type: RowUpdate, ID: 2, Original: &row{ID: 1}}}}
	for _, p := range []Profile{XPlatformXML4000, NexacroJSON10} {
		b, err := Marshal(in, p)
		if err != nil {
			t.Fatal(err)
		}
		var out request
		if err := Unmarshal(b, &out, p, WithStrictMode()); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if len(out.Rows) != 1 || out.Rows[0].Type != RowUpdate || out.Rows[0].Original == nil || out.Rows[0].Original.ID != 1 {
			t.Fatalf("%s: %#v", p, out.Rows)
		}
	}
}

func TestNexacroJSONOriginalRow(t *testing.T) {
	type request struct {
		Users []Tracked[testUser] `xapi:"dataset=users"`
	}
	x := `{"version":"1.0","Datasets":[{"id":"users","ColumnInfo":{"Column":[{"id":"id","type":"INT"},{"id":"amount","type":"BIGDECIMAL"}]},"Rows":[{"_RowType_":"U","id":9,"amount":"2.00"},{"_RowType_":"O","id":8,"amount":"1.00"}]}]}`
	var out request
	if err := Unmarshal([]byte(x), &out, NexacroJSON10, WithStrictMode()); err != nil {
		t.Fatal(err)
	}
	if len(out.Users) != 1 || out.Users[0].Type != RowUpdate || out.Users[0].Current.ID != 9 || out.Users[0].Original == nil || out.Users[0].Original.ID != 8 {
		t.Fatalf("bad U/O: %#v", out.Users)
	}
}
func TestNexacroJSONDatasetPropertiesMayBeReordered(t *testing.T) {
	var out testResponse
	x := `{"Datasets":[{"Rows":[{"id":3,"amount":65.7700}],"ColumnInfo":{"Column":[{"id":"id","type":"INT"},{"id":"amount","type":"BIGDECIMAL"}]},"id":"users"}]}`
	if e := Unmarshal([]byte(x), &out, NexacroJSON10); e != nil {
		t.Fatal(e)
	}
	if len(out.Users) != 1 || out.Users[0].ID != 3 {
		t.Fatalf("reordered dataset properties lost: %#v", out)
	}
}

func TestNexacroJSONReorderedBlobDatasetProperties(t *testing.T) {
	type row struct {
		Blob []byte `xapi:"col=blob,type=BLOB"`
	}
	type response struct {
		Rows []row `xapi:"dataset=rows"`
	}
	x := `{"Datasets":[{"Rows":[{"blob":"AAEC"}],"ColumnInfo":{"Column":[{"id":"blob","type":"BLOB"}]},"id":"rows"}]}`
	var out response
	if err := Unmarshal([]byte(x), &out, NexacroJSON10, WithStrictMode()); err != nil {
		t.Fatal(err)
	}
	if len(out.Rows) != 1 || !bytes.Equal(out.Rows[0].Blob, []byte{0, 1, 2}) {
		t.Fatalf("%#v", out)
	}
}

func TestNexacroJSONReorderedPropertiesPreserveConstColumns(t *testing.T) {
	type row struct {
		Market string `xapi:"const=market,type=STRING"`
		ID     int    `xapi:"col=id,type=INT"`
	}
	type response struct {
		Rows []row `xapi:"dataset=rows"`
	}
	x := `{"Datasets":[{"Rows":[{"id":3}],"ColumnInfo":{"ConstColumn":[{"id":"market","type":"STRING","value":"KSE"}],"Column":[{"id":"id","type":"INT"}]},"id":"rows"}]}`
	var out response
	if err := Unmarshal([]byte(x), &out, NexacroJSON10, WithStrictMode()); err != nil {
		t.Fatal(err)
	}
	if len(out.Rows) != 1 || out.Rows[0].ID != 3 || out.Rows[0].Market != "KSE" {
		t.Fatalf("reordered const column lost: %#v", out.Rows)
	}
}

func TestXMLOriginalRow(t *testing.T) {
	type request struct {
		Users []Tracked[testUser] `xapi:"dataset=users"`
	}
	x := `<Root><Dataset id="users"><ColumnInfo><Column id="id" type="INT"/><Column id="amount" type="BIGDECIMAL"/></ColumnInfo><Rows><Row type="update"><Col id="id">9</Col><Col id="amount">2.00</Col><OrgRow><Col id="id">8</Col><Col id="amount">1.00</Col></OrgRow></Row></Rows></Dataset></Root>`
	var out request
	if err := Unmarshal([]byte(x), &out, XPlatformXML4000, WithStrictMode()); err != nil {
		t.Fatal(err)
	}
	if len(out.Users) != 1 || out.Users[0].Type != RowUpdate || out.Users[0].Original == nil || out.Users[0].Original.ID != 8 {
		t.Fatalf("bad XML U/O: %#v", out.Users)
	}
}

func TestOptionalPresence(t *testing.T) {
	type row struct {
		Name Optional[string] `xapi:"col=name,type=STRING"`
	}
	type response struct {
		Rows []row `xapi:"dataset=rows"`
	}
	x := `{"Datasets":[{"id":"rows","ColumnInfo":{"Column":[{"id":"name","type":"STRING"}]},"Rows":[{"name":null},{ }]}]}`
	var out response
	if err := Unmarshal([]byte(x), &out, NexacroJSON10); err != nil {
		t.Fatal(err)
	}
	if !out.Rows[0].Name.Present || !out.Rows[0].Name.Null || out.Rows[1].Name.Present {
		t.Fatalf("presence lost: %#v", out.Rows)
	}
}

func TestOptionalEncodePreservesMissingAndEmpty(t *testing.T) {
	type row struct {
		Missing []byte           `xapi:"col=missing,type=BLOB,optional"`
		Empty   []byte           `xapi:"col=empty,type=BLOB,optional"`
		Name    Optional[string] `xapi:"col=name,type=STRING"`
	}
	type response struct {
		Rows []row `xapi:"dataset=rows"`
	}
	in := response{Rows: []row{{Empty: []byte{}, Name: Optional[string]{Present: true, Value: ""}}}}
	for _, p := range []Profile{XPlatformXML4000, NexacroJSON10} {
		b, err := Marshal(in, p)
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		if strings.Contains(s, `<Col id="missing"`) || strings.Contains(s, `"missing":`) {
			t.Fatalf("missing field encoded for %s: %s", p, s)
		}
		if p == XPlatformXML4000 && !strings.Contains(s, `id="empty"`) {
			t.Fatalf("empty XML field omitted: %s", s)
		}
		if p == NexacroJSON10 && !strings.Contains(s, `"empty":""`) {
			t.Fatalf("empty JSON BLOB omitted: %s", s)
		}
		if p == NexacroJSON10 && !strings.Contains(s, `"name":""`) {
			t.Fatalf("present empty Optional omitted: %s", s)
		}
	}
}

func TestConstColumnIsInSchema(t *testing.T) {
	type row struct {
		Market string `xapi:"const=market,type=STRING"`
		ID     int    `xapi:"col=id,type=INT"`
	}
	type response struct {
		Rows []row `xapi:"dataset=rows"`
	}
	b, err := Marshal(response{}, XPlatformXML4000)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "ConstColumn") || !strings.Contains(s, "id=\"market\"") {
		t.Fatalf("missing const column: %s", s)
	}
}
func TestConstColumnDecodesIntoEveryRow(t *testing.T) {
	type row struct {
		Market string `xapi:"const=market,type=STRING"`
		ID     int    `xapi:"col=id,type=INT"`
	}
	type response struct {
		Rows []row `xapi:"dataset=rows"`
	}
	x := `<Root><Dataset id="rows"><ColumnInfo><ConstColumn id="market" type="STRING" value="KSE"/><Column id="id" type="INT"/></ColumnInfo><Rows><Row><Col id="id">1</Col></Row></Rows></Dataset></Root>`
	var out response
	if err := Unmarshal([]byte(x), &out, XPlatformXML4000); err != nil {
		t.Fatal(err)
	}
	if out.Rows[0].Market != "KSE" {
		t.Fatalf("const lost: %#v", out.Rows)
	}
}
func TestBlobSourceEncode(t *testing.T) {
	type row struct {
		Content BlobSource `xapi:"col=content,type=BLOB"`
	}
	type response struct {
		Rows []row `xapi:"dataset=rows"`
	}
	in := response{Rows: []row{{Content: testBlobSource([]byte{0, 1, 2, 3})}}}
	for _, p := range []Profile{XPlatformXML4000, NexacroJSON10} {
		b, e := Marshal(in, p)
		if e != nil {
			t.Fatalf("%s: %v", p, e)
		}
		var out struct {
			Rows []struct {
				Content []byte `xapi:"col=content,type=BLOB"`
			} `xapi:"dataset=rows"`
		}
		if e = Unmarshal(b, &out, p); e != nil {
			t.Fatalf("%s: %v (%s)", p, e, b)
		}
		if !bytes.Equal(out.Rows[0].Content, []byte{0, 1, 2, 3}) {
			t.Fatalf("%s: %v", p, out)
		}
	}
}
func TestXMLBlobDecodeAcrossReaderChunks(t *testing.T) {
	type response struct {
		Blob []byte `xapi:"param=blob,type=BLOB"`
		Rows []struct {
			Blob []byte `xapi:"col=blob,type=BLOB"`
		} `xapi:"dataset=rows"`
	}
	x := []byte(`<Root><Parameters><Parameter id="blob" type="BLOB">AAECAw==</Parameter></Parameters><Dataset id="rows"><ColumnInfo><Column id="blob" type="BLOB"/></ColumnInfo><Rows><Row><Col id="blob">AAECAw==</Col></Row></Rows></Dataset></Root>`)
	var out response
	if e := Decode(chunkReader{r: bytes.NewReader(x), n: 2}, &out, XPlatformXML4000); e != nil {
		t.Fatal(e)
	}
	if string(out.Blob) != string([]byte{0, 1, 2, 3}) || string(out.Rows[0].Blob) != string([]byte{0, 1, 2, 3}) {
		t.Fatalf("blob chunk decode lost: %#v", out)
	}
}

func TestXMLParameterValueAttributeCompatibility(t *testing.T) {
	type response struct {
		Code int    `xapi:"param=code,type=INT"`
		Text string `xapi:"param=text,type=STRING"`
		Blob []byte `xapi:"param=blob,type=BLOB"`
	}
	x := `<Root><Parameters><Parameter id="code" type="INT" value="7"/><Parameter id="text" type="STRING" value="hello"/><Parameter id="blob" type="BLOB" value="AAEC"/></Parameters></Root>`
	var out response
	if err := Unmarshal([]byte(x), &out, XPlatformXML4000); err != nil {
		t.Fatal(err)
	}
	if out.Code != 7 || out.Text != "hello" || !bytes.Equal(out.Blob, []byte{0, 1, 2}) {
		t.Fatalf("attribute parameter lost: %#v", out)
	}
	wire, err := Marshal(response{Code: 8, Text: "world", Blob: []byte{3, 4}}, XPlatformXML4000, WithXMLParameterValueAttribute(), WithXMLVersionAttribute())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), `version="4000"`) || !strings.Contains(string(wire), `id="code" type="int" value="8"`) || !strings.Contains(string(wire), `<Parameter id="blob" type="blob">`) {
		t.Fatalf("unexpected attribute-mode XML: %s", wire)
	}
	var round response
	if err := Unmarshal(wire, &round, XPlatformXML4000); err != nil {
		t.Fatal(err)
	}
	if round.Code != 8 || round.Text != "world" || !bytes.Equal(round.Blob, []byte{3, 4}) {
		t.Fatalf("attribute-mode round trip lost: %#v", round)
	}
}
func TestEncodePropagatesWriterErrors(t *testing.T) {
	src := testResponse{}
	for _, p := range []Profile{XPlatformXML4000, NexacroJSON10} {
		if e := Encode(failWriter{}, src, p); e == nil {
			t.Fatalf("%s swallowed writer error", p)
		}
	}
}
