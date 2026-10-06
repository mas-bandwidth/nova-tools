package sprint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The adoption pipeline (docs/SPRINT-COORDINATOR.md, "Adoption is a pipeline";
// adoption-is-a-pipeline-b.w2). On 2026-10-04 and 05 the live server ran a
// side-branch build for a day while the base moved ahead, because every
// adoption was done by hand: a build on a bench, rollback copies, a cold read,
// a switch, a fleet push, each waiting on the coordinator remembering. Adopt
// is that list as one machine. A pass starts it whenever the base tip is not
// the commit the live build was made from, and moves it as far as it can go:
//
//	idle -> built -> checked -> reading -> asked -> watching -> adopted
//	                                          |         `-> rolled-back
//	                                          `-> declined
//	(any stage before the switch) -> blocked
//
// Every stage is one call on AdoptSteps, so a test runs the whole line on
// fakes and no clock but Now. The only human-shaped stage is the one
// judgment: switch, yes or no, with the canary, shadow and cold-read evidence.
// On yes the pass keeps the rollback copies, switches the server and the
// friends' daemons, pushes to every machine row (funded or not), reads each
// version back, and watches the server's ticks: missed ticks roll it back by
// itself. A pass never waits: a stage that is not done yet (the cold read
// still out, no answer yet) is where the next pass picks it up.

// Adoption stages, the record's Stage.
const (
	AdoptIdle       = "idle"        // nothing in flight: the live build is the base tip, or nothing has started
	AdoptBuilt      = "built"       // every tool built from the tip on a bench
	AdoptChecked    = "checked"     // the canary and the shadow tick passed
	AdoptReading    = "reading"     // the cold-read card is dealt to a friend
	AdoptAsked      = "asked"       // the one judgment is open
	AdoptWatching   = "watching"    // switched and pushed; the server's ticks are watched
	AdoptAdopted    = "adopted"     // the watch passed: the tip is the live build
	AdoptRolledBack = "rolled-back" // missed ticks: the kept copies are back in place
	AdoptDeclined   = "declined"    // the judgment said no: the live build stays
	AdoptBlocked    = "blocked"     // a stage failed before the switch: Blocked names it
)

// AdoptDone says the stage ends the pipeline for its tip: a new pass starts
// again only when the base moves to another tip.
func AdoptDone(stage string) bool {
	switch stage {
	case AdoptAdopted, AdoptRolledBack, AdoptDeclined, AdoptBlocked:
		return true
	}
	return false
}

// AdoptBuild is what the build stage made: the tip it was built from, the
// release version it carries, and where its binaries are.
type AdoptBuild struct {
	Tip     string `json:"tip"`
	Version string `json:"version"`
	Dir     string `json:"dir"`
}

// AdoptRead is the cold-read card's state: Done once the friend has finished
// it, OK when the read found nothing broken, Finding the reader's words.
type AdoptRead struct {
	Done    bool   `json:"done"`
	OK      bool   `json:"ok"`
	Finding string `json:"finding,omitempty"`
}

// AdoptJudgment is the one judgment the pipeline raises: switch to Build or
// not, with the evidence. ID is the tip, so an answer names what it answers.
type AdoptJudgment struct {
	ID       string     `json:"id"`
	Live     string     `json:"live"`
	Build    AdoptBuild `json:"build"`
	Canary   string     `json:"canary"`
	Shadow   string     `json:"shadow"`
	Card     string     `json:"card"`
	Read     AdoptRead  `json:"read"`
	At       time.Time  `json:"at"`
	Question string     `json:"question"`
}

// AdoptMachine is one machine row's push: the version read back, and the
// error of the push or the read when either failed.
type AdoptMachine struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
}

