package main

import (
	"bufio"
	"compress/zlib"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	xapi "github.com/Clickin/xapi-go"
	conformancecodec "github.com/Clickin/xapi-go/internal/conformance/codec"
	conformanceprotocol "github.com/Clickin/xapi-go/internal/conformance/protocol"
)

type request struct {
	Case      string         `json:"case,omitempty"`
	Operation string         `json:"operation"`
	Profile   string         `json:"profile"`
	Input     *wireInput     `json:"input,omitempty"`
	Value     map[string]any `json:"value,omitempty"`
	Options   map[string]any `json:"options,omitempty"`
}
type wireInput struct {
	Encoding string `json:"encoding"`
	Data     string `json:"data"`
}
type response struct {
	OK     bool           `json:"ok"`
	Value  map[string]any `json:"value,omitempty"`
	Output *wireInput     `json:"output,omitempty"`
	Error  *errorBody     `json:"error,omitempty"`
}
type errorBody struct {
	Class   string `json:"class"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message,omitempty"`
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "stdio" {
		runStdio()
		return
	}
	addr := ":8787"
	if len(os.Args) > 1 {
		addr = os.Args[1]
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/capabilities", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeResponse(w, response{Error: &errorBody{Class: "invalid-request", Message: "GET required"}}, http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, capabilities())
	})
	for _, op := range []string{"decode", "encode", "roundtrip"} {
		op := op
		mux.HandleFunc("/"+op, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				writeResponse(w, failure("invalid-request", "", "POST required"), http.StatusMethodNotAllowed)
				return
			}
			var req request
			if err := json.NewDecoder(io.LimitReader(r.Body, 10<<20+1)).Decode(&req); err != nil {
				writeResponse(w, failure("invalid-request", "", err.Error()), http.StatusBadRequest)
				return
			}
			if req.Operation != op {
				writeResponse(w, failure("unsupported-operation", "operation", "operation does not match endpoint"), http.StatusBadRequest)
				return
			}
			writeResponse(w, handle(req), http.StatusOK)
		})
	}
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runStdio() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64<<10), 10<<20)
	enc := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			_ = enc.Encode(failure("invalid-request", "", err.Error()))
			continue
		}
		if req.Operation == "capabilities" {
			_ = enc.Encode(capabilities())
			continue
		}
		_ = enc.Encode(handle(req))
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}

func handle(req request) response {
	if req.Operation != "decode" && req.Operation != "encode" && req.Operation != "roundtrip" {
		return failure("unsupported-operation", "operation", "unsupported operation")
	}
	if _, err := parseProfile(req.Profile); err != nil {
		return failure("unsupported-profile", "profile", err.Error())
	}
	if _, err := parseOptions(req.Options, req.Profile); err != nil {
		return failure("invalid-request", "options", err.Error())
	}
	strict := true
	if value, ok := req.Options["strict"].(bool); ok {
		strict = value
	}
	decodeOptions := conformancecodec.DecodeOptions{
		Strict:             strict,
		SSVUnitSeparator:   stringOption(req.Options, "ssvUnitSeparator"),
		SSVRecordSeparator: stringOption(req.Options, "ssvRecordSeparator"),
	}
	switch req.Operation {
	case "encode":
		if req.Value == nil {
			return failure("invalid-request", "value", "value is required")
		}
		value, err := canonicalValue(req.Value)
		if err != nil {
			return failure("invalid-value", "value", err.Error())
		}
		out, err := conformancecodec.Encode(value, req.Profile)
		if err != nil {
			class := "invalid-value"
			if strings.Contains(err.Error(), "does not support") || strings.Contains(err.Error(), "saveType") {
				class = "malformed-input"
			}
			return failure(class, "value", err.Error())
		}
		if zlibEnabled(req.Options) {
			out, err = conformancecodec.DeflateZlib(out)
			if err != nil {
				return failure("invalid-value", "value", err.Error())
			}
		}
		return response{OK: true, Value: req.Value, Output: &wireInput{Encoding: "base64", Data: base64.StdEncoding.EncodeToString(out)}}
	case "decode", "roundtrip":
		if req.Input == nil {
			return failure("invalid-request", "input", "input is required")
		}
		in, err := conformanceprotocol.DecodeInput(conformanceprotocol.Input{Encoding: req.Input.Encoding, Data: req.Input.Data}, false)
		if err != nil {
			return failure("malformed-input", "input.data", err.Error())
		}
		if conformancecodec.IsZlib(in) {
			in, err = conformancecodec.InflateZlibLimit(in, 10<<20)
			if err != nil {
				return failure("malformed-input", "wire", err.Error())
			}
		}
		value, err := conformancecodec.DecodeProfile(in, req.Profile, decodeOptions)
		if err != nil {
			return failure("malformed-input", "wire", err.Error())
		}
		valueMap, err := canonicalMap(value)
		if err != nil {
			return failure("internal", "", err.Error())
		}
		normalizeDecodedValue(valueMap, req.Profile, strict, req.Operation == "decode")
		if path, err := preflightCanonicalLimits(valueMap, req.Options); err != nil {
			return failure("limit-exceeded", path, err.Error())
		}
		if req.Operation == "decode" {
			return response{OK: true, Value: valueMap}
		}
		out, err := conformancecodec.Encode(value, req.Profile)
		if err != nil {
			return failure("invalid-value", "value", err.Error())
		}
		if zlibEnabled(req.Options) {
			out, err = conformancecodec.DeflateZlib(out)
			if err != nil {
				return failure("invalid-value", "value", err.Error())
			}
		}
		return response{OK: true, Value: valueMap, Output: &wireInput{Encoding: "base64", Data: base64.StdEncoding.EncodeToString(out)}}
	}
	return failure("unsupported-operation", "operation", "unsupported operation")
}
func canonicalValue(raw map[string]any) (conformanceprotocol.Value, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return conformanceprotocol.Value{}, err
	}
	var value conformanceprotocol.Value
	if err := json.Unmarshal(b, &value); err != nil {
		return conformanceprotocol.Value{}, err
	}
	return value, nil
}

