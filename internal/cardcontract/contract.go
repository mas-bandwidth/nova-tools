// Package cardcontract is the frame around a card's task (docs/SPEC-CARD-CONTRACT.md): the
// frame the member hands native (Frame), the result shape a child ends with (Result), and
// the profiles, keyed by model family, that write JOB.md and the shims first on the child's
// PATH so the child meets the frame through the commands it already knows.
package cardcontract

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
)

// Frame is what the member knows of one launch and hands native as a file: the repository
// and the commit to stage, the branch the checkout is on (and the member pushes), the
// attempt, what the attempt before left, the tier and the model. It is the packet's, never
// the brief's prose.
type Frame struct {
	Kind       string `json:"kind"` // work or read
	Card       string `json:"card"`
	Attempt    int    `json:"attempt"`
	Tier       string `json:"tier,omitempty"`
	Model      string `json:"model"`
	Repo       string `json:"repo"`                  // the clone URL or local path the card works in; "" when it names none
	BaseRef    string `json:"base_ref,omitempty"`    // the ref the card's work is based on (a pull request's base)
	StageSha   string `json:"stage_sha,omitempty"`   // the commit staged: a previous attempt's pushed head, a read's head under read, else the card's base sha
	Branch     string `json:"branch"`                // the branch the checkout is on: the card's sprint branch, a read's work branch
	PrevHead   string `json:"prev_head,omitempty"`   // the previous attempt's pushed head
	Finding    string `json:"finding,omitempty"`     // the fix the reads of the attempt before asked for
	ReviewBase string `json:"review_base,omitempty"` // a read: the ref the change is reviewed against
	Rules      string `json:"rules,omitempty"`       // the RULES paragraph of the sprint's rules file, carried into JOB.md
}

// Staged is what native knows once the checkout is staged: the job directory, the checkout,
// the commit it is at, and the real git and gh the shims hand through to.
type Staged struct {
	Job  string // <slot>/jobs/<label>
	Repo string // <job>/repo
	Head string // the full sha the checkout is at
	Git  string // the real git, absolute
	Gh   string // the real gh, absolute; "" when the machine has none
}

// Shim is one script a profile writes first on the child's PATH.
type Shim struct {
	Name   string // the command it answers for: git, gh
	Script string // a POSIX sh script
}

// Profile is how one model family meets the frame (docs/SPEC-CARD-CONTRACT.md section 5).
type Profile interface {
	Family() string
	JobText(f Frame, s Staged) string
	Shims(f Frame, s Staged) []Shim
}

// Families are the model families a profile is keyed by, plain last: the fallback.
var Families = []string{"claude", "openai", "gemini", "grok", "deepseek", "plain"}

// profiles are the built profiles by family; a family with none here serves plain.
var profiles = map[string]Profile{"claude": claude{}, "plain": plain{family: "plain"}}

// For is the profile of a family: its own, else plain under the family's name.
func For(family string) Profile {
	if p, ok := profiles[family]; ok {
		return p
	}
	return plain{family: family}
}

// familyWords are the words of a model id (provider/model) that name its family, in order.
var familyWords = []struct{ family, word string }{
	{"claude", "claude"}, {"claude", "anthropic"},
	{"openai", "openai"}, {"openai", "gpt"},
	{"gemini", "gemini"}, {"gemini", "google"},
	{"grok", "grok"}, {"grok", "xai"},
	{"deepseek", "deepseek"},
}

// FamilyOf is the family of a model id: the first family word it carries, else plain.
func FamilyOf(model string) string {
	m := strings.ToLower(model)
	for _, fw := range familyWords {
		if strings.Contains(m, fw.word) {
			return fw.family
		}
	}
	return "plain"
}

// FrameName is the file name the member writes a frame under, beside the card file.
const FrameName = ".frame.json"

// WriteFrame writes a frame as JSON at path.
func WriteFrame(path string, f Frame) error {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(path, append(b, '\n'), 0o644)
}

// ReadFrame reads the frame at path; a frame with no kind or no card is refused.
func ReadFrame(path string) (Frame, error) {
	var f Frame
	b, err := os.ReadFile(path)
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return f, fmt.Errorf("the frame %s is not JSON: %w", path, err)
	}
	if (f.Kind != "work" && f.Kind != "read") || f.Card == "" {
		return f, fmt.Errorf("the frame %s wants kind work or read and a card, got kind %q card %q", path, f.Kind, f.Card)
	}
	return f, nil
}

