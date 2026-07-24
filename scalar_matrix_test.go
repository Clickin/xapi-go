package xapi

import "testing"

type scalarMatrix struct {
	PString     string      `xapi:"param=p_string,type=STRING"`
	PInt        int32       `xapi:"param=p_int,type=INT"`
	PLong       int64       `xapi:"param=p_long,type=LONG"`
	PFloat      float32     `xapi:"param=p_float,type=FLOAT"`
	PDouble     float64     `xapi:"param=p_double,type=DOUBLE"`
	PDecimal    Decimal     `xapi:"param=p_decimal,type=DECIMAL"`
	PBigDecimal Decimal     `xapi:"param=p_bigdecimal,type=BIGDECIMAL"`
	PDate       Date        `xapi:"param=p_date,type=DATE"`
	PTime       Time        `xapi:"param=p_time,type=TIME"`
	PDateTime   DateTime    `xapi:"param=p_datetime,type=DATETIME"`
	PBlob       []byte      `xapi:"param=p_blob,type=BLOB"`
	PBool       bool        `xapi:"param=p_bool,type=BOOLEAN"`
	Rows        []scalarRow `xapi:"dataset=rows"`
}
type scalarRow struct {
	String     string   `xapi:"col=string,type=STRING"`
	Int        int32    `xapi:"col=int,type=INT"`
	Long       int64    `xapi:"col=long,type=LONG"`
	Float      float32  `xapi:"col=float,type=FLOAT"`
	Double     float64  `xapi:"col=double,type=DOUBLE"`
	Decimal    Decimal  `xapi:"col=decimal,type=DECIMAL"`
	BigDecimal Decimal  `xapi:"col=bigdecimal,type=BIGDECIMAL"`
	Date       Date     `xapi:"col=date,type=DATE"`
	Time       Time     `xapi:"col=time,type=TIME"`
	DateTime   DateTime `xapi:"col=datetime,type=DATETIME"`
	Blob       []byte   `xapi:"col=blob,type=BLOB"`
	Bool       bool     `xapi:"col=bool,type=BOOLEAN"`
}
type extendedIntegerMatrix struct {
	Short int16  `xapi:"param=short,type=SHORT"`
	UInt  uint32 `xapi:"param=uint,type=UINT"`
	ULong uint64 `xapi:"param=ulong,type=ULONG"`
}

func TestExtendedIntegerWireTypesRoundTrip(t *testing.T) {
	in := extendedIntegerMatrix{Short: -2, UInt: ^uint32(0), ULong: ^uint64(0)}
	for _, p := range []Profile{XPlatformXML4000, NexacroXML4000, NexacroJSON10} {
		b, err := Marshal(in, p)
		if err != nil {
			t.Fatalf("%s encode: %v", p, err)
		}
		var out extendedIntegerMatrix
		if err = Unmarshal(b, &out, p); err != nil {
			t.Fatalf("%s decode: %v (%s)", p, err, b)
		}
		if out != in {
			t.Fatalf("%s integer round-trip: %#v", p, out)
		}
	}
}

func TestAllScalarTypesParameterAndColumnRoundTrip(t *testing.T) {
	in := scalarMatrix{PString: "text", PInt: -7, PLong: 9000000000, PFloat: 1.25, PDouble: -2.5, PDecimal: "12.3400", PBigDecimal: "999999999999999999.0001", PDate: "20240229", PTime: "123456789", PDateTime: "20240229123456789", PBlob: []byte{0, 1, 2, 3}, PBool: true, Rows: []scalarRow{{String: "row", Int: -8, Long: 9000000001, Float: 2.5, Double: 3.75, Decimal: "0.1000", BigDecimal: "12345678901234567890.1234", Date: "20240101", Time: "010203", DateTime: "20240101010203", Blob: []byte{3, 2, 1}, Bool: false}}}
	for _, p := range []Profile{XPlatformXML4000, NexacroXML4000, NexacroJSON10} {
		b, e := Marshal(in, p)
		if e != nil {
			t.Fatalf("%s encode: %v", p, e)
		}
		var out scalarMatrix
		if e = Unmarshal(b, &out, p); e != nil {
			t.Fatalf("%s decode: %v (%s)", p, e, b)
		}
		if out.PBigDecimal != in.PBigDecimal || out.PDecimal != in.PDecimal || out.Rows[0].BigDecimal != in.Rows[0].BigDecimal || string(out.PBlob) != string(in.PBlob) || string(out.Rows[0].Blob) != string(in.Rows[0].Blob) || out.PDateTime != in.PDateTime {
			t.Fatalf("%s lexical/type loss: %#v", p, out)
		}
	}
}
