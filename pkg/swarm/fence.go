package swarm

import (
	"encoding/json"
	"maps"
	"math"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/mas-bandwidth/nova-tools/pkg/swarmwire"
)

// THE HARNESS'S OWN FENCE. OpenCode asks before a tool touches a path it calls
// EXTERNAL, and a `run` with no terminal answers every such question by rejecting it:
//
//	permission requested: external_directory (<dir>/*); auto-rejecting
//	Error: The user rejected permission to use this specific tool call.
//
// The model stops there, publishes nothing, and the batch scored the card `no-result` -- the
// model's own doing -- when the truth is that the machinery fenced off a path the CARD was
// told to use.
//
// TWO THINGS ARE WRONG AND BOTH ARE THIS FILE'S BUSINESS. The fence is configured by nothing
// -- the job runs on the harness's defaults, which call everything outside the harness's own
// cwd external -- and a rejection it makes is invisible to the reader, who sees only the
// absence of a result.
//
// WHAT IS NAMED AND WHAT IS NOT. The job directory and everything under it, and nothing
// else -- not the job's `jobs` parent. An earlier draft of this fix named the parent too, on
// the belief that the fence resolves a card's `../scratch` against the HARNESS's cwd rather
// than the shell's cwd after `cd repo`; a real run of both binaries against the harness
// showed it resolving after the `cd`, with no rejection either way, so the parent rule bought
// nothing and widened a job's fence to every SIBLING job in the same slot on a --no-wall
// bench. The WALL, not the fence, is what keeps a card inside its job,
// and a fence rule is no place to hand one card another's work.
//
// BOTH WILDCARD SPELLINGS ARE WRITTEN, and not because `*` stops at a separator -- it does
// not: the harness's own matcher turns `*` into `.*`, which crosses `/` freely, so `<job>/*`
// already admits `<job>/scratch/*`. `<job>/**` is written beside it because it is the
// spelling a person reads as "everything under here", and a matcher that ever tightened `*`
// to one segment would leave the rule meaning what it says.
const (
	// FenceAllow and FenceDeny are the harness's own permission actions. `ask` in
	// a non-interactive `run` is auto-rejected and the model STOPS -- the whole run ends and
	// the card's commits are stranded. `deny` is a TOOL ERROR returned to the model, which
	// notes it, works inside the job instead, and continues. So the fence's
	// fallback is deny, and `ask` is never written: there is no terminal to answer it.
	FenceAllow = "allow"
	FenceDeny  = "deny"

	// FenceExternalDirectory is the permission key the harness asks under for a path
	// outside the cwd; FenceWebfetch is the network tool. Neither is left to prompt.
	FenceExternalDirectory = "external_directory"
	FenceWebfetch          = "webfetch"
)

// FenceJobPatterns is every pattern that makes ONE JOB internal to the harness's fence: the
// job directory and everything under it, in both wildcard spellings, and NOTHING ABOVE IT.
// A card's own `../scratch` and `../wt-<n>` live under the job directory, which is where the
// card was told to put them; a sibling job's directory does not, and is not this card's.
func FenceJobPatterns(jobDir string) []string {
	job := fencePath(jobDir)
	if job == "" {
		return nil
	}
	return []string{job + "/*", job + "/**"}
}

// FenceReadPatterns is every pattern that admits ONE read-only path a card named on its
// `READ:` line. The harness asks about the path's PARENT with a `/*` on it -- `read` of
// /sys/kernel/security/lsm asks about `/sys/kernel/security/*` -- so the parent is named
// beside the path itself, and both wildcard spellings are given for a path that is a
// directory. A relative path, and the root itself, are named by nobody and dropped.
func FenceReadPatterns(reads []string) []string {
	var out []string
	for _, r := range reads {
		p := fencePath(r)
		if p == "" || p == "/" || !strings.HasPrefix(p, "/") {
			continue
		}
		parent := path.Dir(p)
		out = append(out, p, p+"/*", p+"/**")
		if parent != "" && parent != "/" && parent != p {
			out = append(out, parent+"/*")
		}
	}
	return out
}

