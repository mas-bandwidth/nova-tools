package fleet

// FIX, THEN PROVE, THEN ESCALATE -- the second half of mechanized certification.
//
// #1369 made a machine DO the work its roles imply and write down that it did. It stops at
// the verdict: a FAIL is a line and a row, and a person goes to the machine. On 2026-09-18
// that person was real, the fleet had four faults, and every one of the four was the same
// fault on four machines -- no non-interactive PATH, eighteen `~/go/bin` shadows ahead of
// the release, no git identity, no Go on a runner's `.path`. Four machines, four hands, and
// nothing in the tools remembered how by the evening.
//
// So the repairs are a table (internal/pulse's remedies), the mapping from a failed class to
// the items that repair it is a table HERE, and the loop runs them: apply, certify that
// machine once more, and if the class still fails, escalate by name -- one line, one bus note
// to the fleet lane, and the class LEFT UNCERTIFIED so `fill` keeps refusing cards for it.
//
// Two rules hold the whole thing honest:
//
//   - A repair is never credit. The class is certified again, by the same workload, through
//     the same wall. A tool that marked a class fixed because a remedy exited 0 is the
//     survey's own lie in a new place.
//   - A class no item repairs is never "repaired". go-test failing is a broken toolchain
//     inside the wall and no line of ~/.bashrc fixes it; the escalation says exactly that
//     rather than applying something plausible and asking again.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The items of the provisioning standard a remedy can repair. They are the NAMES of the
// checks in internal/pulse's standard table -- one spelling, held by a test in that package,
// because an item that names no check is a repair for nothing.
const (
	// ItemPathNonInteractive is the marker block above ~/.bashrc's interactive guard. A
	// non-interactive shell -- what an ssh, a card and a CI shard actually get -- returns at
	// that guard before any PATH line below it.
	ItemPathNonInteractive = "path-noninteractive"
	// ItemGobinShadow is the `go install`-built nova-* binaries in ~/go/bin, ahead of the
	// release on the same PATH. The remedy MOVES them aside and never deletes them.
	ItemGobinShadow = "gobin-shadow"
	// ItemGitIdentity is `user.name` and `user.email` in the global git config: empty on
	// every Linux machine in the fleet, and a card that commits finds out after the work.
	ItemGitIdentity = "git-identity"
	// ItemRunnerPathGo is the Go toolchain on the first line of each runner's `.path` file,
	// which a runner reads instead of any shell.
	ItemRunnerPathGo = "runner-path-go"
	// ItemNovaStamp is the installed nova build. It is the ONE item apply never runs itself:
	// installing a release is `nova-update release adopt`, which stops cards, swaps binaries
	// and re-certifies, and a repair loop is no place to start it.
	ItemNovaStamp = "nova-stamp"
)

// ByHandItems are the items a repair names and never performs. Keeping this a list rather
// than a flag on one item is deliberate: the next item that must not be automated is added
// here and the loop needs no new branch.
var ByHandItems = map[string]string{
	ItemNovaStamp: "adopt",
}

// knownItems is every item an apply may be asked for.
var knownItems = map[string]bool{
	ItemPathNonInteractive: true,
	ItemGobinShadow:        true,
	ItemGitIdentity:        true,
	ItemRunnerPathGo:       true,
	ItemNovaStamp:          true,
}

// KnownItem says whether a name is an item of the provisioning standard.
func KnownItem(item string) bool { return knownItems[item] }

// classItems is THE MAPPING: a failed workload class to the standard items whose remedies
// repair it. Every entry is a fault the hand pass of 2026-09-18 found and repaired; a class
// that is absent is one no remedy can fix, and its escalation says so.
var classItems = map[string][]string{
	// `nova-merge` unfound over a plain ssh was both faults at once: no PATH for a
	// non-interactive shell, and eighteen shadows ahead of the release when there was.
	"path-resolves": {ItemGobinShadow, ItemPathNonInteractive},
	// Three machines had no `go` at all non-interactively; the SDK was there the whole time.
	"go-on-path": {ItemPathNonInteractive},
	// `nova-update` unfound on the coordination machine is the same PATH fault, and a stale
	// stamp behind it is the adopt nobody runs from a loop.
	"release-path": {ItemNovaStamp, ItemPathNonInteractive},
	"git-identity": {ItemGitIdentity},
	"runner-path":  {ItemRunnerPathGo},
}

// ItemsForClass is the items that repair one class, sorted, or nothing.
func ItemsForClass(class string) []string {
	items := classItems[class]
	if len(items) == 0 {
		return nil
	}
	out := append([]string(nil), items...)
	sort.Strings(out)
	return out
}

// ItemsForClasses is every item that repairs any of these classes, each once and sorted, and
// -- separately -- the ones apply will NAME rather than run. The two lists are separate
// because an escalation that says "run the adopt" is a different sentence from one that says
// "I applied these and it still fails".
func ItemsForClasses(classes []string) (items, byHand []string) {
	seen := map[string]bool{}
	for _, class := range classes {
		for _, item := range classItems[class] {
			if seen[item] {
				continue
			}
			seen[item] = true
			items = append(items, item)
			if _, ok := ByHandItems[item]; ok {
				byHand = append(byHand, item)
			}
		}
	}
	sort.Strings(items)
	sort.Strings(byHand)
	return items, byHand
}

