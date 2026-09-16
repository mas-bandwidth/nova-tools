package swarm

// A CARD IS A PIPELINE, NOT AN AGENT LOOP (issue #856).
//
// Glenn, 2026-09-16: "The idea is for it to have no memory between calls. The idea is to
// just do work." Measured the same day (#855): 1,068 cards, 62M input tokens and 1,435M
// CACHE-READ tokens -- 23x the input -- because every card ran as an agent loop whose
// growing transcript was re-sent at every tool call. When the card already names the next
// step, the transcript is pure cost.
//
// So a card's STEPs are read as a pipeline:
//
//   - a DETERMINISTIC step is a shell command -- clone, checkout, test, commit -- and the
//     harness runs it itself, in its own shell, inside the fence. No model, no tokens.
//   - a MODEL step is ONE chat completion whose input is exactly the card's preamble, that
//     step's text, and the inputs the step NAMES, and whose output is ONE artifact: a
//     unified diff, a file body, or the RESULT.md text. No tools, no transcript; each call
//     is independent of every other.
//
// This file is the grammar alone -- what a step is, which kind it is, what it demands and
// what it may read. The calls live in pipeline_client.go and the run in pipeline_steps.go.

import (
	"os"
	"path"
	"regexp"
	"strings"
)

// The modes a card may declare. A card with no mode runs the way it runs today.
const (
	ModePipeline = "pipeline"
	ModeExplore  = "explore"
)

// CardModeMarker is the token a card declares its mode with, read from the CONTRACT LINES
// only -- lines 1-3, the same three lines practice 17's word check reads (issue #529). A
// card that QUOTES an issue mentioning a mode deeper down is a card ABOUT one, not one.
const CardModeMarker = "MODE:"

// cardModeLines is how far down the card the mode may be declared.
const cardModeLines = 3

// CardMode returns the mode a card declares -- ModePipeline, ModeExplore, or "" when it
// declares none. An unknown word is no mode: the caller falls back to today's behaviour
// rather than guessing at a typo.
func CardMode(card []byte) string {
	lines := strings.Split(string(card), "\n")
	for i := 0; i < len(lines) && i < cardModeLines; i++ {
		idx := strings.Index(lines[i], CardModeMarker)
		if idx < 0 {
			continue
		}
		word := strings.ToLower(strings.TrimSpace(firstField(lines[i][idx+len(CardModeMarker):])))
		switch word {
		case ModePipeline, ModeExplore:
			return word
		}
	}
	return ""
}

// StepKind is who runs a step: the harness's own shell, or the model.
type StepKind int

const (
	// StepShell is a deterministic step: a shell command the harness runs itself.
	StepShell StepKind = iota
	// StepModel is one chat completion answering with one artifact.
	StepModel
)

func (k StepKind) String() string {
	if k == StepShell {
		return "shell"
	}
	return "model"
}

// ArtifactKind is the ONE thing a model step must answer with, in one fenced block.
type ArtifactKind int

const (
	// ArtifactDiff is a unified diff the harness applies with `git apply` inside ./repo.
	ArtifactDiff ArtifactKind = iota
	// ArtifactFile is the whole body of one named file.
	ArtifactFile
	// ArtifactResult is the RESULT.md text, whose line 1 is the card's contract line.
	ArtifactResult
)

func (a ArtifactKind) String() string {
	switch a {
	case ArtifactFile:
		return "file"
	case ArtifactResult:
		return "result"
	}
	return "diff"
}

// Step is one line of the pipeline.
type Step struct {
	Label    string       // the card's own label: "1", "2", "T", "C"
	Index    int          // 1-based position in the card, which is what a refusal names
	Kind     StepKind     // who runs it
	Text     string       // the step's text, its continuation lines folded in
	Artifact ArtifactKind // what a model step must answer with
	OutPath  string       // for ArtifactFile: the path the body is written to
	Inputs   []string     // the paths (and `path:from-to` ranges) the step NAMES in backticks
}

// PipelineCard is a parsed card: the contract line, the preamble every model call carries,
// and the steps. It is named apart from warm.go's Card, which is a different parse for a
// different caller.
type PipelineCard struct {
	Contract string // line 1, the line a RESULT.md must repeat
	Preamble string // lines 2..first STEP: the role, the rules, the working directory
	Mode     string
	Steps    []Step
}

// ModelSteps is how many calls this card costs when nothing is retried.
func (c PipelineCard) ModelSteps() int {
	n := 0
	for _, s := range c.Steps {
		if s.Kind == StepModel {
			n++
		}
	}
	return n
}

// stepHead matches a step's own first line: `STEP 1.`, `STEP T.`, `STEP C.` -- a number or
// one of the two lettered steps the practice already writes, then a period or a colon.
var stepHead = regexp.MustCompile(`^STEP\s+([0-9]{1,3}|[A-Z])\s*[.:]\s*(.*)$`)