// FencePermission is the harness `permission` block one job runs under: everything external
// is DENIED WITHOUT PROMPTING -- a tool error the model routes around, never the auto-reject
// that ends the run -- and the job's own paths, plus the read-only paths the card
// named, are allowed. webfetch is denied as well, for the same reason: a run with no terminal
// has nobody to answer a prompt, and a prompt here is a dead card. The set is deduped so the
// file this writes is byte-stable for one job.
func FencePermission(jobDir string, reads []string) map[string]any {
	external := map[string]any{"*": FenceDeny}
	for _, p := range append(FenceJobPatterns(jobDir), FenceReadPatterns(reads)...) {
		external[p] = FenceAllow
	}
	return map[string]any{FenceExternalDirectory: external, FenceWebfetch: FenceDeny}
}

// MergeFencePermission returns the config bytes the job's harness reads: the provider config
// the caller carried (raw, which may be empty when `--config` named none) with this job's
// permission block merged into it. A config this side cannot parse is returned unchanged and
// ok=false -- the copy is not this function's to refuse (a provider config is carried
// verbatim) -- and the caller says on stderr that the fence went unconfigured.
//
// A permission block already in the carried config is KEPT and added to: a person's own
// `read` or `bash` rules are theirs, and only the external_directory patterns this job needs
// are written over.
func MergeFencePermission(raw []byte, jobDir string, reads []string) ([]byte, bool) {
	cfg := map[string]any{}
	if len(strings.TrimSpace(string(raw))) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return raw, false
		}
	}
	if _, named := cfg["$schema"]; !named {
		cfg["$schema"] = "https://opencode.ai/config.json"
	}
	mine := FencePermission(jobDir, reads)
	switch existing := cfg["permission"].(type) {
	case map[string]any:
		// The person's own external_directory rules stay under this job's, so a pattern
		// they allowed is still allowed and the job's own are never absent.
		if prior, ok := existing[FenceExternalDirectory].(map[string]any); ok {
			merged := map[string]any{}
			maps.Copy(merged, prior)
			maps.Copy(merged, mine[FenceExternalDirectory].(map[string]any))
			existing[FenceExternalDirectory] = merged
		} else {
			existing[FenceExternalDirectory] = mine[FenceExternalDirectory]
		}
		// webfetch is denied unless the person named their own choice: a prompt here is a
		// dead card, and this run has no terminal to answer one.
		if _, named := existing[FenceWebfetch]; !named {
			existing[FenceWebfetch] = FenceDeny
		}
		cfg["permission"] = existing
	case string:
		// A whole-config `"permission": "ask"` keeps its meaning for every other key.
		cfg["permission"] = map[string]any{"*": existing, FenceExternalDirectory: mine[FenceExternalDirectory], FenceWebfetch: FenceDeny}
	default:
		cfg["permission"] = mine
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return raw, false
	}
	return append(out, '\n'), true
}

// CardReadPathsMarker is the card line this reads. A card that needs a read-only path
// outside its job says so in one line -- `READ: /sys/kernel/security/lsm` -- and that line,
// and nothing inferred from the card's prose, is what opens the fence for it.
const CardReadPathsMarker = "READ:"

// CardReadPaths is every absolute path a card named on a `READ:` line, in the order the card
// names them, deduped. Only absolute paths are taken: a relative one is inside the job (which
// is already internal) or is a word in a sentence, and neither is a fence rule.
func CardReadPaths(card []byte) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(string(card), "\n") {
		trimmed := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#-*> "))
		if !strings.HasPrefix(trimmed, CardReadPathsMarker) {
			continue
		}
		for _, field := range strings.Fields(strings.TrimPrefix(trimmed, CardReadPathsMarker)) {
			field = strings.Trim(field, "`\"',;")
			if !strings.HasPrefix(field, "/") || seen[field] {
				continue
			}
			seen[field] = true
			out = append(out, field)
		}
	}
	return out
}

