// native-model-check is a finite proposal encoding witness, not a strict raw
// profile parser, OpenCode config reader, SDK loader, or launcher.
// Run from the repository root: go run ./docs/drafts/fixtures/native-model-check
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf16"

	"github.com/mas-bandwidth/nova-tools/internal/records"
)

type fixture struct {
	Schema  string   `json:"schema"`
	Status  string   `json:"status"`
	Vectors []vector `json:"vectors"`
}
type vector struct {
	Name                string   `json:"name"`
	Resolved            resolved `json:"resolved"`
	Canonical           string   `json:"canonical"`
	ContentID           string   `json:"content_id"`
	LoweredSDKOptions   string   `json:"lowered_sdk_options"`
	LoweredModelOptions string   `json:"lowered_model_options"`
}
type resolved struct {
	Provider string      `json:"provider"`
	Model    string      `json:"model"`
	Endpoint string      `json:"endpoint"`
	Native   nativeModel `json:"native"`
}
type nativeModel struct {
	Schema   string  `json:"schema"`
	APIID    string  `json:"api_id"`
	SDK      string  `json:"sdk"`
	ModelURL string  `json:"model_url"`
	Runtime  runtime `json:"runtime"`
}
type runtime struct {
	Name         string          `json:"name"`
	Family       string          `json:"family"`
	ReleaseDate  string          `json:"release_date"`
	Status       string          `json:"status"`
	Capabilities capabilities    `json:"capabilities"`
	Limits       limits          `json:"limits"`
	Headers      []header        `json:"headers"`
	SDKOptions   node            `json:"sdk_options"`
	ModelOptions node            `json:"model_options"`
	Variant      json.RawMessage `json:"variant"`
}
type capabilities struct {
	Temperature bool            `json:"temperature"`
	Reasoning   bool            `json:"reasoning"`
	Attachment  bool            `json:"attachment"`
	Toolcall    bool            `json:"toolcall"`
	Input       modalities      `json:"input"`
	Output      modalities      `json:"output"`
	Interleaved json.RawMessage `json:"interleaved"`
}
type modalities struct {
	Text  bool `json:"text"`
	Audio bool `json:"audio"`
	Image bool `json:"image"`
	Video bool `json:"video"`
	PDF   bool `json:"pdf"`
}
type limits struct {
	Context string          `json:"context"`
	Input   json.RawMessage `json:"input"`
	Output  string          `json:"output"`
}
type header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
type node struct {
	Kind    string          `json:"kind"`
	Value   json.RawMessage `json:"value"`
	Decimal string          `json:"decimal"`
	Items   *[]node         `json:"items"`
	Members *[]nodeMember   `json:"members"`
}
type nodeMember struct {
	Name  string `json:"name"`
	Value node   `json:"value"`
}

func (m *nodeMember) UnmarshalJSON(raw []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if len(fields) != 2 || !present(fields["name"]) || !present(fields["value"]) {
		return fmt.Errorf("object member must have exactly name and value")
	}
	name, err := parseString(fields["name"])
	if err != nil {
		return err
	}
	var value node
	if err := json.Unmarshal(fields["value"], &value); err != nil {
		return err
	}
	*m = nodeMember{Name: name, Value: value}
	return nil
}

// UnmarshalJSON enforces the closed node carrier's key set and presence. It
// intentionally cannot prove a duplicate raw JSON member: encoding/json has
// already resolved that ambiguity before this method sees its map, so a future
// strict admission parser must retain that separate check.
func (n *node) UnmarshalJSON(raw []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	kind, ok := fields["kind"]
	if !ok {
		return fmt.Errorf("node lacks kind")
	}
	var k string
	if err := json.Unmarshal(kind, &k); err != nil {
		return fmt.Errorf("node kind is not a string")
	}
	var expected map[string]bool
	switch k {
	case "null":
		expected = map[string]bool{"kind": true}
	case "bool", "string":
		expected = map[string]bool{"kind": true, "value": true}
	case "number":
		expected = map[string]bool{"kind": true, "decimal": true}
	case "array":
		expected = map[string]bool{"kind": true, "items": true}
	case "object":
		expected = map[string]bool{"kind": true, "members": true}
	default:
		return fmt.Errorf("unknown node kind %q", k)
	}
	if len(fields) != len(expected) {
		return fmt.Errorf("node has missing or unknown member")
	}
	for key := range expected {
		if _, ok := fields[key]; !ok {
			return fmt.Errorf("node lacks %q", key)
		}
	}
	type plain node
	var decoded plain
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	*n = node(decoded)
	return nil
}