// ---------------------------------------------------------------------------
// the seams
// ---------------------------------------------------------------------------

// Fixer applies the provisioning standard's remedies to one machine and answers which items
// it changed. The production one is `nova-pulse fleet standard --apply --machine <m>`, run
// in-process through internal/pulse; a test drives a fake and no machine is reached.
type Fixer interface {
	Apply(machine string, items []string) (changed []string, err error)
}

// BusPoster carries one note to one lane. The production one is `nova-bus send` -- the bus's
// own send path, with its own locking, index and push -- and never a second copy of it.
type BusPoster interface {
	Post(lane, subject, body string) error
}

// DefaultFixRounds is how many times one machine is repaired and re-certified in one run.
// ONE: a second round that repairs what the first round could not repair is a loop that puts
// a machine's whole load into a hole it cannot climb out of, and the escalation exists so a
// person is asked exactly once (memory: every wait has a deadline).
const DefaultFixRounds = 1

// DefaultLane is the lane an escalation's note goes to when none is named.
const DefaultLane = "fleet"

// ---------------------------------------------------------------------------
// the escalation
// ---------------------------------------------------------------------------

// escalation is one machine's remaining failures, after every repair this run will make.
type escalation struct {
	machine  string
	classes  []string          // sorted, the classes still failing
	evidence map[string]string // class -> what the machine said, for the note
	applied  []string          // the items apply ran, sorted
	byHand   []string          // the items apply named and did not run
	failed   string            // non-empty: the apply itself could not run, and why
}

// remedy is the one field a person acts on. It is a sentence and not a token because the
// three cases are genuinely different work: run the adopt, go and look at the machine, or
// find out why the apply could not reach it.
func (e escalation) remedy() string {
	switch {
	case e.failed != "":
		return fmt.Sprintf("the apply could not run on %s (%s); reach the machine, then: nova-pulse fleet certify --machine %s",
			e.machine, e.failed, e.machine)
	case len(e.byHand) > 0 && len(e.applied) == len(e.byHand):
		return fmt.Sprintf("run: nova-update release adopt --machines <file> --version <v>, then: nova-pulse fleet certify --machine %s", e.machine)
	case len(e.applied) > 0:
		return fmt.Sprintf("applied %s and %s still fails; go to %s by hand",
			strings.Join(e.applied, ","), strings.Join(e.classes, ","), e.machine)
	default:
		return fmt.Sprintf("no item of the provisioning standard repairs %s; go to %s by hand",
			strings.Join(e.classes, ","), e.machine)
	}
}

// Line is the one line an escalation prints: one per MACHINE and never one per class,
// because a fleet of twenty machines that prints a line per class prints nothing anybody
// reads.
func (e escalation) Line() string {
	return fmt.Sprintf("CERTIFY ESCALATE machine=%s classes=%s remedy=%s",
		oneline.Field(e.machine), oneline.Field(strings.Join(e.classes, ",")),
		oneline.Quote(oneline.Cap(e.remedy(), EvidenceCap)))
}

// subject and note are what reaches the lane. The body carries the evidence, because a note
// that says "hulk failed" sends its reader back to the machine to find out what this run
// already knows.
func (e escalation) subject() string {
	return fmt.Sprintf("CERTIFY ESCALATE %s: %s", e.machine, strings.Join(e.classes, ","))
}

func (e escalation) note(hash, build string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "`nova-pulse fleet certify` could not certify %s, and the repair did not fix it.\n\n", e.machine)
	fmt.Fprintf(&b, "machine: %s\nbuild: %s\nstandard-hash: %s\n", e.machine, dash(build), dash(hash))
	fmt.Fprintf(&b, "applied: %s\nby hand: %s\n\n", dash(strings.Join(e.applied, ",")), dash(strings.Join(e.byHand, ",")))
	if e.failed != "" {
		fmt.Fprintf(&b, "the apply itself could not run: %s\n\n", e.failed)
	}
	b.WriteString("still failing:\n\n")
	for _, class := range e.classes {
		fmt.Fprintf(&b, "- %s: %s\n", class, oneline.Cap(e.evidence[class], EvidenceCap))
	}
	fmt.Fprintf(&b, "\nremedy: %s\n", e.remedy())
	b.WriteString("\nThese classes stay UNCERTIFIED, so `nova-pulse fill` refuses cards for them\n")
	b.WriteString("until a real pass proves them.\n")
	return b.String()
}

// failedClasses is every class whose final verdict this run is FAIL, sorted. A WARN is a
// pass with a note and never reaches here.
func failedClasses(verdicts map[string]string) []string {
	var out []string
	for class, v := range verdicts {
		if v == VerdictFail {
			out = append(out, class)
		}
	}
	sort.Strings(out)
	return out
}
