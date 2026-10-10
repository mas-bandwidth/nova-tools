package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/release"
	"github.com/mas-bandwidth/nova-tools/internal/secrets"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// fleetWords is how a fleet member's status comes about, in nova-sprint help
// and nova-sprint help fleet.
func fleetWords() string {
	return strings.TrimSpace(`
The fleet: each member says it is there with nova-sprint fleet beat <member>,
run on the machine every few seconds; a beat writes the time and the
machine's load. A beat window is `+sprint.BeatDeadline.String()+`; a member is up until it has missed `+fmt.Sprint(sprint.MissedBeatsDown)+` windows
in a row (one missed beat marks nothing; a beat resets the count) and down past
that or when it has never beaten; the tick applies each change
(a member down has its unfinished work cards dealt to the members up; a
member up levels the ready queues). hold <member> holds a member whatever it
beats (status held), and fleet down <member> is hold --return in the old
words; unhold or fleet up releases the hold, fleet up adding a member the
sprint does not know; fleet up <m> --width 0 drains a member instead: no new
deal reaches it, its untaken ready cards are levelled away and its working
cards finish where they are (fleet down deals them again elsewhere); --width
<n> ends the drain. Each says where the cards went on its MOVED line: down
"moved=N to <member>(n),...; stayed=K withdrawn: <ids>" (a card no member up
has room for is withdrawn and dealt again where there is room), up
"moved=N to <member>(n) from <member>(n),..." (the level). fleet up --deadline <d> pins
the deadline every card dealt to the member gets (--deadline default takes the
pin off): a card's deadline is otherwise the larger of its own and three times
the member's median run wall over its last fifty ok attempts, so a slow
machine does not time out twice as often. The load cell is the machine's CPU busy percent of all
its cores (the one-minute load average over the cores where that cannot be
measured), the highest of the last `+sprint.LoadWindow.String()+`. A beat measures the
machine's open file descriptors beside its load, given or measured: over --fd-warn
(else NOVA_FD_WARN, else `+fmt.Sprint(hostload.FilesWarnDefault)+`) it says warn and lists the top
holders, read at most once every `+hostload.HoldersEvery.String()+`; over --fd-alarm (else
NOVA_FD_ALARM, else `+fmt.Sprint(hostload.FilesAlarmDefault)+`) it says alarm, and the tick writes
one judgment of the member ("`+sprint.NFilesAlarm+`") naming the count, the top
holders and its cards that ended on a timeout, closed with one cleared note when
the count falls under the alarm.

fleet sync makes the fleet match nova-config's machine rows in one step (--pg,
else NOVA_PG_DSN, as nova-config takes it): a member the table lacks is added
at its width (the row's width, nova-config machine set <m> --width <n>; a row
with no width takes half the cores its machine's beat reports), a
width that differs is set, a width of 0 is no member and its row is held and
its cards are dealt to the members that stay; a row with no machine row is held
the same way and removed (its row and width out of the fleet) once no card stays
on it, and comes back when its machine row does; nothing else changes, and a
second sync writes nothing. --check
prints the drift and writes nothing: exit 0 none, 2 some, 3 the config cannot
be read.`) + "\n"
}

// readerWords is how a reader's state comes about, in nova-sprint help and
// nova-sprint help reader.
func readerWords() string {
	return strings.TrimSpace(`
The readers: a reader is a row of the readers table, which the coordinator
declares (init --readers, reader add); no beat and no loop record makes one. A
reader with its row says it is there by asking for its own queue (queue --as
<reader> is its beat); the queue of a name with no row writes none and answers
reader false, and the reader loop says MEMBER NOT A READER. A reader is up while its last beat is under `+sprint.ReaderBeatBound.String()+` old, away
when it beat and has lapsed, down when it has never beaten; reader away holds
one away whatever it beats and reader up releases the hold (the old words of
hold <reader> --return and unhold <reader>: its state reads held). A flash card is
read once and a pro card twice, by two different readers, one read at a time
(the second asked once the first comes back ok), each read on a route of the
card's tier. The ask deals a read to a reader up only: a read asked of a
reader that is not up is asked of another at the next tick, and a card that
needs more readers than are up is not asked: the tick raises one judgment
(fewer than two readers up). A reader row carries the tiers it reads: reader add --tiers
flash[,pro,heavy,frontier] and reader set --tiers. Omitted, all and default
store an empty cell, which means every tier (today's behaviour). The ask
counts a reader only for a primary whose read tier it reads, and never asks
it a read outside those tiers. A card with fewer readers of its tier up than
it needs raises that same judgment, and a returned read is never asked again
in place of a reader outside the tier. where prints the tiers, all when the
cell is empty. reader remove takes a row off the readers table, refused while the
reader holds a read (asked, reading, ok or broken); reader retire keeps the
row and its read cards (the history) and takes the reader off the table for
good: never asked, its queue no beat and reader false, until reader up brings
it back.`) + "\n"
}

// fleetStep is the coordinator's fleet verb as a step: up releases a hold,
// counts as a beat of the member, and brings it up at once when it is alive, and sets its
// width when width is above zero, and drains it at a width of 0; level evens
// the ready queues (fleet down is hold --return, hold.go).
func (a *app) fleetStep(st *store.Store, op, member, who string, width int, drain bool, deadline int, deadlineOff bool) store.Step {
	r := sprint.FleetReq{Op: op, Member: member, Who: who, Width: width, Drain: drain, Deadline: deadline, DeadlineOff: deadlineOff}
	switch op {
	case "up":
		r.Op = "release"
		// a member a sync removed in this epoch comes back with its own control
		// card, placed again before the step reads it (store.RejoinMembers)
		// ignored: a rejoin that failed leaves the card off the table, and the step's create of it is refused by the store, naming the member
		_, _ = st.RejoinMembers(context.Background(), []string{member})
		// the release counts as a beat (docs/SPEC-SPRINT.md section 5): the
		// member's last beat is now, so the next tick within the beat window
		// finds it up
		// ignored: a store that keeps no beats leaves the beat as it was, and the read below says so
		_, _ = st.TouchBeat(context.Background(), member)
		if beats, err := st.Beats(context.Background(), []string{member}); err == nil {
			r.Fresh = beats[member].Alive(a.now())
		}
	}
	return store.FleetStep(r)
}

