package pulse

// THE BRIEF IS RENDERED, NOT WRITTEN. Pit stop 3, class L (#828); Glenn 2026-09-16:
// "Stateless is the way."
//
// A pulse is a fresh window that reads one page, does one thing and exits. That page was
// queue/PULSE-BRIEF.md, edited by hand every time a slot count, a route or a rule moved --
// and on 2026-09-16 it carried a log filter that selected by clock time, which matched
// YESTERDAY's lines at 00:xxZ and made a busy hour read as a silent one. A page a person
// maintains drifts from the bench it describes; this verb renders it from the bench:
// pulse.toml for the paths and the routes, RULES.tsv for the rows the pulse may decide.
//
// Two shapes: the coordinator's pulse (`brief --as Rowan`) and a friend's (`--friend`).
// Both obey the one rule a brief has to obey, which is that a pulse reads ONE page: no line
// longer than 200 bytes, no listing without a count, and the log read by `tail -n` and
// never by a clock.

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// BriefDecidableKinds are the rule kinds a pulse may decide on its own. Everything else is
// one ESCALATE line: a pulse that decides outside its rows is a pulse inventing policy.
var BriefDecidableKinds = []string{"stop", "hold", "admission"}

// briefLineMax is the longest line a brief may carry. A line longer than this is a
// paragraph, and a paragraph in a brief is a rule nobody made into a row.
const briefLineMax = 200

// BriefInput is the brief verb.
type BriefInput struct {
	As     string
	Queue  string
	Roots  string // the swarm roots the status command reads; the coordinator's brief needs them
	Bus    string // the bus clone the wake runs from; "." is the friend's own clone
	Friend bool
	Stdout io.Writer
	Stderr io.Writer
}

