// Package jevcalib is Jev's calibration harness (nova-tools #2536): the prompt
// file a score question is asked with, the same-head pairs a prompt is judged
// on, and the tuning rule that says when a candidate prompt replaces the
// incumbent.
//
// A prompt is a FILE, so tuning Jev never needs a rebuild: `nova-decide review
// --prompt <file|sha8>` (or `prompt=` in etc/jev.conf) picks one, and the
// shipped prompts live here, embedded, under their sha8.
package jevcalib

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/prereview"
)

//go:embed prompts/*.txt
var promptFS embed.FS

// SeedSha8 is the seed prompt: today's prereview.ScoreQuestion() Instructions
// plus one trailing newline, no EXEMPLAR and no LEVEL lines (502 bytes).
const SeedSha8 = "fd94795e"

// DefaultSha8 is the prompt `nova-decide review` asks with when neither
// --prompt nor a conf `prompt=` names one: the best prompt of the 2026-09-24
// tuning run (rowan-new reports/jev-tuning-2026-09-24.tsv, iteration 7: the
// friends' read rubric as the score question, start at 10 and deduct only for
// a finding pointed to by file and line; testdata/pairs-2026-09-24.tsv holds
// its scores beside the friends' lines).
const DefaultSha8 = "6b7343c3"

// Prompt is one parsed prompt file.
type Prompt struct {
	// Sha8 is the first 8 hex of sha256 over the file bytes exactly as stored.
	Sha8 string
	// Instructions is every line that is not an EXEMPLAR or LEVEL line, in
	// order, joined with \n, trailing newlines trimmed: the string the score
	// question sends verbatim.
	Instructions string
	// Levels are the ten LEVEL lines in file order, or nil when the file has
	// none, in which case the question sends prereview.ScoreLevels. A prompt
	// that changes the levels is a different question: its answers are never
	// compared with another level set's, which is why the sha8 covers them.
	Levels []string
	// Exemplars are the EXEMPLAR <owner>/<repo>#<n> lines: metadata for the
	// tuning rule (no exemplar may sit on the held-out side), never sent.
	Exemplars []string
	// Source is where the prompt came from: "embedded:<sha8>" or the path.
	Source string
}

var (
	exemplarRE = regexp.MustCompile(`^EXEMPLAR ([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+#[1-9][0-9]*)$`)
	levelRE    = regexp.MustCompile(`^LEVEL (.+)$`)
	sha8RE     = regexp.MustCompile(`^[0-9a-f]{8}$`)
)

// Sha8 is the first 8 hex of sha256 over b.
func Sha8(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:8]
}

// ParsePrompt reads a prompt file's bytes. A LEVEL line count other than 0 or
// 10 is refused: the answer maps onto 1-10 by level index, so a question with
// another number of levels would print a score on another scale.
func ParsePrompt(raw []byte) (Prompt, error) {
	p := Prompt{Sha8: Sha8(raw)}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	var keep []string
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(line, "EXEMPLAR"):
			m := exemplarRE.FindStringSubmatch(line)
			if m == nil {
				return Prompt{}, fmt.Errorf("jevcalib: bad EXEMPLAR line %q; want EXEMPLAR <owner>/<repo>#<n>", line)
			}
			p.Exemplars = append(p.Exemplars, m[1])
		case strings.HasPrefix(line, "LEVEL "):
			p.Levels = append(p.Levels, levelRE.FindStringSubmatch(line)[1])
		default:
			keep = append(keep, line)
		}
	}
	p.Instructions = strings.TrimRight(strings.Join(keep, "\n"), "\n")
	if strings.TrimSpace(p.Instructions) == "" {
		return Prompt{}, fmt.Errorf("jevcalib: the prompt has no instructions")
	}
	if n := len(p.Levels); n != 0 && n != len(prereview.ScoreLevels) {
		return Prompt{}, fmt.Errorf("jevcalib: the prompt has %d LEVEL lines; want none or %d", n, len(prereview.ScoreLevels))
	}
	return p, nil
}

// Resolve reads a prompt by sha8 (one of the embedded prompts) or by path.
func Resolve(ref string) (Prompt, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Prompt{}, fmt.Errorf("jevcalib: empty prompt reference")
	}
	if sha8RE.MatchString(ref) {
		if _, err := os.Stat(ref); err != nil {
			raw, err := promptFS.ReadFile("prompts/" + ref + ".txt")
			if err != nil {
				return Prompt{}, fmt.Errorf("jevcalib: no embedded prompt %s (and no file by that name)", ref)
			}
			p, err := ParsePrompt(raw)
			if err != nil {
				return Prompt{}, err
			}
			if p.Sha8 != ref {
				return Prompt{}, fmt.Errorf("jevcalib: embedded prompt %s hashes to %s", ref, p.Sha8)
			}
			p.Source = "embedded:" + ref
			return p, nil
		}
	}
	raw, err := os.ReadFile(ref)
	if err != nil {
		return Prompt{}, fmt.Errorf("jevcalib: prompt file: %w", err)
	}
	p, err := ParsePrompt(raw)
	if err != nil {
		return Prompt{}, fmt.Errorf("%w (%s)", err, ref)
	}
	p.Source = ref
	return p, nil
}

// Default is the embedded default prompt.
func Default() Prompt {
	p, err := Resolve(DefaultSha8)
	if err != nil {
		panic("jevcalib: the embedded default prompt does not resolve: " + err.Error())
	}
	return p
}

// Question is the one score question this prompt asks.
func (p Prompt) Question() map[string]decide.Question {
	return prereview.ScoreQuestionWith(p.Instructions, p.Levels)
}

// ConfPrompt reads the `prompt=` key from a jev.conf file (key=value, '#'
// comments, never sourced; the grammar bin/jev-loop's conf_get reads). It
// returns "" when the file or the key is absent. A relative value is resolved
// against the conf file's directory, so `prompt=jev-prompts/x.txt` beside
// etc/jev.conf means etc/jev-prompts/x.txt.
func ConfPrompt(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	val := ""
	for _, line := range strings.Split(string(raw), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		i := strings.IndexByte(t, '=')
		if i < 0 || strings.TrimSpace(t[:i]) != "prompt" {
			continue
		}
		v := t[i+1:]
		if j := strings.Index(v, " #"); j >= 0 {
			v = v[:j]
		}
		val = strings.TrimSpace(v)
	}
	if val == "" || sha8RE.MatchString(val) || strings.HasPrefix(val, "/") {
		return val, nil
	}
	dir := path[:strings.LastIndexByte(path, '/')+1]
	return dir + val, nil
}
