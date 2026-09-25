package swarm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mas-bandwidth/nova-tools/internal/records"
)

// Profile is the per-job profile record of SPEC-SWARM-PROFILES. Its six binding members --
// worker, route, env_var, model, allowed_models, prompt -- are exactly the fields the
// preimage hashes. The loader never returns a profile that did not pass every bound below.
type Profile struct {
	ID            string
	Worker        WorkerProfile
	Route         RouteProfile
	EnvVar        string
	Model         string
	AllowedModels []string
	Prompt        PromptProfile

	// raw is the parsed, validated profile record, kept so Preimage hashes exactly the
	// binding fields it validated rather than a reconstruction that could reorder them.
	raw *profileNode
}

type WorkerProfile struct {
	Name              string
	Usage             string
	Harness           string
	HarnessArgs       []string
	WorkerDir         string
	Deadline          string
	ReadRoots         []string
	InputLimitPhrases []string
}

type RouteProfile struct {
	Provider    string
	Endpoint    string
	Credentials CredentialsProfile
}

type CredentialsProfile struct {
	Kind     string
	Store    string
	Seat     string
	AgeKey   string
	Sops     string
	Gate     string
	Launcher string
}

type PromptProfile struct {
	Mode   string
	Prefix string
	Tools  []string
}

// ProfileExitCode is the exit status a refused profile load carries: admission refusals
// are exit 2 before any worker starts (SPEC-SWARM-PROFILES, refusals and admission bounds).
const ProfileExitCode = 2

// ProfileRefusal is the one failure LoadProfile produces. It names the field -- never the
// value, because a private path or a prompt can sit in a profile field -- and its exit
// status is ProfileExitCode.
type ProfileRefusal struct {
	Field string
	Note  string
}

func (r *ProfileRefusal) Error() string {
	return fmt.Sprintf("PROFILE REFUSED %s: %s", r.Field, r.Note)
}

func refuseProfile(field, note string) *ProfileRefusal {
	return &ProfileRefusal{Field: field, Note: note}
}

// ProfileCatalogMax is the catalog read ceiling from SPEC-SWARM-PROFILES: at most 262144
// bytes, plus the overflow-detection byte a reader uses to prove it is not truncating.
const ProfileCatalogMax = 262144

// LoadProfile reads one profile file and returns the single profile it holds.
//
// The file is a strict-JSON single-profile catalog in the spec's shape:
//
//	{"version":1,"profiles":{"<id>":{<profile record>}}}
//
// `version` must be the integer token 1 and `profiles` must hold exactly one entry, so a
// file names one profile and its id. Every field is validated against its bound or
// enumerated set; any failure is a *ProfileRefusal naming the field, exit 2.
func LoadProfile(path string) (*Profile, error) {
	raw, err := readRegularBounded(path, ProfileCatalogMax)
	if err != nil {
		return nil, refuseProfile("profile", "the profile file could not be read as a regular file within the catalog ceiling")
	}
	if !utf8.Valid(raw) {
		return nil, refuseProfile("profile", "the profile file is not valid UTF-8")
	}

	root, ref := parseProfileTree(raw)
	if ref != nil {
		return nil, ref
	}
	p, ref := validateCatalog(root)
	if ref != nil {
		return nil, ref
	}
	return p, nil
}

// profileKind is what a parsed value is; the profile format uses objects, arrays, strings
// and the single `version` number, and nothing else.
type profileKind int

const (
	pNodeObject profileKind = iota
	pNodeArray
	pNodeString
	pNodeNumber
)

// profileNode is the parsed tree. Object member order is kept so a diagnostic can name a
// duplicate occurrence and so validation walks a source-order list.
type profileNode struct {
	kind profileKind
	keys []string
	vals map[string]*profileNode
	arr  []*profileNode
	str  string
	num  json.Number
}

// parseProfileTree parses the strict-JSON document with duplicate-member refusal and a
// nesting ceiling of 32 counting the root object as one.
func parseProfileTree(raw []byte) (*profileNode, *ProfileRefusal) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	root, ref := parseProfileValue(dec, "profile", 1)
	if ref != nil {
		return nil, ref
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, refuseProfile("profile", "trailing content after the catalog")
	}
	return root, nil
}