// beatReport is what fleet beat prints with --json.
type beatReport struct {
	Member string          `json:"member"`
	At     time.Time       `json:"at"`
	Load   float64         `json:"load"`
	Last   float64         `json:"last"`
	How    string          `json:"how"`
	Cores  int             `json:"cores"`
	Files  *hostload.Files `json:"files,omitempty"`
	// StopReturns is how many stop-returns the member's lanes still owe (section 14).
	StopReturns int `json:"stop_returns,omitempty"`
}

// fdBound is one of fleet beat's open-files bounds: the flag's count when given, else the
// environment's, else 0 (the default); a count under 1 is refused.
func (a *app) fdBound(flagged int, given bool, flag, env string) (int, string) {
	if given {
		if flagged < 1 {
			return 0, fmt.Sprintf("--%s wants a count of open file descriptors of at least 1, found %d", flag, flagged)
		}
		return flagged, ""
	}
	v := strings.TrimSpace(a.getenv(env))
	if v == "" {
		return 0, ""
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 0, env + " wants a count of open file descriptors of at least 1, found " + v
	}
	return n, ""
}

func (a *app) cmdFleetBeat(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("fleet beat")
	load := fs.String("load", "", "the load as a percent of all the machine's cores, instead of measuring it (a test's, or another meter's)")
	cores := fs.Int("cores", 0, "the machine's logical cores the beat reports, instead of this machine's own (a test's, or another meter's); a member with the default width takes half")
	stopReturns := fs.Int("stop-returns", 0, "how many stop-returns the member's lanes still owe after the machine's stop (section 14): start waits for zero")
	fdWarn := fs.Int("fd-warn", 0, fmt.Sprintf("the machine's open file descriptors above which the beat says warn and lists the top holders (else NOVA_FD_WARN, else %d)", hostload.FilesWarnDefault))
	fdAlarm := fs.Int("fd-alarm", 0, fmt.Sprintf("the machine's open file descriptors above which the beat says alarm and the tick writes one judgment of the member (else NOVA_FD_ALARM, else %d)", hostload.FilesAlarmDefault))
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "fleet beat", err.Error())
	}
	if len(pos) != 1 {
		return refuse(stderr, "fleet beat", "wants one member")
	}
	var given *float64
	if *load != "" {
		v, err := strconv.ParseFloat(strings.TrimSuffix(*load, "%"), 64)
		if err != nil {
			return refuse(stderr, "fleet beat", "--load wants a percent, found "+*load)
		}
		given = &v
	}
	if *cores < 0 {
		return refuse(stderr, "fleet beat", fmt.Sprintf("--cores wants a count of logical cores of at least 1, found %d", *cores))
	}
	src := a.meter
	if a.serving {
		// A served beat names a remote member: this process cannot read its files.
		src.OpenFiles, src.Holders = nil, nil
	}
	if *cores > 0 {
		src.NCPU = *cores
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	var why string
	if src.FilesWarn, why = a.fdBound(*fdWarn, set["fd-warn"], "fd-warn", "NOVA_FD_WARN"); why != "" {
		return refuse(stderr, "fleet beat", why)
	}
	if src.FilesAlarm, why = a.fdBound(*fdAlarm, set["fd-alarm"], "fd-alarm", "NOVA_FD_ALARM"); why != "" {
		return refuse(stderr, "fleet beat", why)
	}
	if warn, alarm := hostload.FilesBounds(src); alarm < warn {
		return refuse(stderr, "fleet beat", fmt.Sprintf("--fd-alarm wants a count at or above the warn bound %d, found %d", warn, alarm))
	}
	if given != nil {
		// a load given is the beat's; the machine's open files are measured beside it
		// (hostload.Source.Given), so a member that gives its one-second samples still
		// reports them
		if *given < 0 || *given > hostload.MaxPercent {
			fmt.Fprintf(stderr, "%s fleet beat: %s\n", prog, oneline.Escape(fmt.Sprintf("a load is a percent from 0 to %v, found %v", hostload.MaxPercent, *given)))
			return 1
		}
		v := *given
		src.Given = func() (float64, bool) { return v, true }
	}
	c.orActor(pos[0])
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "fleet beat", err.Error())
	}
	var owing *int
	if set["stop-returns"] {
		if *stopReturns < 0 {
			return refuse(stderr, "fleet beat", "--stop-returns wants a whole number of at least 0")
		}
		v := *stopReturns
		owing = &v
	}
	b, err := st.BeatOwing(context.Background(), pos[0], nil, src, owing)
	if err != nil {
		fmt.Fprintf(stderr, "%s fleet beat: %s\n", prog, oneline.Escape(err.Error()))
		return 1
	}
	last := 0.0
	if n := len(b.Samples); n > 0 {
		last = b.Samples[n-1].Pct
	}
	files := b.Meter.Files
	if c.json {
		out, _ := json.Marshal(beatReport{Member: pos[0], At: b.At, Load: b.Load, Last: last, How: b.How, Cores: b.Cores, Files: files, StopReturns: b.StopReturns})
		fmt.Fprintln(stdout, string(out))
		return 0
	}
	fmt.Fprintf(stdout, "FLEET-BEAT OK %s at=%s load=%.1f%% last=%.1f%% how=%s cores=%d", pos[0], b.At.Format(time.RFC3339), b.Load, last, b.How, b.Cores)
	if files != nil {
		fmt.Fprintf(stdout, " fds=%d fds-max=%d fds-level=%s", files.Open, files.Max, files.Level())
	}
	if b.StopReturns > 0 {
		fmt.Fprintf(stdout, " stop_returns=%d", b.StopReturns)
	}
	fmt.Fprintln(stdout)
	if files != nil && files.Level() != hostload.LevelOK {
		if files.TopErr != "" {
			fmt.Fprintf(stdout, "  holders not listed: %s\n", oneline.Escape(files.TopErr))
		}
		for _, h := range files.Top {
			fmt.Fprintf(stdout, "  %s\n", oneline.Escape(h.String()))
		}
	}
	return 0
}

