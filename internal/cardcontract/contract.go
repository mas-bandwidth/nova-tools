// Package cardcontract is the frame around a card's task (docs/SPEC-CARD-CONTRACT.md): the
// frame the member hands native (Frame), the result shape a child ends with (Result), and
// the profiles, keyed by model family, that write JOB.md and the shims first on the child's
// PATH so the child meets the frame through the commands it already knows.
package cardcontract

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// Frame is what the member knows of one launch and hands native as a file: the repository
// and the commit to stage, the branch the checkout is on (and the member pushes), the
// attempt, what the attempt before left, the tier and the model. It is the packet's, never
// the brief's prose.
type Frame struct {
	Kind       string   `json:"kind"` // work or read
	Card       string   `json:"card"`
	Attempt    int      `json:"attempt"`
	Tier       string   `json:"tier,omitempty"`
	Model      string   `json:"model"`
	Repo       string   `json:"repo"`                   // the clone URL or local path the card works in; "" when it names none
	BaseRef    string   `json:"base_ref,omitempty"`     // the ref the card's work is based on (a pull request's base)
	StageSha   string   `json:"stage_sha,omitempty"`    // the commit staged: a previous attempt's pushed head, a read's head under read, else the card's base sha
	Branch     string   `json:"branch"`                 // the branch the checkout is on: the card's sprint branch, a read's work branch
	PrevHead   string   `json:"prev_head,omitempty"`    // the last pushed head of any earlier attempt (sprint.BaseOf)
	PrevFrom   int      `json:"prev_attempt,omitempty"` // the attempt PrevHead is the head of
	Why        string   `json:"why,omitempty"`          // a rework: how the attempt before ended
	Finding    string   `json:"finding,omitempty"`      // a rework: what the readers of the attempt before found
	Fix        string   `json:"fix,omitempty"`          // a rework: what the coordinator asks of this attempt
	ReviewBase string   `json:"review_base,omitempty"`  // a read: the ref the change is reviewed against
	Stage      []string `json:"stage,omitempty"`        // the recipe files the brief's Stage: header lines name, relative to Recipes
	Recipes    string   `json:"recipes,omitempty"`      // the member's recipes directory, <root>/recipes
}

