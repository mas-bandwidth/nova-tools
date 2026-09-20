package pulse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// textKinds are the three text-only templates: read, text and tone. Their cards carry the
// no-build line and are routed to any model that can hold them (SPEC-PULSE rule 6 and 7).
var textKinds = map[string]bool{"read": true, "text": true, "tone": true}

// SlotDash is the cards.tsv slot every cut card carries until launch allocates one.
const SlotDash = "-"

// CutInput is everything the cut verb needs, held apart from flag parsing so a test can
// drive it with a fake templates directory and a fake pool.tsv.
type CutInput struct {
	Pool      string // path to pool.tsv: source, id, kind, title, template per line
	Templates string // the directory holding read.md fix.md text.md replay.md drift.md tone.md and benches.tsv (or routes.tsv, or a models.tsv fallback)
	Out       string // the directory the card files go into
	Root      string // the state root; skipped.tsv and retry.tsv are read and written here
	Local     string // an optional ollama tag that overrides the flash model for read and text cards
	Max       int    // cap on the cards cut, and on the skipped lines printed; 0 means no cap
	Probe     bool   // run the learned admission checklist before writing each card (#585)
	History   string // the abstain history the checklist is cut from, when Probe is set
	Budget    int    // the card's byte budget, when Probe is set; 0 means unbounded
	Stdout    io.Writer
	Stderr    io.Writer
	// ValidateContract preflights every candidate's locator before any card file is
	// written: a locator that does not resolve (gh repo view non-zero) is refused with
	// the reason and no card is written, so a dead repo never spends admission plus the
	// scaffold before an abstain (issue #675).
	ValidateContract bool
}