func parseProfileValue(dec *json.Decoder, field string, depth int) (*profileNode, *ProfileRefusal) {
	tok, err := dec.Token()
	if err != nil {
		return nil, refuseProfile(field, "the document does not parse")
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			return parseProfileObject(dec, field, depth)
		case '[':
			return parseProfileArray(dec, field, depth)
		}
		return nil, refuseProfile(field, "the document does not parse")
	case string:
		return &profileNode{kind: pNodeString, str: t}, nil
	case json.Number:
		return &profileNode{kind: pNodeNumber, num: t}, nil
	}
	return nil, refuseProfile(field, "the document does not parse")
}

func parseProfileObject(dec *json.Decoder, field string, depth int) (*profileNode, *ProfileRefusal) {
	if depth > 32 {
		return nil, refuseProfile(field, "object or array nesting is deeper than 32")
	}
	n := &profileNode{kind: pNodeObject, vals: map[string]*profileNode{}}
	for i := 0; ; i++ {
		kt, err := dec.Token()
		if err != nil {
			return nil, refuseProfile(field, "the document does not parse")
		}
		if d, ok := kt.(json.Delim); ok && d == '}' {
			return n, nil
		}
		key, ok := kt.(string)
		if !ok {
			return nil, refuseProfile(field, "a member name is not a string")
		}
		child := field + "." + key
		if _, dup := n.vals[key]; dup {
			return nil, refuseProfile(child, "the member occurs twice")
		}
		v, ref := parseProfileValue(dec, child, depth+1)
		if ref != nil {
			return nil, ref
		}
		n.keys = append(n.keys, key)
		n.vals[key] = v
	}
}

func parseProfileArray(dec *json.Decoder, field string, depth int) (*profileNode, *ProfileRefusal) {
	if depth > 32 {
		return nil, refuseProfile(field, "object or array nesting is deeper than 32")
	}
	n := &profileNode{kind: pNodeArray}
	for i := 0; ; i++ {
		et, err := dec.Token()
		if err != nil {
			return nil, refuseProfile(field, "the document does not parse")
		}
		if d, ok := et.(json.Delim); ok && d == ']' {
			return n, nil
		}
		v, ref := parseProfileFromToken(dec, et, fmt.Sprintf("%s[%d]", field, i), depth+1)
		if ref != nil {
			return nil, ref
		}
		n.arr = append(n.arr, v)
	}
}

func parseProfileFromToken(dec *json.Decoder, tok json.Token, field string, depth int) (*profileNode, *ProfileRefusal) {
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			return parseProfileObject(dec, field, depth)
		case '[':
			return parseProfileArray(dec, field, depth)
		}
		return nil, refuseProfile(field, "the document does not parse")
	case string:
		return &profileNode{kind: pNodeString, str: t}, nil
	case json.Number:
		return &profileNode{kind: pNodeNumber, num: t}, nil
	}
	return nil, refuseProfile(field, "the document does not parse")
}

// validateCatalog checks the catalog root, the single profile id, and then the whole
// profile record. `profile.<id>.<path>` is how every refusal names its field.
func validateCatalog(root *profileNode) (*Profile, *ProfileRefusal) {
	if root.kind != pNodeObject {
		return nil, refuseProfile("profile", "the catalog root must be an object")
	}
	for _, k := range root.keys {
		if k != "version" && k != "profiles" {
			return nil, refuseProfile("profile."+k, "unknown member; the catalog root has exactly version and profiles")
		}
	}
	v := root.vals["version"]
	if v == nil || v.kind != pNodeNumber || v.num.String() != "1" {
		return nil, refuseProfile("profile.version", "version must be the integer token 1; 1.0, 1e0 and \"1\" all refuse")
	}
	profiles := root.vals["profiles"]
	if profiles == nil || profiles.kind != pNodeObject {
		return nil, refuseProfile("profile.profiles", "profiles must be an object")
	}
	if len(profiles.keys) != 1 {
		return nil, refuseProfile("profile.profiles", "a profile file holds exactly one profile")
	}
	id := profiles.keys[0]
	if id == "" {
		return nil, refuseProfile("profile.profiles.<id>", "the profile id is empty")
	}
	return validateProfileRecord(id, profiles.vals[id])
}

// bindingFields are the profile record's six members, exactly the fields the preimage
// hashes (SPEC-SWARM-PROFILES, field-ownership table).
var bindingFields = []string{"worker", "route", "env_var", "model", "allowed_models", "prompt"}

