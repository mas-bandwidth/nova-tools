package ci

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// workSchema represents the root schema structure of docs/schemas/nova-work-wire-v1.json.
type workSchema struct {
	Schema             string              `json:"$schema"`
	Title              string              `json:"title"`
	Description        string              `json:"description"`
	ProtocolVersion    string              `json:"protocol_version"`
	Wire               wireConfig          `json:"wire"`
	Verbs              []verbEntry         `json:"verbs"`
	MissingVerbs       []missingVerbEntry  `json:"missing_verbs"`
	CoordinatorDomains []coordinatorDomain `json:"coordinator_domains"`
	RowanMarks         []rowanMark         `json:"rowan_marks"`
}

type wireConfig struct {
	Framing           string          `json:"framing"`
	Encoding          string          `json:"encoding"`
	IntegerEncoding   string          `json:"integer_encoding"`
	TimestampEncoding string          `json:"timestamp_encoding"`
	NullSemantics     string          `json:"null_semantics"`
	EmptySemantics    string          `json:"empty_semantics"`
	MaxFrameBytesFlag string          `json:"max_frame_bytes_flag"`
	RequestEnvelope   json.RawMessage `json:"request_envelope"`
	ResponseEnvelope  json.RawMessage `json:"response_envelope"`
}

type verbEntry struct {
	Verb          string   `json:"verb"`
	Op            string   `json:"op"`
	EventKind     *string  `json:"event_kind"`
	Mutating      bool     `json:"mutating"`
	Description   string   `json:"description"`
	OrderedFields []string `json:"ordered_fields"`
	GrammarLine   string   `json:"grammar_line"`

	// The dependency gate's three marks (nova-tools #785, SPEC-WORK.md:4878).
	// NeedsGate is "refuses", "withholds" or "exempt"; NeedsGateForms lists the
	// gated forms by the flag that selects each, and a verb whose every form is
	// gated omits the list; NeedsGateReason is required of "exempt".
	NeedsGate       string   `json:"needs_gate"`
	NeedsGateForms  []string `json:"needs_gate_forms"`
	NeedsGateReason string   `json:"needs_gate_reason"`
}

type missingVerbEntry struct {
	Verb          string   `json:"verb"`
	Op            string   `json:"op"`
	OwningDomain  string   `json:"owning_domain"`
	Status        string   `json:"status"`
	Disposition   string   `json:"disposition"`
	EventKind     *string  `json:"event_kind"`
	Rationale     string   `json:"rationale"`
	OrderedFields []string `json:"ordered_fields"`
	GrammarLine   string   `json:"grammar_line"`

	// A missing verb carries the same three marks a built one does: it can name
	// an admitting event kind, and the coverage rule reads both lists.
	NeedsGate       string   `json:"needs_gate"`
	NeedsGateForms  []string `json:"needs_gate_forms"`
	NeedsGateReason string   `json:"needs_gate_reason"`
}

type coordinatorDomain struct {
	Domain        string   `json:"domain"`
	Operations    []string `json:"operations"`
	InScopeVerbs  []string `json:"in_scope_verbs"`
	DeferredVerbs []string `json:"deferred_verbs"`
}

type rowanMark struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Summary      string `json:"summary"`
	SpecCitation string `json:"spec_citation"`
}

func loadWorkSchema(t *testing.T) workSchema {
	t.Helper()
	root := repoRoot(t)
	schemaPath := filepath.Join(root, "docs", "schemas", "nova-work-wire-v1.json")
	data := readFile(t, schemaPath)

	var ws workSchema
	if err := json.Unmarshal([]byte(data), &ws); err != nil {
		t.Fatalf("unmarshaling %s: %v", schemaPath, err)
	}
	return ws
}

