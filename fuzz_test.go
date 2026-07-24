package xapi

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"reflect"
	"testing"
)

func FuzzXMLDecodeDoesNotPanic(f *testing.F) {
	f.Add([]byte(`<Root><Dataset id="rows"><ColumnInfo/><Rows/></Dataset></Root>`))
	f.Fuzz(func(t *testing.T, data []byte) {
		var dst struct {
			Rows []struct {
				Value string `xapi:"col=value,type=STRING"`
			} `xapi:"dataset=rows"`
		}
		_ = Unmarshal(data, &dst, XPlatformXML4000)
	})
}
func FuzzJSONDecodeDoesNotPanic(f *testing.F) {
	f.Add([]byte(`{"Datasets":[]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		var dst struct {
			Rows []struct {
				Value string `xapi:"col=value,type=STRING"`
			} `xapi:"dataset=rows"`
		}
		_ = Unmarshal(data, &dst, NexacroJSON10)
	})
}
func FuzzJSONBlobDecodeDoesNotPanic(f *testing.F) {
	f.Add([]byte(`{"Datasets":[{"id":"rows","ColumnInfo":{"Column":[{"id":"blob","type":"BLOB"}]},"Rows":[{"blob":"AAEC"}]}]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		var dst struct {
			Rows []struct {
				Blob []byte `xapi:"col=blob,type=BLOB"`
			} `xapi:"dataset=rows"`
		}
		_ = Unmarshal(data, &dst, NexacroJSON10)
	})
}
func FuzzTagParserDoesNotPanic(f *testing.F) {
	f.Add("dataset=rows,col=id,type=INT")
	f.Fuzz(func(t *testing.T, s string) { _ = parseTag(s) })
}
func FuzzBase64DecodeDoesNotPanic(f *testing.F) {
	f.Add([]byte("AAEC"))
	f.Fuzz(func(t *testing.T, b []byte) {
		dst := reflect.New(reflect.TypeOf([]byte{})).Elem()
		_ = assign(dst, string(b), "BLOB")
	})
}

func BenchmarkJSONDecodeRows(b *testing.B) {
	type row struct {
		ID   int    `xapi:"col=id,type=INT"`
		Name string `xapi:"col=name,type=STRING"`
	}
	type response struct {
		Rows []row `xapi:"dataset=rows"`
	}
	data, _ := Marshal(response{Rows: make([]row, 1000)}, NexacroJSON10)
	b.SetBytes(int64(len(data)))
	b.ReportMetric(float64(len(data)), "input-bytes")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var out response
		if err := Unmarshal(data, &out, NexacroJSON10); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkJSONDecode100kRows(b *testing.B) {
	type row struct {
		ID   int    `xapi:"col=id,type=INT"`
		Name string `xapi:"col=name,type=STRING"`
	}
	type response struct {
		Rows []row `xapi:"dataset=rows"`
	}
	rows := make([]row, 100000)
	for i := range rows {
		rows[i].ID = i
	}
	data, _ := Marshal(response{Rows: rows}, NexacroJSON10)
	b.SetBytes(int64(len(data)))
	b.ReportMetric(float64(len(data)), "input-bytes")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var out response
		if e := Unmarshal(data, &out, NexacroJSON10); e != nil {
			b.Fatal(e)
		}
	}
}

type benchmarkRow struct {
	ID   int    `xapi:"col=id,type=INT"`
	Name string `xapi:"col=name,type=STRING"`
}

type benchmarkResponse struct {
	Rows []benchmarkRow `xapi:"dataset=rows"`
}

func benchmarkRows(n int) benchmarkResponse {
	r := benchmarkResponse{Rows: make([]benchmarkRow, n)}
	for i := range r.Rows {
		r.Rows[i] = benchmarkRow{ID: i, Name: "user"}
	}
	return r
}

func BenchmarkXMLDecode10kRows(b *testing.B) {
	data, err := Marshal(benchmarkRows(10_000), NexacroXML4000)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportMetric(float64(len(data)), "input-bytes")
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var out benchmarkResponse
		if err := Unmarshal(data, &out, NexacroXML4000); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkXMLDecode100kRows(b *testing.B) {
	data, err := Marshal(benchmarkRows(100_000), NexacroXML4000)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportMetric(float64(len(data)), "input-bytes")
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var out benchmarkResponse
		if err := Unmarshal(data, &out, NexacroXML4000); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkXMLTokenize100kRows(b *testing.B) {
	data, err := Marshal(benchmarkRows(100_000), NexacroXML4000)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dec := xml.NewDecoder(bytes.NewReader(data))
		for {
			if _, err := dec.Token(); err == io.EOF {
				break
			} else if err != nil {
				b.Fatal(err)
			}
		}
	}
}

type stdXMLBenchmarkRoot struct {
	Dataset stdXMLBenchmarkDataset `xml:"Dataset"`
}
type stdXMLBenchmarkDataset struct {
	Rows stdXMLBenchmarkRows `xml:"Rows"`
}
type stdXMLBenchmarkRows struct {
	Rows []stdXMLBenchmarkRow `xml:"Row"`
}
type stdXMLBenchmarkRow struct {
	Cols []stdXMLBenchmarkCol `xml:"Col"`
}
type stdXMLBenchmarkCol struct {
	ID    string `xml:"id,attr"`
	Value string `xml:",chardata"`
}

func BenchmarkXMLStdlibUnmarshal100kRows(b *testing.B) {
	data, err := Marshal(benchmarkRows(100_000), NexacroXML4000)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportMetric(float64(len(data)), "input-bytes")
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var out stdXMLBenchmarkRoot
		if err := xml.Unmarshal(data, &out); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkJSONStdlibUnmarshal100kRows(b *testing.B) {
	data, err := Marshal(benchmarkRows(100_000), NexacroJSON10)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportMetric(float64(len(data)), "input-bytes")
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var out any
		if err := json.Unmarshal(data, &out); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkJSONEncode100kRows(b *testing.B) {
	src := benchmarkRows(100_000)
	data, err := Marshal(src, NexacroJSON10)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := Encode(io.Discard, src, NexacroJSON10); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkXMLBlobEncode1MB(b *testing.B) {
	type row struct {
		Content []byte `xapi:"col=content,type=BLOB"`
	}
	type response struct {
		Rows []row `xapi:"dataset=rows"`
	}
	src := response{Rows: []row{{Content: make([]byte, 1<<20)}}}
	b.SetBytes(1 << 20)
	b.ReportMetric(float64(1<<20), "blob-bytes")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if e := Encode(io.Discard, src, XPlatformXML4000); e != nil {
			b.Fatal(e)
		}
	}
}
func BenchmarkXMLBlobEncode32MB(b *testing.B) {
	type row struct {
		Content []byte `xapi:"col=content,type=BLOB"`
	}
	type response struct {
		Rows []row `xapi:"dataset=rows"`
	}
	src := response{Rows: []row{{Content: make([]byte, 32<<20)}}}
	b.SetBytes(32 << 20)
	b.ReportMetric(float64(32<<20), "blob-bytes")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if e := Encode(io.Discard, src, XPlatformXML4000); e != nil {
			b.Fatal(e)
		}
	}
}
func BenchmarkJSONBlobDecode32MB(b *testing.B) {
	type row struct {
		Content []byte `xapi:"col=content,type=BLOB"`
	}
	type response struct {
		Rows []row `xapi:"dataset=rows"`
	}
	data, _ := Marshal(response{Rows: []row{{Content: make([]byte, 32<<20)}}}, NexacroJSON10)
	b.SetBytes(32 << 20)
	b.ReportMetric(float64(len(data)), "input-bytes")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var out response
		if e := Unmarshal(data, &out, NexacroJSON10); e != nil {
			b.Fatal(e)
		}
	}
}
