package ci

// ci_cardtemplates.go is the machine behind the `cardtemplates` class test in
// docs/SPEC-CI.md. A card template is the text a worker is handed verbatim: the
// swarm does not rewrite it, so every command spelt in it runs, as written, on
// whatever bench the card was dealt to. The estate is mixed -- linux benches
// (hulk, vision, space, mini) and darwin ones (the Studio, the Air) -- so a
// template that spells a GNU-only command is a card that is dead on arrival the
// first time the router picks a Mac.
//
// The hurt, measured 2026-09-18 by the schema dogfood: the round-1 card
// templates spelt `/usr/bin/time -f`, `nproc`, `go --version` and `java
// --version`. Every one of them is fine on hulk and wrong on the Air, and the
// card did not fail at cut time or at admission -- it failed inside the worker,
// minutes in, with a shell error that named nothing about portability.
//
// This file reads every *.md and *.card under the template directories as TEXT
// and refuses a known OS-specific or wrong-tool spelling. It writes nothing,
// runs nothing, and never executes what it reads. Its only exception set is a
// shrink-only allowlist, each row naming the file, the spelling, a date and a
// reason; a row that names no offender is itself refused, so the file can only
// ever get shorter.

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// CardTemplatesVerbLine is the help line the class test is entered under, word
// for word as docs/SPEC-CI.md prints it.
const CardTemplatesVerbLine = "cardtemplates  read every shipped card template; refuse a command only one of the estate's platforms has"

// CardTemplateDirs are the directories this repository ships card templates in,
// relative to the repository root. A directory that is not there is not an
// error: the list names where a template MAY live, and the estate grows its
// directories before it grows its templates.
var CardTemplateDirs = []string{
	"cmd/nova-pulse/testdata/templates",
	"cmd/nova-swarm/testdata/templates",
	"docs/templates",
	"tools/templates",
}

// cardTemplateExts are the two suffixes a card template carries. A .tsv beside
// them (benches.tsv) is a table, not a card, and is not read.
var cardTemplateExts = []string{".md", ".card"}

// CardTemplateFinding is one refused spelling: where it is, what was spelt, the
// platform it belongs to, and the one thing to do about it.
type CardTemplateFinding struct {
	File   string // path relative to the repository root, slashes
	Line   int    // 1-based
	Spell  string // the rule's name, which is also the allowlist's key
	Only   string // "linux", "darwin" or "wrong-tool"
	Text   string // the offending line, trimmed and bounded
	Remedy string // the portable spelling
}

// Render is the one-line refusal for this finding.
func (f CardTemplateFinding) Render() string {
	return fmt.Sprintf("CI-CARDTEMPLATES file=%s line=%d spell=%s only=%s line=%q remedy=%q",
		f.File, f.Line, f.Spell, f.Only, f.Text, f.Remedy)
}

// CardTemplatesResult is one run: the templates read, the entries the allowlist
// still honours, the offenders left, and the entries that name no offender.
type CardTemplatesResult struct {
	Templates   int
	Allowlisted int
	Findings    []CardTemplateFinding
	Stale       []CardTemplateFinding
}

// Refused is the number of lines a run would print: offenders plus stale rows.
func (r CardTemplatesResult) Refused() int { return len(r.Findings) + len(r.Stale) }

// OKLine is the one line a clean run prints.
func (r CardTemplatesResult) OKLine() string {
	return fmt.Sprintf("CI-CARDTEMPLATES OK templates=%d allowlisted=%d refused=0", r.Templates, r.Allowlisted)
}

// FailLine closes a refusal with the counts.
func (r CardTemplatesResult) FailLine() string {
	return fmt.Sprintf("CI-CARDTEMPLATES FAIL templates=%d allowlisted=%d refused=%d", r.Templates, r.Allowlisted, r.Refused())
}

// ExitCode is 2 when anything is refused and 0 when the tree is clean.
func (r CardTemplatesResult) ExitCode() int {
	if r.Refused() > 0 {
		return 2
	}
	return 0
}

// cardTemplateRule is one spelling the check knows. Pattern is what it looks
// for; Unless, when it is set, is what makes the same line portable anyway --
// the `uname`-chosen pair, or the `||` fallback -- so the portable spelling of
// a fact is never refused for naming the platform-specific half of itself.
type cardTemplateRule struct {
	name    string
	only    string
	pattern *regexp.Regexp
	unless  *regexp.Regexp
	remedy  string
}

