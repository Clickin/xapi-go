// Package xapihttp contains the optional net/http integration for xapi.
package xapihttp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	xapi "github.com/Clickin/xapi-go"
)

// Client sends one xapi request and decodes its response. The core xapi
// package remains independent of net/http.
type Client struct {
	HTTP    *http.Client
	Headers http.Header
}

func (c Client) Do(ctx context.Context, method, url string, src, dst any, profile xapi.Profile, options ...xapi.Option) error {
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	pr, pw := io.Pipe()
	defer pr.Close()
	go func() { err := xapi.Encode(pw, src, profile, options...); _ = pw.CloseWithError(err) }()
	req, err := http.NewRequestWithContext(ctx, method, url, pr)
	if err != nil {
		_ = pr.Close()
		return err
	}
	req.Header.Set("Content-Type", xapi.ContentType(profile))
	for key, values := range c.Headers {
		if strings.EqualFold(key, "Content-Type") {
			continue
		}
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	resp, err := hc.Do(req)
	if err != nil {
		_ = pr.Close()
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("xapihttp: unexpected HTTP status %s", resp.Status)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		if rp, e := xapi.ProfileFromContentType(ct); e != nil {
			return e
		} else if (rp == xapi.NexacroJSON10) != (profile == xapi.NexacroJSON10) {
			return fmt.Errorf("xapihttp: response profile %s does not match requested %s", rp, profile)
		}
	}
	return xapi.Decode(resp.Body, dst, profile, options...)
}
