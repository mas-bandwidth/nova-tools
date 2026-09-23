package pulse

// Red for nova-tools #2215: "Add the kinds table and the first-card rule (SPEC-TOOLWORK
// §5 rules 2-5)". On the base there is no internal/pulse/kinds.go and no first-card
// refusal in launch, so every leg below is red; at head they are green; and with the
// production change reverted — kinds.go deleted, launch.go restored — this file still
// COMPILES and goes red again, which is the control the card demands. That is why the
// kinds-table leg reads kinds.go's own text instead of calling its code: a test that
// referenced the table's symbols would stop compiling on the revert, and a control that
// only fails to compile proves nothing. Reading the repository's own text is the class
// test's own trade (AGENTS.md, "A class test reads this repository's own text"), and the
// launch legs drive Launch itself, whose signature the change never touches.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	// The alias: package pulse already holds a `hygiene` type (hygiene.go), so the name
	// set's package comes in under the name of the thing it holds.
	kinds "github.com/mas-bandwidth/nova-tools/internal/hygiene"
)

// issue2215Now is the fixed clock every launch below runs on, so a pulse id is a name the
// test can read and no leg depends on the wall.
func issue2215Now() time.Time {
	return time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
}

// issue2215Row is one row of the kinds table as this test reads it: the kind's name and
// the five cells SPEC-TOOLWORK.md §5 rule 2's table holds for it.
type issue2215Row struct {
	name, does, paths, gate, control, tokens string
}

// parseKindTable reads the kindRules rows out of kinds.go's own text, one struct literal
// per kind with the fields in the spec's column order. Zero rows is a failure, not a
// pass: a parser that matched nothing must never hold a table green.
func parseKindTable(t *testing.T, src string) []issue2215Row {
	t.Helper()
	re := regexp.MustCompile(`(?s)\{\s*Name:\s*"([^"]*)",\s*Does:\s*"([^"]*)",\s*Paths:\s*"([^"]*)",\s*Gate:\s*"([^"]*)",\s*Control:\s*"([^"]*)",\s*Tokens:\s*"([^"]*)",?\s*\}`)
	matches := re.FindAllStringSubmatch(src, -1)
	if len(matches) == 0 {
		t.Fatalf("kinds.go holds no kindRules rows this parser can read (the table is data: one literal per kind, fields in the spec's column order)")
	}
	rows := make([]issue2215Row, 0, len(matches))
	for _, m := range matches {
		rows = append(rows, issue2215Row{m[1], m[2], m[3], m[4], m[5], m[6]})
	}
	return rows
}

// specKindTable reads §5 rule 2's table out of the spec's own text — the receipt the
// issue quotes — with the read family's one row expanded to the five names it holds, so
// the comparison is one row per declared kind on both sides.
func specKindTable(t *testing.T) []issue2215Row {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-TOOLWORK.md"))
	if err != nil {
		t.Fatalf("docs/SPEC-TOOLWORK.md: %v (the quoted lines are this issue's receipt)", err)
	}
	text := string(raw)
	start := strings.Index(text, "2. **The kinds.**")
	end := strings.Index(text, "3. **A kind's gate is declared")
	if start < 0 || end < 0 || end <= start {
		t.Fatal("could not find §5 rule 2's table in the spec (between \"2. **The kinds.**\" and \"3. **A kind's gate is declared\")")
	}
	var rows []issue2215Row
	for _, line := range strings.Split(text[start:end], "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") {
			continue
		}
		cells := strings.Split(trimmed, "|")
		if len(cells) != 8 {
			t.Fatalf("a §5 rule 2 row does not hold six cells: %q", trimmed)
		}
		vals := [6]string{}
		for i := range vals {
			vals[i] = issue2215NormCell(cells[i+1])
		}
		if vals[0] == "kind" {
			continue // the header row
		}
		if strings.Trim(vals[0], "-") == "" {
			continue // the separator row
		}
		for _, name := range strings.Split(vals[0], ",") {
			if name = strings.TrimSpace(name); name != "" {
				rows = append(rows, issue2215Row{name, vals[1], vals[2], vals[3], vals[4], vals[5]})
			}
		}
	}
	if len(rows) == 0 {
		t.Fatal("§5 rule 2's table named no kinds")
	}
	return rows
}