// Cut writes one card per pool.tsv candidate from its typed template and returns the exit
// code: 0 when every candidate was cut, 1 when any candidate was skipped, 2 when the pool,
// the templates or the routing table could not be read. Routing is the cost table
// (benches.tsv, or routes.tsv beside it) when it is there; a templates directory with only
// a models.tsv falls back to the two-line flash/pro table cut wrote before the cost table
// landed (issue #635), so a spend rule that holds every card to one model still works.
func Cut(in CutInput) int {
	pool, err := readPool(in.Pool)
	if err != nil {
		fmt.Fprintf(in.Stderr, "CUT REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	table, tableErr := readCostTable(in.Templates)
	var models map[string]string
	if tableErr != nil {
		models, err = readModels(filepath.Join(in.Templates, "models.tsv"))
		if err != nil {
			fmt.Fprintf(in.Stderr, "CUT REFUSED: %s\n", oneline.Err(tableErr))
			return 2
		}
	}
	if err := os.MkdirAll(in.Out, 0o755); err != nil {
		fmt.Fprintf(in.Stderr, "CUT REFUSED: --out %s: %s (pass a directory cut may create)\n", oneline.Field(in.Out), oneline.Err(err))
		return 2
	}
	retries := readRetries(in.Root)

	zero, flat, metered, flash, pro, skipped, probed := 0, 0, 0, 0, 0, 0, 0
	skipList := bounded.Capped(in.Stderr, in.Max, "CUT", "skipped", "use --max 0 to show all")
	var cards []CardRow

	if in.ValidateContract {
		seen := map[string]bool{}
		for _, row := range pool {
			if seen[row.Source] {
				continue
			}
			seen[row.Source] = true
			if reason := locatorUnresolvable(row.Source); reason != "" {
				fmt.Fprintf(in.Stderr, "CUT REFUSED locator=%s: %s (check gh auth and the repo name)\n", oneline.Field(row.Source), oneline.Escape(reason))
				return 2
			}
		}
	}

	for _, row := range pool {
		if in.Max > 0 && len(cards) >= in.Max {
			break
		}
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
		if in.Probe {
			if code := Probe(ProbeInput{Label: row.ID, Card: card, History: in.History, Budget: in.Budget, Stdout: in.Stdout, Stderr: in.Stderr}); code != 0 {
				probed++
				continue
			}
		}
		cardName := row.ID + ".md"
		if err := os.WriteFile(filepath.Join(in.Out, cardName), []byte(card), 0o644); err != nil {
			fmt.Fprintf(in.Stderr, "CUT REFUSED: card %s: %s\n", oneline.Field(cardName), oneline.Err(err))
			return 2
		}
		var model string
		if tableErr == nil {
			route := routeFor(row.Kind, retries[row.ID], table)
			switch route.Class {
			case "zero":
				zero++
			case "flat":
				flat++
			case "metered":
				metered++
			}
			fmt.Fprintf(in.Stdout, "CUT ROUTE route=%s reason=%s\n", oneline.Field(route.Name), oneline.Field(route.Class))
			model = route.Name
		} else {
			model = modelFor(row.Kind, in.Local, models)
			if isProRoute(row.Kind, in.Local, models) {
				pro++
			} else {
				flash++
			}
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
	probeField := ""
	if in.Probe {
		probeField = fmt.Sprintf(" probe=%d", probed)
	}
	if tableErr == nil {
		fmt.Fprintf(in.Stdout, "CUT OK cards=%d skipped=%d zero=%d flat=%d metered=%d out=%s%s\n", len(cards), skipped, zero, flat, metered, oneline.Field(in.Out), probeField)
	} else {
		fmt.Fprintf(in.Stdout, "CUT OK cards=%d skipped=%d flash=%d pro=%d out=%s%s\n", len(cards), skipped, flash, pro, oneline.Field(in.Out), probeField)
	}
	if skipped > 0 || probed > 0 {
		return 1
	}
	return 0
}

func isFlashKind(kind string) bool { return textKinds[kind] }

// locatorUnresolvable returns a non-empty reason when the candidate's locator (owner/repo)
// does not resolve through gh repo view, else "". A locator that does not resolve is a card
// that would clone nothing and abstain after the scaffold, so cut refuses it up front.
func locatorUnresolvable(locator string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "repo", "view", locator)
	if _, err := cmd.Output(); err != nil {
		return "does not resolve"
	}
	return ""
}

// modelFor decides the model by kind and nowhere else: read/text/tone -> flash, the rest ->
// pro, with --local naming an ollama/<tag> override for read and text (SPEC-PULSE rule 7).
// A models.tsv may name only one of the two ids; the cards that would take the missing model
// hold to the one the table names, so `flash <id>` alone is the spend rule "flash only".
func modelFor(kind, local string, models map[string]string) string {
	if local != "" && (kind == "read" || kind == "text") {
		return "ollama/" + local
	}
	if isFlashKind(kind) {
		if models["flash"] != "" {
			return models["flash"]
		}
		return models["pro"]
	}
	if models["pro"] != "" {
		return models["pro"]
	}
	return models["flash"]
}

// isProRoute says whether a card's route is the pro one, keyed the same way modelFor picks:
// when the table names only one model every card rides it, and --local rides the flash route.
func isProRoute(kind, local string, models map[string]string) bool {
	if local != "" && (kind == "read" || kind == "text") {
		return false
	}
	if isFlashKind(kind) {
		return models["flash"] == "" && models["pro"] != ""
	}
	return models["pro"] != "" || models["flash"] == ""
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

// readModels reads models.tsv: one line `flash <model>` and/or one line `pro <model>`. A
// table that names only one is legal and holds every card to that model (issue #635). It is
// the fallback when no cost table sits beside the templates.
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
	if models["flash"] == "" && models["pro"] == "" {
		return nil, fmt.Errorf("models.tsv wants a flash or a pro model id (name one to hold every card to that model, or both to route read to flash and fix to pro)")
	}
	return models, nil
}

// costModel is one model column of the cost table: the model id, its cost class and its
// usd per Mtok, and the capability classes it can hold.
type costModel struct {
	Name  string
	Class string // zero|flat|metered
	USD   float64
	Caps  []string
}

// costOrder ranks the three cost classes: zero beats flat beats metered, ties on the class
// broken by the usd per Mtok (SPEC-PULSE rule 7).
var costOrder = map[string]int{"zero": 0, "flat": 1, "metered": 2}

// capOrder ranks the four capability classes; a retry moves the pick one class up this
// ladder (read -> text -> code -> replay).
var capOrder = map[string]int{"read": 0, "text": 1, "code": 2, "replay": 3}

// requiredCaps is what each card kind needs a route to cover: read, text and tone any
// reading-capable model, fix and drift a code model, replay a replay model.
var requiredCaps = map[string][]string{
	"read":   {"read", "text", "replay"},
	"text":   {"read", "text", "replay"},
	"tone":   {"read", "text", "replay"},
	"fix":    {"code"},
	"drift":  {"code"},
	"replay": {"replay"},
}

// fallbackRoutes is the two-tier routing a relaunch uses when the cost table is absent:
// the same flash/pro split cut wrote before the table landed (rule 7, the table is the
// whole policy and the relaunch uses it when it is there).
var fallbackRoutes = []costModel{
	{Name: "flash", Class: "flat", Caps: []string{"read", "text", "replay"}},
	{Name: "pro", Class: "flat", Caps: []string{"code"}},
}

// readCostTable reads the cost table -- benches.tsv beside the templates, or a routes.tsv
// beside it -- one column per model: a `model` row of names, a `cost` row of a class
// `zero|flat|metered` with a `usd per Mtok`, and a `capability` row of `read|text|code|replay`.
func readCostTable(dir string) ([]costModel, error) {
	path := filepath.Join(dir, "benches.tsv")
	if _, err := os.Stat(path); err != nil {
		path = filepath.Join(dir, "routes.tsv")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("--templates wants a benches.tsv (or a routes.tsv beside it): one column per model -- a `model` row of names, a `cost` row of a class `zero|flat|metered` with a `usd per Mtok`, and a `capability` row of `read|text|code|replay`: %s", oneline.Err(err))
	}
	var names []string
	var costs []string
	var caps []string
	for i, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		switch parts[0] {
		case "model":
			names = parts[1:]
		case "cost":
			costs = parts[1:]
		case "capability":
			caps = parts[1:]
		default:
			return nil, fmt.Errorf("cost table line %d names a row %q, want `model`, `cost` or `capability`", i+1, parts[0])
		}
	}
	if len(names) == 0 || len(names) != len(costs) || len(costs) != len(caps) {
		return nil, fmt.Errorf("cost table wants one column per model: the `model`, `cost` and `capability` rows must carry the same number of columns")
	}
	table := make([]costModel, 0, len(names))
	for i, name := range names {
		cf := strings.Fields(strings.TrimSpace(costs[i]))
		if len(cf) != 2 {
			return nil, fmt.Errorf("cost table model %s wants a cost of `<zero|flat|metered> <usd per Mtok>`, got %q", name, costs[i])
		}
		if _, ok := costOrder[cf[0]]; !ok {
			return nil, fmt.Errorf("cost table model %s has a cost class %q, want zero, flat or metered", name, cf[0])
		}
		usd, err := strconv.ParseFloat(cf[1], 64)
		if err != nil {
			return nil, fmt.Errorf("cost table model %s has a non-numeric usd per Mtok %q", name, cf[1])
		}
		var modelCaps []string
		for _, c := range strings.Split(strings.TrimSpace(caps[i]), "|") {
			c = strings.TrimSpace(c)
			if _, ok := capOrder[c]; !ok {
				return nil, fmt.Errorf("cost table model %s has a capability %q, want read, text, code or replay", name, c)
			}
			modelCaps = append(modelCaps, c)
		}
		table = append(table, costModel{Name: strings.TrimSpace(name), Class: cf[0], USD: usd, Caps: modelCaps})
	}
	return table, nil
}

// readRetries returns the set of labels retry.tsv names: cards rewritten after a second
// abstain (rule 14), which route one capability class up.
func readRetries(root string) map[string]bool {
	retries := map[string]bool{}
	raw, err := os.ReadFile(filepath.Join(root, "retry.tsv"))
	if err != nil {
		return retries
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if parts := strings.Split(line, "\t"); parts[0] != "" {
			retries[parts[0]] = true
		}
	}
	return retries
}

// routeFor picks the cheapest capable route for a card kind from the cost table: the
// capable model with the lowest average cost per token -- zero beats flat beats metered,
// ties broken by the usd per Mtok (rule 7). A retry after an abstain moves the pick one
// capability class up, so a rewritten card routes to a stronger model.
func routeFor(kind string, retry bool, table []costModel) costModel {
	capable := func(m costModel) bool {
		for _, c := range m.Caps {
			for _, need := range requiredCaps[kind] {
				if c == need {
					return true
				}
			}
		}
		return false
	}
	better := func(a, b costModel) bool {
		if costOrder[a.Class] != costOrder[b.Class] {
			return costOrder[a.Class] < costOrder[b.Class]
		}
		return a.USD < b.USD
	}
	maxCap := func(m costModel) int {
		best := -1
		for _, c := range m.Caps {
			if capOrder[c] > best {
				best = capOrder[c]
			}
		}
		return best
	}

	var pick costModel
	found := false
	for _, m := range table {
		if !capable(m) {
			continue
		}
		if !found || better(m, pick) {
			pick, found = m, true
		}
	}
	if !found {
		// No model is capable of this kind: the table does not cover it. Pick the cheapest
		// model rather than refusing, so a table that is simply missing a class never
		// stalls the pulse (the capability rule is the table's, and the table is editable
		// in git).
		for _, m := range table {
			if !found || better(m, pick) {
				pick, found = m, true
			}
		}
	}
	if retry && found {
		var r costModel
		rf := false
		for _, m := range table {
			if !capable(m) || maxCap(m) <= maxCap(pick) {
				continue
			}
			if !rf || better(m, r) {
				r, rf = m, true
			}
		}
		if rf {
			return r
		}
	}
	return pick
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
	if steps := countSteps(lines); steps > turnBudget(row.Kind) {
		return "", fmt.Sprintf("the card has %d steps, over the %d-turn budget (#855; the step count is the turn budget: name the exact file and line range, one check per step)", steps, turnBudget(row.Kind))
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

// countSteps counts the numbered STEP lines a card carries. Every tool call in a card
// re-sends the whole context, so the step count is the turn budget and the budget is the
// bill (#855: 1,434.6M cache-read tokens against 62.1M input).
func countSteps(lines []string) int {
	n := 0
	for _, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "STEP ") {
			n++
		}
	}
	return n
}

// turnBudget is the step count each kind may spend: the read family (read, text, tone) gets
// 8 turns, the writing family (fix, replay, drift) 20 (#855).
func turnBudget(kind string) int {
	if textKinds[kind] {
		return 8
	}
	return 20
}

// branchOf is where a card's branch NAME comes from, and DefaultBranchPrefix is the one
// spelling of the prefix -- the same constant harvest refuses a push outside of (Stella's
// ruling on #1824) and a bench harvest filters by. It was a second literal "rowan/" here,
// which is exactly how a generator and its gate drift apart.
func branchOf(row PoolRow) string {
	return DefaultBranchPrefix + strings.ReplaceAll(row.ID, " ", "-")
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
