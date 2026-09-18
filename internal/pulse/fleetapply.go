package pulse

// `fleet standard --apply`: the provisioning standard as REMEDIES, not only as checks.
//
// `fleet standard` reads a machine and says what drifted. On the morning of 2026-09-18 it
// would have said the same four things about four Linux machines -- no non-interactive PATH,
// eighteen `~/go/bin` shadows ahead of the release, no git identity, no Go on a runner's
// `.path` -- and a person fixed all four by hand, four times, and nothing remembered how.
//
// So each check that CAN be repaired carries one idempotent remedy, and the remedy is a
// file's worth of shell that answers exactly one line: `APPLY<TAB>item<TAB>changed|unchanged
// <TAB>detail`. The Go side is the only place a STANDARD APPLY line is formatted and the only
// place a verdict is decided, exactly as the checks work.
//
// Three rules the remedies are written under:
//
//   - IDEMPOTENT. Applying twice changes nothing the second time, and says `unchanged`.
//     The loop runs this after a failure, and a remedy that appends a PATH line every time
//     is a ~/.bashrc nobody can read by Friday.
//   - NOTHING IS DELETED. The shadow binaries are MOVED to ~/nova-bench/stale-gobin-<date>/,
//     because the one thing worse than a stale tool on PATH is a stale tool nobody can get
//     back (memory: deletion is a verb over a validated path, and this is not it).
//   - THE BIG ONE IS NOT AUTOMATED. A stale nova build is `nova-update release adopt`, which
//     stops cards, swaps binaries and re-certifies. Apply names it (`remedy=adopt`) and does
//     not run it.
//
// Everything reaches the machine through the same FleetRunner seam the other fleet verbs
// use, so a test drives a fake ssh that runs the remedy against a temporary home -- the
// remedies here are run FOR REAL by their tests, against files, twice, which is the only way
// idempotence can be claimed.

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The verdicts one item may answer with. They are lower case, unlike a check's OK/DRIFT,
// because they say what APPLY DID and not what the machine IS.
const (
	ApplyChanged   = "changed"
	ApplyUnchanged = "unchanged"
	ApplyWould     = "would"  // --dry-run: this is what would be run, and nothing was
	ApplyFailed    = "failed" // the remedy could not run, or said nothing this tool reads
)

// StandardRemedy is one idempotent repair for one item of the provisioning standard.
type StandardRemedy struct {
	Item   string // the check it repairs; the token on the APPLY line
	OS     string // "linux", "darwin", or "" for every machine
	ByHand string // non-empty: apply NAMES this remedy and never runs it
	Script string // POSIX shell printing one APPLY<TAB>item<TAB>verdict<TAB>detail line
}

// StandardRemedies is the table: the repairs for one operating system, in item order.
// gitName and gitEmail fill the identity remedy; they are arguments and not constants
// because the identity a machine commits under is a decision, not a default.
func StandardRemedies(goos, gitName, gitEmail string) []StandardRemedy {
	all := []StandardRemedy{
		{Item: fleet.ItemGitIdentity, Script: gitIdentityRemedy(gitName, gitEmail)},
		{Item: fleet.ItemGobinShadow, Script: gobinShadowRemedy},
		{Item: fleet.ItemNovaStamp, ByHand: "adopt", Script: ""},
		{Item: fleet.ItemPathNonInteractive, Script: pathNonInteractiveRemedy},
		{Item: fleet.ItemRunnerPathGo, Script: runnerPathGoRemedy(fleet.DefaultGo)},
	}
	out := make([]StandardRemedy, 0, len(all))
	for _, r := range all {
		if r.OS == "" || r.OS == goos {
			out = append(out, r)
		}
	}
	return out
}