// issue2215NormCell is the one normalization both sides of the comparison get: the
// markdown around the spec's words (backticks, folded whitespace) off, an em-dash-only
// cell (a kind that carries no tokens) as "-", and the emphasis markers kept —
// `testdata/firstrun/**` is a glob, not emphasis, and the test compares the spec's own
// words rather than two normalizations against each other.
func issue2215NormCell(cell string) string {
	cell = strings.ReplaceAll(cell, "`", "")
	cell = strings.Join(strings.Fields(cell), " ")
	if cell == "—" {
		return "-"
	}
	return cell
}

// writeTypedCards writes n cards of one (kind, template) pair under root — the typed
// header §5 rule 1 names, plus the TEMPLATE: line the first-card rule keys on — and
// returns the cards.tsv path and the card paths in order.
func writeTypedCards(t *testing.T, root string, n int, kind, tmpl string) (string, []string) {
	t.Helper()
	cardsDir := filepath.Join(root, "src")
	if err := os.MkdirAll(cardsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	paths := make([]string, n)
	for i := 0; i < n; i++ {
		label := "card-" + string(rune('a'+i))
		body := "RESULT " + label + " sha=000000000000\n" +
			"KIND: " + kind + "\n"
		if tmpl != "" {
			body += "TEMPLATE: " + tmpl + "\n"
		}
		body += "PATHS: internal/x/a.go, internal/x/a_test.go\n" +
			"TEST: ./internal/x TestA\n" +
			"LEGS: go\n" +
			"SOURCE: mas-bandwidth/nova-tools#2215\n" +
			"\n" +
			"STEP 1. The step the card carries.\n"
		path := filepath.Join(cardsDir, label)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		paths[i] = path
		sb.WriteString(label + "\t-\tpro\t" + path + "\n")
	}
	cards := filepath.Join(root, "cards.tsv")
	if err := os.WriteFile(cards, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return cards, paths
}

// issue2215BatchRan says whether the fake nova-swarm recorded a batch: the receipt that a
// launch the rule admitted actually reached the swarm.
func issue2215BatchRan(t *testing.T, argvLog string) bool {
	t.Helper()
	raw, err := os.ReadFile(argvLog)
	if err != nil {
		return false
	}
	fields := strings.Fields(string(raw))
	return len(fields) >= 2 && fields[0] == "nova-swarm" && fields[1] == "batch"
}

func TestIssue2215(t *testing.T) {
	// §5 rules 2 and 3: the table is data in internal/pulse/kinds.go, its names are the
	// declared name set's, and its cells are the spec's own — the same kinds, steps and
	// tokens the issue's receipt quotes.
	t.Run("the kinds table names the spec's kinds, steps and tokens", func(t *testing.T) {
		raw, err := os.ReadFile("kinds.go")
		if err != nil {
			t.Fatalf("internal/pulse/kinds.go: %v (SPEC-TOOLWORK.md §5 rule 3 holds the kinds table there: nova-tools #2215)", err)
		}
		got := parseKindTable(t, string(raw))
		want := specKindTable(t)
		if len(got) != len(want) {
			t.Fatalf("kinds.go holds %d rows; §5 rule 2's table names %d kinds", len(got), len(want))
		}
		for i, w := range want {
			if got[i] != w {
				t.Errorf("the %s row:\nkinds.go holds      {%s %s %s %s %s %s}\nthe spec's §5 holds {%s %s %s %s %s %s}",
					w.name, got[i].name, got[i].does, got[i].paths, got[i].gate, got[i].control, got[i].tokens,
					w.name, w.does, w.paths, w.gate, w.control, w.tokens)
			}
		}
		var names []string
		for _, r := range got {
			names = append(names, r.name)
		}
		if g, w := strings.Join(names, ","), strings.Join(kinds.Kinds(), ","); g != w {
			t.Errorf("kinds.go's kinds are %q; the declared name set (internal/hygiene/kinds.txt) is %q", g, w)
		}
	})

	// §5 rule 3: there is no default kind. A typed card whose KIND: names a kind the
	// table does not hold was never cut by this toolchain and can never be accepted, so
	// launch refuses it rather than spending a slot to learn that.
	t.Run("a kind the table does not hold is refused", func(t *testing.T) {
		root := t.TempDir()
		argvLog := filepath.Join(root, "argv.log")
		fakeSwarm(t, argvLog)
		cards, _ := writeTypedCards(t, root, 1, "not-a-declared-kind", "59007f0ee12a")
		code, out, errb := runLaunch(t, LaunchInput{
			Cards: cards, Root: root, Slots: 2, Deadline: "120", Now: issue2215Now,
		})
		if code != 2 {
			t.Fatalf("exit=%d, want 2; stderr=%q stdout=%q", code, errb, out)
		}
		if want := "PULSE REFUSED: kind=not-a-declared-kind is not a kind the kinds table holds (there is no default kind: SPEC-TOOLWORK.md §5 rule 3)\n"; errb != want {
			t.Fatalf("stderr=%q, want %q", errb, want)
		}
		if out != "" {
			t.Fatalf("stdout=%q, want empty: the refusal is before any spend", out)
		}
		if issue2215BatchRan(t, argvLog) {
			t.Fatal("the fake nova-swarm ran a batch for a card the table does not hold")
		}
	})

	// §5 rule 5: one card of a new template runs alone before the batch widens.
	t.Run("a wide batch of an unproven pair is refused", func(t *testing.T) {
		root := t.TempDir()
		argvLog := filepath.Join(root, "argv.log")
		fakeSwarm(t, argvLog)
		cards, _ := writeTypedCards(t, root, 2, "fix-red", "59007f0ee12a")
		code, out, errb := runLaunch(t, LaunchInput{
			Cards: cards, Root: root, Slots: 4, Deadline: "120", Now: issue2215Now,
		})
		if code != 2 {
			t.Fatalf("exit=%d, want 2; stderr=%q stdout=%q", code, errb, out)
		}
		if want := "PULSE REFUSED: no accepted first card for kind=fix-red template=59007f0ee12a (launch one card first)\n"; errb != want {
			t.Fatalf("stderr=%q, want the spec's own refusal line %q", errb, want)
		}
		if out != "" {
			t.Fatalf("stdout=%q, want empty: the refusal is before any spend", out)
		}
		if issue2215BatchRan(t, argvLog) {
			t.Fatal("the fake nova-swarm ran a batch the rule refuses")
		}
	})

	// The first card itself is never refused on this rule: a single card IS the first
	// card, whatever first.tsv says about it.
	t.Run("one card of an unproven pair launches alone", func(t *testing.T) {
		root := t.TempDir()
		argvLog := filepath.Join(root, "argv.log")
		fakeSwarm(t, argvLog)
		cards, _ := writeTypedCards(t, root, 1, "fix-red", "59007f0ee12a")
		code, out, errb := runLaunch(t, LaunchInput{
			Cards: cards, Root: root, Slots: 4, Deadline: "120", Now: issue2215Now,
		})
		if code != 0 {
			t.Fatalf("exit=%d, want 0; stderr=%q", code, errb)
		}
		if !strings.Contains(out, "PULSE OK") {
			t.Fatalf("stdout=%q, want a PULSE OK line", out)
		}
		if !issue2215BatchRan(t, argvLog) {
			t.Fatal("the fake nova-swarm recorded no batch: the first card did not run")
		}
	})

	// An ACCEPT OK row for the pair is what widens the batch: the file, not the caller's
	// word, is the proof.
	t.Run("an ACCEPT OK row proves the pair and the batch widens", func(t *testing.T) {
		root := t.TempDir()
		argvLog := filepath.Join(root, "argv.log")
		fakeSwarm(t, argvLog)
		if err := os.MkdirAll(filepath.Join(root, "accept"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "accept", "first.tsv"),
			[]byte("fix-red\t59007f0ee12a\tACCEPT OK\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		cards, _ := writeTypedCards(t, root, 2, "fix-red", "59007f0ee12a")
		code, out, errb := runLaunch(t, LaunchInput{
			Cards: cards, Root: root, Slots: 4, Deadline: "120", Now: issue2215Now,
		})
		if code != 0 {
			t.Fatalf("exit=%d, want 0; stderr=%q", code, errb)
		}
		if !strings.Contains(out, "PULSE OK") {
			t.Fatalf("stdout=%q, want a PULSE OK line", out)
		}
		if !issue2215BatchRan(t, argvLog) {
			t.Fatal("the fake nova-swarm recorded no batch: the proven pair did not widen")
		}
	})

	// Rule 5's own sentence: a first card that is REJECT or ABSTAIN leaves the pair
	// unproven.
	t.Run("a REJECT first card leaves the pair unproven", func(t *testing.T) {
		root := t.TempDir()
		argvLog := filepath.Join(root, "argv.log")
		fakeSwarm(t, argvLog)
		if err := os.MkdirAll(filepath.Join(root, "accept"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "accept", "first.tsv"),
			[]byte("fix-red\t59007f0ee12a\tACCEPT REJECT\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		cards, _ := writeTypedCards(t, root, 2, "fix-red", "59007f0ee12a")
		code, out, errb := runLaunch(t, LaunchInput{
			Cards: cards, Root: root, Slots: 4, Deadline: "120", Now: issue2215Now,
		})
		if code != 2 {
			t.Fatalf("exit=%d, want 2; stderr=%q stdout=%q", code, errb, out)
		}
		if want := "PULSE REFUSED: no accepted first card for kind=fix-red template=59007f0ee12a (launch one card first)\n"; errb != want {
			t.Fatalf("stderr=%q, want %q", errb, want)
		}
	})

	// A typed card that names no template is a pair with no template half, and it runs
	// alone like any other unproven pair — which is the state of every v2 card until the
	// cutter writes the TEMPLATE: line.
	t.Run("typed cards that name no template run alone too", func(t *testing.T) {
		root := t.TempDir()
		argvLog := filepath.Join(root, "argv.log")
		fakeSwarm(t, argvLog)
		cards, _ := writeTypedCards(t, root, 2, "fix-red", "")
		code, out, errb := runLaunch(t, LaunchInput{
			Cards: cards, Root: root, Slots: 4, Deadline: "120", Now: issue2215Now,
		})
		if code != 2 {
			t.Fatalf("exit=%d, want 2; stderr=%q stdout=%q", code, errb, out)
		}
		if want := "PULSE REFUSED: no accepted first card for kind=fix-red template= (launch one card first)\n"; errb != want {
			t.Fatalf("stderr=%q, want %q", errb, want)
		}
	})

	// The rule reaches kinds, not legacy shapes: a wide batch of cards with no typed
	// header — every card cut before §5 — launches exactly as before.
	t.Run("a wide batch of untyped cards is untouched", func(t *testing.T) {
		root := t.TempDir()
		argvLog := filepath.Join(root, "argv.log")
		fakeSwarm(t, argvLog)
		cards, _ := writeCards(t, root, 3)
		code, out, errb := runLaunch(t, LaunchInput{
			Cards: cards, Root: root, Slots: 4, Deadline: "120", Now: issue2215Now,
		})
		if code != 0 {
			t.Fatalf("exit=%d, want 0; stderr=%q", code, errb)
		}
		if !strings.Contains(out, "PULSE OK") {
			t.Fatalf("stdout=%q, want a PULSE OK line", out)
		}
		if !issue2215BatchRan(t, argvLog) {
			t.Fatal("the fake nova-swarm recorded no batch: the legacy cards did not launch")
		}
	})
}