const (
	maxResolvedBytes = 256 * 1024
	maxOptionNodes   = 4096
	maxOptionDepth   = 32
	maxDecimalBytes  = 4096
)

func object(kv ...any) *records.Object {
	o := records.NewObject()
	for i := 0; i < len(kv); i += 2 {
		if err := o.Set(kv[i].(string), kv[i+1]); err != nil {
			panic(err)
		}
	}
	return o
}

func present(raw json.RawMessage) bool { return len(raw) != 0 }

func parseBool(raw json.RawMessage) (bool, error) {
	var v bool
	if !present(raw) || json.Unmarshal(raw, &v) != nil || string(raw) != "true" && string(raw) != "false" {
		return false, fmt.Errorf("expected JSON bool")
	}
	return v, nil
}

func parseString(raw json.RawMessage) (string, error) {
	var v string
	if !present(raw) || raw[0] != '"' || json.Unmarshal(raw, &v) != nil {
		return "", fmt.Errorf("expected JSON string")
	}
	return v, nil
}

func decimal(s string) bool {
	if s == "" {
		return false
	}
	if s[0] == '-' {
		if len(s) == 1 || s == "-0" {
			return false
		}
		s = s[1:]
	}
	integer, fraction, dot := strings.Cut(s, ".")
	if integer == "0" {
		// allowed
	} else if integer == "" || integer[0] < '1' || integer[0] > '9' {
		return false
	}
	for _, c := range integer {
		if c < '0' || c > '9' {
			return false
		}
	}
	if !dot {
		return true
	}
	if fraction == "" || fraction[len(fraction)-1] == '0' {
		return false
	}
	for _, c := range fraction {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func lessUTF16(a, b string) bool {
	ua, ub := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			return ua[i] < ub[i]
		}
	}
	return len(ua) < len(ub)
}

func nodeRecord(n node) (records.Value, error) {
	count := 0
	return nodeRecordAt(n, 1, &count)
}

func nodeRecordAt(n node, depth int, count *int) (records.Value, error) {
	if depth > maxOptionDepth {
		return nil, fmt.Errorf("option node exceeds depth")
	}
	*count = *count + 1
	if *count > maxOptionNodes {
		return nil, fmt.Errorf("option tree has too many nodes")
	}
	switch n.Kind {
	case "null":
		if present(n.Value) || n.Decimal != "" || n.Items != nil || n.Members != nil {
			return nil, fmt.Errorf("null node has extra member")
		}
		return object("kind", n.Kind), nil
	case "bool":
		v, err := parseBool(n.Value)
		if err != nil || n.Decimal != "" || n.Items != nil || n.Members != nil {
			return nil, fmt.Errorf("bool node: %w", err)
		}
		return object("kind", n.Kind, "value", v), nil
	case "string":
		v, err := parseString(n.Value)
		if err != nil || n.Decimal != "" || n.Items != nil || n.Members != nil {
			return nil, fmt.Errorf("string node: %w", err)
		}
		return object("kind", n.Kind, "value", v), nil
	case "number":
		if present(n.Value) || len(n.Decimal) > maxDecimalBytes || !decimal(n.Decimal) || n.Items != nil || n.Members != nil {
			return nil, fmt.Errorf("invalid decimal node")
		}
		return object("kind", n.Kind, "decimal", n.Decimal), nil
	case "array":
		if present(n.Value) || n.Decimal != "" || n.Items == nil || n.Members != nil {
			return nil, fmt.Errorf("invalid array node")
		}
		items := make([]records.Value, 0, len(*n.Items))
		for _, item := range *n.Items {
			v, err := nodeRecordAt(item, depth+1, count)
			if err != nil {
				return nil, err
			}
			items = append(items, v)
		}
		return object("kind", n.Kind, "items", items), nil
	case "object":
		if present(n.Value) || n.Decimal != "" || n.Items != nil || n.Members == nil {
			return nil, fmt.Errorf("invalid object node")
		}
		members := make([]records.Value, 0, len(*n.Members))
		var previous string
		for i, member := range *n.Members {
			if i > 0 && !lessUTF16(previous, member.Name) {
				return nil, fmt.Errorf("object node members not strictly UTF-16 sorted")
			}
			v, err := nodeRecordAt(member.Value, depth+1, count)
			if err != nil {
				return nil, err
			}
			members = append(members, object("name", member.Name, "value", v))
			previous = member.Name
		}
		return object("kind", n.Kind, "members", members), nil
	default:
		return nil, fmt.Errorf("unknown node kind %q", n.Kind)
	}
}

