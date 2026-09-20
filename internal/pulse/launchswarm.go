package pulse

// The three inches `nova-pulse launch` was missing, measured by dogfooding it against a real
// card on 2026-09-19 (nova-tools #1760, #1761) and fixed here rather than in launch.go so the
// verb's own shape stays readable.
//
//  1. WHICH nova-swarm. `launch` resolved both the runner and `nova-swarm` through PATH, so
//     the one adjustment an operator must make to satisfy the runner lookup -- putting the
//     directory holding the runner in front of PATH -- silently shadowed `nova-swarm` with a
//     stale binary sitting beside it. The stale one answered `flag provided but not defined:
//     -id`, which reads exactly like `launch` building a bad call. It was not. So: the caller
//     may name the binary outright (`--swarm <path>`), and whichever binary answers is asked
//     its version BEFORE the batch, because a launcher that drives another binary's private
//     flag contract may not find out which build it got from the refusal.
//
//  2. WHICH runner. `--runner <path>` removes the need to touch PATH at all, which is what
//     closes (1) at the root rather than at the symptom.
//
//  3. WHAT WENT WRONG. `PULSE REFUSED: exit status 3` is a Go *exec.ExitError stringified
//     with nothing added, while the layers underneath had already written the label, the rc,
//     the wall clock and the provider's own error reference one directory away. The same
//     reading tells a start-time provider 5xx from a real failure, which is what makes the
//     bounded retry possible: the shell launcher this replaces has retried exactly that
//     signature all along (`flash-native-bench.sh:17-19`).

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const (
	// DefaultLaunchAttempts is the bound on start-time provider failures: the first call
	// plus two retries, the same three the shell launcher has always taken. A bound rather
	// than a policy of persistence, because a provider that is down stays down and a pulse
	// that never returns is worse than one that refuses.
	DefaultLaunchAttempts = 3

	// startWindow is how early a failure has to be to be a START-time one. A card that ran
	// for longer than this did work, and re-running it would pay for that work twice; the
	// shell launcher draws the same line at the same place.
	startWindow = 15 * time.Second

	// swarmBinary is the name looked up on PATH when the caller names no --swarm.
	swarmBinary = "nova-swarm"
)

// providerStartFailure is the signature of a provider that failed the call rather than the
// work: the two 5xx bodies this suite has actually seen, plus the reference the provider
// stamps on its own unknown errors. It deliberately does NOT include "Invalid API key",
// which the shell launcher retries: a dead key is dead on the third call too, and retrying
// it spends the deadline to reach the same refusal three times slower.
var providerStartFailure = regexp.MustCompile(`(?i)unexpected server error|internal server error|service unavailable|\bhttp 5\d\d\b|err_[0-9a-f]{8}`)

// resolvedSwarm is the nova-swarm launch will drive: the path it will exec and the version
// that path answered with.
type resolvedSwarm struct {
	Path    string
	Version string
}

// resolveSwarm answers WHICH nova-swarm this launch drives, and refuses rather than guessing.
//
// `want` is the caller's --swarm, empty for a PATH lookup. `mine` is this nova-pulse's own
// build version: empty means the caller could not say (every test that predates this check
// passes none), and then no probe is run at all, because a check whose answer cannot be
// compared to anything is a second exec for nothing. cmd/nova-pulse always passes one --
// internal/buildinfo's floor is the word "devel", never empty -- so a shipped binary always
// probes.
//
// The probe is `<path> version`, whose one line is `nova-swarm <version> <os>/<arch> <go>`.
// A binary old enough to predate that verb answers `unknown subcommand "version"` on stderr
// with a non-zero exit, and that is precisely the stale shadow this exists to catch.
//
// The versions are compared EXACTLY, not by release prefix: the shadow measured on this
// fleet was `c839379e` against `0f7ed3b5`, two builds of the same `v0.16.0-dev` release, and
// the older one was missing a flag the newer one passes. A release-prefix comparison would
// have let it through. Two builds that are not the same build do not share a private flag
// contract. The one exception is an unstamped build on either side -- `devel`, or a vcs
// stamp, neither of which is a release -- where there is nothing to compare and the probe
// stands on its own.
func resolveSwarm(want, mine string) (resolvedSwarm, error) {
	path := strings.TrimSpace(want)
	if path == "" {
		found, err := exec.LookPath(swarmBinary)
		if err != nil {
			return resolvedSwarm{}, fmt.Errorf("SWARM-LOOKUP: %s is not on PATH (%v); name it with --swarm <path to the nova-swarm this nova-pulse drives>", swarmBinary, err)
		}
		path = found
	} else {
		if _, err := os.Stat(path); err != nil {
			return resolvedSwarm{}, fmt.Errorf("SWARM-LOOKUP: --swarm %s cannot be read (%v); pass the path of the nova-swarm binary this nova-pulse drives", oneline.Field(path), err)
		}
	}
	if strings.TrimSpace(mine) == "" {
		return resolvedSwarm{Path: path}, nil
	}

	var out, errb bytes.Buffer
	probe := exec.Command(path, "version")
	probe.Stdout = &out
	probe.Stderr = &errb
	if err := probe.Run(); err != nil {
		said := firstSaidLine(errb.String())
		if said == "" {
			said = firstSaidLine(out.String())
		}
		return resolvedSwarm{}, fmt.Errorf("SWARM-VERSION: %s did not answer `version` (%v): %s -- this is not the nova-swarm this nova-pulse drives, and a binary that predates `version` predates the batch flags launch passes; pass --swarm <path to the matching nova-swarm>",
			oneline.Field(path), err, oneline.Cap(oneline.Escape(said), 200))
	}
	theirs, ok := swarmVersionOf(out.String())
	if !ok {
		return resolvedSwarm{}, fmt.Errorf("SWARM-VERSION: %s answered `version` with %s, which does not begin `%s <version>`; pass --swarm <path to the matching nova-swarm>",
			oneline.Field(path), oneline.Quote(oneline.Cap(oneline.Escape(firstSaidLine(out.String())), 200)), swarmBinary)
	}
	if comparableVersion(mine) && comparableVersion(theirs) && theirs != mine {
		return resolvedSwarm{}, fmt.Errorf("SWARM-VERSION: %s is nova-swarm %s and this is nova-pulse %s -- launch drives nova-swarm's own flag contract, so the two are one build or neither; pass --swarm <path to the nova-swarm of this build>, or install the pair",
			oneline.Field(path), oneline.Field(theirs), oneline.Field(mine))
	}
	return resolvedSwarm{Path: path, Version: theirs}, nil
}

