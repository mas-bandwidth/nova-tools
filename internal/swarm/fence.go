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
// WHY THE JOB'S PARENT IS NAMED TOO. The fence resolves a command's path arguments against
// the harness's OWN cwd (the job directory), NOT against the cwd the shell is in when the
// command runs. A card whose STEP 1 is `cd repo` and which then writes `../scratch/TOUCHED`
// means <job>/scratch; the fence reads that same `../scratch` as <job>/../scratch, one
// directory up, and asks about `<slot>/jobs/scratch/*`. That is the pattern the rejections
// on the record carry -- `external_directory (/Users/.../1/jobs/*)` -- so a rule naming only
// the job directory would not have admitted a single one of them. The job's own `jobs`
// parent is the slot's own work area and nothing above it; the WALL, not the fence, is what
// keeps a card inside the job (SPEC-SANDBOX rule 1), and the wall's rules are untouched here.
const (
	// FenceAllow and FenceAsk are the harness's own permission actions. `ask` in a
	// non-interactive `run` is a rejection, which is the whole of this issue.
	FenceAllow = "allow"
	FenceAsk   = "ask"

	// FenceExternalDirectory is the permission key the harness asks under.
	FenceExternalDirectory = "external_directory"
)

// FenceJobPatterns is every pattern that makes ONE JOB internal to the harness's fence: the
// job directory and everything under it, and the `jobs` directory the fence resolves a
// card's `../<name>` into. Both the `/*` and the `/**` spellings are named: the harness
// matches a rule pattern against the pattern it is asking about, and `*` does not cross a
// separator, so `<job>/*` alone does not admit the `<job>/scratch/*` a card's own worktree
// or scratch directory produces.
func FenceJobPatterns(jobDir string) []string {
	job := fencePath(jobDir)
	if job == "" {
		return nil
	}
	jobs := path.Dir(job)
	out := []string{job + "/*", job + "/**"}
	if jobs != "" && jobs != "/" && jobs != job {
		out = append(out, jobs+"/*", jobs+"/**")
	}
	return out
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
// is still ASKED about -- which in a `run` is a rejection, and is now a REPORTED one -- and
// the job's own paths, plus the read-only paths the card named, are allowed. The set is
// deduped and ordered so the file this writes is byte-stable for one job.
func FencePermission(jobDir string, reads []string) map[string]any {
	external := map[string]any{"*": FenceAsk}
	for _, p := range append(FenceJobPatterns(jobDir), FenceReadPatterns(reads)...) {
		external[p] = FenceAllow
	}
	return map[string]any{FenceExternalDirectory: external}
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
		cfg["permission"] = existing
	case string:
		// A whole-config `"permission": "ask"` keeps its meaning for every other key.
		cfg["permission"] = map[string]any{"*": existing, FenceExternalDirectory: mine[FenceExternalDirectory]}
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