func lowerNode(n node) (string, error) {
	quote := func(s string) (string, error) {
		b, err := records.Canonicalize(s)
		return string(b), err
	}
	switch n.Kind {
	case "null":
		if _, err := nodeRecord(n); err != nil {
			return "", err
		}
		return "null", nil
	case "bool":
		v, err := parseBool(n.Value)
		if err != nil {
			return "", err
		}
		if _, err := nodeRecord(n); err != nil {
			return "", err
		}
		if v {
			return "true", nil
		}
		return "false", nil
	case "string":
		v, err := parseString(n.Value)
		if err != nil {
			return "", err
		}
		if _, err := nodeRecord(n); err != nil {
			return "", err
		}
		return quote(v)
	case "number":
		if _, err := nodeRecord(n); err != nil {
			return "", err
		}
		return n.Decimal, nil
	case "array":
		if _, err := nodeRecord(n); err != nil {
			return "", err
		}
		out := make([]string, 0, len(*n.Items))
		for _, item := range *n.Items {
			v, err := lowerNode(item)
			if err != nil {
				return "", err
			}
			out = append(out, v)
		}
		return "[" + strings.Join(out, ",") + "]", nil
	case "object":
		if _, err := nodeRecord(n); err != nil {
			return "", err
		}
		out := make([]string, 0, len(*n.Members))
		for _, member := range *n.Members {
			key, err := quote(member.Name)
			if err != nil {
				return "", err
			}
			value, err := lowerNode(member.Value)
			if err != nil {
				return "", err
			}
			out = append(out, key+":"+value)
		}
		return "{" + strings.Join(out, ",") + "}", nil
	default:
		return "", fmt.Errorf("unknown node kind %q", n.Kind)
	}
}

func capabilityRecord(c capabilities) (records.Value, error) {
	interleaved, err := interleavedRecord(c.Interleaved)
	if err != nil {
		return nil, err
	}
	modal := func(m modalities) records.Value {
		return object("text", m.Text, "audio", m.Audio, "image", m.Image, "video", m.Video, "pdf", m.PDF)
	}
	return object("temperature", c.Temperature, "reasoning", c.Reasoning, "attachment", c.Attachment, "toolcall", c.Toolcall, "input", modal(c.Input), "output", modal(c.Output), "interleaved", interleaved), nil
}