func canonicalMap(value conformanceprotocol.Value) (map[string]any, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func stringOption(options map[string]any, key string) string {
	value, _ := options[key].(string)
	return value
}
func normalizeDecodedValue(value map[string]any, profile string, strict, projectDecode bool) {
	if wire, ok := value["wire"].(map[string]any); ok {
		normalized := map[string]any{}
		for _, key := range []string{"root", "version"} {
			if item, exists := wire[key]; exists {
				normalized[key] = item
			}
		}
		if len(normalized) == 0 {
			delete(value, "wire")
		} else {
			value["wire"] = normalized
		}
		if root, ok := normalized["root"].(map[string]any); ok {
			if profile == "xplatform-xml-4000" || profile == "xplatform-xml-5000" {
				if version, exists := root["version"]; exists {
					if _, hasVer := root["ver"]; !hasVer {
						root["ver"] = version
					}
					delete(root, "version")
				}
			} else if version, exists := root["ver"]; exists {
				if _, hasVersion := root["version"]; !hasVersion {
					root["version"] = version
				}
				delete(root, "ver")
			}
			if profile == "nexacro-xml-4000" {
				root["namespace"] = "http://www.nexacroplatform.com/platform/dataset"
				root["version"] = "4000"
			}
			if profile == "xplatform-xml-4000" || profile == "xplatform-xml-5000" {
				root["namespace"] = "http://www.tobesoft.com/platform/Dataset"
				root["ver"] = "4000"
				if profile == "xplatform-xml-5000" {
					root["ver"] = "5000"
				}
				delete(root, "version")
			}
		}
	}
	for _, rawDataset := range array(value["datasets"]) {
		dataset, ok := rawDataset.(map[string]any)
		if !ok {
			continue
		}
		delete(dataset, "wire")
		for _, key := range []string{"columns", "constColumns"} {
			for i, rawColumn := range array(dataset[key]) {
				if column, ok := rawColumn.(map[string]any); ok {
					delete(column, "size")
					delete(column, "encoding")
					delete(column, "prop")
					delete(column, "sumtext")
					if key == "constColumns" {
						if profile != "nexacro-xml-4000" && profile != "xplatform-xml-4000" && profile != "xplatform-xml-5000" {
							column["index"] = len(array(dataset["columns"])) + i
						}
						delete(column, "value")
					}
				}
			}
		}
	}
	if profile == "nexacro-xml-4000" || profile == "xplatform-xml-4000" || profile == "xplatform-xml-5000" {
		for _, rawParameter := range array(value["parameters"]) {
			if parameter, ok := rawParameter.(map[string]any); ok && parameter["state"] == "empty" {
				parameter["lexical"] = ""
			}
		}
		for _, rawDataset := range array(value["datasets"]) {
			if dataset, ok := rawDataset.(map[string]any); ok {
				for _, rawRow := range array(dataset["rows"]) {
					if row, ok := rawRow.(map[string]any); ok {
						normalizeXMLRowCells(row)
					}
				}
			}
		}
	}
	if strings.Contains(profile, "ssv") {
		for _, rawDataset := range array(value["datasets"]) {
			if dataset, ok := rawDataset.(map[string]any); ok {
				for _, rawRow := range array(dataset["rows"]) {
					if row, ok := rawRow.(map[string]any); ok {
						if cells, ok := row["values"].(map[string]any); ok {
							for _, rawCell := range cells {
								if cell, ok := rawCell.(map[string]any); ok && cell["state"] == "empty" {
									cell["lexical"] = ""
								}
							}
						}
					}
				}
			}
		}
	}
	for _, rawParameter := range array(value["parameters"]) {
		if parameter, ok := rawParameter.(map[string]any); ok {
			delete(parameter, "wire")
		}
	}
}

func normalizeXMLRowCells(row map[string]any) {
	if cells, ok := row["values"].(map[string]any); ok {
		for _, rawCell := range cells {
			if cell, ok := rawCell.(map[string]any); ok && cell["state"] == "empty" {
				cell["lexical"] = ""
			}
		}
	}
	if original, ok := row["orgRow"].(map[string]any); ok {
		normalizeXMLRowCells(original)
	}
}

func parseProfile(name string) (xapi.Profile, error) {
	switch name {
	case "xplatform-xml-4000":
		return xapi.XPlatformXML4000, nil
	case "xplatform-xml-5000":
		return xapi.XPlatformXML5000, nil
	case "nexacro-xml-4000":
		return xapi.NexacroXML4000, nil
	case "nexacro-json-1.0":
		return xapi.NexacroJSON10, nil
	case "nexacro-ssv":
		return xapi.NexacroSSV, nil
	case "xplatform-ssv":
		return xapi.XPlatformSSV, nil
	case "nexacro-binary-5000":
		return xapi.NexacroBinary5000, nil
	case "xplatform-binary-5000":
		return xapi.XPlatformBinary5000, nil
	default:
		return 0, fmt.Errorf("unsupported profile %q", name)
	}
}

func parseOptions(raw map[string]any, profile string) ([]xapi.Option, error) {
	var out []xapi.Option
	strict := true
	if v, ok := raw["strict"]; ok {
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("strict must be boolean")
		}
		strict = b
	}
	if strict {
		out = append(out, xapi.WithStrictMode())
	} else {
		out = append(out, xapi.WithCompatibility())
	}
	if v, ok := raw["base64Whitespace"]; ok {
		ignore := false
		switch value := v.(type) {
		case bool:
			ignore = value
		case string:
			if value != "ignore" && value != "reject" {
				return nil, fmt.Errorf("base64Whitespace must be ignore or reject")
			}
			ignore = value == "ignore"
		default:
			return nil, fmt.Errorf("base64Whitespace must be boolean or string")
		}
		if ignore {
			out = append(out, xapi.WithBase64Whitespace(xapi.AllowBase64Whitespace))
		}
	}
	if v, ok := raw["ssvUnitSeparator"]; ok {
		separator, ok := v.(string)
		if !ok || utf8.RuneCountInString(separator) != 1 {
			return nil, fmt.Errorf("ssvUnitSeparator must be one Unicode scalar")
		}
		if !strings.Contains(profile, "ssv") {
			return nil, fmt.Errorf("unknown option %q", "ssvUnitSeparator")
		}
	}
	if v, ok := raw["ssvRecordSeparator"]; ok {
		separator, ok := v.(string)
		if !ok || utf8.RuneCountInString(separator) != 1 {
			return nil, fmt.Errorf("ssvRecordSeparator must be one Unicode scalar")
		}
		if !strings.Contains(profile, "ssv") {
			return nil, fmt.Errorf("unknown option %q", "ssvRecordSeparator")
		}
	}
	if unit, unitOK := raw["ssvUnitSeparator"].(string); unitOK {
		record, recordOK := raw["ssvRecordSeparator"].(string)
		if unitOK && recordOK {
			if unit == record {
				return nil, fmt.Errorf("SSV separators must differ")
			}
			out = append(out, xapi.WithSSVSeparators(unit, record))
		}
	}
	if rawLimits, ok := raw["limits"].(map[string]any); ok {
		l := xapi.Limits{}
		for key, value := range rawLimits {
			n, ok := value.(float64)
			if !ok || n < 0 {
				return nil, fmt.Errorf("limit %s must be a non-negative number", key)
			}
			switch key {
			case "payloadBytes":
				l.MaxDocumentBytes = int64(n)
			case "scalarBytes":
				l.MaxScalarBytes = int64(n)
			case "blobBytes":
				l.MaxBlobBytes = int64(n)
			case "depth":
				l.MaxDepth = int(n)
			case "datasets":
				l.MaxDatasets = int(n)
			case "columns":
				l.MaxColumns = int(n)
			case "rows":
				l.MaxRows = int(n)
			default:
				return nil, fmt.Errorf("unknown limit %q", key)
			}
		}
		out = append(out, xapi.WithLimits(l))
	}
	for key := range raw {
		if key == "strict" || key == "base64Whitespace" || key == "limits" || key == "zlib" {
			continue
		}
		if (profile == "nexacro-ssv" || profile == "xplatform-ssv") && (key == "ssvUnitSeparator" || key == "ssvRecordSeparator") {
			continue
		}
		return nil, fmt.Errorf("unknown option %q", key)
	}
	return out, nil
}