// AdoptRecord is the pipeline's state, kept between passes by an AdoptStore.
type AdoptRecord struct {
	Stage    string         `json:"stage"`
	Tip      string         `json:"tip,omitempty"`
	Live     string         `json:"live,omitempty"`
	Build    AdoptBuild     `json:"build,omitempty"`
	Canary   string         `json:"canary,omitempty"`
	Shadow   string         `json:"shadow,omitempty"`
	Card     string         `json:"card,omitempty"`
	Read     AdoptRead      `json:"read,omitempty"`
	Judgment *AdoptJudgment `json:"judgment,omitempty"`
	// Answer is the coordinator's answer to the judgment: "yes" or "no",
	// set by AnswerAdoption, read by the next pass.
	Answer   string         `json:"answer,omitempty"`
	Reason   string         `json:"reason,omitempty"`
	Kept     []string       `json:"kept,omitempty"`
	Switched time.Time      `json:"switched,omitzero"`
	Machines []AdoptMachine `json:"machines,omitempty"`
	Blocked  string         `json:"blocked,omitempty"`
	// Owed is an earlier switched build that some machine rows do not read
	// back yet: kept across a new tip, pushed again on every pass until each
	// row reads it, and dropped by the next switch, which pushes every row.
	Owed    *AdoptOwed `json:"owed,omitempty"`
	Updated time.Time  `json:"updated,omitzero"`
}

// AdoptOwed is a switched build's push still owed to some machine rows.
type AdoptOwed struct {
	Build    AdoptBuild     `json:"build"`
	Machines []AdoptMachine `json:"machines,omitempty"`
}

// fleetDone says every machine row in ms reads back version; no rows read is
// not done, so a fleet that could not be read at the switch is read again.
func fleetDone(ms []AdoptMachine, version string) bool {
	if len(ms) == 0 {
		return false
	}
	for _, m := range ms {
		if m.Error != "" || m.Version != version {
			return false
		}
	}
	return true
}

// owedOf is what a record leaves owed when a new tip replaces it: the push of
// its adopted build when a row missed it (FleetReachesTheBuild of the adoption
// model, proposed as tla/Adoption.tla with this card),
// else what it was itself carrying. A rolled-back build is never owed.
func owedOf(r AdoptRecord) *AdoptOwed {
	if r.Stage == AdoptAdopted && !fleetDone(r.Machines, r.Build.Version) {
		return &AdoptOwed{Build: r.Build, Machines: r.Machines}
	}
	if r.Stage == AdoptRolledBack {
		return nil
	}
	return r.Owed
}

// AdoptSteps is every stage of an adoption, one call each. The production
// steps are cmd/nova-sprint/adopt.go's; a test gives fakes.
type AdoptSteps interface {
	// BaseTip is the commit the sprint base branch is at.
	BaseTip(ctx context.Context) (string, error)
	// LiveBuild is the commit the live server binary was built from.
	LiveBuild(ctx context.Context) (string, error)
	// Build builds every tool from tip on a bench.
	Build(ctx context.Context, tip string) (AdoptBuild, error)
	// Canary runs the built server's own check, its evidence line.
	Canary(ctx context.Context, b AdoptBuild) (string, error)
	// Shadow runs the built server's shadow tick on the live store, read-only.
	Shadow(ctx context.Context, b AdoptBuild) (string, error)
	// DealColdRead adds the cold-read card of the build, dealt to a friend.
	DealColdRead(ctx context.Context, b AdoptBuild) (string, error)
	// ColdRead is the cold-read card's state.
	ColdRead(ctx context.Context, card string) (AdoptRead, error)
	// Ask surfaces the one judgment to the coordinator.
	Ask(ctx context.Context, j AdoptJudgment) error
	// KeepRollback copies the live server and daemon binaries aside and
	// names the copies.
	KeepRollback(ctx context.Context) ([]string, error)
	// Switch puts the build in place of the server and the friends' daemons.
	Switch(ctx context.Context, b AdoptBuild) error
	// Machines is every machine row, funded or not.
	Machines(ctx context.Context) ([]string, error)
	// Push installs the build on one machine.
	Push(ctx context.Context, machine string, b AdoptBuild) error
	// Version reads the version one machine's tools report.
	Version(ctx context.Context, machine string) (string, error)
	// LastTick is when the live server last ticked.
	LastTick(ctx context.Context) (time.Time, error)
	// Rollback puts the kept copies back.
	Rollback(ctx context.Context, kept []string) error
}