// ParsePipelineCard reads a card into its contract line, its preamble and its steps.
func ParsePipelineCard(raw []byte) (PipelineCard, error) {
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	card := PipelineCard{Mode: CardMode(raw)}
	if len(lines) > 0 {
		card.Contract = strings.TrimRight(lines[0], " \t")
	}
	var preamble []string
	var cur *Step
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		m := stepHead.FindStringSubmatch(strings.TrimLeft(line, " \t"))
		if m == nil {
			if cur == nil {
				preamble = append(preamble, line)
				continue
			}
			// A continuation line belongs to the step above it: a card's quoted issue text
			// is indented under its own STEP and is that step's input, not the next step's.
			cur.Text += "\n" + strings.TrimRight(line, " \t")
			continue
		}
		if cur != nil {
			card.Steps = append(card.Steps, *cur)
		}
		cur = &Step{Label: m[1], Index: len(card.Steps) + 1, Text: strings.TrimRight(m[2], " \t")}
	}
	if cur != nil {
		card.Steps = append(card.Steps, *cur)
	}
	if len(card.Steps) == 0 {
		return card, errNoSteps
	}
	card.Preamble = strings.TrimSpace(strings.Join(preamble, "\n"))
	for i := range card.Steps {
		classifyStep(&card.Steps[i])
	}
	return card, nil
}

// errNoSteps is the one shape refusal this grammar makes: a card with no STEP line is not a
// pipeline, and guessing one out of its prose is exactly the exploration this ends.
var errNoSteps = &cardError{"the card names no STEP line, so it has no pipeline to run"}

type cardError struct{ msg string }

func (e *cardError) Error() string { return e.msg }

// Step markers a card may write to settle a step's kind or its artifact by hand, rather
// than leaving it to the word test below. They are the escape hatch for a shell command
// this list has never seen and for a prose step whose first word happens to be a command.
const (
	stepShellMarker = "SHELL:"
	stepModelMarker = "MODEL:"
	stepOutMarker   = "OUT:"
)

// shellFirstWords is the word test: a step whose first word is one of these is a command
// this harness runs itself. It is a LIST, not a heuristic over the whole line -- a step that
// begins `git`, `go` or `mkdir` is a command, and a step that begins "Write", "Read" or
// "Implement" is work for the model. The list holds the words the cards on this bench
// actually begin with; anything else a card can settle with `SHELL:`.
var shellFirstWords = map[string]bool{
	"mkdir": true, "cd": true, "git": true, "go": true, "gofmt": true, "set": true,
	"export": true, "sh": true, "bash": true, "make": true, "npm": true, "cargo": true,
	"python": true, "python3": true, "cat": true, "ls": true, "cp": true, "mv": true,
	"rm": true, "echo": true, "env": true, "true": true, "test": true, "grep": true,
	"sed": true, "awk": true, "tail": true, "head": true, "sort": true, "wc": true,
	"chmod": true, "touch": true, "diff": true, "find": true, "xargs": true, "tar": true,
	"{": true, "(": true, "./": true, "!": true,
}

// lettered steps whose kind is settled by the label alone: T is the test run and C is the
// commit, and both are the harness's own work by the practice that writes them.
var shellLabels = map[string]bool{"T": true, "C": true}

// classifyStep settles a step's kind, the artifact it demands and the inputs it names.
func classifyStep(s *Step) {
	text := strings.TrimSpace(s.Text)
	switch {
	case strings.HasPrefix(text, stepShellMarker):
		s.Kind = StepShell
		s.Text = strings.TrimSpace(strings.TrimPrefix(text, stepShellMarker))
		return
	case strings.HasPrefix(text, stepModelMarker):
		s.Kind = StepModel
		s.Text = strings.TrimSpace(strings.TrimPrefix(text, stepModelMarker))
	case shellLabels[s.Label]:
		s.Kind = StepShell
		return
	case shellFirstWords[firstField(text)] || strings.HasPrefix(text, "./"):
		s.Kind = StepShell
		return
	default:
		s.Kind = StepModel
	}
	s.Artifact, s.OutPath = demandedArtifact(s.Text)
	s.Inputs = namedInputs(s.Text)
}

// demandedArtifact reads what a model step must answer with. A step may say so itself --
// `OUT: diff`, `OUT: result`, `OUT: file <path>` -- and otherwise a step that names
// RESULT.md writes the result and every other step answers with a diff, which is the shape
// of every fix card on this bench: a patch the harness applies and then tests.
func demandedArtifact(text string) (ArtifactKind, string) {
	if idx := strings.Index(text, stepOutMarker); idx >= 0 {
		rest := strings.TrimSpace(text[idx+len(stepOutMarker):])
		fields := strings.Fields(rest)
		if len(fields) > 0 {
			switch strings.ToLower(fields[0]) {
			case "diff", "patch":
				return ArtifactDiff, ""
			case "result":
				return ArtifactResult, ""
			case "file":
				if len(fields) > 1 {
					return ArtifactFile, strings.Trim(fields[1], "`\"',;")
				}
			}
		}
	}
	if strings.Contains(text, ResultFile) {
		return ArtifactResult, ""
	}
	return ArtifactDiff, ""
}

// ResultFile is the one file a card publishes.
const ResultFile = "RESULT.md"

// inputToken matches a backticked token: the way a card names the file, or the line range,
// a step is to be given. Everything a model step reads is named here; nothing is searched
// for, because searching is the loop this ends.
var inputToken = regexp.MustCompile("`([^`\n]{1,200})`")

