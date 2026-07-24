package xapihttp

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	xapi "github.com/senghyunjo/xapi-go"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestClientStreamsRoundTrip(t *testing.T) {
	type row struct {
		ID int `xapi:"col=id,type=INT"`
	}
	type msg struct {
		Rows []row `xapi:"dataset=rows"`
	}
	hc := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Content-Type") == "" {
			t.Error("missing content type")
		}
		if r.Header.Get("Authorization") != "Bearer test" {
			t.Errorf("missing custom header: %q", r.Header.Get("Authorization"))
		}
		b, _ := io.ReadAll(r.Body)
		return &http.Response{StatusCode: 200, Status: "200 OK", Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(b)))}, nil
	})}
	var got msg
	if err := (Client{HTTP: hc, Headers: http.Header{"Authorization": {"Bearer test"}}}).Do(context.Background(), http.MethodPost, "http://xapi.test", msg{Rows: []row{{ID: 4}}}, &got, xapi.NexacroJSON10); err != nil {
		t.Fatal(err)
	}
	if got.Rows[0].ID != 4 {
		t.Fatalf("%#v", got)
	}
}

func TestClientHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	hc := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	err := (Client{HTTP: hc}).Do(ctx, http.MethodPost, "http://xapi.test", struct{}{}, &struct{}{}, xapi.NexacroJSON10)
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("unexpected cancellation error: %v", err)
	}
}

func TestClientReturnsMalformedResponse(t *testing.T) {
	hc := &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Status: "200 OK", Header: make(http.Header), Body: io.NopCloser(strings.NewReader("not-json"))}, nil
	})}
	if err := (Client{HTTP: hc}).Do(context.Background(), http.MethodPost, "http://xapi.test", struct{}{}, &struct{}{}, xapi.NexacroJSON10); err == nil {
		t.Fatal("malformed response accepted")
	}
}

func TestClientAcceptsXMLProfileContentType(t *testing.T) {
	type row struct {
		ID int `xapi:"col=id,type=INT"`
	}
	type msg struct {
		Rows []row `xapi:"dataset=rows"`
	}
	hc := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: 200, Status: "200 OK", Header: http.Header{"Content-Type": {"application/xml; charset=utf-8"}}, Body: io.NopCloser(strings.NewReader(string(b)))}, nil
	})}
	var out msg
	if err := (Client{HTTP: hc}).Do(context.Background(), http.MethodPost, "http://xapi.test", msg{Rows: []row{{ID: 7}}}, &out, xapi.XPlatformXML4000); err != nil {
		t.Fatal(err)
	}
	if len(out.Rows) != 1 || out.Rows[0].ID != 7 {
		t.Fatalf("%#v", out)
	}
}
func TestClientClosesRequestPipeOnNon2xx(t *testing.T) {
	hc := &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 500, Status: "500 Server Error", Header: make(http.Header), Body: io.NopCloser(strings.NewReader("error"))}, nil
	})}
	err := (Client{HTTP: hc}).Do(context.Background(), http.MethodPost, "http://xapi.test", struct{}{}, &struct{}{}, xapi.NexacroJSON10)
	if err == nil || !strings.Contains(err.Error(), "unexpected HTTP status") {
		t.Fatalf("unexpected error: %v", err)
	}
}