// swarmVersionOf reads the version token out of `nova-swarm version`'s one line.
func swarmVersionOf(line string) (string, bool) {
	fields := strings.Fields(firstSaidLine(line))
	if len(fields) < 2 || fields[0] != swarmBinary {
		return "", false
	}
	return fields[1], true
}

// comparableVersion reports whether a version string is a release a comparison can mean
// something about. `devel` is the honest floor of a build with no recorded origin and a vcs
// stamp is a working tree; neither is a release, and comparing either would refuse every
// developer's own build for saying what it is.
func comparableVersion(v string) bool {
	return strings.HasPrefix(v, "v") && v != "version"
}

func firstSaidLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}

// resolveRunner answers WHICH native runner nova-swarm starts per card.
//
// A runner named as a path is checked here, because the caller who bothered to name a path
// gets told that path is wrong, by the flag they named it with. A bare name is left to
// `nova-swarm batch`, whose own refusal for it is already exact ("runner <name> could not
// start for <label>: executable file not found in $PATH") -- launch only adds the door.
func resolveRunner(want string) (string, error) {
	runner := strings.TrimSpace(want)
	if runner == "" {
		return nativeRunner, nil
	}
	if !strings.ContainsRune(runner, '/') && !strings.ContainsRune(runner, os.PathSeparator) {
		return runner, nil
	}
	abs, err := filepath.Abs(runner)
	if err != nil {
		return "", fmt.Errorf("RUNNER: --runner %s cannot be made absolute (%v); nova-swarm starts it from a job directory, so it wants a path this machine can resolve from anywhere", oneline.Field(runner), err)
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("RUNNER: --runner %s cannot be read (%v); pass the path of this deployment's native runner", oneline.Field(abs), err)
	}
	if fi.IsDir() {
		return "", fmt.Errorf("RUNNER: --runner %s is a directory; pass the path of this deployment's native runner", oneline.Field(abs))
	}
	return abs, nil
}

// cardDiag is what the layers under launch already wrote about one card, read back so the
// operator's one line can say it. Every field is read, never recomputed: `native` looked at
// its own capture while the job directory held what the child put there.
type cardDiag struct {
	Label  string
	Job    string        // <root>/<slot>/jobs/<label>, the door
	RC     string        // rc= on the runner's NATIVE line, "-" when it wrote none
	Wall   time.Duration // wall= on the same line
	Reason string        // reason= on the same line
	Err    string        // the first telling line of the harness's own capture
	Ref    string        // the provider's own error reference, when the capture carries one
}

// providerRef is the reference a provider stamps on its own unknown errors. It is pulled
// out separately because a capture is a pretty-printed JSON body and the reference is
// three lines below the message: the one line an operator reads has to carry BOTH, and the
// dogfood's whole complaint was having to go and read the file to find this string.
var providerRef = regexp.MustCompile(`err_[0-9a-f]{8}`)

// startFailure reports whether this card failed at START time on the provider's side: early
// enough not to have done any work, with the provider's own signature in the capture. This
// is the whole retry predicate, and it is deliberately conservative -- a card with no capture
// to read is not retried, because "we do not know" is not "it was the provider".
func (d cardDiag) startFailure() bool {
	if d.Wall <= 0 || d.Wall > startWindow {
		return false
	}
	return providerStartFailure.MatchString(d.Err) || (d.Err != "" && d.Ref != "")
}