func decodeInput(in wireInput, options map[string]any) ([]byte, error) {
	if in.Encoding != "base64" {
		return nil, fmt.Errorf("input encoding must be base64")
	}
	data := in.Data
	if base64WhitespaceIgnore(options["base64Whitespace"]) {
		data = strings.Map(func(r rune) rune {
			if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
				return -1
			}
			return r
		}, data)
	}
	return base64.StdEncoding.DecodeString(data)
}

func classify(err error, path string) response {
	class := "malformed-input"
	if strings.Contains(err.Error(), "limit") {
		class = "limit-exceeded"
	}
	return failure(class, path, err.Error())
}

func failure(class, path, message string) response {
	return response{OK: false, Error: &errorBody{Class: class, Path: path, Message: message}}
}
func capabilities() map[string]any {
	profiles := make([]map[string]any, 0, 8)
	for _, name := range []string{"nexacro-json-1.0", "nexacro-xml-4000", "xplatform-xml-4000", "xplatform-xml-5000", "nexacro-ssv", "xplatform-ssv", "nexacro-binary-5000", "xplatform-binary-5000"} {
		options := []string{"strict", "base64Whitespace", "limits", "zlib"}
		if name == "nexacro-ssv" || name == "xplatform-ssv" {
			options = append(options, "ssvUnitSeparator", "ssvRecordSeparator")
		}
		profiles = append(profiles, map[string]any{
			"name": name, "operations": []string{"decode", "encode", "roundtrip"},
			"options": options,
			"limits":  map[string]int{"payloadBytes": 10 << 20, "datasets": 100, "rows": 100000, "columns": 1000, "scalarBytes": 1 << 20, "blobBytes": 10 << 20},
		})
	}
	return map[string]any{"protocolVersion": "1.0", "implementation": "xapi-go-conformance", "profiles": profiles}
}
func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
func writeResponse(w http.ResponseWriter, value response, status int) {
	w.WriteHeader(status)
	writeJSON(w, value)
}
func base64WhitespaceIgnore(value any) bool {
	if b, ok := value.(bool); ok {
		return b
	}
	s, _ := value.(string)
	return s == "ignore"
}
func zlibEnabled(options map[string]any) bool {
	enabled, _ := options["zlib"].(bool)
	return enabled
}

func isTransportZlib(data []byte) bool {
	return len(data) >= 2 && data[0] == 0xff && data[1] == 0xad
}

func inflateTransport(data []byte) ([]byte, error) {
	reader, err := zlib.NewReader(strings.NewReader(string(data[2:])))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(io.LimitReader(reader, 10<<20+1))
}

func deflateTransport(data []byte) ([]byte, error) {
	var out strings.Builder
	out.WriteString(string([]byte{0xff, 0xad}))
	writer := zlib.NewWriter(&out)
	if _, err := writer.Write(data); err != nil {
		_ = writer.Close()
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return []byte(out.String()), nil
}