func interleavedRecord(raw json.RawMessage) (records.Value, error) {
	var b bool
	if json.Unmarshal(raw, &b) == nil && (string(raw) == "true" || string(raw) == "false") {
		return b, nil
	}
	var field struct {
		Field string `json:"field"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&field); err != nil || field.Field == "" {
		return nil, fmt.Errorf("invalid interleaved value")
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing interleaved value")
	}
	return object("field", field.Field), nil
}

func runtimeRecord(r runtime) (records.Value, error) {
	var input records.Value
	if string(r.Limits.Input) == "null" {
		input = nil
	} else {
		s, err := parseString(r.Limits.Input)
		if err != nil {
			return nil, fmt.Errorf("invalid input limit")
		}
		input = s
	}
	capabilities, err := capabilityRecord(r.Capabilities)
	if err != nil {
		return nil, err
	}
	headers := make([]records.Value, 0, len(r.Headers))
	for _, h := range r.Headers {
		headers = append(headers, object("name", h.Name, "value", h.Value))
	}
	sdk, err := nodeRecord(r.SDKOptions)
	if err != nil {
		return nil, fmt.Errorf("sdk options: %w", err)
	}
	if r.SDKOptions.Kind != "object" {
		return nil, fmt.Errorf("sdk options root is not an object")
	}
	model, err := nodeRecord(r.ModelOptions)
	if err != nil {
		return nil, fmt.Errorf("model options: %w", err)
	}
	if r.ModelOptions.Kind != "object" {
		return nil, fmt.Errorf("model options root is not an object")
	}
	var variant records.Value
	if string(r.Variant) == "null" {
		variant = nil
	} else {
		s, err := parseString(r.Variant)
		if err != nil || s == "" {
			return nil, fmt.Errorf("invalid variant")
		}
		variant = s
	}
	return object("name", r.Name, "family", r.Family, "release_date", r.ReleaseDate, "status", r.Status, "capabilities", capabilities, "limits", object("context", r.Limits.Context, "input", input, "output", r.Limits.Output), "headers", headers, "sdk_options", sdk, "model_options", model, "variant", variant), nil
}

func resolvedRecord(r resolved) (records.Value, error) {
	if r.Provider == "" || r.Model == "" || r.Endpoint == "" || r.Native.Schema != "nova.swarm.native-model/1" || r.Native.APIID == "" || r.Native.SDK == "" || r.Native.ModelURL == "" {
		return nil, fmt.Errorf("invalid resolved identity")
	}
	runtime, err := runtimeRecord(r.Native.Runtime)
	if err != nil {
		return nil, err
	}
	return object("provider", r.Provider, "model", r.Model, "endpoint", r.Endpoint, "native", object("schema", r.Native.Schema, "api_id", r.Native.APIID, "sdk", r.Native.SDK, "model_url", r.Native.ModelURL, "runtime", runtime)), nil
}

func decodeNode(raw string) error {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	var n node
	if err := dec.Decode(&n); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("trailing node bytes")
	}
	_, err := nodeRecord(n)
	return err
}

func main() {
	raw, err := os.ReadFile("docs/drafts/fixtures/swarm-native-model-vectors.json")
	if err != nil {
		panic(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var f fixture
	if err := dec.Decode(&f); err != nil {
		panic(err)
	}
	if _, err := dec.Token(); err != io.EOF {
		panic("trailing fixture bytes")
	}
	if f.Schema != "nova.swarm.native-model.vectors/1" || len(f.Vectors) != 2 {
		panic("unexpected fixture")
	}
	for _, bad := range []string{
		`{"kind":"wat"}`,
		`{"kind":"number","decimal":"01"}`,
		`{"kind":"number","decimal":"1.0"}`,
		`{"kind":"object","members":[{"name":"x","value":{"kind":"null"}},{"name":"x","value":{"kind":"null"}}]}`,
		`{"kind":"null","extra":true}`,
		`{"kind":"null","decimal":""}`,
		`{"kind":"null","members":null}`,
		`{"kind":"string","value":null}`,
		`{"kind":"object","members":[{"name":"x","value":{"kind":"null"},"extra":false}]}`,
		`{"kind":"object","members":[{"name":null,"value":{"kind":"null"}}]}`,
	} {
		if decodeNode(bad) == nil {
			panic("invalid node accepted")
		}
	}
	decimalNode := node{Kind: "number", Decimal: "0.5"}
	stringNode := node{Kind: "string", Value: json.RawMessage(`"0.5"`)}
	decimalLowered, err := lowerNode(decimalNode)
	if err != nil {
		panic(err)
	}
	stringLowered, err := lowerNode(stringNode)
	if err != nil {
		panic(err)
	}
	decimalValue, err := nodeRecord(decimalNode)
	if err != nil {
		panic(err)
	}
	stringValue, err := nodeRecord(stringNode)
	if err != nil {
		panic(err)
	}
	decimalID, err := records.ContentID(decimalValue)
	if err != nil {
		panic(err)
	}
	stringID, err := records.ContentID(stringValue)
	if err != nil {
		panic(err)
	}
	if decimalLowered != "0.5" || stringLowered != `"0.5"` || decimalID == stringID {
		panic("number/string lowering or identity collapsed")
	}
	for _, v := range f.Vectors {
		value, err := resolvedRecord(v.Resolved)
		if err != nil {
			panic(err)
		}
		canonical, err := records.Canonicalize(value)
		if err != nil {
			panic(err)
		}
		if len(canonical) > maxResolvedBytes {
			panic("resolved projection exceeds byte bound")
		}
		id, err := records.ContentID(value)
		if err != nil {
			panic(err)
		}
		sdk, err := lowerNode(v.Resolved.Native.Runtime.SDKOptions)
		if err != nil {
			panic(err)
		}
		model, err := lowerNode(v.Resolved.Native.Runtime.ModelOptions)
		if err != nil {
			panic(err)
		}
		if v.Canonical == "" || v.ContentID == "" || string(canonical) != v.Canonical || id != v.ContentID || sdk != v.LoweredSDKOptions || model != v.LoweredModelOptions {
			panic("vector mismatch")
		}
		fmt.Printf("VECTOR name=%s content_id=%s PASS\n", v.Name, id)
	}
	fmt.Println("LIMIT finite canonical encoding witness only; no raw duplicate-key, URL/header, route, SDK semantic schema, config discovery, provider, secret, worker, or launcher validation")
}