// cardTemplateRules is the list, and it GROWS: every card that dies on a bench
// for a spelling reason adds a row here, so the next card cannot. (The
// allowlist beside it is the one that only shrinks.) Ordering is the order the
// findings come back in, so a template with two spellings reports both, in the
// order a reader reads the file.
var cardTemplateRules = []cardTemplateRule{
	{
		name: "proc", only: "linux",
		pattern: regexp.MustCompile(`/proc/`),
		remedy:  "darwin has no procfs; ask the fact portably (sysctl -n, ps, uname) or choose by `case \"$(uname -s)\" in`",
	},
	{
		name: "nproc", only: "linux",
		pattern: regexp.MustCompile(`\bnproc\b`),
		unless:  regexp.MustCompile(`hw\.ncpu`),
		remedy:  "cores=$(if [ \"$(uname -s)\" = Darwin ]; then sysctl -n hw.ncpu; else nproc; fi)",
	},
	{
		name: "hw_ncpu", only: "darwin",
		pattern: regexp.MustCompile(`sysctl -n hw\.`),
		unless:  regexp.MustCompile(`\bnproc\b|/proc/`),
		remedy:  "cores=$(if [ \"$(uname -s)\" = Darwin ]; then sysctl -n hw.ncpu; else nproc; fi)",
	},
	{
		name: "gnu_time", only: "linux",
		pattern: regexp.MustCompile(`/usr/bin/time\s+-f|\btime\s+-f\b`),
		remedy:  "there is no /usr/bin/time on a stock Mac; report the harness's own timing line instead of measuring it in the card",
	},
	{
		name: "df_blocksize", only: "linux",
		pattern: regexp.MustCompile(`\bdf\s+-B`),
		remedy:  "df -k, which both platforms take; -BG is GNU coreutils only",
	},
	{
		name: "time_style", only: "linux",
		pattern: regexp.MustCompile(`--time-style`),
		remedy:  "GNU ls only; ask stat, or do not report a timestamp from the card",
	},
	{
		name: "readlink_f", only: "linux",
		pattern: regexp.MustCompile(`\breadlink\s+-f\b`),
		unless:  regexp.MustCompile(`\|\||2>/dev/null`),
		remedy:  "readlink -f is GNU; use `cd \"$(dirname \"$p\")\" && pwd -P`, or give it a `|| ` fallback",
	},
	{
		name: "stat_c", only: "linux",
		pattern: regexp.MustCompile(`\bstat\s+(-c|--format)\b`),
		remedy:  "BSD stat spells it -f; choose by uname, or do not read the mode in the card",
	},
	{
		name: "sha256sum", only: "linux",
		pattern: regexp.MustCompile(`\b(sha256sum|md5sum)\b`),
		remedy:  "shasum -a 256 (and shasum -a 1) is on both; sha256sum and md5sum are coreutils only",
	},
	{
		name: "grep_perl", only: "linux",
		pattern: regexp.MustCompile(`\bgrep\s+(-[A-Za-z]*P\b|--perl-regexp)`),
		remedy:  "BSD grep has no -P; use grep -E",
	},
	{
		name: "free", only: "linux",
		pattern: regexp.MustCompile(`\bfree\s+-[mgk]\b|\blsb_release\b|\bapt-get\b|\bldd\b`),
		remedy:  "a linux-only utility; ask uname first, or drop the step -- a card reports its work, not its machine",
	},
	{
		name: "mac_only", only: "darwin",
		pattern: regexp.MustCompile(`\b(sw_vers|diskutil|pbcopy|pbpaste|otool|codesign|launchctl|sips)\b`),
		remedy:  "a darwin-only utility; a card dealt to a linux bench has none of it",
	},
	{
		name: "brew", only: "darwin",
		pattern: regexp.MustCompile(`\bbrew\s+(install|list|--prefix)\b`),
		remedy:  "no card installs anything; the bench is provisioned before the loop trusts it",
	},
	{
		name: "which", only: "wrong-tool",
		pattern: regexp.MustCompile("\\$\\(which\\s|`which\\s"),
		remedy:  "command -v <name>, which is POSIX and is a shell builtin on both",
	},
	{
		name: "go_version", only: "wrong-tool",
		pattern: regexp.MustCompile(`\bgo\s+--version\b`),
		remedy:  "go version -- the go command has no --version and exits 2 with a usage wall",
	},
	{
		name: "java_version", only: "wrong-tool",
		pattern: regexp.MustCompile(`\bjava\s+--version\b`),
		remedy:  "java -version 2>&1 -- the long form is JDK 9+ only and the short one writes to stderr",
	},
	{
		name: "dotnet_version", only: "wrong-tool",
		pattern: regexp.MustCompile(`\bdotnet\s+version\b`),
		remedy:  "dotnet --version -- `dotnet version` is not a dotnet command",
	},
}

