package swarm

import (
	"encoding/json"
	"path"
	"path/filepath"
	"strings"
)

// THE HARNESS'S OWN FENCE (issue #644). OpenCode asks before a tool touches a path it calls
// EXTERNAL, and a `run` with no terminal answers every such question by rejecting it:
//
//	permission requested: external_directory (<dir>/*); auto-rejecting
//	Error: The user rejected permission to use this specific tool call.
//
// The model stops there, publishes nothing, and the batch scored the card `no-result` -- the
// model's own doing -- when the truth is that the machinery fenced off a path the CARD was
// told to use. Eight of thirty cards on Rowan's bench died this way on 2026-09-16.
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
// bench. The WALL, not the fence, is what keeps a card inside its job (SPEC-SANDBOX rule 1),
// and a fence rule is no place to hand one card another's work.
//
// BOTH WILDCARD SPELLINGS ARE WRITTEN, and not because `*` stops at a separator -- it does
// not: the harness's own matcher turns `*` into `.*`, which crosses `/` freely, so `<job>/*`
// already admits `<job>/scratch/*`. `<job>/**` is written beside it because it is the
// spelling a person reads as "everything under here", and a matcher that ever tightened `*`
// to one segment would leave the rule meaning what it says.
const (
	// FenceAllow, FenceAsk and FenceDeny are the harness's own permission actions. `ask` in
	// a non-interactive `run` is auto-rejected and the model STOPS -- the whole run ends and
	// the card's commits are stranded. `deny` is a TOOL ERROR returned to the model, which
	// notes it, works inside the job instead, and continues (issue #918). So the fence's
	// fallback is deny, and `ask` is never written: there is no terminal to answer it.
	FenceAllow = "allow"
	FenceAsk   = "ask"
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
// that ends the run (issue #918) -- and the job's own paths, plus the read-only paths the card
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
// verbatim, #465) -- and the caller says on stderr that the fence went unconfigured.
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
			for k, v := range prior {
				merged[k] = v
			}
			for k, v := range mine[FenceExternalDirectory].(map[string]any) {
				merged[k] = v
			}
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
		open := strings.Index(asked, "(")
		if open < 0 || !strings.HasSuffix(asked, ")") {
			if asked == "" {
				continue
			}
			return asked, true
		}
		patterns := asked[open+1 : len(asked)-1]
		first := strings.TrimSpace(strings.Split(patterns, ",")[0])
		if first == "" {
			first = strings.TrimSpace(asked[:open])
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