// Staged is what native knows once the checkout is staged: the job directory, the checkout,
// the commit it is at, and the real git the shims hand through to.
type Staged struct {
	Job  string // <slot>/jobs/<label>
	Repo string // <job>/repo
	Head string // the full sha the checkout is at
	Git  string // the real git, absolute
	// Start is a read's: the commit the work under review started from, a full sha (the
	// merge base of Head and the review base, found when the checkout was staged), so the
	// read sees exactly the work's change however far the base branch has moved since;
	// "" for work, or when no merge base was found.
	Start string
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
var profiles = map[string]Profile{"claude": claude{}, "openai": openai{}, "plain": plain{family: "plain"}}

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

// ReadTitle begins the first line of a read's JOB.md in every profile, and of no work's: a
// brief that speaks to its readers names it, so a work card never takes itself for a read (a
// work card of the 5000-card load test, 2026-10-01, ended "nothing to do: no PR to review").
const ReadTitle = "# JOB: read"

// PushedName is the file in the job directory the git shim records each push in:
// branch, head and checkout top, tab separated, one line a push.
const PushedName = ".sprint/pushed.tsv"

// FinishName is the file in the job directory the gh shim records the finish in
// (gh pr create, gh pr review), in the result shape: a file the child is never
// told to write, so a RESULT.md the child writes after it does not overwrite it.
const FinishName = ".sprint/finish.md"

// StagedName is the file in the slot directory (outside the job, which the wall
// lets the child write) where native records the commit it staged, the one the
// member counts the child's commits from.
const StagedName = "staged"

// IsFinish is whether raw is the finish the gh shim recorded in the job, byte for byte:
// native publishes that record as the card's result when the child wrote no RESULT.md.
func IsFinish(job string, raw []byte) bool {
	b, err := os.ReadFile(filepath.Join(job, FinishName))
	return err == nil && string(b) == string(raw)
}

// ReadFinish is the finish the gh shim recorded in the job directory, and whether
// there is one (an empty file is none).
func ReadFinish(job string) (typedrec.CardResult, bool) {
	b, err := os.ReadFile(filepath.Join(job, FinishName))
	if err != nil || len(strings.TrimSpace(string(b))) == 0 {
		return typedrec.CardResult{}, false
	}
	return typedrec.ParseCardResult(b), true
}

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

// RecipesName is the directory recipes are kept in, in the member's root, and staged into,
// in the job directory.
const RecipesName = "recipes"

// StageRecipes copies the recipe files a frame names from the member's recipes directory
// into <job>/recipes, each at its own relative path (docs/SPEC-CARD-CONTRACT.md, staged
// recipes): a brief holds 16 KiB, a recipe a card works from can be larger, and the wall
// gives the child no forge to fetch one from. A name that is not a local relative path, or
// is not a regular file in the recipes directory, refuses the whole staging; every source is
// opened through an os.Root of the recipes directory, so no path component, a symlinked
// directory included, leaves it, and a refusal names the Stage line and the reason.
func StageRecipes(f Frame, job string) error {
	if len(f.Stage) == 0 {
		return nil
	}
	root, err := os.OpenRoot(f.Recipes)
	if err != nil {
		return fmt.Errorf("Stage: the recipes directory %s: %w", f.Recipes, err)
	}
	defer root.Close()
	for _, rel := range f.Stage {
		if !filepath.IsLocal(rel) {
			return fmt.Errorf("Stage: %q is not a path inside the recipes directory", rel)
		}
		fi, err := root.Lstat(rel)
		if err != nil || !fi.Mode().IsRegular() {
			return fmt.Errorf("Stage: %s is not a regular file inside %s (put it there, or drop the Stage: line)", rel, f.Recipes)
		}
		src, err := root.Open(rel)
		if err != nil {
			return fmt.Errorf("Stage: %s cannot be opened inside %s: %w", rel, f.Recipes, err)
		}
		b, err := io.ReadAll(src)
		src.Close()
		if err != nil {
			return fmt.Errorf("Stage: %s: %w", rel, err)
		}
		to := filepath.Join(job, RecipesName, rel)
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		if err := atomicfile.Write(to, b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// Prompt is the harness prompt of a framed card: JOB.md first, then the card.
func Prompt(job, card string) string {
	return "Read " + filepath.Join(job, JobName) + " first.\n\n" + card
}

// LastPushed is the last head the git shim recorded in the job directory, "" when none.
func LastPushed(job string) (branch, head string) {
	b, err := os.ReadFile(filepath.Join(job, PushedName))
	if err != nil {
		return "", ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Split(line, "\t")
		if len(f) >= 2 && typedrec.IsSha(strings.TrimSpace(f[1])) {
			branch, head = strings.TrimSpace(f[0]), strings.TrimSpace(f[1])
		}
	}
	return branch, head
}

// BrokenFindingText is what every profile's read JOB.md says of a broken verdict
// (docs/SPEC-CARD-CONTRACT.md section 3): it names the defect, or it is no verdict.
const BrokenFindingText = "A broken verdict tells them what to do: at least one finding line names the file (file:line), the line, or the card's STEP or RULE the work breaks, and says what to change. \"Request changes.\" alone, or an approval's words, is no finding: a broken verdict that names no file, line or rule is not a verdict, and the sprint asks another reader."

// ShapeText is the result shape as JOB.md quotes it, for a work card or a read.
func ShapeText(kind string) string {
	verdict := "ok | not-done | nothing"
	body := "what you would put in a pull request body"
	if kind == "read" {
		verdict = "ok | broken"
		body = "your findings, each with file:line"
	}
	return "    head: <the commit, full sha>\n    branch: <the branch it is on>\n    verdict: " + verdict +
		"\n    gate: <the gate command you ran, or ->\n    output: <the path of its output, or ->\n    report: <one line>\n\n    ## Body\n\n    <" + body + ">\n"
}
