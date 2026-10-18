// Package cardcontract is the frame around a card's task (docs/SPEC-CARD-CONTRACT.md): the
// frame the member hands native (Frame), the result shape a child ends with (Result), and
// the profiles, keyed by model family, that write JOB.md and the shims first on the child's
// PATH so the child meets the frame through the commands it already knows.
package cardcontract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
	wire "github.com/mas-bandwidth/nova-tools/pkg/decidewire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/mas-bandwidth/nova-tools/pkg/typedrec"
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
	// A decide read's bars on p(defect) (docs/SPEC-SPRINT.md section 6): native asks the
	// read decision over the card and the diff before any child and routes the read by
	// them; both empty for a strings read.
	DecideBounce string `json:"decide_bounce,omitempty"`
	DecideReview string `json:"decide_review,omitempty"`
	// A work card's gate decision bars (docs/SPEC-SPRINT.md section 5, the gate verdict):
	// native classifies a red gate's failures by them after the child, before the member
	// reports the take; both empty for none.
	DecideGateFlaky       string `json:"decide_gate_flaky,omitempty"`
	DecideGatePreexisting string `json:"decide_gate_preexisting,omitempty"`
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
	Start   string
	GoCache string // the machine's shared build cache the child's GOCACHE names; "" when it has none
	Gate    *Gate  // a read's gate (ReadGate); nil: the card's
	// Carry is a rework's staging at the tip of its base branch (Carry); nil for a first
	// attempt, a read, and a rework whose base is a sha or a tag, which never moves.
	Carry *Carry
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

// The frame's one strict read (owner's rules S2 and S4, readers first; C13):
// ReadFrame reads the frame from the binary image of the decide Frame table,
// the padded standard base64 of that image, the table's schema JSON text and
// the legacy JSON WriteFrame writes today, refuses a damaged value by name
// (`FRAME REFUSED: <reason>; run: nova-swarm -h`) and counts each read under
// its form. Writers stay legacy (WriteFrame), so the per-form counters are
// the cutover's evidence.
const (
	frameRecord = "FRAME"
	frameHelp   = "nova-swarm -h"
)

// frameDoc is one read of the frame: the record every form reads to, and the
// region the table forms hold it in.
type frameDoc struct {
	rec    Frame
	region []byte
}

// frameReads is the frame's one reader, so its per-form counters accumulate.
var frameReads = frameReader()

func frameReader() *schemaread.Reader[frameDoc] {
	return schemaread.NewReader(&schemaread.Codec[frameDoc]{
		Name:  frameRecord,
		Help:  frameHelp,
		Reset: func(d *frameDoc) { *d = frameDoc{} },
		Measure: func(w []byte) error {
			_, err := wire.FrameLoadMeasureReason(w)
			return err
		},
		LoadWire:   frameLoadWire,
		LoadJSON:   frameLoadJSON,
		Legacy:     frameLegacy,
		LoadLegacy: frameLoadLegacy,
	})
}

// frameStrict is one generated TableReport as the strict read decides on it.
func frameStrict(r wire.TableReport, ok bool) schemaread.Report {
	out := schemaread.Report{Unknown: int(r.Unknown), KindMismatch: int(r.KindMismatch), Clamped: int(r.Clamped), Malformed: r.Malformed || !ok || r.Duplicate > 0}
	if r.Verdict == wire.TableOpenRefused || r.Verdict == wire.TableOpenDamaged {
		out.Reason = r.Err().Error()
	}
	return out
}

// frameBuilt hands use the Frame table the schema JSON text builds, while its
// builder is alive.
func frameBuilt(text []byte, use func(*wire.Frame)) schemaread.Report {
	var b wire.FrameBuilder
	if !b.Init() {
		return frameStrict(wire.TableReport{}, false)
	}
	defer b.Shutdown()
	var r wire.TableReport
	if !wire.FrameFromJson(&b, text, &r) || !b.Lock() {
		return frameStrict(r, false)
	}
	use(b.AsConst())
	return frameStrict(r, true)
}

// frameRegion is a 64-byte aligned region of n bytes for a table load.
func frameRegion(n int64) []byte {
	raw := make([]byte, n+63)
	off := (-uintptr(unsafe.Pointer(&raw[0]))) & 63
	return raw[off : off+uintptr(n)]
}