// pathNonInteractiveRemedy writes ONE marker block at the TOP of ~/.bashrc, above the
// interactive guard. Ubuntu's own ~/.bashrc returns at that guard for a non-interactive
// shell -- which is what an ssh, a card and a CI shard all get -- so every PATH line below it
// is invisible to the only shells that matter, and ~/.profile is never read at all. That is
// why `ssh hulk nova-merge version` answered `command not found` on a machine where the same
// command in a login shell worked.
//
// The block is bounded by markers so applying twice is a no-op and a person can delete it in
// one motion. The Go SDK goes on too: a bench with `go` only in a login shell is the fault
// `go-on-path` names.
const pathNonInteractiveRemedy = `
f="$HOME/.bashrc"
marker="# >>> nova non-interactive PATH >>>"
if [ ! -f "$f" ]; then
  : > "$f"
fi
if grep -Fq "$marker" "$f"; then
  printf 'APPLY\tpath-noninteractive\tunchanged\tthe marker block already leads %s\n' "$f"
else
  t="$f.nova.$$"
  cat > "$t" <<'NOVA_PATH_BLOCK'
# >>> nova non-interactive PATH >>>
# Written by: nova-pulse fleet standard --apply. A non-interactive shell -- an ssh, a card,
# a CI shard -- reads this file and returns at the interactive guard below, so these lines
# are ABOVE it. Delete the whole block to undo this.
if [ -d "$HOME/.local/bin" ] && ! printf '%s' ":$PATH:" | grep -Fq ":$HOME/.local/bin:"; then
  PATH="$HOME/.local/bin:$PATH"
fi
for nova_go_bin in "$HOME"/sdk/go*/bin; do
  if [ -x "$nova_go_bin/go" ] && ! printf '%s' ":$PATH:" | grep -Fq ":$nova_go_bin:"; then
    PATH="$nova_go_bin:$PATH"
  fi
done
unset nova_go_bin
export PATH
# <<< nova non-interactive PATH <<<
NOVA_PATH_BLOCK
  cat "$f" >> "$t"
  mv -f "$t" "$f"
  printf 'APPLY\tpath-noninteractive\tchanged\tthe marker block now leads %s\n' "$f"
fi
`

// gobinShadowRemedy MOVES the `go install`-built nova-* binaries out of ~/go/bin. Eighteen
// of them sat there on 2026-09-18 answering v0.15.3, ahead of the release in ~/.local/bin on
// the same PATH -- which is also why `release adopt` reported tools=0 skipped=21: it asked
// the binary in --bin, which was current, while the shadow was what served PATH.
//
// They are moved and never deleted. A dated directory, so a second apply on another day does
// not overwrite the first day's evidence.
const gobinShadowRemedy = `
d="$HOME/nova-bench/stale-gobin-$(date -u +%Y-%m-%d)"
n=0
for b in "$HOME"/go/bin/nova-*; do
  [ -e "$b" ] || continue
  mkdir -p "$d"
  mv -f "$b" "$d/"
  n=$((n + 1))
done
if [ "$n" -eq 0 ]; then
  printf 'APPLY\tgobin-shadow\tunchanged\tno nova-* under $HOME/go/bin\n'
else
  printf 'APPLY\tgobin-shadow\tchanged\tmoved %s aside to %s\n' "$n" "$d"
fi
`

// runnerPathGoRemedy puts the Go SDK on the FIRST line of every runner's `.path`. A runner
// reads its PATH from that file and from no shell at all, so every fault there is invisible
// to every other check: space's sixteen carried the bare distro PATH with no go, and every
// Go shard scheduled there ran with no toolchain. Both namings are globbed, because both
// exist in this fleet.
//
// It asks for the WANTED Go and not for `go`. Writing it the other way -- `command -v go` --
// is the whole hurt of 2026-09-18 repeated inside its own repair: hulk has /usr/bin/go 1.22
// on the distro PATH, go.mod refuses 1.22 by name, and a repair that saw a `go` there would
// have reported every runner on that machine healthy.
func runnerPathGoRemedy(goWant string) string {
	return `
want=` + fleetQuote(goWant) + `
g=""
for c in "$HOME"/sdk/go*/bin; do
  if [ -x "$c/go" ] && "$c/go" version 2>/dev/null | grep -Fq "$want"; then
    g="$c"
  fi
done
if [ -z "$g" ]; then
  printf 'APPLY\trunner-path-go\tunchanged\tno %s under $HOME/sdk to put on a .path\n' "$want"
else
  n=0
  for p in "$HOME"/runner-nova-tools-*/.path "$HOME"/actions-runner-*/.path; do
    [ -f "$p" ] || continue
    first="$(head -n 1 "$p")"
    if PATH="$first" go version 2>/dev/null | grep -Fq "$want"; then
      continue
    fi
    t="$p.nova.$$"
    printf '%s:%s\n' "$g" "$first" > "$t"
    tail -n +2 "$p" >> "$t"
    mv -f "$t" "$p"
    n=$((n + 1))
  done
  if [ "$n" -eq 0 ]; then
    printf 'APPLY\trunner-path-go\tunchanged\tevery .path already reaches %s\n' "$want"
  else
    printf 'APPLY\trunner-path-go\tchanged\t%s .path file(s) now lead with %s\n' "$n" "$g"
  fi
fi
`
}

