package pulse

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// textKinds are the three text-only templates: read, text and tone. Their cards go to the
// flash route and must carry the no-build line (SPEC-PULSE rule 6).
var textKinds = map[string]bool{"read": true, "text": true, "tone": true}

// SlotDash is the cards.tsv slot every cut card carries until launch allocates one.
const SlotDash = "-"

// CutInput is everything the cut verb needs, held apart from flag parsing so a test can
// drive it with a fake templates directory and a fake pool.tsv.
type CutInput struct {
	Pool      string // path to pool.tsv: source, id, kind, title, template per line
	Templates string // the directory holding read.md fix.md text.md replay.md drift.md tone.md and models.tsv
	Out       string // the directory the card files go into
	Root      string // the state root; skipped.tsv is written here
	Local     string // an optional ollama tag that overrides the flash model for read and text cards
	Max       int    // per-kind cap on skipped lines; 0 prints all
	Stdout    io.Writer
	Stderr    io.Writer
}

// Cut writes one card per pool.tsv candidate from its typed template and returns the exit
// code: 0 when every candidate was cut, 1 when any candidate was skipped, 2 when the pool,
// the templates or the models could not be read.
func Cut(in CutInput) int {
	pool, err := readPool(in.Pool)
	if err != nil {
		fmt.Fprintf(in.Stderr, "CUT REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	models, err := readModels(filepath.Join(in.Templates, "models.tsv"))
	if err != nil {
		fmt.Fprintf(in.Stderr, "CUT REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	if err := os.MkdirAll(in.Out, 0o755); err != nil {
		fmt.Fprintf(in.Stderr, "CUT REFUSED: --out %s: %s (pass a directory cut may create)\n", oneline.Field(in.Out), oneline.Err(err))
		return 2
	}

	flash, pro, skipped := 0, 0, 0
	skipList := bounded.Capped(in.Stderr, in.Max, "CUT", "skipped", "use --max 0 to show all")
	var cards []CardRow

	for _, row := range pool {
		name := row.Template
		raw, err := os.ReadFile(filepath.Join(in.Templates, name+".md"))
		if err != nil {
			skipped++
			skipList.Line(fmt.Sprintf("CUT SKIPPED source=%s id=%s template=%s: no template", oneline.Field(row.Source), oneline.Field(row.ID), oneline.Field(name)))
			continue
		}
		card, reason := renderCard(string(raw), row)
		if reason != "" {
			fmt.Fprintf(in.Stderr, "CUT REFUSED template=%s: %s\n", oneline.Field(name), oneline.Escape(reason))
			return 2
		}
		cardName := row.ID + ".md"
		if err := os.WriteFile(filepath.Join(in.Out, cardName), []byte(card), 0o644); err != nil {
			fmt.Fprintf(in.Stderr, "CUT REFUSED: card %s: %s\n", oneline.Field(cardName), oneline.Err(err))
			return 2
		}
		model := modelFor(row.Kind, in.Local, models)
		if isFlashKind(row.Kind) {
			flash++
		} else {
			pro++
		}
		cards = append(cards, CardRow{Label: row.ID, Slot: SlotDash, Model: model, Card: filepath.Join(in.Out, cardName)})
	}
	skipList.More()

	if err := writeCardsTSV(filepath.Join(in.Out, "cards.tsv"), cards); err != nil {
		fmt.Fprintf(in.Stderr, "CUT REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	if skipped > 0 {
		if err := os.MkdirAll(in.Root, 0o755); err != nil {
			fmt.Fprintf(in.Stderr, "CUT REFUSED: --root %s: %s\n", oneline.Field(in.Root), oneline.Err(err))
			return 2
		}
		if err := writeSkippedTSV(filepath.Join(in.Root, "skipped.tsv"), pool, in.Templates); err != nil {
			fmt.Fprintf(in.Stderr, "CUT REFUSED: %s\n", oneline.Err(err))
			return 2
		}
	}
	fmt.Fprintf(in.Stdout, "CUT OK cards=%d skipped=%d flash=%d pro=%d out=%s\n", len(cards), skipped, flash, pro, oneline.Field(in.Out))
	if skipped > 0 {
		return 1
	}
	return 0
}

func isFlashKind(kind string) bool { return textKinds[kind] }

// modelFor decides the model by kind and nowhere else: read/text/tone -> flash, the rest ->
// pro, with --local naming an ollama/<tag> override for read and text (SPEC-PULSE rule 7).
func modelFor(kind, local string, models map[string]string) string {
	if local != "" && (kind == "read" || kind == "text") {
		return "ollama/" + local
	}
	if isFlashKind(kind) {
		return models["flash"]
	}
	return models["pro"]
}

// readPool reads pool.tsv: five fields per line, blank lines skipped.
func readPool(path string) ([]PoolRow, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("--pool wants a readable pool.tsv: %s", oneline.Err(err))
	}
	var rows []PoolRow
	for i, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 5 {
			return nil, fmt.Errorf("--pool line %d wants source, id, kind, title, template, got %d fields", i+1, len(parts))
		}
		rows = append(rows, PoolRow{Source: parts[0], ID: parts[1], Kind: parts[2], Title: parts[3], Template: parts[4]})
	}
	return rows, nil
}

// readModels reads models.tsv: two lines, `flash <model>` and `pro <model>`.
func readModels(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("--templates wants a models.tsv: two lines, `flash <model id>` then `pro <model id>`: %s", oneline.Err(err))
	}
	models := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 2 {
			return nil, fmt.Errorf("models.tsv line wants `flash <id>` or `pro <id>`, got %q", line)
		}
		models[parts[0]] = parts[1]
	}
	if models["flash"] == "" || models["pro"] == "" {
		return nil, fmt.Errorf("models.tsv wants both a flash and a pro model id")
	}
	return models, nil
}