// The fleet add verb (docs/FLEET.md, "Adding a member"): one verb that runs one
// play, fleet/member.yml, and does a member end to end. The play converges the
// machine (the pinned tools, the nova binaries at the adopted release, the
// member and reader loop units and their records, the route credential through
// the sealed-secrets path, and a mirror for every repository a live card
// names); the verb adds the fleet and reader rows to the store, the member
// drained, and checks the result: the member beats within a minute, its reader
// row is up, and the play has finished one probe card. Only then does the verb
// widen the member, so until the check passes the member is dealt no work.
// Each step prints one FLEET-ADD line, done or the refusal with its remedy; a
// second run changes nothing; --dry-run lists every step and writes nothing.
func init() {
	notServed = append(notServed, "fleet add")
	verbClasses["fleet add"] = classMachine
	verbExit["fleet add"] = "exit codes: 0 every step ran and the member is proved and dealt work (or, with --dry-run, every step was listed), 1 a step refused, the play ended without a step's line, or the member did not prove (it is left drained, width 0), 2 usage"
	verbEffect["fleet add"] = "local and remote writes through ansible-playbook: it adds the member drained (width 0) and its reader row to the store, runs fleet/member.yml for the one host (the pinned tools, the nova binaries at the adopted release, the member and reader loop units and their records, the route credential through the sealed-secrets path, and a mirror for every repository a live card names), checks that the member beats within a minute and its reader row is up, and only then widens the member; --dry-run runs the play with --check, lists every step and writes nothing"
	// the verb's row is installed here, not in verbs.go, so the whole fleet add
	// lives in the file its card's PATHS name
	installVerbs = append(installVerbs, verb{
		name:    "fleet add",
		syntax:  "<host> --width <n> [--dry-run] --source <checkout> --inventory <file>",
		example: "fleet add bench-a --width 64 --source . --inventory ./nova-inventory --dry-run",
		run:     (*app).cmdFleetAdd,
	})
}

// fleetAddSteps are the steps of adding a member end to end, in order: the
// machine steps the plays print (fleet/member.yml and the fleet/tools.yml it
// imports), and the store and check steps the verb does. Every one prints
// exactly one FLEET-ADD line.
var fleetAddSteps = []string{"rows", "tools", "binaries", "units", "credential", "mirrors", "probe", "beat", "reader"}

// fleetAddPlaySteps are the steps the plays must print: member.yml prints the
// tools, units, credential, mirrors and probe steps, and tools.yml prints the
// binaries step (the verb does rows, beat and reader itself).
var fleetAddPlaySteps = []string{"tools", "binaries", "units", "credential", "mirrors", "probe"}

// fleetAddSetupSteps are the steps the setup run of fleet/member.yml (the one
// that converges the machine and starts the member and reader loops) must
// print: the probe step is skipped there, because the member is not dealt a
// probe until its loop has genuinely beaten (nova_member_probe=0).
var fleetAddSetupSteps = []string{"tools", "binaries", "units", "credential", "mirrors"}

// fleetAddProbeStep is the one step the probe run of fleet/member.yml prints,
// after the verb has dealt the member its probe card (nova_member_probe=1).
const fleetAddProbeStep = "probe"

// fleetAddBeatBound is how long a just-added member's beat is allowed to take:
// its loop has a minute to start and beat.
const fleetAddBeatBound = time.Minute

// fleetAddBeatPoll is how long the verb waits between reads of the member's own
// beat and its reader row while a just-added member's loops start: the setup
// play's loops load asynchronously and never wait for a beat, so the verb polls
// the store on its own clock up to fleetAddBeatBound before refusing.
const fleetAddBeatPoll = 2 * time.Second

var (
	fleetAddLine    = regexp.MustCompile(`"FLEET-ADD (?:[^"\\]|\\.)*"`)
	fleetAddStepRe  = regexp.MustCompile(`\bstep=(\S+)`)
	fleetAddTaskRe  = regexp.MustCompile(`(?m)^(?:TASK|RUNNING HANDLER) \[([^\]]*)\]`)
	fleetAddFatalRe = regexp.MustCompile(`(?m)^(?:fatal|failed): .*$`)
)

// fleetAddPlayOf is a test's play runner for one app (*app to release.Ansible).
var fleetAddPlayOf sync.Map

// fleetAddReport is what the play's output says: its FLEET-ADD lines, the
// steps the host printed, and the first refusal.
type fleetAddReport struct {
	lines   []string
	steps   map[string]bool
	refused string
}

// readFleetAdd reads the play's FLEET-ADD lines: the lines as printed, the step
// each names, and the first refusal. An ansible debug message is a JSON string,
// so a quoted line is unquoted first (as readAdopt does).
func readFleetAdd(output string) fleetAddReport {
	r := fleetAddReport{steps: map[string]bool{}}
	for _, q := range fleetAddLine.FindAllString(output, -1) {
		var text string
		if json.Unmarshal([]byte(q), &text) != nil {
			text = strings.Trim(q, `"`)
		}
		l := strings.Join(strings.Fields(text), " ")
		if len(r.lines) > 0 && r.lines[len(r.lines)-1] == l {
			continue
		}
		r.lines = append(r.lines, l)
		if strings.HasPrefix(l, "FLEET-ADD REFUSED ") {
			if r.refused == "" {
				r.refused = strings.TrimPrefix(l, "FLEET-ADD REFUSED ")
			}
			continue
		}
		if m := fleetAddStepRe.FindStringSubmatch(l); m != nil {
			r.steps[m[1]] = true
		}
	}
	return r
}