// JobName is the frame's text in the job directory, the first thing the child reads.
const JobName = "JOB.md"

// PushedName is the file in the job directory the git shim records each push in:
// branch, head and checkout top, tab separated, one line a push.
const PushedName = "pushed.tsv"

// Install writes JOB.md into the job directory and the profile's shims into shimDir.
func Install(p Profile, f Frame, s Staged, shimDir string) error {
	if err := atomicfile.Write(filepath.Join(s.Job, JobName), []byte(p.JobText(f, s)), 0o644); err != nil {
		return fmt.Errorf("JOB.md: %w", err)
	}
	if err := os.MkdirAll(shimDir, 0o755); err != nil {
		return err
	}
	for _, sh := range p.Shims(f, s) {
		if err := atomicfile.Write(filepath.Join(shimDir, sh.Name), []byte(sh.Script), 0o755, atomicfile.ExactMode()); err != nil {
			return fmt.Errorf("the %s shim: %w", sh.Name, err)
		}
	}
	return nil
}

// Prompt is the harness prompt of a framed card: JOB.md first, then the card.
func Prompt(job, card string) string {
	return "Read " + filepath.Join(job, JobName) + " first.\n\n" + card
}

// Result is a child's RESULT.md read in the contract's shape (section 3).
type Result struct {
	Shaped  bool // the six keys are present and head and verdict read
	Head    string
	Branch  string
	Verdict string
	Gate    string
	Output  string
	Report  string
	Title   string
	Body    string
}

// resultKeys are the six keys a shaped result carries.
var resultKeys = []string{"head", "branch", "verdict", "gate", "output", "report"}

// shaRE is a commit a result names.
var shaRE = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// ParseResult reads a result: the first `key: value` of each key, and the text under a
// `## Body` line as the body.
func ParseResult(b []byte) Result {
	var r Result
	seen := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	var body []string
	inBody := false
	for sc.Scan() {
		line := sc.Text()
		if inBody {
			body = append(body, line)
			continue
		}
		t := strings.TrimSpace(line)
		if strings.EqualFold(t, "## Body") {
			inBody = true
			continue
		}
		k, v, ok := strings.Cut(t, ":")
		k = strings.ToLower(strings.TrimSpace(k))
		if !ok || strings.ContainsAny(k, " \t") {
			continue
		}
		if _, dup := seen[k]; !dup {
			seen[k] = strings.TrimSpace(v)
		}
	}
	r.Head, r.Branch = strings.ToLower(seen["head"]), seen["branch"]
	r.Verdict = strings.ToLower(seen["verdict"])
	r.Gate, r.Output, r.Report, r.Title = seen["gate"], seen["output"], seen["report"], seen["title"]
	r.Body = strings.TrimSpace(strings.Join(body, "\n"))
	r.Shaped = true
	for _, k := range resultKeys {
		if _, ok := seen[k]; !ok {
			r.Shaped = false
		}
	}
	if r.Report == "" || (r.Head != "-" && !shaRE.MatchString(r.Head)) || !knownVerdict(r.Verdict) {
		r.Shaped = false
	}
	if r.Head == "-" {
		r.Head = ""
	}
	return r
}

// knownVerdict is a verdict the shape allows: a work card's ok or not-done, a read's ok or broken.
func knownVerdict(v string) bool { return v == "ok" || v == "not-done" || v == "broken" }

// LastPushed is the last head the git shim recorded in the job directory, "" when none.
func LastPushed(job string) (branch, head string) {
	b, err := os.ReadFile(filepath.Join(job, PushedName))
	if err != nil {
		return "", ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Split(line, "\t")
		if len(f) >= 2 && shaRE.MatchString(strings.TrimSpace(f[1])) {
			branch, head = strings.TrimSpace(f[0]), strings.TrimSpace(f[1])
		}
	}
	return branch, head
}

// ShapeText is the result shape as JOB.md quotes it, for a work card or a read.
func ShapeText(kind string) string {
	verdict := "ok | not-done"
	body := "what you would put in a pull request body"
	if kind == "read" {
		verdict = "ok | broken"
		body = "your findings, each with file:line"
	}
	return "    head: <the commit, full sha>\n    branch: <the branch it is on>\n    verdict: " + verdict +
		"\n    gate: <the gate command you ran, or ->\n    output: <the path of its output, or ->\n    report: <one line>\n\n    ## Body\n\n    <" + body + ">\n"
}