// frameLoadWire loads the table's binary image; base64 reaches it decoded.
func frameLoadWire(d *frameDoc, w []byte) schemaread.Report {
	n, err := wire.FrameLoadMeasureReason(w)
	if err != nil || n < 0 {
		return schemaread.Report{Malformed: true}
	}
	var r wire.TableReport
	d.region = frameRegion(n)
	root := wire.FrameLoad(d.region, w, &r)
	if root == nil {
		return frameStrict(r, false)
	}
	d.rec = frameOfRoot(root)
	return frameStrict(r, true)
}

// frameLoadJSON loads the table's schema JSON text.
func frameLoadJSON(d *frameDoc, text []byte) schemaread.Report {
	return frameBuilt(text, func(root *wire.Frame) { d.rec = frameOfRoot(root) })
}

// frameLoadLegacy loads the legacy JSON WriteFrame writes today, exactly as it
// has always been read: a strict decode into the frame. The legacy form is the
// one the current writer emits, and it reads to the same record as the table
// forms (S2, C13). Its stage is a list of strings, where the table's schema
// text writes it as a list of {path} objects.
func frameLoadLegacy(d *frameDoc, in []byte) schemaread.Report {
	if err := json.Unmarshal(in, &d.rec); err != nil {
		return schemaread.Report{Malformed: true}
	}
	return schemaread.Report{}
}

// frameLegacy is true when in is the legacy JSON object: one the legacy
// writer emits, whose kind is the lowercase word work or read (the table
// JSON text writes the enum word Work or Read); an object that is not JSON is
// legacy too, so its damage is named by the table read.
func frameLegacy(in []byte) bool {
	t := bytes.TrimLeft(in, " \t\r\n")
	return len(t) > 0 && t[0] == '{' && !frameTableJSON(t)
}

// frameTableJSON reports whether the object is the schema JSON text of the
// Frame table: its kind is the table's enum word, Work or Read, which the
// legacy writer never emits (it writes work/read). A legacy frame that also
// carries the table's later fields (recipes and the decide answers) is not
// the table form, so it reads as legacy.
func frameTableJSON(t []byte) bool {
	d := json.NewDecoder(bytes.NewReader(t))
	if tok, err := d.Token(); err != nil || tok != json.Delim('{') {
		return false
	}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return false
		}
		k, ok := key.(string)
		if !ok {
			return false
		}
		if k == "kind" {
			var v string
			if err := d.Decode(&v); err != nil {
				return false
			}
			return v == "Work" || v == "Read"
		}
		var skip json.RawMessage
		if err := d.Decode(&skip); err != nil {
			return false
		}
	}
	return false
}

// frameOfRoot is a loaded Frame table as the frame the member hands native.
func frameOfRoot(r *wire.Frame) Frame {
	f := Frame{
		Kind:                  frameKindName(r.Kind),
		Card:                  frameText(&r.Card),
		Attempt:               int(r.Attempt),
		Tier:                  frameText(&r.Tier),
		Model:                 frameText(&r.Model),
		Repo:                  frameText(&r.Repo),
		BaseRef:               frameText(&r.BaseRef),
		StageSha:              frameText(&r.StageSha),
		Branch:                frameText(&r.Branch),
		PrevHead:              frameText(&r.PrevHead),
		PrevFrom:              int(r.PrevAttempt),
		Why:                   frameText(&r.Why),
		Finding:               frameText(&r.Finding),
		Fix:                   frameText(&r.Fix),
		ReviewBase:            frameText(&r.ReviewBase),
		Recipes:               frameText(&r.Recipes),
		DecideBounce:          frameText(&r.DecideBounce),
		DecideReview:          frameText(&r.DecideReview),
		DecideGateFlaky:       frameText(&r.DecideGateFlaky),
		DecideGatePreexisting: frameText(&r.DecideGatePreexisting),
	}
	wire.FrameStageEach(&r.Stage, func(p *wire.StagePath) bool {
		f.Stage = append(f.Stage, frameText(&p.Path))
		return true
	})
	return f
}

// frameText is the text of a table string.
func frameText(ref *wire.TableRef) string {
	v := wire.TableStringAt(ref)
	return string(v.Data[:v.Length])
}

// frameKindName is a FrameKind ordinal as the frame's kind word.
func frameKindName(k wire.FrameKind) string {
	switch k {
	case wire.FrameKindWork:
		return "work"
	case wire.FrameKindRead:
		return "read"
	}
	return ""
}