// fleetAddFailedStep is the step of the play task that failed: the word before
// the colon of the last TASK header before the first fatal line, or "play" when
// that names no step of the play.
func fleetAddFailedStep(output string) string {
	fatal := fleetAddFatalRe.FindStringIndex(output)
	if fatal == nil {
		return "play"
	}
	tasks := fleetAddTaskRe.FindAllStringSubmatch(output[:fatal[0]], -1)
	if len(tasks) == 0 {
		return "play"
	}
	step, _, _ := strings.Cut(tasks[len(tasks)-1][1], ":")
	if !slices.Contains(fleetAddPlaySteps, step) {
		return "play"
	}
	return step
}

// fleetAddInputs are the values fleet add supplies to fleet/member.yml from the
// store when the caller names none: the sealed secret names the member's routes
// read (routeKey), the repository names its mirrors cover (repos) and the url
// each is fetched from (mirrorBase).
type fleetAddInputs struct {
	routeKey   string
	repos      []string
	mirrorBase string
}

// fleetAddDerive reads the store for what a member add supplies itself: the live
// work cards name the repositories the member must mirror, and the enabled
// routes name the credentials it must be able to read. A value the caller names
// (--route-key, --repos, --mirror-base) wins over the derived one, so an
// operator can stand in where the store names none.
func (a *app) fleetAddDerive(ctx context.Context, st *store.Store, routeKey, repos, mirrorBase string) (fleetAddInputs, error) {
	in := fleetAddInputs{routeKey: routeKey, mirrorBase: mirrorBase}
	if routeKey == "" {
		derived, err := a.fleetAddRouteKey(ctx, st)
		if err != nil {
			return in, err
		}
		in.routeKey = derived
	}
	names, bases, err := a.fleetAddMirrors(ctx, st)
	if err != nil {
		return in, err
	}
	if repos == "" {
		in.repos = names
	} else {
		in.repos = splitCSV(repos)
	}
	if in.mirrorBase == "" && len(in.repos) > 0 {
		base := ""
		for _, n := range in.repos {
			b, ok := bases[n]
			if !ok {
				return in, fmt.Errorf("--repos names %s, which no live work card names, so its base is unknown; name the base with --mirror-base <url>, or admit a card whose REPO: line names it", n)
			}
			if base == "" {
				base = b
			} else if base != b {
				return in, fmt.Errorf("the repositories to mirror live under more than one base (%s and %s); one mirror fetches one base", base, b)
			}
		}
		in.mirrorBase = base
	}
	return in, nil
}

// fleetAddMirrors reads the live work cards' briefs for the repositories a
// member must mirror: each repository name and the base url its REPO: line
// resolves to (<base>/<name>.git, the shape nova-swarm mirror takes). A
// repository two live cards name under two bases is a refusal: one mirror
// fetches one base.
func (a *app) fleetAddMirrors(ctx context.Context, st *store.Store) (names []string, bases map[string]string, err error) {
	snap, err := st.Load(ctx, []string{sprint.Work}, nil)
	if err != nil {
		return nil, nil, err
	}
	bases = map[string]string{}
	for _, c := range snap.Work.Column(sprint.Waiting, sprint.Ready, sprint.Working, sprint.Review, sprint.Merging) {
		cb := swarm.ReadCardBase([]byte(c.F("brief")))
		name, base := mirrorNameBase(cb.Repo)
		if name == "" {
			continue
		}
		if prev, ok := bases[name]; ok && prev != base {
			return nil, nil, fmt.Errorf("the live cards name %s under two bases (%s and %s); one mirror fetches one base", name, prev, base)
		}
		bases[name] = base
	}
	for n := range bases {
		names = append(names, n)
	}
	slices.Sort(names)
	return names, bases, nil
}

// mirrorNameBase is a repository clone url as nova-swarm mirror takes it: the
// repository name and the base url it is fetched from, <base>/<name>.git. ""
// when the url names no repository under a base.
func mirrorNameBase(repo string) (name, base string) {
	r := strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(repo), "/"), ".git")
	i := strings.LastIndex(r, "/")
	if i <= 0 || i == len(r)-1 {
		return "", ""
	}
	return r[i+1:], r[:i]
}