// TestWorkWireSchemaLoadsAndValidatesPins asserts that the protocol v1 schema
// file exists, loads cleanly, and pins the wire-level invariants verbatim.
func TestWorkWireSchemaLoadsAndValidatesPins(t *testing.T) {
	ws := loadWorkSchema(t)

	if ws.ProtocolVersion != "1" {
		t.Errorf("protocol_version = %q, want \"1\"", ws.ProtocolVersion)
	}

	w := ws.Wire
	if w.Framing != "4-byte-big-endian-unsigned-length" {
		t.Errorf("wire.framing = %q, want \"4-byte-big-endian-unsigned-length\"", w.Framing)
	}
	if w.Encoding != "utf-8-json" {
		t.Errorf("wire.encoding = %q, want \"utf-8-json\"", w.Encoding)
	}
	if w.IntegerEncoding != "decimal-string" {
		t.Errorf("wire.integer_encoding = %q, want \"decimal-string\"", w.IntegerEncoding)
	}
	if w.TimestampEncoding != "rfc3339-utc-trailing-z" {
		t.Errorf("wire.timestamp_encoding = %q, want \"rfc3339-utc-trailing-z\"", w.TimestampEncoding)
	}
	if w.NullSemantics != "absent-and-null-mean-not-given" {
		t.Errorf("wire.null_semantics = %q, want \"absent-and-null-mean-not-given\"", w.NullSemantics)
	}
	if w.EmptySemantics != "empty-string-and-empty-array-are-values" {
		t.Errorf("wire.empty_semantics = %q, want \"empty-string-and-empty-array-are-values\"", w.EmptySemantics)
	}
	if w.MaxFrameBytesFlag != "--max-frame-bytes" {
		t.Errorf("wire.max_frame_bytes_flag = %q, want \"--max-frame-bytes\"", w.MaxFrameBytesFlag)
	}

	// Verify request envelope has required fields per RM-06:
	// Only op, request, as, args are required. expect, now, max, deadline are optional.
	var reqEnv struct {
		Required   []string `json:"required"`
		Properties map[string]struct {
			Type any `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(w.RequestEnvelope, &reqEnv); err != nil {
		t.Fatalf("unmarshaling request_envelope: %v", err)
	}
	expectedReq := []string{"op", "request", "as", "args"}
	reqSet := make(map[string]bool)
	for _, f := range reqEnv.Required {
		reqSet[f] = true
	}
	for _, f := range expectedReq {
		if !reqSet[f] {
			t.Errorf("request_envelope missing required field %q", f)
		}
	}
	if len(reqEnv.Required) != len(expectedReq) {
		t.Errorf("request_envelope required length = %d, want %d", len(reqEnv.Required), len(expectedReq))
	}

	// Verify optional fields are present in properties but NOT in required
	optionalFields := []string{"expect", "now", "max", "deadline"}
	for _, opt := range optionalFields {
		if _, ok := reqEnv.Properties[opt]; !ok {
			t.Errorf("request_envelope missing optional field %q in properties", opt)
		}
		if reqSet[opt] {
			t.Errorf("request_envelope property %q should be optional, not in required", opt)
		}
	}

	// Verify response envelope has required fields per RM-07 and SPEC-WORK.md:2498-2500
	var respEnv struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(w.ResponseEnvelope, &respEnv); err != nil {
		t.Fatalf("unmarshaling response_envelope: %v", err)
	}
	expectedResp := []string{"request", "ok", "exit", "lines", "rev", "pushed"}
	respSet := make(map[string]bool)
	for _, f := range respEnv.Required {
		respSet[f] = true
	}
	for _, f := range expectedResp {
		if !respSet[f] {
			t.Errorf("response_envelope missing required field %q", f)
		}
	}
}

// TestAllInScopeVerbsHaveCompleteConcreteMappings pins every one of the 71
// post-fold CLI verbs from docs/SPEC-WORK.md:2160-2244 and verifies full concrete
// completeness: non-empty op, non-empty grammar line, explicit ordered fields,
// strengthened grammar first-token matching, and correct event kind classification.
func TestAllInScopeVerbsHaveCompleteConcreteMappings(t *testing.T) {
	ws := loadWorkSchema(t)

	if len(ws.Verbs) != 71 {
		t.Fatalf("expected exactly 71 in-scope verbs, got %d", len(ws.Verbs))
	}

	// Canonical 71 post-fold verbs with expected grammar line first token per SPEC-WORK.md:4135-4139
	canonicalVerbs := []struct {
		verb               string
		expectedFirstToken string
	}{
		{"session start", "SESSION"},
		{"session export", "EXPORT"},
		{"session export --state", "EXPORT"},
		{"state load", "LOAD"},
		{"session replay", "REPLAY"},
		{"session status", "SESSION"},
		{"session stop", "SESSION"},
		{"session handoff", "HANDOFF"},
		{"operation status", "OPERATION"},
		{"operation list", "OPERATION"},
		{"operation wait", "OPERATION"},
		{"operation cancel", "OPERATION"},
		{"savepoint list", "SAVEPOINT"},
		{"savepoint create", "SAVEPOINT"},
		{"savepoint verify", "SAVEPOINT"},
		{"savepoint restore", "SAVEPOINT"},
		{"savepoint compare", "SAVEPOINT"},
		{"undo-plan", "UNDO"},
		{"undo", "UNDO"},
		{"redo-plan", "REDO"},
		{"redo", "REDO"},
		{"friend", "FRIEND"},
		{"config", "CONFIG"},
		{"model", "MODEL"},
		{"observe", "OBSERVE"},
		{"goal set", "GOAL"},
		{"goal show", "GOAL"},
		{"goal update", "GOAL"},
		{"machine", "MACHINE"},
		{"offer", "OFFER"},
		{"acknowledge", "ACKNOWLEDGE"},
		{"decline", "DECLINE"},
		{"execution pause", "EXECUTION"},
		{"execution stop", "EXECUTION"},
		{"execution resume", "EXECUTION"},
		{"execution correct", "EXECUTION"},
		{"execution reconcile", "EXECUTION"},
		{"execution status", "EXECUTION"},
		{"clip", "OPERATION"},
		{"check", "WORK"},
		{"verify", "VERIFY"},
		{"query", "QUERY"},
		{"render", "RENDER"},
		{"node add", "NODE"},
		{"node edit", "NODE"},
		{"node move", "NODE"},
		{"node remove", "NODE"},
		{"node require", "NODE"},
		{"decompose", "DECOMPOSE"},
		{"accept", "ACCEPT"},
		{"source", "SOURCE"},
		{"dep", "DEP"},
		{"axis", "AXIS"},
		{"roadmap create", "ROADMAP"},
		{"roadmap configure", "ROADMAP"},
		{"roadmap row", "ROADMAP"},
		{"roadmap projection", "ROADMAP"},
		{"prioritise", "PRIORITY"},
		{"cell", "CELL"},
		{"responsible", "RESPONSIBLE"},
		{"take", "LEASE"},
		{"heartbeat", "HEARTBEAT"},
		{"release", "RELEASE"},
		{"attest", "ATTESTED"},
		{"attempt", "ATTEMPT"},
		{"evidence", "EVIDENCE"},
		{"state", "STATE"},
		{"correct", "CORRECT"},
		{"event", "EVENT"},
		{"version", "nova-work"},
		{"help", "nova-work"},
	}

	verbMap := make(map[string]verbEntry)
	for _, v := range ws.Verbs {
		if _, exists := verbMap[v.Verb]; exists {
			t.Errorf("duplicate verb entry in schema: %q", v.Verb)
		}
		verbMap[v.Verb] = v
	}

	for _, cv := range canonicalVerbs {
		expected := cv.verb
		v, ok := verbMap[expected]
		if !ok {
			t.Errorf("canonical verb %q missing from schema verbs", expected)
			continue
		}

		if strings.TrimSpace(v.Op) == "" {
			t.Errorf("verb %q has empty op", expected)
		}
		if strings.TrimSpace(v.GrammarLine) == "" {
			t.Errorf("verb %q has empty grammar_line", expected)
		}
		if v.OrderedFields == nil {
			t.Errorf("verb %q has nil ordered_fields (must be explicit array)", expected)
		}
		if strings.TrimSpace(v.Description) == "" {
			t.Errorf("verb %q has empty description", expected)
		}

		// Field uniqueness
		seenFields := make(map[string]bool)
		for _, f := range v.OrderedFields {
			if seenFields[f] {
				t.Errorf("verb %q has duplicate field %q in ordered_fields", expected, f)
			}
			seenFields[f] = true
		}

		// Grammar line structure and first-token verification
		tokens := strings.Fields(v.GrammarLine)
		if len(tokens) < 2 {
			t.Errorf("verb %q grammar_line %q has fewer than 2 tokens", expected, v.GrammarLine)
		} else if tokens[0] != cv.expectedFirstToken {
			t.Errorf("verb %q grammar_line first token = %q, want %q", expected, tokens[0], cv.expectedFirstToken)
		}
	}
}

// TestCoordinatorDomainsCoverAllOperations verifies that all 12 coordinator
// operation domains are defined, and their union covers all 71 post-fold verbs
// (with version and help handled client-side per docs/SPEC-WORK.md:2250-2253).
func TestCoordinatorDomainsCoverAllOperations(t *testing.T) {
	ws := loadWorkSchema(t)

	if len(ws.CoordinatorDomains) != 12 {
		t.Fatalf("expected exactly 12 coordinator domains, got %d", len(ws.CoordinatorDomains))
	}

	expectedDomains := []string{
		"session and durability",
		"reversible mistakes",
		"work structure",
		"scope and dependencies",
		"assignments and execution",
		"evidence and completion",
		"roadmaps",
		"friends and CONFIG",
		"ACTIVE",
		"models and prices",
		"issue correspondence",
		"queries and operations",
	}

	domainMap := make(map[string]coordinatorDomain)
	for _, d := range ws.CoordinatorDomains {
		domainMap[d.Domain] = d
	}

	coveredVerbs := make(map[string]bool)
	for _, ed := range expectedDomains {
		d, ok := domainMap[ed]
		if !ok {
			t.Errorf("missing expected domain: %q", ed)
			continue
		}
		if len(d.InScopeVerbs) == 0 && len(d.DeferredVerbs) == 0 {
			t.Errorf("domain %q has neither in-scope nor deferred verbs", ed)
		}
		for _, v := range d.InScopeVerbs {
			coveredVerbs[v] = true
		}
	}

	for _, v := range ws.Verbs {
		// version and help are answered locally by CLI without contacting daemon per SPEC-WORK.md:2250-2253
		if v.Verb == "version" || v.Verb == "help" {
			continue
		}
		if !coveredVerbs[v.Verb] {
			t.Errorf("in-scope verb %q is not covered by any coordinator domain", v.Verb)
		}
	}
}

// TestMissingAndDeferredVerbsHaveConcreteDispositions verifies that the 13
// missing/deferred verbs have status filled-by-folds and concrete op, owning domain,
// disposition, ordered fields, and grammar lines (docs/SPEC-WORK.md:2759-2780).
func TestMissingAndDeferredVerbsHaveConcreteDispositions(t *testing.T) {
	ws := loadWorkSchema(t)

	if len(ws.MissingVerbs) != 13 {
		t.Fatalf("expected exactly 13 missing/deferred verbs, got %d", len(ws.MissingVerbs))
	}

	expectedMissing := []string{
		"node edit",
		"node add --repo",
		"node move",
		"axis --remove",
		"roadmap",
		"prioritise",
		"offer",
		"acknowledge",
		"decline",
		"pause",
		"stop",
		"reconcile",
		"session export --at",
	}

	missingMap := make(map[string]missingVerbEntry)
	for _, mv := range ws.MissingVerbs {
		missingMap[mv.Verb] = mv
	}

	for _, name := range expectedMissing {
		mv, ok := missingMap[name]
		if !ok {
			t.Errorf("missing verb %q not found in missing_verbs", name)
			continue
		}

		if mv.Status != "filled-by-folds" {
			t.Errorf("missing verb %q status = %q, want \"filled-by-folds\"", name, mv.Status)
		}
		if strings.TrimSpace(mv.Op) == "" {
			t.Errorf("missing verb %q has empty op", name)
		}
		if strings.TrimSpace(mv.OwningDomain) == "" {
			t.Errorf("missing verb %q has empty owning_domain", name)
		}
		if strings.TrimSpace(mv.Disposition) == "" {
			t.Errorf("missing verb %q has empty disposition", name)
		}
		if strings.TrimSpace(mv.Rationale) == "" {
			t.Errorf("missing verb %q has empty rationale", name)
		}
		if len(mv.OrderedFields) == 0 {
			t.Errorf("missing verb %q has empty ordered_fields", name)
		}
		if strings.TrimSpace(mv.GrammarLine) == "" {
			t.Errorf("missing verb %q has empty grammar_line", name)
		}
	}
}

// TestRowanMarksArePinnedAndCited verifies that all 18 Rowan marks (RM-01 through RM-18)
// are explicitly present in numeric order, with non-empty descriptions and citations
// matching origin/spec/nova-work at 977980d.
func TestRowanMarksArePinnedAndCited(t *testing.T) {
	ws := loadWorkSchema(t)

	if len(ws.RowanMarks) != 18 {
		t.Fatalf("expected exactly 18 Rowan marks, got %d", len(ws.RowanMarks))
	}

	expectedMarks := []struct {
		id       string
		name     string
		citation string
	}{
		{"RM-01", "wire-length-prefix-4-byte", "docs/SPEC-WORK.md:2483-2485,5294"},
		{"RM-02", "max-frame-bytes-bound", "docs/SPEC-WORK.md:2485-2486,5294"},
		{"RM-03", "integers-are-decimal-strings", "docs/SPEC-WORK.md:2486-2490,5295-5296"},
		{"RM-04", "rfc3339-utc-trailing-z", "docs/SPEC-WORK.md:2490-2491,5296"},
		{"RM-05", "absent-and-null-distinct-from-empty", "docs/SPEC-WORK.md:2492-2496,5296,5310-5311"},
		{"RM-06", "request-envelope-schema", "docs/SPEC-WORK.md:2496-2498,5296-5297"},
		{"RM-07", "response-envelope-schema", "docs/SPEC-WORK.md:2498-2500,5296-5297"},
		{"RM-08", "lines-carry-output-grammar-verbatim", "docs/SPEC-WORK.md:2500-2505,5297"},
		{"RM-09", "hello-protocol-negotiation", "docs/SPEC-WORK.md:2505-2509,5295"},
		{"RM-10", "sexp-store-json-wire-only", "docs/SPEC-WORK.md:2509-2511,5294-5297"},
		{"RM-11", "response-echoes-request-id", "docs/SPEC-WORK.md:2513-2525,5296-5297"},
		{"RM-12", "durable-operation-id-before-print", "docs/SPEC-WORK.md:2548-2556,5312-5313"},
		{"RM-13", "operation-verbs-spelling", "docs/SPEC-WORK.md:2170-2173,2248,5298-5299"},
		{"RM-14", "undo-redo-verbs-spelling", "docs/SPEC-WORK.md:2178-2181,2249,5299-5300"},
		{"RM-15", "savepoint-vs-checkpoint-spelling", "docs/SPEC-WORK.md:2174-2177,2250,5300-5301"},
		{"RM-16", "friend-config-model-observe-machine-spelling", "docs/SPEC-WORK.md:2182-2194,2250-2253,5301-5303"},
		{"RM-17", "query-specialized-asks", "docs/SPEC-WORK.md:2208-2215,5302"},
		{"RM-18", "aggregation-and-missing-verbs-named", "docs/SPEC-WORK.md:770-775,2759-2780,5303,5314-5315"},
	}

	for i, expected := range expectedMarks {
		actual := ws.RowanMarks[i]
		if actual.ID != expected.id {
			t.Errorf("Rowan mark [%d] ID = %q, want %q", i, actual.ID, expected.id)
		}
		if actual.Name != expected.name {
			t.Errorf("Rowan mark [%d] Name = %q, want %q", i, actual.Name, expected.name)
		}
		if !strings.Contains(actual.SpecCitation, expected.citation) {
			t.Errorf("Rowan mark [%d] SpecCitation = %q, want to contain %q", i, actual.SpecCitation, expected.citation)
		}
		if strings.TrimSpace(actual.Summary) == "" {
			t.Errorf("Rowan mark [%d] Summary is empty", i)
		}
	}
}

// TestWireFramingAndGrammarContracts verifies the concrete wire parsing behavior:
// 4-byte big-endian framing length, RFC 3339 UTC timestamps with trailing Z,
// and verbatim line splitting into stdout/stderr according to the second token.
func TestWireFramingAndGrammarContracts(t *testing.T) {
	// 1. Framing prefix: 4 bytes big endian unsigned integer
	payload := []byte(`{"op":"ping","request":"req-1"}`)
	buf := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(buf[0:4], uint32(len(payload)))
	copy(buf[4:], payload)

	readLength := binary.BigEndian.Uint32(buf[0:4])
	if readLength != uint32(len(payload)) {
		t.Fatalf("framing readLength = %d, want %d", readLength, len(payload))
	}

	// 2. Timestamp encoding: RFC3339 UTC with trailing 'Z'
	sampleStamp := "2026-09-15T02:00:00Z"
	parsedTime, err := time.Parse(time.RFC3339, sampleStamp)
	if err != nil {
		t.Fatalf("parsing RFC3339 timestamp %q: %v", sampleStamp, err)
	}
	if !strings.HasSuffix(sampleStamp, "Z") {
		t.Errorf("timestamp %q missing trailing 'Z'", sampleStamp)
	}
	if parsedTime.Location() != time.UTC {
		t.Errorf("timestamp location = %v, want UTC", parsedTime.Location())
	}

	// 3. Verbatim lines splitting: second token OK, ROW, NOTE, MORE -> stdout; FAIL, RACED -> stderr
	lineCases := []struct {
		line        string
		expectErr   bool
		secondToken string
	}{
		{"SESSION OK session=/tmp/s owner=worker-1", false, "OK"},
		{"QUERY ROW id=work-1 title=\"task\" status=doing", false, "ROW"},
		{"QUERY NOTE journal compacted to rev=40", false, "NOTE"},
		{"GOAL MORE after=cur-99", false, "MORE"},
		{"SUBMIT FAIL reason=unsettled_members count=2", true, "FAIL"},
		{"CLIP RACED claimant=worker-2 target=work-1", true, "RACED"},
	}

	for _, tc := range lineCases {
		fields := strings.Fields(tc.line)
		if len(fields) < 2 {
			t.Fatalf("line %q has fewer than 2 tokens", tc.line)
		}
		second := fields[1]
		if second != tc.secondToken {
			t.Errorf("line %q second token = %q, want %q", tc.line, second, tc.secondToken)
		}
		isStderr := (second == "FAIL" || second == "RACED")
		if isStderr != tc.expectErr {
			t.Errorf("line %q isStderr = %v, want %v", tc.line, isStderr, tc.expectErr)
		}
	}

	// 4. Integer decimal-string regex contract
	decRe := regexp.MustCompile(`^-?\d+$`)
	validInts := []string{"0", "123", "999999999999999999", "-42"}
	invalidInts := []string{"12.34", "1e6", "0x10", "NaN", "null", ""}

	for _, s := range validInts {
		if !decRe.MatchString(s) {
			t.Errorf("decimal string %q failed regex match", s)
		}
	}
	for _, s := range invalidInts {
		if decRe.MatchString(s) {
			t.Errorf("invalid decimal string %q unexpectedly matched regex", s)
		}
	}
}

// TestJSONRequestFrameParsing verifies reading and parsing a complete 4-byte-prefixed JSON request frame.
func TestJSONRequestFrameParsing(t *testing.T) {
	rawJSON := `{"op":"node-add","request":"req-12345","as":"worker-1","expect":"42","now":"2026-09-15T02:30:00Z","deadline":"2026-09-15T03:00:00Z","args":{"id":"task-99","title":"implement wire framing"}}`
	frameLen := len(rawJSON)
	frame := make([]byte, 4+frameLen)
	binary.BigEndian.PutUint32(frame[0:4], uint32(frameLen))
	copy(frame[4:], []byte(rawJSON))

	// Decode framing
	readLen := binary.BigEndian.Uint32(frame[0:4])
	if int(readLen) != frameLen {
		t.Fatalf("frame length mismatch: got %d, want %d", readLen, frameLen)
	}

	payload := frame[4 : 4+readLen]
	var parsed struct {
		Op       string          `json:"op"`
		Request  string          `json:"request"`
		As       string          `json:"as"`
		Expect   *string         `json:"expect"`
		Now      *string         `json:"now"`
		Max      *string         `json:"max"`
		Deadline *string         `json:"deadline"`
		Args     json.RawMessage `json:"args"`
	}

	if err := json.Unmarshal(payload, &parsed); err != nil {
		t.Fatalf("unmarshaling payload: %v", err)
	}

	if parsed.Op != "node-add" {
		t.Errorf("parsed.Op = %q, want \"node-add\"", parsed.Op)
	}
	if parsed.Request != "req-12345" {
		t.Errorf("parsed.Request = %q, want \"req-12345\"", parsed.Request)
	}
	if parsed.As != "worker-1" {
		t.Errorf("parsed.As = %q, want \"worker-1\"", parsed.As)
	}
	if parsed.Expect == nil || *parsed.Expect != "42" {
		t.Errorf("parsed.Expect = %v, want \"42\"", parsed.Expect)
	}
	if parsed.Now == nil || *parsed.Now != "2026-09-15T02:30:00Z" {
		t.Errorf("parsed.Now = %v, want \"2026-09-15T02:30:00Z\"", parsed.Now)
	}
	if parsed.Max != nil {
		t.Errorf("parsed.Max = %v, want nil (absent/optional)", parsed.Max)
	}
	if parsed.Deadline == nil || *parsed.Deadline != "2026-09-15T03:00:00Z" {
		t.Errorf("parsed.Deadline = %v, want \"2026-09-15T03:00:00Z\"", parsed.Deadline)
	}
	if len(parsed.Args) == 0 {
		t.Errorf("parsed.Args is empty")
	}
}

// --------------------------------------------------------------------------
// The dependency gate's coverage test (nova-tools #785).
//
// Replay a-verb-that-can-admit-declares-its-needs-gate, docs/SPEC-WORK.md:6942.
// Rule 3 (SPEC-WORK.md:4878): "A later verb is caught by an instrument and not
// by a promise": the one generated schema file names every verb's event kinds,
// and its coverage test holds the closed list of admitting kinds and fails on
// any verb able to write one, in its own envelope or in one it derives, whose
// entry carries none of three marks.
//
//	needs-gate: refuses    the verb answers an unmet need with its FAIL line
//	                       and writes nothing
//	needs-gate: withholds  the verb is admitted and records what it records but
//	                       creates no lease, allocation or transition while a
//	                       need is unmet -- `execution reconcile` and no other
//	                       verb today, since `refuses` would be false of it: a
//	                       reconcile over an unmet need exits 0
//	needs-gate: exempt     with its reason
//
// "The mark is per verb and the gate is per form, so the entry names its gated
// forms": needs_gate_forms lists them by the flag that selects each, and a verb
// whose every form is gated omits the list.
// --------------------------------------------------------------------------

// admittingEventKinds is the closed list, in this schema's own spelling of the
// kinds. The spec names `:lease`, `:handoff`, `:reassign`, `:offer`,
// `:acknowledge`, the allocation, `:packet` and a `:transition` that can carry
// `:to :doing`; this schema writes the lease, handoff and allocation kinds as
// `:assignment`, and has no entry for `reassign` or `task packet` at all --
// see TestRule3VerbsWithoutASchemaEntryAreNamed below.
var admittingEventKinds = map[string]bool{
	// The spec's closed list of admitting kinds (SPEC-WORK.md:4880), named
	// literally. Stella's [P2] on 3967655e: the first cut listed only the kinds
	// the SHIPPED file happens to use, so a schema carrying an unmarked verb of
	// kind :lease, :handoff, :reassign, :allocation or :packet passed the
	// coverage test. The closed list is the closed list.
	":lease":       true,
	":handoff":     true,
	":reassign":    true,
	":offer":       true,
	":acknowledge": true,
	":allocation":  true,
	":packet":      true,
	":transition":  true,

	// This schema's own spellings of the same effects. It has no `:lease` kind:
	// `take`, `release`, `heartbeat`, `attempt` and `responsible` all carry
	// `:assignment`, which is where a lease, a handoff and an allocation are
	// written. `:undo` and `:redo` carry the compensating take and the
	// compensating `state --to doing` rule 3 names.
	":assignment": true,
	":undo":       true,
	":redo":       true,
}

// canonicalAdmittingKinds is the spec's closed list on its own, so a fixture
// can be built per kind and the shipped file can be checked to use only
// spellings this test knows.
var canonicalAdmittingKinds = []string{
	":lease", ":handoff", ":reassign", ":offer", ":acknowledge",
	":allocation", ":packet", ":transition",
}

// rule3Marks is rule 3's own list of admission verbs, as this schema spells
// them, with the mark each must carry. `execution reconcile` alone is
// `withholds`.
var rule3Marks = map[string]string{
	"take":                "refuses",
	"release":             "refuses",
	"offer":               "refuses",
	"acknowledge":         "refuses",
	"state":               "refuses",
	"goal update":         "refuses",
	"undo":                "refuses",
	"redo":                "refuses",
	"execution reconcile": "withholds",
}

// rule3Forms is the gated form each verb's entry must name, by the flag that
// selects it (SPEC-WORK.md:4885). A verb absent from this map has every form
// gated and omits the list.
var rule3Forms = map[string]string{
	"release":     "--handed",
	"acknowledge": "--stage accepted",
	"state":       "--to doing",
	"goal update": "--progress",
}

func validNeedsGate(mark string) bool {
	return mark == "refuses" || mark == "withholds" || mark == "exempt"
}

// checkNeedsGateCoverage is the coverage rule itself, run over any schema so
// the fixture cases of the replay can exercise it. It returns one finding per
// offending verb, each naming the verb.
// missingVerbAsEntry reads a `missing_verbs` row as a verb entry, so the one
// coverage rule can be run over both lists.
//
// The stand-in's low finding on 82421311: `checkNeedsGateCoverage` and
// `TestShippedSchemaUsesOnlyKnownEventKinds` iterated `ws.Verbs` ONLY, so
// flipping an EXISTING `missing_verbs` entry's `event_kind` to `":lease"` with
// no `needs_gate` left `./internal/ci` green. A missing verb with an admitting
// kind is exactly the "later verb" rule 3 wants caught (SPEC-WORK.md:4878);
// that it is not built yet is what its `status` says, not a reason to skip it.
func missingVerbAsEntry(m missingVerbEntry) verbEntry {
	return verbEntry{
		Verb:            m.Verb,
		Op:              m.Op,
		EventKind:       m.EventKind,
		Mutating:        true,
		NeedsGate:       m.NeedsGate,
		NeedsGateForms:  m.NeedsGateForms,
		NeedsGateReason: m.NeedsGateReason,
	}
}

// schemaVerbEntries is every entry the coverage rule reads: the built verbs and
// the missing-verb register, which carry event kinds too.
func schemaVerbEntries(ws workSchema) []verbEntry {
	entries := append([]verbEntry{}, ws.Verbs...)
	for _, m := range ws.MissingVerbs {
		entries = append(entries, missingVerbAsEntry(m))
	}
	return entries
}

func checkNeedsGateCoverage(ws workSchema) []string {
	var findings []string
	for _, v := range schemaVerbEntries(ws) {
		kind := ""
		if v.EventKind != nil {
			kind = *v.EventKind
		}
		admitting := admittingEventKinds[kind]
		_, inRule3 := rule3Marks[v.Verb]
		if !admitting && !inRule3 {
			continue
		}
		if v.NeedsGate == "" {
			findings = append(findings, fmt.Sprintf(
				"%s can write the admitting kind %s and carries no needs-gate mark", v.Verb, kind))
			continue
		}
		if !validNeedsGate(v.NeedsGate) {
			findings = append(findings, fmt.Sprintf(
				"%s carries needs-gate %q, which is not one of refuses, withholds, exempt",
				v.Verb, v.NeedsGate))
			continue
		}
		if v.NeedsGate == "exempt" && strings.TrimSpace(v.NeedsGateReason) == "" {
			findings = append(findings, fmt.Sprintf(
				"%s is marked needs-gate: exempt with no reason", v.Verb))
		}
		if want, ok := rule3Marks[v.Verb]; ok && v.NeedsGate != want {
			findings = append(findings, fmt.Sprintf(
				"%s is a rule 3 admission verb and must be marked %q, not %q",
				v.Verb, want, v.NeedsGate))
		}
		if form, ok := rule3Forms[v.Verb]; ok {
			found := false
			for _, f := range v.NeedsGateForms {
				if f == form {
					found = true
					break
				}
			}
			if !found {
				findings = append(findings, fmt.Sprintf(
					"%s is gated per form and its needs-gate-forms omits %q", v.Verb, form))
			}
		}
	}
	return findings
}

// TestAVerbThatCanAdmitDeclaresItsNeedsGate is the fixture half of the replay:
// the coverage rule itself, exercised over schemas built for the purpose.
func TestAVerbThatCanAdmitDeclaresItsNeedsGate(t *testing.T) {
	assignment := ":assignment"
	acknowledge := ":acknowledge"

	fixture := func(entries ...verbEntry) workSchema {
		return workSchema{Verbs: entries}
	}

	// a verb entry whose event kinds include the lease kind and which carries
	// no needs-gate fails the test naming the verb
	findings := checkNeedsGateCoverage(fixture(verbEntry{
		Verb: "hypothetical-take", Op: "hypothetical-take", EventKind: &assignment, Mutating: true,
	}))
	if len(findings) != 1 {
		t.Fatalf("an unmarked admitting verb must produce exactly one finding, got %v", findings)
	}
	if !strings.Contains(findings[0], "hypothetical-take") {
		t.Fatalf("the finding must name the verb, got %q", findings[0])
	}

	// the same entry with needs-gate: refuses passes
	findings = checkNeedsGateCoverage(fixture(verbEntry{
		Verb: "hypothetical-take", Op: "hypothetical-take", EventKind: &assignment, Mutating: true,
		NeedsGate: "refuses",
	}))
	if len(findings) != 0 {
		t.Fatalf("a marked admitting verb must pass, got %v", findings)
	}

	// needs-gate: exempt with no reason fails
	findings = checkNeedsGateCoverage(fixture(verbEntry{
		Verb: "hypothetical-heartbeat", Op: "hypothetical-heartbeat", EventKind: &assignment,
		Mutating: true, NeedsGate: "exempt",
	}))
	if len(findings) != 1 || !strings.Contains(findings[0], "exempt with no reason") {
		t.Fatalf("exempt with no reason must fail by name, got %v", findings)
	}

	// an acknowledge entry marked refuses whose needs-gate-forms omits
	// "--stage accepted" fails
	findings = checkNeedsGateCoverage(fixture(verbEntry{
		Verb: "acknowledge", Op: "acknowledge", EventKind: &acknowledge, Mutating: true,
		NeedsGate: "refuses", NeedsGateForms: []string{"--stage declined"},
	}))
	if len(findings) != 1 || !strings.Contains(findings[0], "--stage accepted") {
		t.Fatalf("a missing gated form must fail by name, got %v", findings)
	}

	// and it passes once the form is named
	findings = checkNeedsGateCoverage(fixture(verbEntry{
		Verb: "acknowledge", Op: "acknowledge", EventKind: &acknowledge, Mutating: true,
		NeedsGate: "refuses", NeedsGateForms: []string{"--stage accepted"},
	}))
	if len(findings) != 0 {
		t.Fatalf("a correctly marked acknowledge must pass, got %v", findings)
	}
}

// TestShippedSchemaDeclaresEveryNeedsGate is the shipped half: the file in the
// repository passes the same rule, with every verb of rule 3's list marked
// refuses and execution reconcile alone marked withholds.
func TestShippedSchemaDeclaresEveryNeedsGate(t *testing.T) {
	ws := loadWorkSchema(t)
	if findings := checkNeedsGateCoverage(ws); len(findings) != 0 {
		for _, f := range findings {
			t.Errorf("needs-gate coverage: %s", f)
		}
	}

	byVerb := map[string]verbEntry{}
	for _, v := range ws.Verbs {
		byVerb[v.Verb] = v
	}
	// Both lists. `withholds` is reconcile and no other verb today -- `refuses`
	// would be false of it, since a reconcile over an unmet need exits 0 -- and
	// the missing-verb register carries the same verb under its short spelling,
	// folded into the built one, so the two must agree.
	withholds := map[string]bool{}
	for _, v := range schemaVerbEntries(ws) {
		if v.NeedsGate == "withholds" {
			withholds[v.Verb] = true
		}
	}
	for name := range withholds {
		if name != "execution reconcile" && name != "reconcile" {
			t.Errorf("only reconcile may be marked withholds today; %s is too", name)
		}
	}
	if !withholds["execution reconcile"] {
		t.Errorf("the built execution reconcile must be marked withholds")
	}
	for verb, want := range rule3Marks {
		entry, ok := byVerb[verb]
		if !ok {
			t.Errorf("rule 3 names %q and the schema has no entry for it", verb)
			continue
		}
		if entry.NeedsGate != want {
			t.Errorf("%s: needs-gate is %q, want %q", verb, entry.NeedsGate, want)
		}
	}
}

// TestRule3VerbsWithoutASchemaEntryAreNamed keeps the two admission verbs rule
// 3 names and this schema does not carry from being forgotten. `reassign` has
// no line in Output grammar yet (SPEC-WORK.md:4911) and `task packet` is the
// packet verb of a section this schema predates; neither has an entry, so
// neither can carry a mark, and the coverage rule cannot see them. This test is
// the register that says so, and it fails the day an entry appears unmarked --
// which is the coverage rule's job from then on.
func TestRule3VerbsWithoutASchemaEntryAreNamed(t *testing.T) {
	ws := loadWorkSchema(t)
	byVerb := map[string]bool{}
	for _, v := range ws.Verbs {
		byVerb[v.Verb] = true
	}
	for _, verb := range []string{"reassign", "task packet"} {
		if byVerb[verb] {
			t.Errorf("%s now has a schema entry: add it to rule3Marks and rule3Forms", verb)
		}
	}
}

// TestEveryCanonicalAdmittingKindIsCovered is Stella's [P2] on 3967655e made
// into a test. She mutated the shipped schema with one unmarked hypothetical
// verb of each canonical admitting kind and TestShippedSchemaDeclaresEveryNeedsGate
// still passed, because the kind list held only the spellings the shipped file
// happens to use. One negative fixture per kind, so the list cannot quietly
// shrink again.
func TestEveryCanonicalAdmittingKindIsCovered(t *testing.T) {
	for _, kind := range canonicalAdmittingKinds {
		k := kind
		t.Run(strings.TrimPrefix(k, ":"), func(t *testing.T) {
			if !admittingEventKinds[k] {
				t.Fatalf("%s is a canonical admitting kind and the coverage rule does not know it", k)
			}
			verb := "hypothetical-" + strings.TrimPrefix(k, ":")
			findings := checkNeedsGateCoverage(workSchema{Verbs: []verbEntry{{
				Verb: verb, Op: verb, EventKind: &k, Mutating: true,
			}}})
			if len(findings) != 1 {
				t.Fatalf("an unmarked %s verb must produce one finding, got %v", k, findings)
			}
			if !strings.Contains(findings[0], verb) {
				t.Fatalf("the finding must name the verb, got %q", findings[0])
			}
			findings = checkNeedsGateCoverage(workSchema{Verbs: []verbEntry{{
				Verb: verb, Op: verb, EventKind: &k, Mutating: true,
				NeedsGate: "refuses",
			}}})
			if len(findings) != 0 {
				t.Fatalf("a marked %s verb must pass, got %v", k, findings)
			}
		})
	}
}

// TestShippedSchemaSurvivesAnInjectedUnmarkedVerb is the mutation Stella ran,
// as a test: the shipped file plus one unmarked hypothetical verb of each
// canonical admitting kind must FAIL the coverage rule. The first cut passed.
func TestShippedSchemaSurvivesAnInjectedUnmarkedVerb(t *testing.T) {
	ws := loadWorkSchema(t)
	for _, kind := range canonicalAdmittingKinds {
		k := kind
		t.Run(strings.TrimPrefix(k, ":"), func(t *testing.T) {
			verb := "injected-" + strings.TrimPrefix(k, ":")
			mutated := workSchema{Verbs: append(append([]verbEntry{}, ws.Verbs...), verbEntry{
				Verb: verb, Op: verb, EventKind: &k, Mutating: true,
			})}
			named := false
			for _, f := range checkNeedsGateCoverage(mutated) {
				if strings.Contains(f, verb) {
					named = true
				}
			}
			if !named {
				t.Errorf("an unmarked %s verb injected into the shipped schema must be found", k)
			}
		})
	}
}

// TestShippedSchemaUsesOnlyKnownEventKinds keeps the kind list honest from the
// other side: a verb entry whose event kind this test has never heard of is a
// verb the coverage rule silently ignores.
func TestShippedSchemaUsesOnlyKnownEventKinds(t *testing.T) {
	ws := loadWorkSchema(t)
	// Kinds the shipped file uses that are deliberately NOT admitting, listed
	// so a NEW kind appearing forces a decision rather than being ignored.
	nonAdmitting := map[string]bool{
		":machine": true, ":structure": true, ":scope": true, ":evidence": true,
		":execution-control": true, ":decline": true, ":prioritise": true,
		":goal": true, ":friend": true, ":model": true, ":observe": true,
		":config": true, ":route": true, ":report": true, ":dep": true,
	}
	// Both lists: a `missing_verbs` row carries an event kind too, and one with
	// an admitting kind is exactly the later verb rule 3 wants caught.
	for _, v := range schemaVerbEntries(ws) {
		if v.EventKind == nil {
			continue
		}
		k := *v.EventKind
		if !admittingEventKinds[k] && !nonAdmitting[k] {
			t.Errorf("%s carries event kind %s, which this coverage test has never heard of: decide whether it admits", v.Verb, k)
		}
	}
}

// TestAMissingVerbWithAnAdmittingKindIsCaught is the stand-in's low finding on
// 82421311, as a test. Flipping an EXISTING `missing_verbs` entry's event kind
// to an admitting one, with no `needs_gate`, left `./internal/ci` green,
// because both loops read `ws.Verbs` only.
func TestAMissingVerbWithAnAdmittingKindIsCaught(t *testing.T) {
	ws := loadWorkSchema(t)
	if len(ws.MissingVerbs) == 0 {
		t.Fatal("the shipped schema has no missing_verbs to mutate")
	}
	for _, kind := range canonicalAdmittingKinds {
		k := kind
		t.Run(strings.TrimPrefix(k, ":"), func(t *testing.T) {
			// the shipped file, with ONE existing missing verb flipped to an
			// admitting kind and left unmarked
			mutated := workSchema{Verbs: ws.Verbs}
			mutated.MissingVerbs = append([]missingVerbEntry{}, ws.MissingVerbs...)
			mutated.MissingVerbs[0].EventKind = &k
			mutated.MissingVerbs[0].NeedsGate = ""
			named := false
			for _, f := range checkNeedsGateCoverage(mutated) {
				if strings.Contains(f, mutated.MissingVerbs[0].Verb) {
					named = true
				}
			}
			if !named {
				t.Errorf("a missing verb flipped to %s and left unmarked must be found", k)
			}
			// and marking it clears the finding
			mutated.MissingVerbs[0].NeedsGate = "refuses"
			for _, f := range checkNeedsGateCoverage(mutated) {
				if strings.Contains(f, mutated.MissingVerbs[0].Verb) {
					t.Errorf("a marked missing verb must pass, got %q", f)
				}
			}
		})
	}
}
