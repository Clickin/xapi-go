package xapi

import (
	"fmt"
	"mime"
	"strings"
)

func ContentType(p Profile) string {
	if p.json() {
		return "application/json; charset=utf-8"
	}
	if p.ssv() {
		return "text/plain; charset=utf-8"
	}
	if p.binary() {
		return "application/octet-stream"
	}
	return "application/xml; charset=utf-8"
}
func ProfileFromContentType(value string) (Profile, error) {
	media, params, err := mime.ParseMediaType(value)
	if err != nil {
		return 0, err
	}
	if cs := params["charset"]; cs != "" && !strings.EqualFold(cs, "utf-8") {
		return 0, fmt.Errorf("xapi: unsupported charset %q; UTF-8 transcoding is not implicit", cs)
	}
	switch strings.ToLower(media) {
	case "application/json":
		return NexacroJSON10, nil
	case "application/xml", "text/xml":
		return NexacroXML4000, nil
	case "text/plain":
		return NexacroSSV, nil
	case "application/octet-stream":
		return NexacroBinary5000, nil
	default:
		return 0, fmt.Errorf("xapi: unsupported content type %q", media)
	}
}