// renderCard renders one candidate's card from its template and returns the card text, or a
// reason to refuse it. The template is the card body with placeholders <label>, <source>,
// <id>, <kind>, <title> and <branch>. cut computes the sha-12 over the rendered body below
// line 1 and rewrites line 1, so the contract always binds the card it heads.
func renderCard(tmpl string, row PoolRow) (string, string) {
	rendered := strings.NewReplacer(
		"<label>", row.ID,
		"<source>", row.Source,
		"<id>", row.ID,
		"<kind>", row.Kind,
		"<title>", row.Title,
		"<branch>", branchOf(row),
	).Replace(tmpl)
	lines := strings.Split(rendered, "\n")
	line1 := strings.TrimSpace(lines[0])
	if !strings.HasPrefix(line1, "RESULT ") || !strings.Contains(line1, "sha=") {
		return "", "rule 5: line 1 is not the RESULT contract (line 1 must be `RESULT <label> sha=<sha12>`)"
	}
	if strings.Contains(rendered, "../scratch") {
		return "", "rule 5: the card mentions ../scratch (scratch notes live in the repo directory as notes.txt)"
	}
	step1, ok := stepOne(lines)
	if !ok {
		return "", "rule 5: no STEP 1 line (a card wants STEP 1 as the mkdir/clone/checkout)"
	}
	for _, want := range []string{"mkdir -p scratch", "checkout -b"} {
		if !strings.Contains(step1, want) {
			return "", fmt.Sprintf("rule 5: STEP 1 lacks %q (STEP 1 must carry mkdir -p scratch and checkout -b)", want)
		}
	}
	// STEP 1 sets no TMPDIR of its own (#460): the runner exports TMPDIR=<slot>/tmp/<label> —
	// the slot directory is never a repo, while admission git-inits the job directory — and a
	// card that exports its own puts its temp dir inside the job's repo, the red cards 247,
	// 266 and 353 reported and did not cause.
	if strings.Contains(step1, "TMPDIR") {
		return "", "rule 5: STEP 1 sets TMPDIR (the runner exports TMPDIR outside every repo; a card sets none of its own)"
	}
	if strings.Contains(step1, "git@") || !strings.Contains(step1, "https://") {
		return "", "rule 5: STEP 1 does not clone over https (the clone URL is https, never git@)"
	}
	if textKinds[row.Kind] {
		if !strings.Contains(strings.ToLower(rendered), "do not run go build") {
			return "", "rule 6: the text template lacks the no-build line (read, text and tone cards must state `Do not run go build, go test or any toolchain`)"
		}
	} else {
		if !strings.Contains(strings.ToLower(rendered), "red line") || !strings.Contains(strings.ToLower(rendered), "green line") {
			return "", "rule 6: the writing template lacks the red-then-green row rule (fix, replay and drift cards must name the red line and the green line)"
		}
	}
	body := strings.Join(lines[1:], "\n")
	sum := sha256.Sum256([]byte(body))
	lines[0] = fmt.Sprintf("RESULT %s sha=%s", row.ID, hex.EncodeToString(sum[:])[:12])
	return strings.Join(lines, "\n"), ""
}

// stepOne returns the STEP 1 line, or false when there is none in the first 20 lines.
func stepOne(lines []string) (string, bool) {
	for i := 0; i < len(lines) && i < 20; i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "STEP 1") {
			return strings.TrimSpace(lines[i]), true
		}
	}
	return "", false
}

func branchOf(row PoolRow) string {
	return "rowan/" + strings.ReplaceAll(row.ID, " ", "-")
}

func writeCardsTSV(path string, cards []CardRow) error {
	var b strings.Builder
	for _, c := range cards {
		slot := c.Slot
		if slot == "" {
			slot = SlotDash
		}
		b.WriteString(fmt.Sprintf("%s\t%s\t%s\t%s\n", c.Label, slot, c.Model, c.Card))
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func writeSkippedTSV(path string, pool []PoolRow, templates string) error {
	var b strings.Builder
	for _, row := range pool {
		if _, err := os.Stat(filepath.Join(templates, row.Template+".md")); err != nil {
			b.WriteString(fmt.Sprintf("%s\t%s\n", row.Source, row.ID))
		}
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