// fleetAddRouteKey is the sealed secret names the member's routes read: the
// provider key of every enabled route (<PROVIDER>_API_KEY), in name order,
// comma separated. A provider whose key name is no secret name is skipped (a
// headless harness's subscription login holds no such secret); "" when no route
// needs one.
func (a *app) fleetAddRouteKey(ctx context.Context, st *store.Store) (string, error) {
	routes, _, err := st.Routes(ctx)
	if err != nil {
		return "", err
	}
	seen := map[string]bool{}
	var keys []string
	for _, r := range routes {
		if !r.Enabled || r.Provider == "" {
			continue
		}
		k := strings.ToUpper(r.Provider) + "_API_KEY"
		if !secrets.IsValidEnvVar(k) || seen[k] {
			continue
		}
		seen[k] = true
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return strings.Join(keys, ","), nil
}

func (a *app) cmdFleetAdd(args []string, stdout, stderr io.Writer) int {
	const name = "fleet add"
	fs, c := a.verbSetup(name)
	width := fs.String("width", "", fmt.Sprintf("the member's width once it is proved: the most work cards it runs at once; the member is added drained and takes no work until it beats, its reader row is up and a probe card is done; 1 to %d", sprint.MaxWidth))
	source := fs.String("source", "", "the nova-tools checkout whose fleet/member.yml is the play")
	inventory := fs.String("inventory", a.getenv("NOVA_INVENTORY"), "the inventory the play reads, the nova-inventory script (else NOVA_INVENTORY)")
	ansible := fs.String("ansible", "ansible-playbook", "the ansible-playbook binary")
	routeKey := fs.String("route-key", "", "the sealed secret names the member's routes read, comma separated, read in the seat's own process; else derived from the store's enabled routes")
	repos := fs.String("repos", "", "the repository names the member mirrors, a comma-separated list; else every repository a live work card names")
	mirrorBase := fs.String("mirror-base", "", "the url each mirrored repository is fetched from, <base>/<name>.git; else derived from the repositories the live work cards name")
	dry := fs.Bool("dry-run", false, "run the play with --check, list every step and write nothing")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if len(pos) != 1 {
		return refuse(stderr, name, "wants one <host>")
	}
	host := pos[0]
	if !sprint.ValidID(host) {
		return refuse(stderr, name, "a host name wants letters, digits, _ and -: "+oneline.Escape(host))
	}
	if strings.TrimSpace(*width) == "" {
		return refuse(stderr, name, "--width names the member's width, 1 to "+strconv.Itoa(sprint.MaxWidth))
	}
	w, err := sprint.ParseWidth(*width)
	if err != nil {
		return refuse(stderr, name, "--width: "+err.Error())
	}
	play := filepath.Join(*source, "fleet", "member.yml")
	switch {
	case *source == "":
		return refuse(stderr, name, "--source names the nova-tools checkout whose fleet/member.yml is the play")
	case *inventory == "":
		return refuse(stderr, name, "--inventory (or NOVA_INVENTORY) names the inventory the play reads")
	}
	if _, err := os.Stat(play); err != nil {
		// an input that does not read, not a usage: exit 1, nothing run
		fmt.Fprintf(stderr, "%s fleet add REFUSED step=play: --source %s holds no fleet/member.yml (%s); nothing was run; run: nova-sprint fleet add -h\n", prog, oneline.Escape(*source), oneline.Err(err))
		return 1
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	ctx := context.Background()
	reader := "reader-" + host
	again := "fix the cause and run the same fleet add again (it changes only what is still missing or stale)"
	// the run's own variables, from the store or the caller: the route credential
	// the member reads, and the repository mirrors every live card names. The
	// play's credential and mirror steps are driven by these; without them the
	// member is set up with neither.
	inputs, err := a.fleetAddDerive(ctx, st, *routeKey, *repos, *mirrorBase)
	if err != nil {
		fmt.Fprintf(stderr, "%s fleet add REFUSED step=inputs host=%s: %s; nothing was run; %s; run: nova-sprint fleet add -h\n", prog, host, oneline.Err(err), again)
		return 1
	}
	// the probe card this run deals the member: the member is widened to place
	// it, so every refusal after the deal is attempted drains it again via
	// fleetAddUndo (proven is set once the member is widened for real)
	probePrimary, proven := "", false
	defer func() {
		if probePrimary != "" && !proven {
			a.fleetAddUndo(ctx, st, host, probePrimary)
		}
	}()
	// the play's argv: the machine, its width and its reader, and the run's own
	// route key and mirrors. nova_member_probe selects the run: 0 converges the
	// machine and starts the loops with the probe step skipped; 1 runs the probe
	// the verb dealt, once the member's own loop has genuinely beaten.
	argv := []string{"-i", *inventory, play, "-e", "nova_member=" + host,
		"-e", "nova_member_width=" + strconv.Itoa(w), "-e", "nova_member_reader=" + reader, "--limit", host}
	if inputs.routeKey != "" {
		argv = append(argv, "-e", "nova_member_route_key="+inputs.routeKey)
	}
	if len(inputs.repos) > 0 {
		argv = append(argv, "-e", "nova_member_repos="+strings.Join(inputs.repos, ","), "-e", "nova_member_mirror_base="+inputs.mirrorBase)
	}
	var runner release.Ansible = release.ExecAnsible{Path: *ansible}
	if fake, ok := fleetAddPlayOf.Load(a); ok {
		runner = fake.(release.Ansible)
	}

	if *dry {
		// the plan the real run would take: the play's own steps, then the
		// store and check steps the verb does; nothing is written
		output, _ := runner.Play(ctx, append(append([]string(nil), argv...), "-e", "nova_member_probe=1", "--check")) // ignored: a dry run lists the plan whether or not the check play answers
		for _, l := range readFleetAdd(output).lines {
			if !strings.HasPrefix(l, "FLEET-ADD REFUSED ") {
				fmt.Fprintln(stdout, oneline.Escape(l))
			}
		}
		for _, step := range fleetAddSteps {
			fmt.Fprintf(stdout, "FLEET-ADD WOULD host=%s step=%s\n", host, step)
		}
		fmt.Fprintf(stdout, "FLEET-ADD WOULD-ADD host=%s width=%d steps=%s\n", host, w, strings.Join(fleetAddSteps, ","))
		return 0
	}

	// the store rows, the member drained (width 0) and no beat of its own, before
	// the play starts its loops: no deal and no probe reaches it until its own
	// loop has beaten and the check below widens it
	added, err := a.fleetAddRows(ctx, st, host, reader)
	if err != nil {
		fmt.Fprintf(stderr, "%s fleet add REFUSED step=rows host=%s: %s; %s; run: nova-sprint fleet add -h\n", prog, host, oneline.Err(err), again)
		return 1
	}
	rowsWord := "already there"
	if added {
		rowsWord = "added"
	}
	fmt.Fprintf(stdout, "FLEET-ADD step=rows host=%s done member=%s reader=%s (%s, the member drained)\n", host, host, reader, rowsWord)

	// every play line is printed once: the setup run and the probe run print the
	// machine steps both times
	seen := map[string]bool{}
	printPlay := func(output string) {
		for _, l := range readFleetAdd(output).lines {
			if strings.HasPrefix(l, "FLEET-ADD REFUSED ") || seen[l] {
				continue
			}
			seen[l] = true
			fmt.Fprintln(stdout, oneline.Escape(l))
		}
	}

	// phase one: the machine and its loops, the probe step skipped. The verb
	// deals no probe until the member's own loop has genuinely beaten, so a host
	// whose loop never runs is never dealt a probe and never widened.
	output, playErr := runner.Play(ctx, append(append([]string(nil), argv...), "-e", "nova_member_probe=0"))
	printPlay(output)
	if code := a.fleetAddPlayRefusal(host, again, output, playErr, fleetAddSetupSteps, stderr); code != 0 {
		return code
	}

	// the proof: the member's beat must be genuine and recent and its reader row
	// up, observed from the store, before the probe is dealt. A member with no
	// beat, or one whose only beat is stale, is left drained: the setup's own
	// words are never taken for its machine's.
	if step, err := a.fleetAddBeatReady(ctx, st, host, reader); err != nil {
		fmt.Fprintf(stderr, "%s fleet add REFUSED step=%s host=%s: %s; the member is left drained (width 0); %s; run: nova-sprint fleet add -h\n", prog, step, host, oneline.Err(err), again)
		return 1
	}

	// the probe: dealt now the member is genuinely up, so its loop can take it.
	// A probe already finished (its primary in Review) is left as it is, so a
	// second run changes nothing.
	probePrimary, dealt, err := a.fleetAddProbe(ctx, st, host)
	if err != nil {
		fmt.Fprintf(stderr, "%s fleet add REFUSED step=probe host=%s: %s; the member is left drained (width 0); %s; run: nova-sprint fleet add -h\n", prog, host, oneline.Err(err), again)
		return 1
	}

	// phase two: the probe run takes and finishes the card the verb dealt
	if dealt {
		output, playErr = runner.Play(ctx, append(append([]string(nil), argv...), "-e", "nova_member_probe=1"))
		printPlay(output)
		if code := a.fleetAddPlayRefusal(host, again, output, playErr, []string{fleetAddProbeStep}, stderr); code != 0 {
			return code
		}
	}

	// the check: the member beat within a minute, its reader row is up, and it
	// has taken and finished the probe card the deal gave it
	if step, err := a.fleetAddProve(ctx, st, host, reader, probePrimary); err != nil {
		fmt.Fprintf(stderr, "%s fleet add REFUSED step=%s host=%s: %s; the member is left drained (width 0); %s; run: nova-sprint fleet add -h\n", prog, step, host, oneline.Err(err), again)
		return 1
	}
	fmt.Fprintf(stdout, "FLEET-ADD step=beat host=%s done at=%s\n", host, a.now().UTC().Format(time.RFC3339))
	fmt.Fprintf(stdout, "FLEET-ADD step=reader host=%s done reader=%s state=%s\n", host, reader, sprint.ReaderUp)

	// proved: widen the member so the deal reaches it
	widened, err := a.fleetAddWiden(ctx, st, host, w)
	if err != nil {
		fmt.Fprintf(stderr, "%s fleet add REFUSED step=width host=%s: %s; the member is left drained (width 0); %s; run: nova-sprint fleet add -h\n", prog, host, oneline.Err(err), again)
		return 1
	}
	proven = true
	word := "unchanged"
	if widened {
		word = "widened"
	}
	fmt.Fprintf(stdout, "FLEET-ADD OK host=%s width=%d steps=%s (%s)\n", host, w, strings.Join(fleetAddSteps, ","), word)
	return 0
}

// fleetAddRows adds the reader row and, when the member has none, the member's
// row drained (width 0) and down until it beats: a member at width 0 takes no
// new deal, so it is dealt no work until fleetAddWiden widens it. The step is a
// release, never an up, so it writes no beat of its own: a member that has never
// beaten, or whose only beat is stale, stays down (store.TouchBeat touches a
// beat that is there, and a release does not call it), and the setup play's
// loops are the only thing that brings it up. It says whether it wrote
// anything, so a second run changes nothing.
func (a *app) fleetAddRows(ctx context.Context, st *store.Store, host, reader string) (bool, error) {
	changed := false
	rows, err := st.ReaderRows(ctx)
	if err != nil {
		return false, err
	}
	if !slices.Contains(rows, reader) {
		if err := st.B.RowsAdd(ctx, st.Names.Table(sprint.Readers), []string{reader}); err != nil {
			return false, err
		}
		changed = true
	}
	snap, err := st.Load(ctx, []string{sprint.Fleet}, nil)
	if err != nil {
		return changed, err
	}
	if !snap.Fleet.HasRow(host) {
		res, err := st.Run(ctx, a.fleetStep(st, "release", host, st.Actor, 0, true, 0, false))
		if err != nil {
			return changed, err
		}
		if len(res.Refused) > 0 {
			return changed, fmt.Errorf("%s: %s", res.Refused[0].Key, res.Refused[0].Why)
		}
		changed = true
	}
	return changed, nil
}

// fleetAddBeatReady is the proof the setup run's loops must have left before the
// verb deals the probe: the member's own beat is recent (within
// fleetAddBeatBound) and its reader row is up, both read from the store, never
// from the setup's own words. The member is added with no beat of its own and
// the setup pass writes none, so a beat read here is the member loop's. The
// setup play starts the member and reader units in fleet/loops.yml, which loads
// them asynchronously and never waits for a beat, so the verb polls the store on
// its own clock up to fleetAddBeatBound before refusing: a member that has never
// beaten, or whose only beat is stale, names its step ("beat" or "reader") and
// is refused, left drained.
func (a *app) fleetAddBeatReady(ctx context.Context, st *store.Store, host, reader string) (string, error) {
	began := a.now()
	for {
		beats, err := st.Beats(ctx, []string{host})
		if err != nil {
			return "beat", err
		}
		b := beats[host]
		fresh := b.Beaten() && a.now().Sub(b.At) <= fleetAddBeatBound
		states, err := st.ReaderStates(ctx, []string{reader}, a.now())
		if err != nil {
			return "reader", err
		}
		if fresh && states[reader] == sprint.ReaderUp {
			return "", nil
		}
		if a.now().Sub(began) >= fleetAddBeatBound {
			if !fresh {
				return "beat", fmt.Errorf("the member has not beat within %s; its member loop is not running on %s", fleetAddBeatBound, host)
			}
			return "reader", fmt.Errorf("the reader row %s is %s, not up; its reader loop is not running on %s", reader, states[reader], host)
		}
		a.sleep(fleetAddBeatPoll)
	}
}

// fleetAddPlayRefusal is one play run's refusal: a FLEET-ADD REFUSED line the
// play printed, the play stopping (the failed step named and its fatal lines), or
// a step the run owed whose line is missing. It prints the refusal with its
// remedy and returns 1; 0 when the run was clean.
func (a *app) fleetAddPlayRefusal(host, again, output string, playErr error, steps []string, stderr io.Writer) int {
	r := readFleetAdd(output)
	switch {
	case r.refused != "":
		fmt.Fprintf(stderr, "%s fleet add REFUSED %s; the member is left drained (width 0); %s; run: nova-sprint fleet add -h\n", prog, oneline.Escape(r.refused), again)
		return 1
	case playErr != nil:
		step := fleetAddFailedStep(output)
		for _, l := range fleetAddFatalRe.FindAllString(output, 5) {
			fmt.Fprintln(stderr, oneline.Escape(truncateLine(l, 300)))
		}
		fmt.Fprintf(stderr, "%s fleet add REFUSED step=%s host=%s: the play stopped (%s); the member is left drained (width 0); %s; run: nova-sprint fleet add -h\n", prog, step, host, oneline.Err(playErr), again)
		return 1
	}
	for _, step := range steps {
		if !r.steps[step] {
			fmt.Fprintf(stderr, "%s fleet add REFUSED step=%s host=%s: the play ended without this step's line; the member is left drained (width 0); %s; run: nova-sprint fleet add -h\n", prog, step, host, again)
			return 1
		}
	}
	return 0
}

// fleetAddProbe deals the probe to a just-added member outside its width: the
// member is briefly widened to one so the deal reaches it, and two probe cards
// on its own stream are admitted and dealt to it. Two fill the member's room
// (DealAhead times width 1, width.go): while the setup runs the member holds no
// ordinary work and an interleaved tick deals it none, so it is dealt no work
// until it is proved. Both cards carry the host's BENCH line, so only the member
// is ever dealt them. The play's member loop takes and finishes the primary
// (-1); the holder (-2) is dropped once the member is proved. A probe already
// finished (its primary in Review) is left as it is, so a second run changes
// nothing; a probe already dealt waits only for the member to take it. It
// returns the probe card's primary id, which fleetAddProve requires finished
// before the member is widened for real, and dealt: whether the probe run of
// the play must run to take it. A primary already taken (in Review) is left as
// it is, so a second run changes nothing and dealt is false. Once the member is
// widened, an error returns the primary id with it, so cmdFleetAdd's defer
// drains the member and drops the probe cards.
func (a *app) fleetAddProbe(ctx context.Context, st *store.Store, host string) (string, bool, error) {
	stream := "probe-" + host
	primary, holder, reserve := stream+"-1", stream+"-2", stream+"-3"
	snap, err := st.Load(ctx, []string{sprint.Work}, nil)
	if err != nil {
		return "", false, err
	}
	dealt := true
	needDeal := false
	switch c := snap.Work.Card(primary); {
	case c == nil:
		// the machine runs, so the add and the deal write the work table
		// directly (Pump), the tick's own first update, instead of queuing for
		// the next tick: the probes must be placed before the play runs. Two
		// holders keep the width-one room full after the primary finishes.
		step := store.AddStep(sprint.AddReq{Stream: stream, IDs: []string{primary, holder, reserve}, Brief: fleetAddProbeBrief(host)})
		step.Pump = true
		res, err := st.Run(ctx, step)
		if err != nil {
			return "", false, err
		}
		if len(res.Refused) > 0 {
			return "", false, fmt.Errorf("%s: %s", res.Refused[0].Key, res.Refused[0].Why)
		}
		needDeal = true
	case c.Col == sprint.Ready:
		needDeal = true
	case c.Col == sprint.Review:
		return primary, false, nil // taken and finished already: a second run changes nothing
	}
	// Widen to two to place the primary and both holders, then return to one:
	// the member takes only its primary, while the two holders fill its room.
	// The widen comes after fleetAddBeatReady, so the member is genuinely up and
	// the deal reaches it. From here the member may be widened, so every error
	// returns the primary id: cmdFleetAdd's defer drains the member (width 0) and
	// drops the probe cards, so a member that is not proved is dealt no work even
	// when the deal or the return to one is refused.
	if res, err := st.Run(ctx, a.fleetStep(st, "up", host, st.Actor, 2, false, 0, false)); err != nil {
		return primary, dealt, err
	} else if len(res.Refused) > 0 {
		return primary, dealt, fmt.Errorf("%s: %s", res.Refused[0].Key, res.Refused[0].Why)
	}
	if needDeal {
		res, err := st.Run(ctx, fleetAddDealStep(sprint.DealReq{Sel: sprint.Sel{Only: []string{primary, holder, reserve}}}))
		if err != nil {
			return primary, dealt, err
		}
		if len(res.Refused) > 0 {
			return primary, dealt, fmt.Errorf("%s: %s", res.Refused[0].Key, res.Refused[0].Why)
		}
	}
	if res, err := st.Run(ctx, a.fleetStep(st, "up", host, st.Actor, 1, false, 0, false)); err != nil {
		return primary, dealt, err
	} else if len(res.Refused) > 0 {
		return primary, dealt, fmt.Errorf("%s: %s", res.Refused[0].Key, res.Refused[0].Why)
	}
	return primary, dealt, nil
}

// fleetAddDealStep cuts and deals the probe card by hand, the step the tick's
// deal (sprint.TickDeal) runs: a targeted selection, so no other ready card is
// dealt while the member is proved. It is a pump step (the machine runs), so
// the primary's move to working applies at once, not at the next tick.
func fleetAddDealStep(r sprint.DealReq) store.Step {
	return store.Step{Args: store.ArgsOf(r), Verb: "deal", Load: []string{sprint.Work, sprint.Fleet, sprint.Merge}, Mirrors: true, Routes: true, Pump: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Deal(s, r) }}
}

// fleetAddUndo undoes a refused add's probe: the probe cards (the primary and
// the holder that reserved the member) are taken off the table and the member is
// drained again (width 0), so a member that did not prove leaves no probe card
// behind and is dealt no work. It runs from cmdFleetAdd's defer on every refusal
// after the member was widened for the probe: the widen-to-place step, the deal,
// the return to one, the probe run, and the proof.
func (a *app) fleetAddUndo(ctx context.Context, st *store.Store, host, probe string) {
	holder, reserve := "probe-"+host+"-2", "probe-"+host+"-3"
	drop := store.DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{probe, holder, reserve}}, Reason: "the probe cards of a member add that did not finish"})
	drop.Pump = true
	_, _ = st.Run(ctx, drop)                                                     // ignored: the drain below leaves the member drained whether or not the drop moves
	_, _ = st.Run(ctx, a.fleetStep(st, "up", host, st.Actor, 0, true, 0, false)) // ignored: the refusal stands; the next run drains the member again
}