// AdoptStore keeps the record between passes.
type AdoptStore interface {
	Load(ctx context.Context) (AdoptRecord, error)
	Save(ctx context.Context, r AdoptRecord) error
}

// FileAdoptStore keeps the record as one JSON file, written whole and renamed
// into place, so a pass that dies mid-write leaves the last record.
type FileAdoptStore struct{ Path string }

func (f FileAdoptStore) Load(context.Context) (AdoptRecord, error) {
	b, err := os.ReadFile(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		return AdoptRecord{Stage: AdoptIdle}, nil
	}
	if err != nil {
		return AdoptRecord{}, err
	}
	var r AdoptRecord
	if err := json.Unmarshal(b, &r); err != nil {
		return AdoptRecord{}, fmt.Errorf("the adoption record %s does not read: %w", f.Path, err)
	}
	return r, nil
}

func (f FileAdoptStore) Save(_ context.Context, r AdoptRecord) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(f.Path), filepath.Base(f.Path)+".tmp.*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close()     // ignored: the write already failed, and that error is returned
		_ = os.Remove(name) // ignored: best-effort cleanup of the temporary file
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name) // ignored: best-effort cleanup of the temporary file
		return err
	}
	return os.Rename(name, f.Path)
}

// Default watch: the server ticks every TickEvery; this many missed in a row
// rolls the switch back, and this many seen since the switch adopts it.
const (
	DefaultAdoptTickEvery = time.Minute
	DefaultAdoptMissed    = 3
	DefaultAdoptWatch     = 15
)

// Adoption is one pipeline: its steps, its record and its clock.
type Adoption struct {
	Steps AdoptSteps
	Store AdoptStore
	Now   func() time.Time
	// TickEvery is the server's tick interval; Missed is how many intervals
	// with no tick roll a switch back; Watch is how many intervals after the
	// switch, every one ticked, adopt it. Zero takes the defaults.
	TickEvery     time.Duration
	Missed, Watch int
}

// AdoptPass is what one pass did: the stage it left the record at and one
// line per thing that happened, in order.
type AdoptPass struct {
	Stage string
	Lines []string
	// Judgment is set on the pass that raised it.
	Judgment *AdoptJudgment
}

func (p *AdoptPass) say(format string, a ...any) {
	p.Lines = append(p.Lines, fmt.Sprintf(format, a...))
}

func (a *Adoption) now() time.Time {
	if a.Now == nil {
		return time.Now()
	}
	return a.Now()
}

func (a *Adoption) watch() (every time.Duration, missed, watch int) {
	every, missed, watch = a.TickEvery, a.Missed, a.Watch
	if every <= 0 {
		every = DefaultAdoptTickEvery
	}
	if missed <= 0 {
		missed = DefaultAdoptMissed
	}
	if watch <= 0 {
		watch = DefaultAdoptWatch
	}
	return every, missed, watch
}