// FenceRejectionMark and fenceRejectionTail are the harness's own words, printed by its
// `run` command for every permission it answers with a reject:
//
//	! permission requested: external_directory (/x/y/*); auto-rejecting
//
// The line carries the harness's colour codes around it, so it is found by its two marks and
// never by an exact match on a whole line.
const (
	FenceRejectionMark = "permission requested: "
	fenceRejectionTail = "; auto-rejecting"
)

// FenceRejection reports the FIRST path the harness's fence rejected in one capture, and
// whether it rejected anything at all. The first is the one that matters: the model stops
// at it, and every later line is a consequence of the same missing rule.
func FenceRejection(raw []byte) (string, bool) {
	for _, line := range strings.Split(string(raw), "\n") {
		i := strings.Index(line, FenceRejectionMark)
		if i < 0 {
			continue
		}
		rest := line[i+len(FenceRejectionMark):]
		j := strings.Index(rest, fenceRejectionTail)
		if j < 0 {
			continue
		}
		asked := strings.TrimSpace(rest[:j])
		// `external_directory (/x/y/*)` -- the patterns are in the parentheses, the first
		// of which is the path the card was stopped at. A form with no parentheses (a
		// permission that carries no pattern) names the permission itself.
		before, after, found := strings.Cut(asked, "(")
		if !found || !strings.HasSuffix(asked, ")") {
			if asked == "" {
				continue
			}
			return asked, true
		}
		patterns := after[:len(after)-1]
		first := strings.TrimSpace(strings.Split(patterns, ",")[0])
		if first == "" {
			first = strings.TrimSpace(before)
		}
		if first == "" {
			continue
		}
		return first, true
	}
	return "", false
}

// fencePath is one path as the harness spells it: cleaned, and with separators the harness's
// own `replaceAll("\\","/")` would have produced, so a rule written on windows matches a
// pattern the harness asks about.
func fencePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	return strings.ReplaceAll(filepath.Clean(p), "\\", "/")
}

// fenceRuleDoc is one external_directory pattern and its action as the table holds it:
// 1 allow, 2 deny (schemas/swarm/swarm.schema, FenceRule); 0 is refused by the reader.
type fenceRuleDoc struct {
	Pattern string `json:"pattern"`
	Action  uint8  `json:"action"`
}

// fenceConfigDoc is the FenceConfig record: FencePermission's input (the job directory and
// the card's READ: paths) and its result (the external_directory rules and the webfetch
// action) (schemas/swarm/swarm.schema, FenceConfig). The harness config the permission
// block is merged into is OpenCode's format, external, and stays as it is.
type fenceConfigDoc struct {
	JobDir   string         `json:"job_dir"`
	Reads    []string       `json:"reads,omitempty"`
	Rules    []fenceRuleDoc `json:"rules,omitempty"`
	Webfetch uint8          `json:"webfetch"`
}

// fenceActions are the harness actions as the table's uint8; `ask` is never written.
var fenceActions = []string{"", FenceAllow, FenceDeny}

// fenceConfigOf is the FenceConfig record of one job: the rules FencePermission writes,
// in pattern order so the record is byte-stable for one job.
func fenceConfigOf(jobDir string, reads []string) fenceConfigDoc {
	d := fenceConfigDoc{JobDir: jobDir, Reads: reads}
	perm := FencePermission(jobDir, reads)
	external, _ := perm[FenceExternalDirectory].(map[string]any)
	for _, p := range slices.Sorted(maps.Keys(external)) {
		a, _ := external[p].(string)
		d.Rules = append(d.Rules, fenceRuleDoc{Pattern: p, Action: uint8(slices.Index(fenceActions, a))})
	}
	if w, _ := perm[FenceWebfetch].(string); w != "" {
		d.Webfetch = uint8(slices.Index(fenceActions, w))
	}
	return d
}