func validateProfileRecord(id string, n *profileNode) (*Profile, *ProfileRefusal) {
	if n.kind != pNodeObject {
		return nil, refuseProfile("profile."+id, "the profile record must be an object")
	}
	known := map[string]bool{}
	for _, k := range bindingFields {
		known[k] = true
	}
	for _, k := range n.keys {
		if !known[k] {
			return nil, refuseProfile("profile."+id+"."+k, "unknown member; a profile record has exactly worker, route, env_var, model, allowed_models and prompt")
		}
	}
	for _, k := range bindingFields {
		if n.vals[k] == nil {
			return nil, refuseProfile("profile."+id+"."+k, "missing required member")
		}
	}

	p := &Profile{ID: id, raw: n}
	base := "profile." + id

	worker, ref := validateWorker(base, n.vals["worker"])
	if ref != nil {
		return nil, ref
	}
	p.Worker = worker

	route, ref := validateRoute(base, n.vals["route"])
	if ref != nil {
		return nil, ref
	}
	p.Route = route

	if p.EnvVar, ref = requiredString(base, n.vals["env_var"], "env_var"); ref != nil {
		return nil, ref
	}
	if p.EnvVar == "all" {
		return nil, refuseProfile(base+".env_var", "env_var=all is refused; a profile names exactly one route secret")
	}
	if p.Model, ref = requiredString(base, n.vals["model"], "model"); ref != nil {
		return nil, ref
	}
	if p.AllowedModels, ref = stringArray(base, n.vals["allowed_models"], "allowed_models", false); ref != nil {
		return nil, ref
	}
	prompt, ref := validatePrompt(base, n.vals["prompt"])
	if ref != nil {
		return nil, ref
	}
	p.Prompt = prompt
	return p, nil
}

// workerAllowed are the worker members this slice pins, from the field-ownership table.
// `worker.provider`, `worker.model`, `worker.base_url`, `worker.env_var`, `worker.key_file`
// and `worker.board` are forbidden in a profile; they are simply not in the set.
var workerAllowed = map[string]bool{
	"name": true, "usage": true, "harness": true, "harness_args": true,
	"worker_dir": true, "deadline": true, "execution": true,
	"read_roots": true, "input_limit_phrases": true,
}

func validateWorker(base string, n *profileNode) (WorkerProfile, *ProfileRefusal) {
	if n.kind != pNodeObject {
		return WorkerProfile{}, refuseProfile(base+".worker", "worker must be an object")
	}
	for _, k := range n.keys {
		if !workerAllowed[k] {
			return WorkerProfile{}, refuseProfile(base+".worker."+k, "unknown member in the worker description")
		}
	}
	var w WorkerProfile
	var ref *ProfileRefusal
	if w.Name, ref = requiredString(base, n.vals["name"], "worker.name"); ref != nil {
		return WorkerProfile{}, ref
	}
	if w.Usage, ref = requiredString(base, n.vals["usage"], "worker.usage"); ref != nil {
		return WorkerProfile{}, ref
	}
	if w.Harness, ref = requiredString(base, n.vals["harness"], "worker.harness"); ref != nil {
		return WorkerProfile{}, ref
	}
	// `worker.harness` is a member of a portable profile record, so "absolute" is
	// platform-independent here: rooted in either pathname grammar. `filepath.IsAbs`
	// alone is false for a leading-`/` path on windows (no volume), which stopped
	// the walk at the harness field on the hosted windows leg (2026-09-16) before it
	// ever reached `worker.execution`. The both-grammars predicate is the one
	// internal/check already uses (attest.go, corpus.go).
	if !filepath.IsAbs(w.Harness) && !strings.HasPrefix(w.Harness, "/") {
		return WorkerProfile{}, refuseProfile(base+".worker.harness", "a profile harness path must be absolute")
	}
	if w.HarnessArgs, ref = stringArray(base, n.vals["harness_args"], "worker.harness_args", true); ref != nil {
		return WorkerProfile{}, ref
	}
	if w.WorkerDir, ref = requiredString(base, n.vals["worker_dir"], "worker.worker_dir"); ref != nil {
		return WorkerProfile{}, ref
	}
	// deadline is the worker's one limit this record carries; a missing one is the
	// missing-limits refusal.
	if w.Deadline, ref = requiredString(base, n.vals["deadline"], "worker.deadline"); ref != nil {
		return WorkerProfile{}, ref
	}
	if _, err := time.ParseDuration(w.Deadline); err != nil {
		return WorkerProfile{}, refuseProfile(base+".worker.deadline", "the deadline is not a duration")
	}
	if ref := validateWorkerExecution(base, n.vals["execution"]); ref != nil {
		return WorkerProfile{}, ref
	}
	if n.vals["read_roots"] != nil {
		if w.ReadRoots, ref = stringArray(base, n.vals["read_roots"], "worker.read_roots", true); ref != nil {
			return WorkerProfile{}, ref
		}
	}
	if n.vals["input_limit_phrases"] != nil {
		if w.InputLimitPhrases, ref = stringArray(base, n.vals["input_limit_phrases"], "worker.input_limit_phrases", true); ref != nil {
			return WorkerProfile{}, ref
		}
	}
	return w, nil
}