// ReadFrame reads the frame at path in every form (S2, C13); a frame with no
// kind or no card is refused.
func ReadFrame(path string) (Frame, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Frame{}, err
	}
	d, _, err := frameReads.Read(b)
	if err != nil {
		return Frame{}, fmt.Errorf("the frame %s is invalid: %w", path, err)
	}
	f := d.rec
	if (f.Kind != "work" && f.Kind != "read") || f.Card == "" {
		return Frame{}, fmt.Errorf("the frame %s wants kind work or read and a card, got kind %q card %q", path, f.Kind, f.Card)
	}
	return f, nil
}

// JobName is the frame's text in the job directory, the first thing the child reads.
const JobName = "JOB.md"

// ReadTitle begins the first line of a read's JOB.md in every profile, and of no work's: a
// brief that speaks to its readers names it, so a work card never takes itself for a read
// and stops with nothing to do.
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

// maxRecord is the ceiling a job file read whole into memory carries, the same ceiling
// readRegular gives pkg/swarm's dispatcher files: a finish and a push record are a few
// lines, and a planted file at their names must not set the member's memory.
const maxRecord = 16 * 1024 * 1024

// readRecord is os.ReadFile for a job file the walled child can rewrite (security#66
// finding 1): FinishName and PushedName live in the job directory, the child's first
// --write, and the member reads them outside the wall into the card's result and the pull
// request body. A SYMLINK there would have the member read past the wall what the wall
// exists to keep from it, and a FIFO would park the read with no deadline, so the posture is
// pkg/swarm's readRegular, copied small because swarm imports this package: a path that
// is not a regular file is no record at all, the outcome an absent file already has. The
// Lstat refuses the link and the pipe, the open runs through an os.Root, which follows no
// component however freshly planted, so a link swapped in after the Lstat is refused too,
// the fstat asks the open file the same question, and the size caps the read. The O_NONBLOCK
// readRegular also carries has no portable spelling in this one file; a FIFO that slips in
// between the Lstat and the open could still park it, and the reads happen at the finish,
// when the launch has ended.
func readRecord(job, name string) ([]byte, error) {
	root, err := os.OpenRoot(job)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	notRegular := func(fi fs.FileInfo) error {
		return fmt.Errorf("%s: not a regular file (%s): %w", name, fi.Mode().Type().String(), fs.ErrInvalid)
	}
	fi, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, notRegular(fi)
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if fi, err = f.Stat(); err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, notRegular(fi)
	}
	if fi.Size() > maxRecord {
		return nil, fmt.Errorf("%s: %d bytes passes the %d a job record carries: %w", name, fi.Size(), maxRecord, fs.ErrInvalid)
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxRecord+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > maxRecord {
		return nil, fmt.Errorf("%s: %d bytes passes the %d a job record carries: %w", name, int64(len(raw)), maxRecord, fs.ErrInvalid)
	}
	return raw, nil
}

// IsFinish is whether raw is the finish the gh shim recorded in the job, byte for byte:
// native publishes that record as the card's result when the child wrote no RESULT.md.
func IsFinish(job string, raw []byte) bool {
	b, err := readRecord(job, FinishName)
	return err == nil && string(b) == string(raw)
}

// ReadFinish is the finish the gh shim recorded in the job directory, and whether
// there is one (an empty file is none).
func ReadFinish(job string) (typedrec.CardResult, bool) {
	b, err := readRecord(job, FinishName)
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
	b, err := readRecord(job, PushedName)
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

// ShapeRules is what a job says right after the shape, in the worker's own terms: the
// file opens with `head:` on line 1 and carries all six keys, written as plain `key: value`
// lines. A RESULT.md in another form is refused with the reason `no RESULT.md shape` and the
// attempt is lost, however much work it reports: measured on a worker that wrote a
// `RESULT: <card> sha=<sha>` line and the word DONE (main-report-schema-new-leg-2.w4, the
// head it named the staged commit and not its own), and on one that left out the report
// line. Plain text, no markdown on the keys, nothing before line 1.
const ShapeRules = "Write the file as plain `key: value` lines, exactly as above: line 1 is `head:` (not a `RESULT:` line, no heading, no code fence, no bold or bullets on the keys), all six keys are required (`-` where the line says \"or -\"). A file in any other form (DONE or BLOCKED words, a prose summary) is refused as `no RESULT.md shape` and the attempt is lost, however much work it reports.\n"

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

// ShapeWorkRule is the rule a work card's job adds: the head is the commit the work made.
const ShapeWorkRule = "`head:` is the commit you made on this branch, never the commit the checkout started from.\n"