// Brief renders one brief and prints it. It returns 0 when it rendered and 2 when it
// refused.
func Brief(in BriefInput) int {
	switch {
	case strings.TrimSpace(in.As) == "":
		fmt.Fprintf(in.Stderr, "BRIEF REFUSED: --as is required (pass the name the pulse answers as, such as --as Rowan)\n")
		return 2
	case strings.TrimSpace(in.Queue) == "":
		fmt.Fprintf(in.Stderr, "BRIEF REFUSED: --queue is required (pass the queue directory %s and %s live in)\n", ConfigFile, RulesFile)
		return 2
	case !in.Friend && strings.TrimSpace(in.Roots) == "":
		fmt.Fprintf(in.Stderr, "BRIEF REFUSED: --roots is required for a coordinator brief (pass the swarm roots the width is read from, comma separated; a friend's brief takes --friend and no roots)\n")
		return 2
	}
	rows, err := LoadRules(in.Queue)
	if err != nil {
		fmt.Fprintf(in.Stderr, "BRIEF REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	cfg, err := LoadConfig(in.Queue, io.Discard, 0)
	if err != nil {
		fmt.Fprintf(in.Stderr, "BRIEF REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	text := RenderBrief(in, cfg, rows)
	for i, l := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if len(l) > briefLineMax {
			fmt.Fprintf(in.Stderr, "BRIEF REFUSED: line %d is %d bytes, over the %d a brief allows (shorten the rule row it came from in %s)\n",
				i+1, len(l), briefLineMax, filepath.Join(in.Queue, RulesFile))
			return 2
		}
	}
	fmt.Fprint(in.Stdout, text)
	return 0
}

// RenderBrief is the page itself, so a test can read it without a file.
func RenderBrief(in BriefInput, cfg Config, rows []RuleRow) string {
	if in.Friend {
		return renderFriendBrief(in, rows)
	}
	return renderCoordinatorBrief(in, cfg, rows)
}

func renderCoordinatorBrief(in BriefInput, cfg Config, rows []RuleRow) string {
	q := in.Queue
	bus := in.Bus
	if strings.TrimSpace(bus) == "" {
		bus = "."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# One coordination pulse for %s (rendered by nova-pulse brief; never hand-edited)\n\n", in.As)
	b.WriteString("A pulse is a fresh window: read this page, run the commands below, decide only the rows\n")
	b.WriteString("under Rules, reply in one line, exit. Read nothing else -- not the log whole, not a spec,\n")
	b.WriteString("not the transcript. Under 5 minutes and under 15 tool calls.\n\n")

	b.WriteString("## Commands (run exactly these, in order)\n")
	fmt.Fprintf(&b, "1. nova-pulse status --queue %s --roots %s --oneline\n", q, in.Roots)
	fmt.Fprintf(&b, "2. nova-pulse rules --queue %s --check\n", q)
	fmt.Fprintf(&b, "3. nova-pulse routes --queue %s\n", q)
	fmt.Fprintf(&b, "4. tail -n 400 %s | grep -c FAILED\n", filepath.Join(q, "pulse.log"))
	b.WriteString("   (tail -n, ALWAYS: a clock filter matched yesterday's lines on 2026-09-16 and read a\n")
	b.WriteString("   busy hour as a silent one. Never select the log by time.)\n")
	fmt.Fprintf(&b, "5. nova-bus wake --bus %s --as %s\n", bus, in.As)
	b.WriteString("   (the pin and the one To: note; never INBOX OPEN, never the cairn)\n\n")

	fmt.Fprintf(&b, "## Rules you may decide (%s, kinds %s)\n", RulesFile, strings.Join(BriefDecidableKinds, ", "))
	writeRuleLines(&b, RulesOfKind(rows, BriefDecidableKinds...))
	b.WriteString("Anything this table does not answer is one ESCALATE item. A pulse never invents a rule;\n")
	b.WriteString("a case with no row becomes a row the same hour, by whoever answers it.\n\n")

	fmt.Fprintf(&b, "## The bench right now (from %s; do not re-read it)\n", ConfigFile)
	fmt.Fprintf(&b, "- slots: %s\n", benchCells(cfg))
	fmt.Fprintf(&b, "- routes: text=%s code=%s benched=%s\n",
		listOrDash(cfg.Routes.Live(RouteClassText)), listOrDash(cfg.Routes.Live(RouteClassCode)), listOrDash(cfg.Routes.Benched))
	fmt.Fprintf(&b, "- integration branches: %s (a read diffs against the PR's OWN base)\n\n", strings.Join(cfg.IntegrationBranches, ","))

	b.WriteString("## Output (your whole final message; nothing else)\n")
	fmt.Fprintf(&b, "PULSE %s <time>Z width=<from status> stop=<yes|no> rules=<rows> route=<next>\n", in.As)
	b.WriteString("notes=<n> actions=<what you did, or none> ESCALATE=<items, or none>\n")
	return b.String()
}

func renderFriendBrief(in BriefInput, rows []RuleRow) string {
	bus := in.Bus
	if strings.TrimSpace(bus) == "" {
		bus = "."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# One pulse for %s (rendered by nova-pulse brief --friend; never hand-edited)\n\n", in.As)
	b.WriteString("A pulse is a fresh window: read this page and the ONE note it names, do the one thing,\n")
	b.WriteString("reply, exit. Not INBOX OPEN, not the cairn, not a spec the note did not name with\n")
	b.WriteString("--rule. Under 5 minutes and under 12 tool calls. A pulse never waits.\n\n")

	b.WriteString("## Steps (run exactly these, in order)\n")
	fmt.Fprintf(&b, "1. nova-bus wake --bus %s --as %s (the pin, the one To: note, its deadline)\n", bus, in.As)
	b.WriteString("2. read that ONE note. A PR to read: nova-review packet --pr N --out packet.md, and read\n")
	b.WriteString("   the packet, never the repo. A rule: nova-review packet --rule spec:n.\n")
	b.WriteString("3. do the one thing the note asks: one verdict, one answer, or one receipt. One artifact.\n")
	fmt.Fprintf(&b, "4. nova-bus send (your verdict first in the subject), then nova-bus receipt --note <id> --verdict <word>\n\n")

	fmt.Fprintf(&b, "## Rules you may decide (%s, kinds %s)\n", RulesFile, strings.Join(BriefDecidableKinds, ", "))
	writeRuleLines(&b, RulesOfKind(rows, BriefDecidableKinds...))
	b.WriteString("A pulse that cannot decide replies ESCALATE with the one question and stops; the next\n")
	b.WriteString("pulse gets the answer.\n\n")

	b.WriteString("## Output (your whole final message; nothing else)\n")
	fmt.Fprintf(&b, "PULSE %s <time>Z note=<id> verdict=<word> tokens=<if your harness prints them>\n", in.As)
	return b.String()
}

// writeRuleLines writes one line per row, each short enough for a brief, and says so when
// there are none -- an empty table is a fact, not an empty section.
func writeRuleLines(b *strings.Builder, rows []RuleRow) {
	if len(rows) == 0 {
		fmt.Fprintf(b, "- (no rows yet: seed them with nova-pulse rules --seed-from <POLICY.md>)\n")
		return
	}
	for _, r := range rows {
		line := fmt.Sprintf("- %s | %s -> %s [%s]", r.Kind, r.Condition, r.Verdict, dashIfEmpty(r.Source))
		if len(line) > briefLineMax {
			line = strings.TrimSpace(line[:briefLineMax-4]) + " ..."
		}
		b.WriteString(line + "\n")
	}
}

// benchCells is the configured width per bench as one field.
func benchCells(cfg Config) string {
	var cells []string
	for _, bench := range Benches {
		cells = append(cells, fmt.Sprintf("%s:%d(-%d)", bench, cfg.Slots[bench], cfg.Headroom[bench]))
	}
	return strings.Join(cells, " ")
}