// nativeAdapter and nativeAdapterRevision are the closed `adapter` /
// `adapter_revision` identifiers this build executes (issue #296): a native adapter
// identity and its one supported compatibility revision. They are implementation
// identifiers, never provider or model names, and any other value refuses before a
// gate or provider is used.
const (
	nativeAdapter         = "opencode-native/1"
	nativeAdapterRevision = "1"
)

// validateWorkerExecution checks the worker's `execution` object: it is the single
// owner of the native adapter identity and its compatibility revision, and an unknown
// or mismatched one is refused on its own field before any gate or provider use. The
// remaining members (path, env, artifact) keep their pin in the execution-binding
// companion.
func validateWorkerExecution(base string, n *profileNode) *ProfileRefusal {
	if n == nil || n.kind != pNodeObject || len(n.keys) == 0 {
		return refuseProfile(base+".worker.execution", "worker.execution must be a nonempty object; its members are pinned by the execution-binding companion")
	}
	adapter := n.vals["adapter"]
	if adapter == nil {
		return refuseProfile(base+".worker.execution.adapter", "missing required member")
	}
	if adapter.kind != pNodeString || adapter.str != nativeAdapter {
		return refuseProfile(base+".worker.execution.adapter", "a native adapter must be the supported closed identifier "+nativeAdapter)
	}
	rev := n.vals["adapter_revision"]
	if rev == nil {
		return refuseProfile(base+".worker.execution.adapter_revision", "missing required member")
	}
	if rev.kind != pNodeString || rev.str != nativeAdapterRevision {
		return refuseProfile(base+".worker.execution.adapter_revision", "a compatibility revision must be the supported closed identifier "+nativeAdapterRevision)
	}
	return nil
}

var credentialsAllowed = map[string]bool{
	"kind": true, "store": true, "seat": true, "age_key": true,
	"sops": true, "gate": true, "launcher": true,
}

func validateRoute(base string, n *profileNode) (RouteProfile, *ProfileRefusal) {
	if n.kind != pNodeObject {
		return RouteProfile{}, refuseProfile(base+".route", "route must be an object")
	}
	for _, k := range n.keys {
		if k != "provider" && k != "endpoint" && k != "credentials" {
			return RouteProfile{}, refuseProfile(base+".route."+k, "unknown member in the route")
		}
	}
	var r RouteProfile
	var ref *ProfileRefusal
	if r.Provider, ref = requiredString(base, n.vals["provider"], "route.provider"); ref != nil {
		return RouteProfile{}, ref
	}
	if n.vals["endpoint"] != nil {
		if r.Endpoint, ref = requiredString(base, n.vals["endpoint"], "route.endpoint"); ref != nil {
			return RouteProfile{}, ref
		}
	}
	cred := n.vals["credentials"]
	if cred == nil || cred.kind != pNodeObject {
		return RouteProfile{}, refuseProfile(base+".route.credentials", "route.credentials must be an object")
	}
	for _, k := range cred.keys {
		if !credentialsAllowed[k] {
			return RouteProfile{}, refuseProfile(base+".route.credentials."+k, "unknown member in the credential binding")
		}
	}
	for _, k := range []string{"kind", "store", "seat", "age_key", "sops", "gate", "launcher"} {
		if cred.vals[k] == nil {
			return RouteProfile{}, refuseProfile(base+".route.credentials."+k, "missing required member")
		}
	}
	if r.Credentials.Kind, ref = requiredString(base, cred.vals["kind"], "route.credentials.kind"); ref != nil {
		return RouteProfile{}, ref
	}
	if r.Credentials.Kind != "nova-secrets" {
		return RouteProfile{}, refuseProfile(base+".route.credentials.kind", "kind must be exactly nova-secrets")
	}
	for _, k := range []string{"store", "seat", "age_key", "sops", "gate", "launcher"} {
		if _, ref = requiredString(base, cred.vals[k], "route.credentials."+k); ref != nil {
			return RouteProfile{}, ref
		}
	}
	r.Credentials.Store = cred.vals["store"].str
	r.Credentials.Seat = cred.vals["seat"].str
	r.Credentials.AgeKey = cred.vals["age_key"].str
	r.Credentials.Sops = cred.vals["sops"].str
	r.Credentials.Gate = cred.vals["gate"].str
	r.Credentials.Launcher = cred.vals["launcher"].str
	return r, nil
}