// namedInputs are the paths a step names in backticks. A token with no path separator and
// no dot is prose in backticks (`git apply`, `--count=1`) and is not asked for.
func namedInputs(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range inputToken.FindAllStringSubmatch(text, -1) {
		tok := strings.Trim(strings.TrimSpace(m[1]), ",;")
		if tok == "" || strings.ContainsAny(tok, " \t") {
			continue
		}
		if !strings.Contains(tok, "/") && !strings.Contains(path.Base(tok), ".") {
			continue
		}
		if seen[tok] {
			continue
		}
		seen[tok] = true
		out = append(out, tok)
	}
	return out
}

func firstField(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return ""
}

// ---------------------------------------------------------------- the diff's own paths

// diffPathLine are the lines of a unified diff that NAME a path.
var (
	diffGitLine    = regexp.MustCompile(`^diff --git (?:a/)?(\S+) (?:b/)?(\S+)`)
	diffMinusLine  = regexp.MustCompile(`^--- (?:a/)?(\S+)`)
	diffPlusLine   = regexp.MustCompile(`^\+\+\+ (?:b/)?(\S+)`)
	diffRenameLine = regexp.MustCompile(`^rename (?:from|to) (\S+)`)
)

// DiffPaths is every path a unified diff names, in the order it names them, with the
// a/ and b/ prefixes taken off and /dev/null dropped: /dev/null is the other side of an
// added or a deleted file by construction, never a path anything is written to.
func DiffPaths(diff []byte) []string {
	var out []string
	for _, line := range strings.Split(string(diff), "\n") {
		var found []string
		switch {
		case strings.HasPrefix(line, "diff --git "):
			if m := diffGitLine.FindStringSubmatch(line); m != nil {
				found = []string{m[1], m[2]}
			}
		case strings.HasPrefix(line, "--- "):
			if m := diffMinusLine.FindStringSubmatch(line); m != nil {
				found = []string{m[1]}
			}
		case strings.HasPrefix(line, "+++ "):
			if m := diffPlusLine.FindStringSubmatch(line); m != nil {
				found = []string{m[1]}
			}
		case strings.HasPrefix(line, "rename "):
			if m := diffRenameLine.FindStringSubmatch(line); m != nil {
				found = []string{m[1]}
			}
		}
		for _, p := range found {
			if p == "" || p == os.DevNull || p == "/dev/null" {
				continue
			}
			out = append(out, p)
		}
	}
	return out
}

// DiffOutside names the FIRST path a diff touches that is not inside the directory the
// diff is applied in, and whether it found one. The rule is the path's own shape, asked
// before `git apply` is ever started: an absolute path, a path with a `..` element, or one
// beginning `~` leaves the repository, and the harness never hands such a diff to git.
// This is the containment a card's own words cannot buy: the model answers with text, and
// text that names /etc/shadow is refused by the machinery, not by the prompt.
func DiffOutside(diff []byte) (string, bool) {
	for _, p := range DiffPaths(diff) {
		if outsideRelative(p) {
			return p, true
		}
	}
	return "", false
}

// outsideRelative reports whether one path leaves the directory it is relative to.
func outsideRelative(p string) bool {
	if p == "" {
		return true
	}
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~") || strings.HasPrefix(p, `\`) {
		return true
	}
	if len(p) > 1 && p[1] == ':' { // a windows drive letter is absolute wherever it is read
		return true
	}
	for _, el := range strings.Split(path.Clean(strings.ReplaceAll(p, `\`, "/")), "/") {
		if el == ".." {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- the harness's own turns

// harnessTurnSigils are the marks the harness prints in column 0, one per TOOL CALL: the
// shell's `$`, the read arrow, the write arrow and the search mark. One
// such line is one turn, which is the unit #855 measured the bill in -- a 45k-token read
// over 30 turns bills 1.35M cache-read tokens.
//
// THE SIGIL IS IN COLUMN 0, under the colour codes and nothing else. The tools' own output
// is indented or unmarked, and a source file the harness printed holds `#` comments at the
// head of an indented line; counting those counted a card's own evidence as its turns.
var harnessTurnSigils = []string{"$", "→", "←", "✱"}

// ansiEscape is the colour the harness writes around every mark.
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

// HarnessTurns counts the tool calls in a harness capture.
func HarnessTurns(raw []byte) int {
	n := 0
	for _, line := range strings.Split(string(raw), "\n") {
		bare := ansiEscape.ReplaceAllString(line, "")
		if bare == "" || bare[0] == ' ' || bare[0] == '\t' {
			continue
		}
		for _, sigil := range harnessTurnSigils {
			if strings.HasPrefix(bare, sigil+" ") && strings.TrimSpace(bare[len(sigil):]) != "" {
				n++
				break
			}
		}
	}
	return n
}

// HarnessTurnsInFile counts the tool calls in a capture on disk. An absent or unreadable
// capture is zero turns, never an error: the budget is a ceiling, and a log nobody can read
// never trips it.
func HarnessTurnsInFile(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return HarnessTurns(raw)
}
