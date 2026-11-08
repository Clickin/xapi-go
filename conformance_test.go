package xapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fixtureResponse struct {
	ErrorCode int32        `xapi:"param=ErrorCode,type=INT"`
	ErrorMsg  string       `xapi:"param=ErrorMsg,type=STRING"`
	Output    []fixtureRow `xapi:"dataset=output"`
}
type fixtureRow struct {
	Market    string  `xapi:"const=market,type=STRING"`
	StockCode string  `xapi:"col=stockCode,type=STRING"`
	Price     int32   `xapi:"col=currentprice,type=INT"`
	Amount    Decimal `xapi:"col=amount,type=BIGDECIMAL"`
}

func fixture(t *testing.T, parts ...string) []byte {
	t.Helper()
	b, e := os.ReadFile(filepath.Join(append([]string{"testdata"}, parts...)...))
	if e != nil {
		t.Fatal(e)
	}
	return b
}

func TestPublicXMLFixtures(t *testing.T) {
	var out fixtureResponse
	if e := Unmarshal(fixture(t, "nexacro-xml", "official-dataset.xml"), &out, NexacroXML4000, WithStrictMode()); e != nil {
		t.Fatalf("%s: %v", NexacroXML4000, e)
	}
	if out.ErrorCode != 0 || len(out.Output) != 1 || out.Output[0].Market != "kse" || out.Output[0].StockCode != "10001" || out.Output[0].Amount != "65.7700" {
		t.Fatalf("%s: %#v", NexacroXML4000, out)
	}
}

func TestPublicXPlatformFixture(t *testing.T) {
	type row struct {
		ID      int     `xapi:"col=id,type=INT"`
		Balance Decimal `xapi:"col=balance,type=BIGDECIMAL"`
	}
	var outValue struct {
		Users []row `xapi:"dataset=users"`
	}
	if err := Unmarshal(fixture(t, "xplatform-xml", "fixture.xml"), &outValue, XPlatformXML4000, WithStrictMode()); err != nil {
		t.Fatal(err)
	}
	if len(outValue.Users) != 1 || outValue.Users[0].Balance != "65.7700" {
		t.Fatalf("%#v", outValue)
	}
}
func TestXMLProfileNamespaces(t *testing.T) {
	for _, tt := range []struct {
		p  Profile
		ns string
	}{{XPlatformXML4000, "http://www.tobesoft.com/platform/Dataset"}, {NexacroXML4000, "http://www.nexacroplatform.com/platform/dataset"}} {
		b, e := Marshal(fixtureResponse{}, tt.p)
		if e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(string(b), tt.ns) {
			t.Fatalf("%s namespace missing: %s", tt.p, b)
		}
	}
}

func TestPublicXPlatformXML4000(t *testing.T) {
	wire, err := Marshal(fixtureResponse{}, XPlatformXML4000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), `xmlns="http://www.tobesoft.com/platform/Dataset"`) || !strings.Contains(string(wire), `ver="4000"`) {
		t.Fatalf("unexpected XPLATFORM XML: %s", wire)
	}
	var decoded fixtureResponse
	if err := Unmarshal(wire, &decoded, XPlatformXML4000, WithStrictMode()); err != nil {
		t.Fatal(err)
	}
}
func TestPublicJSONFixture(t *testing.T) {
	var out fixtureResponse
	if e := Unmarshal(fixture(t, "nexacro-json", "official-dataset.json"), &out, NexacroJSON10, WithStrictMode()); e != nil {
		t.Fatal(e)
	}
	if len(out.Output) != 1 || out.Output[0].Market != "kse" || out.Output[0].Price != 5700 || out.Output[0].Amount != "65.7700" {
		t.Fatalf("%#v", out)
	}
	b, e := Marshal(out, NexacroJSON10)
	if e != nil {
		t.Fatal(e)
	}
	var round fixtureResponse
	if e = Unmarshal(b, &round, NexacroJSON10, WithStrictMode()); e != nil {
		t.Fatal(e)
	}
	if round.Output[0].Amount != "65.7700" {
		t.Fatalf("lexical loss: %q", round.Output[0].Amount)
	}
}
func TestPublicJSONTrackedFixture(t *testing.T) {
	type response struct {
		Output []Tracked[fixtureRow] `xapi:"dataset=output"`
	}
	var out response
	if e := Unmarshal(fixture(t, "nexacro-json", "official-tracked.json"), &out, NexacroJSON10, WithStrictMode()); e != nil {
		t.Fatal(e)
	}
	if len(out.Output) != 1 || out.Output[0].Original == nil || out.Output[0].Current.Amount != "65.7700" || out.Output[0].Original.Amount != "64.0000" {
		t.Fatalf("%#v", out.Output)
	}
}