func validatePrompt(base string, n *profileNode) (PromptProfile, *ProfileRefusal) {
	if n.kind != pNodeObject {
		return PromptProfile{}, refuseProfile(base+".prompt", "prompt must be an object")
	}
	for _, k := range n.keys {
		if k != "mode" && k != "prefix" && k != "tools" {
			return PromptProfile{}, refuseProfile(base+".prompt."+k, "unknown member in the prompt")
		}
	}
	var p PromptProfile
	var ref *ProfileRefusal
	if p.Mode, ref = requiredString(base, n.vals["mode"], "prompt.mode"); ref != nil {
		return PromptProfile{}, ref
	}
	if p.Mode != "legacy" && p.Mode != "compact" {
		return PromptProfile{}, refuseProfile(base+".prompt.mode", "mode must be legacy or compact")
	}
	if p.Prefix, ref = requiredString(base, n.vals["prefix"], "prompt.prefix"); ref != nil {
		return PromptProfile{}, ref
	}
	if len([]byte(p.Prefix)) > 4096 {
		return PromptProfile{}, refuseProfile(base+".prompt.prefix", "prefix is at most 4096 bytes")
	}
	if p.Tools, ref = stringArray(base, n.vals["tools"], "prompt.tools", false); ref != nil {
		return PromptProfile{}, ref
	}
	return p, nil
}

// requiredString reads a member that must be present as a non-empty string.
func requiredString(base string, n *profileNode, field string) (string, *ProfileRefusal) {
	if n == nil {
		return "", refuseProfile(base+"."+field, "missing required member")
	}
	if n.kind != pNodeString {
		return "", refuseProfile(base+"."+field, "must be a string")
	}
	if n.str == "" {
		return "", refuseProfile(base+"."+field, "must not be empty")
	}
	return n.str, nil
}

// stringArray reads a member that must be an array of non-empty strings. When nonEmpty is
// true, an empty array is refused as well.
func stringArray(base string, n *profileNode, field string, nonEmpty bool) ([]string, *ProfileRefusal) {
	if n == nil {
		return nil, refuseProfile(base+"."+field, "missing required member")
	}
	if n.kind != pNodeArray {
		return nil, refuseProfile(base+"."+field, "must be an array of strings")
	}
	out := make([]string, 0, len(n.arr))
	for i, e := range n.arr {
		at := fmt.Sprintf("%s.%s[%d]", base, field, i)
		if e.kind != pNodeString {
			return nil, refuseProfile(at, "must be a string")
		}
		if e.str == "" {
			return nil, refuseProfile(at, "must not be empty")
		}
		out = append(out, e.str)
	}
	if nonEmpty && len(out) == 0 {
		return nil, refuseProfile(base+"."+field, "must not be empty")
	}
	return out, nil
}

// Preimage returns sha256:<64 lowercase hex> over exactly the profile record's six binding
// fields, in RFC 8785 canonical field order, using the same number-free canonical encoding
// the catalog digest pins (SPEC-SWARM-PROFILES, encoding and admission bounds). Member
// order, whitespace and equivalent JSON escapes in the source file do not change it;
// changing any binding field does.
func (p *Profile) Preimage() (string, error) {
	obj := records.NewObject()
	for _, k := range bindingFields {
		v, err := profileNodeToRecord(p.raw.vals[k])
		if err != nil {
			return "", err
		}
		if err := obj.Set(k, v); err != nil {
			return "", err
		}
	}
	return records.ContentID(obj)
}

// profileNodeToRecord converts a parsed profile value into the records wire form. A JSON
// number anywhere in the binding fields is refused, exactly as the number-free encoding
// requires.
func profileNodeToRecord(n *profileNode) (records.Value, error) {
	switch n.kind {
	case pNodeString:
		return n.str, nil
	case pNodeArray:
		out := make([]records.Value, 0, len(n.arr))
		for _, e := range n.arr {
			v, err := profileNodeToRecord(e)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case pNodeObject:
		o := records.NewObject()
		for _, k := range n.keys {
			v, err := profileNodeToRecord(n.vals[k])
			if err != nil {
				return nil, err
			}
			if err := o.Set(k, v); err != nil {
				return nil, err
			}
		}
		return o, nil
	}
	return nil, refuseProfile("profile.<record>", "a JSON number occurs where the binding fields are number-free")
}