// SameCommit says two commit names are one commit: equal, or one the other's
// prefix of at least 7 hex (a version line carries a 12-hex revision).
func SameCommit(a, b string) bool {
	a, b = strings.ToLower(strings.TrimSpace(a)), strings.ToLower(strings.TrimSpace(b))
	if a == "" || b == "" {
		return false
	}
	if len(a) > len(b) {
		a, b = b, a
	}
	return len(a) >= 7 && strings.HasPrefix(b, a)
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// Pass moves the adoption as far as it can go now, and saves the record
// after every stage, so a pass that dies resumes where it stopped. Its error
// is a step it could not read (the base, the live build, the record); a stage
// that fails is not an error: the record is blocked and the pass says why.
func (a *Adoption) Pass(ctx context.Context) (AdoptPass, error) {
	var p AdoptPass
	r, err := a.Store.Load(ctx)
	if err != nil {
		return p, err
	}
	if r.Stage == "" {
		r.Stage = AdoptIdle
	}
	save := func() error {
		r.Updated = a.now()
		return a.Store.Save(ctx, r)
	}

	// a switched build is watched to its end before anything else starts
	if r.Stage == AdoptWatching {
		if err := a.watchPass(ctx, &r, &p); err != nil {
			return p, err
		}
		p.Stage = r.Stage
		return p, save()
	}

	tip, err := a.Steps.BaseTip(ctx)
	if err != nil {
		return p, fmt.Errorf("the base tip does not read: %w", err)
	}
	live, err := a.Steps.LiveBuild(ctx)
	if err != nil {
		return p, fmt.Errorf("the live build does not read: %w", err)
	}
	if SameCommit(tip, live) {
		if r.Stage != AdoptIdle && !AdoptDone(r.Stage) {
			p.say("ADOPT ABANDONED tip=%s stage=%s: the live build is the base tip", short(r.Tip), r.Stage)
		}
		if r.Stage != AdoptIdle && !(AdoptDone(r.Stage) && SameCommit(r.Tip, tip)) {
			r = AdoptRecord{Stage: AdoptIdle, Live: live, Owed: owedOf(r)}
		}
		p.say("ADOPT CURRENT live=%s base=%s", short(live), short(tip))
		a.pushOwed(ctx, &r, &p)
		p.Stage = r.Stage
		return p, save()
	}
	if !SameCommit(r.Tip, tip) {
		if r.Stage != AdoptIdle && !AdoptDone(r.Stage) {
			// the base moved under an adoption not yet switched: the newer tip
			// is the one worth a judgment, and the old one is never asked
			p.say("ADOPT RESTARTED tip=%s stage=%s: the base moved to %s", short(r.Tip), r.Stage, short(tip))
		}
		r = AdoptRecord{Stage: AdoptIdle, Tip: tip, Live: live, Owed: owedOf(r)}
		p.say("ADOPT START tip=%s live=%s: the base is past the live build", short(tip), short(live))
	}
	a.pushOwed(ctx, &r, &p)
	if AdoptDone(r.Stage) {
		p.say("ADOPT %s tip=%s live=%s%s", strings.ToUpper(r.Stage), short(r.Tip), short(r.Live), blockedTail(r))
		p.Stage = r.Stage
		return p, save()
	}
	r.Live = live

	block := func(stage string, err error) (AdoptPass, error) {
		r.Stage, r.Blocked = AdoptBlocked, stage+": "+err.Error()
		p.say("ADOPT BLOCKED tip=%s at %s: %s; the live build %s is unchanged", short(r.Tip), stage, err.Error(), short(live))
		p.Stage = r.Stage
		return p, save()
	}

	if r.Stage == AdoptIdle {
		b, err := a.Steps.Build(ctx, tip)
		if err != nil {
			return block("build", err)
		}
		r.Build, r.Stage = b, AdoptBuilt
		p.say("ADOPT BUILT tip=%s version=%s dir=%s", short(tip), b.Version, b.Dir)
		if err := save(); err != nil {
			return p, err
		}
	}
	if r.Stage == AdoptBuilt {
		canary, err := a.Steps.Canary(ctx, r.Build)
		if err != nil {
			return block("canary", err)
		}
		shadow, err := a.Steps.Shadow(ctx, r.Build)
		if err != nil {
			return block("shadow", err)
		}
		r.Canary, r.Shadow, r.Stage = canary, shadow, AdoptChecked
		p.say("ADOPT CHECKED tip=%s canary=%q shadow=%q", short(tip), canary, shadow)
		if err := save(); err != nil {
			return p, err
		}
	}
	if r.Stage == AdoptChecked {
		card, err := a.Steps.DealColdRead(ctx, r.Build)
		if err != nil {
			return block("cold read", err)
		}
		r.Card, r.Stage = card, AdoptReading
		p.say("ADOPT READING tip=%s card=%s: dealt to a friend", short(tip), card)
		if err := save(); err != nil {
			return p, err
		}
	}
	if r.Stage == AdoptReading {
		read, err := a.Steps.ColdRead(ctx, r.Card)
		if err != nil {
			return block("cold read", err)
		}
		if !read.Done {
			p.say("ADOPT WAIT tip=%s card=%s: the cold read is out", short(tip), r.Card)
			p.Stage = r.Stage
			return p, save()
		}
		j := AdoptJudgment{
			ID: short(tip), Live: live, Build: r.Build, Canary: r.Canary, Shadow: r.Shadow,
			Card: r.Card, Read: read, At: a.now(),
			Question: fmt.Sprintf("switch the server, the friends' daemons and every machine from %s to %s (%s)? answer: nova-sprint adopt --answer yes|no --judgment %s --reason <text>", short(live), short(tip), r.Build.Version, short(tip)),
		}
		if err := a.Steps.Ask(ctx, j); err != nil {
			return p, fmt.Errorf("the judgment was not raised: %w", err)
		}
		r.Read, r.Judgment, r.Stage = read, &j, AdoptAsked
		p.Judgment = &j
		p.say("JUDGMENT adopt %s canary=%q shadow=%q read=%s finding=%q: %s", j.ID, j.Canary, j.Shadow, readWord(read), read.Finding, j.Question)
		if err := save(); err != nil {
			return p, err
		}
	}
	if r.Stage == AdoptAsked {
		switch r.Answer {
		case "":
			p.say("ADOPT WAIT tip=%s judgment=%s: no answer yet", short(tip), short(tip))
			p.Stage = r.Stage
			return p, save()
		case "no":
			r.Stage = AdoptDeclined
			p.say("ADOPT DECLINED tip=%s reason=%q: the live build %s stays", short(tip), r.Reason, short(live))
			p.Stage = r.Stage
			return p, save()
		}
		kept, err := a.Steps.KeepRollback(ctx)
		if err != nil {
			return block("rollback copies", err)
		}
		r.Kept = kept
		p.say("ADOPT KEPT %s", strings.Join(kept, " "))
		if err := save(); err != nil {
			return p, err
		}
		if err := a.Steps.Switch(ctx, r.Build); err != nil {
			// a half switch is put back from the copies just kept
			if rb := a.Steps.Rollback(ctx, kept); rb != nil {
				err = fmt.Errorf("%w; and the rollback failed: %v", err, rb)
			}
			return block("switch", err)
		}
		r.Stage, r.Switched, r.Owed = AdoptWatching, a.now(), nil
		p.say("ADOPT SWITCHED tip=%s version=%s: the server and the friends' daemons", short(tip), r.Build.Version)
		if err := save(); err != nil {
			return p, err
		}
		r.Machines = a.push(ctx, r.Build, r.Machines, &p)
	}
	p.Stage = r.Stage
	return p, save()
}

// push installs b on every machine row not already reading it back in have,
// and reads each version back. A machine that fails is named and tried again
// on a later pass; it never undoes the switch. The rows are read again each
// time, so a row added since is pushed too.
func (a *Adoption) push(ctx context.Context, b AdoptBuild, have []AdoptMachine, p *AdoptPass) []AdoptMachine {
	names, err := a.Steps.Machines(ctx)
	if err != nil {
		p.say("ADOPT FLEET UNREAD: %s; the push is tried again next pass", err.Error())
		return have
	}
	done := map[string]AdoptMachine{}
	for _, m := range have {
		done[m.Name] = m
	}
	sort.Strings(names)
	var out []AdoptMachine
	ok := 0
	for _, n := range names {
		if m, seen := done[n]; seen && m.Error == "" && m.Version == b.Version {
			out = append(out, m)
			ok++
			continue
		}
		m := AdoptMachine{Name: n}
		if err := a.Steps.Push(ctx, n, b); err != nil {
			m.Error = "push: " + err.Error()
		} else if v, err := a.Steps.Version(ctx, n); err != nil {
			m.Error = "version: " + err.Error()
		} else if v != b.Version {
			m.Version, m.Error = v, "reads back "+v+", not "+b.Version
		} else {
			m.Version = v
		}
		if m.Error != "" {
			p.say("ADOPT FLEET MISSED machine=%s: %s", n, m.Error)
		} else {
			ok++
		}
		out = append(out, m)
	}
	p.say("ADOPT FLEET version=%s machines=%d read_back=%d", b.Version, len(out), ok)
	return out
}

// pushOwed pushes a switched build to the rows that still miss it: the
// record's own adopted build, else the one a new tip carried over as Owed,
// whose debt is dropped once every row reads it back.
func (a *Adoption) pushOwed(ctx context.Context, r *AdoptRecord, p *AdoptPass) {
	if r.Stage == AdoptAdopted && !fleetDone(r.Machines, r.Build.Version) {
		r.Machines = a.push(ctx, r.Build, r.Machines, p)
		return
	}
	if r.Owed == nil {
		return
	}
	r.Owed.Machines = a.push(ctx, r.Owed.Build, r.Owed.Machines, p)
	if fleetDone(r.Owed.Machines, r.Owed.Build.Version) {
		r.Owed = nil
	}
}

// watchPass is a switched build's pass: missed ticks roll back, a watch
// with every tick seen adopts, and a machine the push missed is tried again.
func (a *Adoption) watchPass(ctx context.Context, r *AdoptRecord, p *AdoptPass) error {
	every, missed, watch := a.watch()
	now := a.now()
	last, err := a.Steps.LastTick(ctx)
	if err != nil {
		return fmt.Errorf("the server's last tick does not read: %w", err)
	}
	since := last
	if since.Before(r.Switched) {
		since = r.Switched
	}
	if gap := now.Sub(since); gap >= time.Duration(missed)*every {
		if err := a.Steps.Rollback(ctx, r.Kept); err != nil {
			p.say("ADOPT ROLLBACK FAILED tip=%s: %s; the coordinator is owed a hand rollback from %s", short(r.Tip), err.Error(), strings.Join(r.Kept, " "))
			r.Blocked = "rollback: " + err.Error()
			return nil
		}
		r.Stage, r.Blocked = AdoptRolledBack, fmt.Sprintf("no tick for %s since %s", gap.Round(time.Second), since.UTC().Format(time.RFC3339))
		p.say("ADOPT ROLLED BACK tip=%s: %s (%d missed at %s); restored %s", short(r.Tip), r.Blocked, missed, every, strings.Join(r.Kept, " "))
		return nil
	}
	r.Machines = a.push(ctx, r.Build, r.Machines, p)
	if now.Sub(r.Switched) >= time.Duration(watch)*every && last.After(r.Switched) {
		r.Stage = AdoptAdopted
		p.say("ADOPT ADOPTED tip=%s version=%s: %d tick intervals since the switch, none missed", short(r.Tip), r.Build.Version, watch)
		return nil
	}
	p.say("ADOPT WATCHING tip=%s last_tick=%s", short(r.Tip), last.UTC().Format(time.RFC3339))
	return nil
}

// AnswerAdoption records the coordinator's answer to the open judgment. The
// judgment named must be the one open, so an answer to an old tip never
// switches a new one.
func AnswerAdoption(ctx context.Context, st AdoptStore, judgment, answer, reason string) (AdoptRecord, error) {
	r, err := st.Load(ctx)
	if err != nil {
		return r, err
	}
	if answer != "yes" && answer != "no" {
		return r, fmt.Errorf("the answer is yes or no, not %q", answer)
	}
	if r.Stage != AdoptAsked || r.Judgment == nil {
		return r, fmt.Errorf("no adoption judgment is open (the stage is %s)", orIdle(r.Stage))
	}
	if !SameCommit(judgment, r.Judgment.ID) {
		return r, fmt.Errorf("the open judgment is %s, not %s", r.Judgment.ID, judgment)
	}
	if r.Answer != "" {
		return r, fmt.Errorf("the judgment %s is already answered %s", r.Judgment.ID, r.Answer)
	}
	r.Answer, r.Reason = answer, strings.TrimSpace(reason)
	return r, st.Save(ctx, r)
}

func orIdle(s string) string {
	if s == "" {
		return AdoptIdle
	}
	return s
}

func readWord(r AdoptRead) string {
	if r.OK {
		return "ok"
	}
	return "broken"
}

func blockedTail(r AdoptRecord) string {
	if r.Blocked == "" {
		return ""
	}
	return ": " + r.Blocked
}