// maxCardTemplateLine bounds the offending text carried into a finding, so one
// pathological line cannot own the failure output.
const maxCardTemplateLine = 160

// CheckCardTemplates reads every card template under the given directories and
// returns the offenders, the allowlist rows honoured, and any row that names no
// offender. The directories come from the caller, never from a walk of the
// repository, so a test drives it with a fixture tree. A directory that does
// not exist is skipped.
func CheckCardTemplates(root string, dirs []string, allowlistPath string) (CardTemplatesResult, error) {
	var res CardTemplatesResult
	entries, err := readCardTemplateAllowlist(allowlistPath)
	if err != nil {
		return res, err
	}
	matched := make([]bool, len(entries))

	for _, dir := range dirs {
		base := filepath.Join(root, filepath.FromSlash(dir))
		info, statErr := os.Stat(base)
		if statErr != nil || !info.IsDir() {
			continue
		}
		walkErr := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !hasCardTemplateExt(path) {
				return nil
			}
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			rel = filepath.ToSlash(rel)
			res.Templates++
			res.Findings = append(res.Findings, scanCardTemplate(rel, string(raw))...)
			return nil
		})
		if walkErr != nil {
			return res, walkErr
		}
	}

	var remaining []CardTemplateFinding
	for _, f := range res.Findings {
		if i := matchCardTemplateAllow(entries, f); i >= 0 {
			matched[i] = true
			res.Allowlisted++
			continue
		}
		remaining = append(remaining, f)
	}
	res.Findings = remaining
	for i, e := range entries {
		if matched[i] {
			continue
		}
		res.Stale = append(res.Stale, CardTemplateFinding{
			File:   e.file,
			Spell:  e.spell,
			Only:   "allowlist",
			Remedy: "delete the stale row; this list only shrinks",
		})
	}
	return res, nil
}

// hasCardTemplateExt reports whether the path is one of the two card suffixes.
func hasCardTemplateExt(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	for _, want := range cardTemplateExts {
		if ext == want {
			return true
		}
	}
	return false
}

// scanCardTemplate reads one template's text and returns its findings, in file
// order and then in rule order.
func scanCardTemplate(rel, src string) []CardTemplateFinding {
	var out []CardTemplateFinding
	for i, line := range strings.Split(src, "\n") {
		text := strings.TrimRight(line, " \t\r")
		if strings.TrimSpace(text) == "" {
			continue
		}
		for _, rule := range cardTemplateRules {
			if !rule.pattern.MatchString(text) {
				continue
			}
			if rule.unless != nil && rule.unless.MatchString(text) {
				continue
			}
			out = append(out, CardTemplateFinding{
				File:   rel,
				Line:   i + 1,
				Spell:  rule.name,
				Only:   rule.only,
				Text:   boundCardTemplateLine(strings.TrimSpace(text)),
				Remedy: rule.remedy,
			})
		}
	}
	return out
}

// boundCardTemplateLine caps the text a finding carries.
func boundCardTemplateLine(s string) string {
	if len(s) <= maxCardTemplateLine {
		return s
	}
	return s[:maxCardTemplateLine] + "..."
}

// cardTemplateAllow is one allowlist row: the offender it names, by FILE and
// SPELLING and never by line. SPEC-CI's shared conventions: a list matched by
// line turns dev red the first time a merge shifts a line in a listed file, and
// the thing being excepted here is a spelling in a template, not a position.
// The date and the reason that follow are for a reader and are not matched on.
type cardTemplateAllow struct {
	file  string
	spell string
}

// readCardTemplateAllowlist parses `file spell date reason` rows, ignoring
// blank lines and # comments. A missing file is an empty allowlist, never an
// error: a tree with nothing parked in it is the goal.
func readCardTemplateAllowlist(path string) ([]cardTemplateAllow, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []cardTemplateAllow
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		out = append(out, cardTemplateAllow{file: fields[0], spell: fields[1]})
	}
	return out, sc.Err()
}

// matchCardTemplateAllow returns the index of the row naming this finding, or -1.
func matchCardTemplateAllow(entries []cardTemplateAllow, f CardTemplateFinding) int {
	for i, e := range entries {
		if e.file == f.File && e.spell == f.Spell {
			return i
		}
	}
	return -1
}