// fleetAddProbeBrief is the probe card's brief: one card whose only work is to
// be taken and finished, proving this member reaches the sprint and reports a
// finish. Its BENCH line names the host, so the probe is dealt only to the
// member being proved; its model, tokens and deadline are drawn from the fleet's
// routes.
func fleetAddProbeBrief(host string) string {
	return fmt.Sprintf(`RESULT: fleet-add-probe
KIND: probe
BASE: dev
PATHS: RESULT.md
TEST: none
DEPENDS-ON: none
BENCH: %s
WHO: any
DONE-WHEN: RESULT.md exists
`, host)
}

// fleetAddProve is the check a just-added member passes before it is dealt
// work: its beat is within fleetAddBeatBound, its reader row is up, and it has
// taken and finished the probe card (its primary in Review). It names the step
// that failed ("beat", "reader" or "probe") and why.
func (a *app) fleetAddProve(ctx context.Context, st *store.Store, host, reader, probe string) (string, error) {
	beats, err := st.Beats(ctx, []string{host})
	if err != nil {
		return "beat", err
	}
	b, ok := beats[host]
	if !ok || !b.Beaten() || a.now().Sub(b.At) > fleetAddBeatBound {
		return "beat", fmt.Errorf("the member has not beat within %s; its member loop is not running on %s", fleetAddBeatBound, host)
	}
	states, err := st.ReaderStates(ctx, []string{reader}, a.now())
	if err != nil {
		return "reader", err
	}
	if states[reader] != sprint.ReaderUp {
		return "reader", fmt.Errorf("the reader row %s is %s, not up; its reader loop is not running on %s", reader, states[reader], host)
	}
	// the probe: the member took and finished the card the deal gave it; the
	// play's member pass reports the finish before it returns, and the verb
	// reads the store
	snap, err := st.Load(ctx, []string{sprint.Work}, nil)
	if err != nil {
		return "probe", err
	}
	c := snap.Work.Card(probe)
	if c == nil || c.Col != sprint.Review {
		state := "-"
		if c != nil {
			state = c.Col
		}
		return "probe", fmt.Errorf("the member has not taken and finished the probe card %s (it is %s); its member loop must reach the sprint and report the probe's finish; run: nova-swarm member --as %s --server <address:port> --once, then run fleet add again", probe, state, host)
	}
	return "", nil
}