var fenceTable = swarmTable[swarmwire.FenceConfig, swarmwire.FenceConfigBuilder, *swarmwire.FenceConfigBuilder]{
	swarmwire.FenceConfigLoadMeasure, swarmwire.FenceConfigLoad, swarmwire.FenceConfigFromJson,
	swarmwire.FenceConfigMeasure, swarmwire.FenceConfigSave, swarmwire.FenceConfigToJsonMeasure, swarmwire.FenceConfigToJson,
}

// fenceConfigReader is the FenceConfig record's strict reader (owner's rules S1 and S2):
// the table wire, its padded standard base64, the schema JSON text and the legacy JSON
// object, whose keys and types are the schema's, so the schema JSON text reads as legacy
// into the same value. Each damaged form is refused by name and counted. An action or a
// webfetch past 2 is refused as clamped, and a rule's action 0 (none) is refused: the
// harness has no third action a non-interactive run can take.
func fenceConfigReader() *schemaread.Reader[fenceConfigDoc] {
	return schemaread.NewReader(swarmCodec("FENCE_CONFIG", "nova-swarm run -h", fenceTable,
		func(r *swarmwire.FenceConfig) fenceConfigDoc {
			d := fenceConfigDoc{JobDir: exitText(&r.JobDir), Reads: swarmList(&r.Reads), Webfetch: r.Webfetch}
			for i := int32(0); i < r.Rules.Count; i++ {
				rule := r.Rules.At(i)
				d.Rules = append(d.Rules, fenceRuleDoc{exitText(&rule.Pattern), rule.Action})
			}
			return d
		},
		func(d *fenceConfigDoc) []byte {
			rules := make([]any, 0, len(d.Rules))
			for _, r := range d.Rules {
				m := map[string]any{"action": r.Action}
				if r.Pattern != "" {
					m["pattern"] = r.Pattern
				}
				rules = append(rules, m)
			}
			return swarmJSON(map[string]any{"job_dir": d.JobDir, "reads": swarmEntries(d.Reads), "rules": rules, "webfetch": d.Webfetch})
		},
		jsonLegacy,
		func(d *fenceConfigDoc, in []byte) schemaread.Report {
			*d = fenceConfigDoc{}
			var legacy struct {
				JobDir   string         `json:"job_dir"`
				Reads    []any          `json:"reads"`
				Rules    []fenceRuleDoc `json:"rules"`
				Webfetch uint8          `json:"webfetch"`
			}
			if json.Unmarshal(in, &legacy) != nil {
				return schemaread.Report{Malformed: true}
			}
			// A read is a bare text or the schema's {"value": text} Entry.
			for _, r := range legacy.Reads {
				switch x := r.(type) {
				case string:
					d.Reads = append(d.Reads, x)
				case map[string]any:
					s, ok := x["value"].(string)
					if !ok {
						*d = fenceConfigDoc{}
						return schemaread.Report{Malformed: true}
					}
					d.Reads = append(d.Reads, s)
				default:
					*d = fenceConfigDoc{}
					return schemaread.Report{Malformed: true}
				}
			}
			d.JobDir, d.Rules, d.Webfetch = legacy.JobDir, legacy.Rules, legacy.Webfetch
			return schemaread.Report{}
		},
		func(d *fenceConfigDoc, c *schemaread.Checker) {
			c.Required("job_dir", d.JobDir != "")
			c.Text("job_dir", d.JobDir, math.MaxInt)
			for _, r := range d.Reads {
				c.Text("reads", r, math.MaxInt)
			}
			for _, r := range d.Rules {
				c.Text("rules.pattern", r.Pattern, math.MaxInt)
				c.Required("rules.action", r.Action != 0)
				c.Bound("rules.action", int64(r.Action), 1, 2)
			}
			c.Bound("webfetch", int64(d.Webfetch), 0, 2)
		}))
}

// ReadFenceConfig reads the FenceConfig record from body in any form and hands the
// refusal to the caller by name; a malformed value is never read as absent or empty.
func ReadFenceConfig(body []byte) (fenceConfigDoc, schemaread.Form, error) {
	d, res, err := fenceReads.Read(body)
	return d, res.Form, err
}