// gitIdentityRemedy sets the global identity when either half is empty, and never overwrites
// one a machine already carries: a machine whose owner set their own name is not drifted.
// Empty on all four Linux machines on 2026-09-18, and a card that commits in a fresh clone
// finds out at the commit -- after the work.
func gitIdentityRemedy(name, email string) string {
	return `
have_name="$(git config --global --get user.name 2>/dev/null || true)"
have_email="$(git config --global --get user.email 2>/dev/null || true)"
if [ -n "$have_name" ] && [ -n "$have_email" ]; then
  printf 'APPLY\tgit-identity\tunchanged\t%s <%s>\n' "$have_name" "$have_email"
else
  if [ -z "$have_name" ]; then
    git config --global user.name ` + fleetQuote(name) + `
  fi
  if [ -z "$have_email" ]; then
    git config --global user.email ` + fleetQuote(email) + `
  fi
  printf 'APPLY\tgit-identity\tchanged\t%s <%s>\n' "$(git config --global --get user.name)" "$(git config --global --get user.email)"
fi
`
}

// ---------------------------------------------------------------------------
// the verb
// ---------------------------------------------------------------------------

// ApplyResult is one item's answer, for a caller that acts on it rather than reads it: the
// certify loop's fixer.
type ApplyResult struct {
	Item    string
	Verdict string
	Remedy  string // "adopt" for the item apply names and does not run; "-" otherwise
	Detail  string
}

// ApplyOutcome is the whole run: the exit the verb makes, and every item's answer.
type ApplyOutcome struct {
	Code    int
	Results []ApplyResult
}

// Changed is the items this run actually changed, in item order.
func (o ApplyOutcome) Changed() []string {
	var out []string
	for _, r := range o.Results {
		if r.Verdict == ApplyChanged {
			out = append(out, r.Item)
		}
	}
	return out
}

// ApplyInput is `fleet standard --apply`: the machine, the items, and the seam.
type ApplyInput struct {
	Machines string   // the machines registry; the ssh target comes from it and nowhere else
	Name     string   // the one machine
	Items    []string // the items to apply; empty is every remedy for that machine's OS
	SSH      string
	Home     string // the machine's HOME; empty is the machine's own
	GitName  string
	GitEmail string
	Timeout  time.Duration
	DryRun   bool
	Runner   FleetRunner
	Stdout   io.Writer
	Stderr   io.Writer
}

// FleetStandardApply applies the standard's remedies to one machine: one line per item, and
// exit 0 when every item answered, 2 when the run was refused before any ssh, 3 when an item
// could not be applied.
func FleetStandardApply(in ApplyInput) ApplyOutcome {
	if in.Stdout == nil {
		in.Stdout = io.Discard
	}
	if in.Stderr == nil {
		in.Stderr = io.Discard
	}
	if strings.TrimSpace(in.Name) == "" {
		return ApplyOutcome{Code: applyRefusal(in.Stderr, fmt.Errorf(
			"missing --machine; refusing to guess (--apply CHANGES a machine, so it is never the whole fleet and never a guess)"))}
	}
	reg, err := fleet.ReadRegistry(in.Machines)
	if err != nil {
		return ApplyOutcome{Code: applyRefusal(in.Stderr, err)}
	}
	m, ok := reg.Lookup(in.Name)
	if !ok {
		return ApplyOutcome{Code: applyRefusal(in.Stderr, fmt.Errorf(
			"%s carries no machine %s; add it, or name one it does", reg.Path(), oneline.Field(in.Name)))}
	}
	remedies := StandardRemedies(m.OS, applyGitName(in.GitName), applyGitEmail(in.GitEmail))
	wanted, err := chosenRemedies(remedies, in.Items)
	if err != nil {
		return ApplyOutcome{Code: applyRefusal(in.Stderr, err)}
	}

	run := in.Runner
	if run == nil {
		run = SSHRunner{Program: in.SSH}
	}
	bound := fleetPowerTimeout(in.Timeout)
	fmt.Fprintf(in.Stderr, "STANDARD APPLY WALK machine=%s os=%s items=%d dry-run=%t\n",
		oneline.Field(m.Name), oneline.Field(m.OS), len(wanted), in.DryRun)

	out := ApplyOutcome{}
	failed := 0
	for _, r := range wanted {
		result := ApplyResult{Item: r.Item, Remedy: "-", Detail: "-"}
		switch {
		case r.ByHand != "":
			// The one apply never runs. It is reported every time, so a stale build is never
			// a silence: `remedy=adopt` is the whole point of the line.
			result.Verdict, result.Remedy = ApplyUnchanged, r.ByHand
			result.Detail = "apply never runs this; run: nova-update release adopt"
		case in.DryRun:
			result.Verdict = ApplyWould
			result.Detail = "the remedy would run and this run changed nothing"
		default:
			result.Verdict, result.Detail = in.applyOne(run, m, r, bound)
		}
		if result.Verdict == ApplyFailed {
			failed++
		}
		out.Results = append(out.Results, result)
		line := fmt.Sprintf("STANDARD APPLY %s %s %s remedy=%s detail=%s",
			oneline.Field(m.Name), r.Item, result.Verdict, oneline.Field(result.Remedy),
			oneline.Quote(oneline.Cap(result.Detail, applyDetailCap)))
		if result.Verdict == ApplyFailed {
			fmt.Fprintln(in.Stderr, line)
			continue
		}
		fmt.Fprintln(in.Stdout, line)
	}
	verdict, code, w := "OK", 0, in.Stdout
	if failed > 0 {
		verdict, code, w = "FAILED", 3, in.Stderr
	}
	fmt.Fprintf(w, "STANDARD APPLY %s machine=%s items=%d changed=%d failed=%d\n",
		verdict, oneline.Field(m.Name), len(out.Results), len(out.Changed()), failed)
	out.Code = code
	return out
}