// fleetAddWiden sets the member's width to w once it is proved, and drops the
// holder probe card that reserved the member during setup (the primary in
// Review is finished and stays as the proof). A member already at w is left as
// it is, so a second run writes nothing; it says whether it wrote the width.
func (a *app) fleetAddWiden(ctx context.Context, st *store.Store, host string, w int) (bool, error) {
	holder, reserve := "probe-"+host+"-2", "probe-"+host+"-3"
	drop := store.DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{holder, reserve}}, Reason: "the fleet add setup is complete"})
	drop.Pump = true
	_, _ = st.Run(ctx, drop) // ignored: the holder is dropped; the widen below sets the width whether or not it was there
	snap, err := st.Load(ctx, []string{sprint.Fleet}, nil)
	if err != nil {
		return false, err
	}
	changed := false
	if ctl := snap.MemberCtl(host); ctl == nil || sprint.MemberWidth(ctl) != w {
		res, err := st.Run(ctx, a.fleetStep(st, "up", host, st.Actor, w, false, 0, false))
		if err != nil {
			return false, err
		}
		if len(res.Refused) > 0 {
			return false, fmt.Errorf("%s: %s", res.Refused[0].Key, res.Refused[0].Why)
		}
		changed = true
	}
	// Only after proof do we drop the reserved holder cards and widen the member.
	return changed, nil
}
