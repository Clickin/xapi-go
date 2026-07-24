package xapi

import (
	"bytes"
	"testing"
)

type constScalarRow struct {
	String     string   `xapi:"const=string,type=STRING"`
	Int        int32    `xapi:"const=int,type=INT"`
	Long       int64    `xapi:"const=long,type=LONG"`
	Float      float32  `xapi:"const=float,type=FLOAT"`
	Double     float64  `xapi:"const=double,type=DOUBLE"`
	Decimal    Decimal  `xapi:"const=decimal,type=DECIMAL"`
	BigDecimal Decimal  `xapi:"const=bigdecimal,type=BIGDECIMAL"`
	Date       Date     `xapi:"const=date,type=DATE"`
	Time       Time     `xapi:"const=time,type=TIME"`
	DateTime   DateTime `xapi:"const=datetime,type=DATETIME"`
	Blob       []byte   `xapi:"const=blob,type=BLOB"`
	Bool       bool     `xapi:"const=bool,type=BOOLEAN"`
}
type constScalarResponse struct {
	Rows []constScalarRow `xapi:"dataset=rows"`
}

func TestAllScalarTypesConstColumnDecode(t *testing.T) {
	x := `{"version":"1.0","Datasets":[{"id":"rows","ColumnInfo":{"ConstColumn":[{"id":"string","type":"STRING","value":"text"},{"id":"int","type":"INT","value":-7},{"id":"long","type":"LONG","value":9000000000},{"id":"float","type":"FLOAT","value":1.25},{"id":"double","type":"DOUBLE","value":-2.5},{"id":"decimal","type":"DECIMAL","value":12.3400},{"id":"bigdecimal","type":"BIGDECIMAL","value":999999999999999999.0001},{"id":"date","type":"DATE","value":"20240229"},{"id":"time","type":"TIME","value":"123456"},{"id":"datetime","type":"DATETIME","value":"20240229123456"},{"id":"blob","type":"BLOB","value":"AAECAw=="},{"id":"bool","type":"BOOLEAN","value":true}]},"Rows":[{}]}]}`
	var out constScalarResponse
	if e := Unmarshal([]byte(x), &out, NexacroJSON10, WithStrictMode()); e != nil {
		t.Fatal(e)
	}
	r := out.Rows[0]
	if r.String != "text" || r.Int != -7 || r.Long != 9000000000 || r.Decimal != "12.3400" || r.BigDecimal != "999999999999999999.0001" || r.Date != "20240229" || string(r.Blob) != string([]byte{0, 1, 2, 3}) || !r.Bool {
		t.Fatalf("const scalar loss: %#v", r)
	}
}

func TestConstBlobRoundTripXMLAndJSON(t *testing.T) {
	type row struct {
		Blob []byte `xapi:"const=blob,type=BLOB"`
	}
	type response struct {
		Rows []row `xapi:"dataset=rows"`
	}
	for _, profile := range []Profile{XPlatformXML4000, NexacroJSON10} {
		wire, err := Marshal(response{}, profile)
		if err != nil {
			t.Fatalf("%s encode: %v", profile, err)
		}
		var out response
		if err := Unmarshal(wire, &out, profile); err != nil {
			t.Fatalf("%s decode: %v (%s)", profile, err, wire)
		}
	}
	var out response
	if err := Unmarshal([]byte(`<Root><Dataset id="rows"><ColumnInfo><ConstColumn id="blob" type="BLOB" value="AAECAw=="/></ColumnInfo><Rows><Row/></Rows></Dataset></Root>`), &out, XPlatformXML4000); err != nil {
		t.Fatal(err)
	}
	if len(out.Rows) != 1 || !bytes.Equal(out.Rows[0].Blob, []byte{0, 1, 2, 3}) {
		t.Fatalf("const BLOB lost: %#v", out)
	}
}

func TestConstColumnTagValueIsEncoded(t *testing.T) {
	type row struct {
		Market string `xapi:"const=market,type=STRING,value=KSE"`
		Count  int    `xapi:"const=count,type=INT,value=7"`
		Blob   []byte `xapi:"const=blob,type=BLOB,value=AAE="`
	}
	type response struct {
		Rows []row `xapi:"dataset=rows"`
	}
	for _, profile := range []Profile{XPlatformXML4000, NexacroJSON10} {
		wire, err := Marshal(response{Rows: []row{{}}}, profile)
		if err != nil {
			t.Fatalf("%s encode: %v", profile, err)
		}
		var out response
		if err := Unmarshal(wire, &out, profile); err != nil {
			t.Fatalf("%s decode: %v (%s)", profile, err, wire)
		}
		if len(out.Rows) != 1 || out.Rows[0].Market != "KSE" || out.Rows[0].Count != 7 || !bytes.Equal(out.Rows[0].Blob, []byte{0, 1}) {
			t.Fatalf("%s const tag value lost: %#v", profile, out.Rows)
		}
	}
}