// applyDetailCap is the most of a remedy's detail that reaches a line.
const applyDetailCap = 160

// applyOne runs ONE remedy on ONE machine and reads its answer. The verdict comes from what
// the machine SAID -- the APPLY line -- and never from the exit code alone: a remedy that
// exits 0 having printed nothing has not repaired anything, and saying `changed` for it is
// the survey's own lie in a new place.
func (in ApplyInput) applyOne(run FleetRunner, m fleet.Machine, r StandardRemedy, bound time.Duration) (string, string) {
	ctx, cancel := context.WithTimeout(context.Background(), bound)
	defer cancel()
	out, err := run.Run(ctx, m.SSH, applyScript(in.Home, r))
	verdict, detail, ok := applyAnswer(out, r.Item)
	if ok {
		return verdict, detail
	}
	if err != nil {
		return ApplyFailed, fleetReason(out, err)
	}
	return ApplyFailed, fmt.Sprintf("the machine printed no APPLY line for %s: %s",
		r.Item, oneline.Cap(strings.TrimSpace(lastLine(out)), applyDetailCap))
}

// applyScript is what goes down the ssh pipe: the machine's HOME when one was named, a
// C locale so a remedy reads the same everywhere, and the remedy itself under `set -eu` so a
// half-applied repair is a failure and not a pass.
func applyScript(home string, r StandardRemedy) string {
	var b strings.Builder
	b.WriteString("set -eu\n")
	if strings.TrimSpace(home) != "" {
		b.WriteString("HOME=" + fleetQuote(home) + "\nexport HOME\n")
	}
	b.WriteString("LC_ALL=C\nexport LC_ALL\n")
	b.WriteString(r.Script)
	b.WriteString("\n")
	return b.String()
}

// applyAnswer reads the one APPLY line back.
func applyAnswer(out, item string) (string, string, bool) {
	for _, raw := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		rest, ok := strings.CutPrefix(strings.TrimRight(raw, "\r"), "APPLY\t")
		if !ok {
			continue
		}
		name, rest, ok := strings.Cut(rest, "\t")
		if !ok || name != item {
			continue
		}
		verdict, detail, _ := strings.Cut(rest, "\t")
		verdict = strings.TrimSpace(verdict)
		if verdict != ApplyChanged && verdict != ApplyUnchanged {
			return ApplyFailed, fmt.Sprintf("%s answered %q, which is neither changed nor unchanged", item, verdict), true
		}
		if strings.TrimSpace(detail) == "" {
			detail = "-"
		}
		return verdict, strings.TrimSpace(detail), true
	}
	return "", "", false
}

// chosenRemedies is the remedies a run will apply: every one for this machine when no item
// was named, and exactly the named ones otherwise. An item this machine has no remedy for is
// a REFUSAL and never a silent skip -- a caller that asked for a repair and got a clean exit
// would report the machine repaired.
func chosenRemedies(remedies []StandardRemedy, items []string) ([]StandardRemedy, error) {
	if len(items) == 0 {
		return remedies, nil
	}
	byItem := map[string]StandardRemedy{}
	for _, r := range remedies {
		byItem[r.Item] = r
	}
	var out []StandardRemedy
	seen := map[string]bool{}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		r, ok := byItem[item]
		if !ok {
			return nil, fmt.Errorf("no remedy for %s on this machine; the items are %s",
				oneline.Field(item), strings.Join(remedyItems(remedies), ", "))
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Item < out[j].Item })
	return out, nil
}

func remedyItems(remedies []StandardRemedy) []string {
	out := make([]string, 0, len(remedies))
	for _, r := range remedies {
		out = append(out, r.Item)
	}
	return out
}

// The identity a machine of this fleet commits under when no flag names another.
func applyGitName(v string) string {
	if strings.TrimSpace(v) == "" {
		return "Rowan"
	}
	return v
}

func applyGitEmail(v string) string {
	if strings.TrimSpace(v) == "" {
		return "rowan@mas-bandwidth.com"
	}
	return v
}

func applyRefusal(w io.Writer, err error) int {
	fmt.Fprintf(w, "STANDARD APPLY REFUSED: %s\n", oneline.Err(err))
	return 2
}