// line is the card's half of the refusal: bounded, one line, every field the operator would
// otherwise have gone and read.
func (d cardDiag) line() string {
	var b strings.Builder
	fmt.Fprintf(&b, "card=%s rc=%s", oneline.Field(dash(d.Label)), oneline.Field(dash(d.RC)))
	if d.Wall > 0 {
		fmt.Fprintf(&b, " wall=%.2fs", d.Wall.Seconds())
	}
	if d.Reason != "" {
		fmt.Fprintf(&b, " reason=%s", oneline.Field(d.Reason))
	}
	if d.Err != "" {
		fmt.Fprintf(&b, " err=%s", oneline.Quote(oneline.Cap(oneline.Escape(d.Err), 240)))
	}
	if d.Ref != "" {
		fmt.Fprintf(&b, " ref=%s", oneline.Field(d.Ref))
	}
	if d.Job != "" {
		fmt.Fprintf(&b, " job=%s", oneline.Field(d.Job))
	}
	return b.String()
}

// diagnose reads back what the run left for each admitted card. It returns only the cards
// that left something to say: a batch that refused before any job directory existed has
// nothing here, and its refusal is the swarm's own line, unchanged.
func diagnose(root string, cards []CardRow) []cardDiag {
	var out []cardDiag
	for _, c := range cards {
		job := findJob(root, c.Label)
		if job == "" {
			continue
		}
		d := cardDiag{Label: c.Label, Job: job}
		readNativeLine(filepath.Join(job, "harness.log"), &d)
		d.Err, d.Ref = harnessError(filepath.Join(job, "harness-output.log"))
		out = append(out, d)
	}
	return out
}

// findJob is <root>/<slot>/jobs/<label>: the slot directory is nova-swarm batch's to choose,
// so it is looked for rather than assumed.
func findJob(root, label string) string {
	if label == "" {
		return ""
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		job := filepath.Join(root, e.Name(), "jobs", label)
		if fi, err := os.Stat(job); err == nil && fi.IsDir() {
			return job
		}
	}
	return ""
}

// readNativeLine fills the rc, the wall clock and the reason from the runner's own NATIVE
// line -- `NATIVE OK label=<l> job=<d> rc=<n> wall=<x>s ... reason=<tok>` -- which the batch
// pins the runner's stdout to. The tokens are read, never recomputed.
func readNativeLine(path string, d *cardDiag) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "NATIVE ") {
			continue
		}
		for _, tok := range strings.Fields(line) {
			switch {
			case strings.HasPrefix(tok, "rc="):
				d.RC = strings.TrimPrefix(tok, "rc=")
			case strings.HasPrefix(tok, "reason="):
				d.Reason = strings.TrimPrefix(tok, "reason=")
			case strings.HasPrefix(tok, "wall="):
				if secs, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimPrefix(tok, "wall="), "s"), 64); err == nil && secs >= 0 {
					d.Wall = time.Duration(secs * float64(time.Second))
				}
			}
		}
	}
}

// harnessError is the first telling line of the harness's own capture: the first line
// carrying the provider's signature where there is one, else the first line that says
// "error", else the first non-blank line. Bounded by what it reads, not by what it is given:
// a capture is a model's whole transcript and only its head is read.
func harnessError(path string) (said, ref string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	if len(raw) > captureHeadBytes {
		raw = raw[:captureHeadBytes]
	}
	head := string(raw)
	ref = providerRef.FindString(head)
	firstSignature, firstError, firstAny := "", "", ""
	for _, line := range strings.Split(head, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if firstSignature == "" && providerStartFailure.MatchString(t) {
			firstSignature = t
		}
		if firstError == "" && strings.Contains(strings.ToLower(t), "error") {
			firstError = t
		}
		if firstAny == "" {
			firstAny = t
		}
	}
	switch {
	case firstSignature != "":
		return firstSignature, ref
	case firstError != "":
		return firstError, ref
	default:
		return firstAny, ref
	}
}

// captureHeadBytes is how much of a harness capture the diagnosis reads. A capture holds a
// whole transcript; the start-time failure this is looking for is in its first breath.
const captureHeadBytes = 64 << 10

// parkJob moves a failed attempt's job directory aside so the retry has a clean one, and so
// the evidence of WHY it was retried survives the retry. Nothing is deleted: the shell
// launcher this replaces removed the directory, and a lane that loses the first failure
// cannot tell a flake from a pattern afterwards.
func parkJob(job string, attempt int) string {
	if job == "" {
		return ""
	}
	parked := fmt.Sprintf("%s.attempt%d", job, attempt)
	if _, err := os.Stat(parked); err == nil {
		return ""
	}
	if err := os.Rename(job, parked); err != nil {
		return ""
	}
	return parked
}

// backoff is the wait before attempt n+1, the shell launcher's own: 15s, then 25s.
func backoff(attempt int) time.Duration {
	return time.Duration(5+attempt*10) * time.Second
}
