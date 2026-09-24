// This file is `nova-sprint lineup` (#3108, #2756 v6 section 7, checks 7.18
// to 7.21, 7.24 and 7.25; controls 49, 64 and 65): the per-bench conform
// record, the probe cut, and the lineup checks `sprint open` refuses on.
//
// bench:<b>:conform is written by one writer, PublishConform, which
// `bench-conform --publish` reaches through `nova-sprint lineup publish` with
// the bench's probe answers on stdin. The record carries the verdict, the
// failing keys, each key's answer and one DRIFT or MISSING line per failing
// key, in bin/bench-conform's own format (rowan-tools #160, #181). The checks
// read only the records, the sprint's probe set and the landed index; they
// never ssh. A bench with no record, or one older than 15 min, is RED on every
// check that needs it: no evidence is not negative evidence.
//
// 7.22 (friend wake paths) and 7.23 (PR records against GitHub, task
// DEPENDS-ON and PATHS) read keys the v6 friend ladder (5.5) and PR record
// (3.4) cuts write; they are not in this cut.
package preflight

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/redis/go-redis/v9"
)

// The conform keys lineup gates on, named as bin/bench-conform's probe prints
// them (rowan-tools #160 for code.*, #181 for nova.build and secrets.store,
// #167 for code.clone-verb).
const (
	KeyBuild           = "nova.build"            // 7.18: the short build sha nova-merge runs
	KeyPushCredential  = "code.push-credential"  // 7.19: present
	KeyMirrorAge       = "code.mirror-age"       // 7.19: seconds, under 600
	KeyResultsRoot     = "code.results-root"     // 7.19: ~/nova-bench/results
	KeyFinishedJobdirs = "code.finished-jobdirs" // 7.19: 0
	KeySecretsStore    = "secrets.store"         // 7.24: branch=,upstream=,rev=,clean=
	KeyCloneVerb       = "code.clone-verb"       // 7.25: verb=ok|absent,mirrors=a:b:c
)

// ConformKeys is the fixed key list, in print order. A key no bench answers is
// still MISSING on every bench.
var ConformKeys = []string{KeyBuild, KeyPushCredential, KeyMirrorAge, KeyResultsRoot,
	KeyFinishedJobdirs, KeySecretsStore, KeyCloneVerb}

// checkOf is the section 7 check that owns each key.
var checkOf = map[string]string{
	KeyBuild: "7.18", KeyPushCredential: "7.19", KeyMirrorAge: "7.19", KeyResultsRoot: "7.19",
	KeyFinishedJobdirs: "7.19", KeySecretsStore: "7.24", KeyCloneVerb: "7.25",
}

// Bounds from #2756 v6.
const (
	ConformTTL   = 15 * time.Minute // 2.2: bench:<b>:conform TTL; older is RED (7.18)
	MirrorMaxAge = 600              // 7.19: seconds
	ProbeCount   = 5                // 7.20: the five probe cards
	ResultsRoot  = "~/nova-bench/results"
)

// CloneMirrors are the mirrors every bench carries (7.25; rowan-tools #170).
var CloneMirrors = []string{"nova-tools", "nova-work", "rowan-tools", "schema"}

// Declared is the fleet standard from fleet/group_vars/all.yml.
type Declared struct {
	Build      string // nova_build's short sha suffix
	SecretsRev string // secrets_rev
}

// ReadDeclared reads nova_build and secrets_rev from all.yml. It reads the two
// scalar lines only; the file has no nesting at those keys.
func ReadDeclared(path string) (Declared, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Declared{}, err
	}
	var d Declared
	for _, l := range strings.Split(string(body), "\n") {
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		if i := strings.Index(v, "#"); i >= 0 {
			v = v[:i]
		}
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		switch strings.TrimSpace(k) {
		case "nova_build":
			d.Build = v[strings.LastIndex(v, ".")+1:]
		case "secrets_rev":
			d.SecretsRev = v
		}
	}
	return d, nil
}

// ParseProbe reads bin/bench-conform's probe output: one key=value per line,
// split at the first '='.
func ParseProbe(r io.Reader) map[string]string {
	out := map[string]string{}
	s := bufio.NewScanner(r)
	for s.Scan() {
		if k, v, ok := strings.Cut(strings.TrimSpace(s.Text()), "="); ok && k != "" {
			out[k] = v
		}
	}
	return out
}

// Conform is one bench's evaluated conformance.
type Conform struct {
	Bench    string
	Verdict  string            // PASS or DRIFT
	Keys     []string          // failing keys, in ConformKeys order
	Answers  map[string]string // key -> the bench's answer
	Findings map[string]string // failing key -> its DRIFT or MISSING line
	Want     Declared
	// Run is the lineup run marker this record was published under (the
	// RunMarkerEnv of the `nova-sprint lineup` that ran bench-conform); empty
	// for a publish outside a lineup run.
	Run string
}

// RunMarkerEnv carries a lineup run's marker from `nova-sprint lineup` through
// bench-conform --publish into each `nova-sprint lineup publish`, which stamps
// it on the record as the run field.
const RunMarkerEnv = "NOVA_LINEUP_RUN"

// NewRunMarker is a lineup run's unique marker: Redis server time to the
// microsecond and 64 random bits, so two runs never share one, even inside
// the same second.
func NewRunMarker(now time.Time) (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d-%s", now.UnixMicro(), hex.EncodeToString(b[:])), nil
}

// Evaluate holds a bench's answers against the declared standard, each key an
// absolute requirement (never fleet consensus).
func Evaluate(bench string, answers map[string]string, d Declared) Conform {
	c := Conform{Bench: bench, Answers: map[string]string{}, Findings: map[string]string{}, Want: d}
	for _, k := range ConformKeys {
		have := answers[k]
		c.Answers[k] = have
		var why string
		if have == "" {
			why = "MISSING " + bench + " " + k
		} else if rule := ruleFor(k, d)(have); rule != "" {
			why = "DRIFT " + bench + " " + k + " have=" + have + " " + rule
		}
		if why != "" {
			c.Keys = append(c.Keys, k)
			c.Findings[k] = oneline.Escape(why)
		}
	}
	c.Verdict = "PASS"
	if len(c.Keys) > 0 {
		c.Verdict = "DRIFT"
	}
	return c
}

// ruleFor returns the key's rule: "" passes, else the want part of the line.
func ruleFor(k string, d Declared) func(string) string {
	switch k {
	case KeyBuild:
		return func(v string) string {
			if d.Build != "" && v == d.Build {
				return ""
			}
			return "want=" + orNone(d.Build, "<no declared nova_build>")
		}
	case KeyPushCredential:
		return equals("present")
	case KeyMirrorAge:
		return func(v string) string {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 && n < MirrorMaxAge {
				return ""
			}
			return fmt.Sprintf("want=<%ds", MirrorMaxAge)
		}
	case KeyResultsRoot:
		return equals(ResultsRoot)
	case KeyFinishedJobdirs:
		return equals("0")
	case KeySecretsStore:
		return func(v string) string {
			got := pairs(v)
			var bad []string
			for _, w := range [][2]string{{"branch", "main"}, {"upstream", "yes"}, {"rev", d.SecretsRev}, {"clean", "yes"}} {
				if w[1] == "" || got[w[0]] != w[1] {
					bad = append(bad, w[0])
				}
			}
			if len(bad) == 0 {
				return ""
			}
			return "want=branch=main,upstream=yes,rev=" + orNone(d.SecretsRev, "<no declared secrets_rev>") +
				",clean=yes failed=" + strings.Join(bad, ",")
		}
	case KeyCloneVerb:
		return func(v string) string {
			got := pairs(v)
			have := map[string]bool{}
			for _, m := range strings.Split(got["mirrors"], ":") {
				have[m] = true
			}
			var bad, missing []string
			if got["verb"] != "ok" {
				bad = append(bad, "verb")
			}
			for _, m := range CloneMirrors {
				if !have[m] {
					missing = append(missing, m)
				}
			}
			if len(missing) > 0 {
				bad = append(bad, "mirrors")
			}
			if len(bad) == 0 {
				return ""
			}
			s := "want=verb=ok,mirrors=" + strings.Join(CloneMirrors, ":") + " failed=" + strings.Join(bad, ",")
			if len(missing) > 0 {
				s += " missing=" + strings.Join(missing, ",")
			}
			return s
		}
	}
	return func(string) string { return "want=<no rule>" }
}

func equals(want string) func(string) string {
	return func(v string) string {
		if v == want {
			return ""
		}
		return "want=" + want
	}
}

func orNone(v, none string) string {
	if v == "" {
		return none
	}
	return v
}

func pairs(v string) map[string]string {
	out := map[string]string{}
	for _, p := range strings.Split(v, ",") {
		if k, val, ok := strings.Cut(p, "="); ok {
			out[k] = val
		}
	}
	return out
}

func conformKey(bench string) string { return "bench:" + bench + ":conform" }

// PublishConform is the one writer of bench:<b>:conform: the record replaces
// the last one in one transaction, stamped with Redis server time and the
// lineup run marker, with the 15 min TTL.
func PublishConform(ctx context.Context, c *redis.Client, cf Conform) error {
	if cf.Bench == "" || strings.ContainsAny(cf.Bench, " :\t\n") {
		return fmt.Errorf("bench name %q", cf.Bench)
	}
	if strings.ContainsAny(cf.Run, " \t\n") {
		return fmt.Errorf("run marker %q", cf.Run)
	}
	now, err := c.Time(ctx).Result()
	if err != nil {
		return err
	}
	fields := map[string]any{
		"verdict":          cf.Verdict,
		"keys":             strings.Join(cf.Keys, " "),
		"at":               now.Unix(),
		"build_have":       cf.Answers[KeyBuild],
		"build_want":       cf.Want.Build,
		"secrets_store":    cf.Answers[KeySecretsStore],
		"secrets_rev_want": cf.Want.SecretsRev,
		"clone_verb":       cf.Answers[KeyCloneVerb],
		"push_credential":  cf.Answers[KeyPushCredential],
		"mirror_age_s":     cf.Answers[KeyMirrorAge],
		"results_root":     cf.Answers[KeyResultsRoot],
		"jobdirs_finished": cf.Answers[KeyFinishedJobdirs],
		"run":              cf.Run,
	}
	for k, f := range cf.Findings {
		fields["finding:"+k] = f
	}
	key := conformKey(cf.Bench)
	_, err = c.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.Del(ctx, key)
		p.HSet(ctx, key, fields)
		p.Expire(ctx, key, ConformTTL)
		return nil
	})
	return err
}

func probesKey(sprint string) string { return "s:" + sprint + ":probes" }

// CutProbes records the sprint's five probe cards in s:<S>:probes. The set is
// create-only: the same five again is no change, a different set is refused.
func CutProbes(ctx context.Context, c *redis.Client, sprint string, labels []string) error {
	seen := map[string]bool{}
	for _, l := range labels {
		if l == "" || strings.ContainsAny(l, " \t\n") {
			return fmt.Errorf("probe label %q", l)
		}
		seen[l] = true
	}
	if len(seen) != ProbeCount {
		return fmt.Errorf("%d distinct probe cards; a sprint cuts %d", len(seen), ProbeCount)
	}
	key := probesKey(sprint)
	return c.Watch(ctx, func(tx *redis.Tx) error {
		have, err := tx.SMembers(ctx, key).Result()
		if err != nil {
			return err
		}
		if len(have) > 0 {
			same := len(have) == len(seen)
			for _, h := range have {
				same = same && seen[h]
			}
			if same {
				return nil
			}
			sort.Strings(have)
			return fmt.Errorf("sprint %s already cut probes %s", sprint, strings.Join(have, ","))
		}
		_, err = tx.TxPipelined(ctx, func(p redis.Pipeliner) error {
			members := make([]any, 0, len(labels))
			for l := range seen {
				members = append(members, l)
			}
			p.SAdd(ctx, key, members...)
			return nil
		})
		return err
	}, key)
}

// ConformRecord is a bench:<b>:conform hash as read.
type ConformRecord struct {
	Present bool
	Fields  map[string]string
}

// GraphQLBudget is the lander login's GraphQL rate budget and a lane pass's
// use of it (7.21).
type GraphQLBudget struct {
	Known        bool
	Remaining    int
	CallsPerPass int
	Cadence      time.Duration
}

// LineupInput is everything the lineup checks read.
type LineupInput struct {
	Now           time.Time // Redis server time
	BenchesLoaded bool      // the registry and beats were read
	Up            []string  // registered benches with a beat, sorted
	Conform       map[string]ConformRecord
	// ConformRun is this run's marker when the run published conform; a
	// record whose run field is not this marker was not republished by this
	// run and is RED, however fresh (a timestamp cannot tell an older record
	// written in the same second from this run's). Empty when the run did
	// not publish (--no-conform): the records are then only TTL-fresh.
	ConformRun string
	// ConformFail is why this run's conform publish failed (error or
	// nonzero exit); non-empty is RED on 7.18.
	ConformFail string

	Sprint        string
	ProbesLoaded  bool
	NotPRSprint   bool // s:<S> pr_producing is "0"; absent counts as PR-producing
	Probes        []string
	Landed        map[string]bool
	GraphQL       GraphQLBudget
	BinaryScanned bool     // the running binary was read
	BinaryHits    []string // GraphQL call sites found in it
}

// GatherLineup reads the registry, the beats, every UP bench's conform record
// and the sprint's probes in two pipelined exchanges.
func GatherLineup(ctx context.Context, c *redis.Client, sprint string) (LineupInput, error) {
	in := LineupInput{Sprint: sprint, Conform: map[string]ConformRecord{}}
	p := c.Pipeline()
	tm := p.Time(ctx)
	reg := p.SMembers(ctx, "benches")
	var probes, flag *redis.StringCmd
	var probeSet *redis.StringSliceCmd
	if sprint != "" {
		probeSet = p.SMembers(ctx, probesKey(sprint))
		flag = p.HGet(ctx, "s:"+sprint, "pr_producing")
	}
	if _, err := p.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return in, err
	}
	_ = probes
	in.Now = tm.Val()
	names := reg.Val()
	sort.Strings(names)
	p = c.Pipeline()
	beats := make([]*redis.IntCmd, len(names))
	recs := make([]*redis.MapStringStringCmd, len(names))
	for i, b := range names {
		beats[i] = p.Exists(ctx, "bench:"+b+":beat")
		recs[i] = p.HGetAll(ctx, conformKey(b))
	}
	var landed *redis.BoolSliceCmd
	if sprint != "" && len(probeSet.Val()) > 0 {
		members := make([]any, 0, len(probeSet.Val()))
		for _, m := range probeSet.Val() {
			members = append(members, m)
		}
		landed = p.SMIsMember(ctx, "s:"+sprint+":idx:card:landed", members...)
	}
	if len(names) > 0 || landed != nil {
		if _, err := p.Exec(ctx); err != nil {
			return in, err
		}
	}
	for i, b := range names {
		if beats[i].Val() == 0 {
			continue
		}
		in.Up = append(in.Up, b)
		if h := recs[i].Val(); len(h) > 0 {
			in.Conform[b] = ConformRecord{Present: true, Fields: h}
		}
	}
	in.BenchesLoaded = true
	if sprint != "" {
		in.ProbesLoaded = true
		in.NotPRSprint = flag.Val() == "0"
		in.Probes = append([]string(nil), probeSet.Val()...)
		sort.Strings(in.Probes)
		in.Landed = map[string]bool{}
		if landed != nil {
			for i, ok := range landed.Val() {
				if ok {
					in.Landed[probeSet.Val()[i]] = true
				}
			}
		}
	}
	return in, nil
}

// LineupChecks runs the lineup checks in section order.
func LineupChecks(in LineupInput) []Line {
	return []Line{CheckConform(in), CheckCodingKeys(in), CheckProbes(in), CheckGraphQL(in), CheckSecretsStore(in), CheckCloneVerb(in)}
}

// PrerequisiteChecks are the lineup checks that must be GREEN before the
// probe cut: every check but 7.20, in section order.
func PrerequisiteChecks(in LineupInput) []Line {
	return []Line{CheckConform(in), CheckCodingKeys(in), CheckGraphQL(in), CheckSecretsStore(in), CheckCloneVerb(in)}
}

// LineupRun is one `nova-sprint lineup` pass.
type LineupRun struct {
	Sprint string
	Probes []string // the five probe cards to cut; nil cuts none
	// Conform runs bench-conform --publish under the run marker it is given
	// (every record it publishes must carry it); an error or nonzero exit is
	// its failure. nil is --no-conform: the records are then only TTL-fresh.
	Conform func(ctx context.Context, run string) error
	// Enrich fills what Redis does not hold: the GraphQL budget and the
	// binary scan. It runs once.
	Enrich func(*LineupInput)
}

// LineupResult is a lineup pass's input, its check lines and the probe cut.
type LineupResult struct {
	In         LineupInput
	Lines      []Line
	ProbesCut  bool
	ProbesHeld string // why the asked-for probe cut was not made
}

// RunLineup runs the lineup in the order #2756 v6 section 7 names: conform
// publish, then the preflight checks, then the probe cut. A failed publish is
// RED on 7.18, and a record that does not carry this run's marker is RED
// whatever its age, so an earlier PASS cannot stand in for the current probe. The probe
// set is cut only when every prerequisite check is GREEN; on any RED it is
// left untouched and ProbesHeld names the red checks.
func RunLineup(ctx context.Context, c *redis.Client, run LineupRun) (LineupResult, error) {
	var res LineupResult
	if len(run.Probes) > 0 && run.Sprint == "" {
		return res, fmt.Errorf("--probes needs --sprint <S>")
	}
	var marker, fail string
	if run.Conform != nil {
		t, err := c.Time(ctx).Result()
		if err != nil {
			return res, err
		}
		if marker, err = NewRunMarker(t); err != nil {
			return res, err
		}
		if err := run.Conform(ctx, marker); err != nil {
			fail = oneline.Err(err)
		}
	}
	in, err := GatherLineup(ctx, c, run.Sprint)
	if err != nil {
		return res, err
	}
	in.ConformRun, in.ConformFail = marker, fail
	if run.Enrich != nil {
		run.Enrich(&in)
	}
	if len(run.Probes) > 0 {
		var red []string
		for _, l := range PrerequisiteChecks(in) {
			if l.Red {
				red = append(red, l.N)
			}
		}
		if len(red) > 0 {
			res.ProbesHeld = "probes not cut: prerequisite checks RED (" + strings.Join(red, ", ") + ")"
		} else {
			if err := CutProbes(ctx, c, run.Sprint, run.Probes); err != nil {
				return res, fmt.Errorf("probe cut: %w", err)
			}
			res.ProbesCut = true
			again, err := GatherLineup(ctx, c, run.Sprint)
			if err != nil {
				return res, err
			}
			again.ConformRun, again.ConformFail = marker, fail
			again.GraphQL, again.BinaryScanned, again.BinaryHits = in.GraphQL, in.BinaryScanned, in.BinaryHits
			in = again
		}
	}
	res.In, res.Lines = in, LineupChecks(in)
	return res, nil
}

// record returns the bench's record, or the red reason it cannot be used.
func (in LineupInput) record(b string) (ConformRecord, string) {
	r, ok := in.Conform[b]
	if !ok || !r.Present {
		return r, oneline.Escape(b) + " conform MISSING"
	}
	at, ok := parseStamp(r.Fields["at"])
	if !ok {
		return r, oneline.Escape(b) + " conform at MISSING"
	}
	if age := in.Now.Sub(at); age > ConformTTL {
		return r, fmt.Sprintf("%s conform %ds old", oneline.Escape(b), int64(age/time.Second))
	}
	if in.ConformRun != "" && r.Fields["run"] != in.ConformRun {
		return r, fmt.Sprintf("%s conform not republished this run (run=%s want=%s)", oneline.Escape(b), oneline.Escape(orNone(r.Fields["run"], "MISSING")), oneline.Escape(in.ConformRun))
	}
	return r, ""
}

// keyCheck is the shape of 7.19, 7.24 and 7.25: every UP bench's record is
// present and fresh and none of the check's keys fails.
func keyCheck(in LineupInput, n, name string, keys []string, green string) Line {
	if !in.BenchesLoaded {
		return verdict(n, name, []string{"bench registry unread (MISSING)"}, "")
	}
	var reds []string
	for _, b := range in.Up {
		r, why := in.record(b)
		if why != "" {
			reds = append(reds, why)
			continue
		}
		for _, k := range keys {
			if f := r.Fields["finding:"+k]; f != "" {
				reds = append(reds, f)
			}
		}
	}
	return verdict(n, name, reds, fmt.Sprintf("%d UP benches: %s", len(in.Up), green))
}

// CheckConform is 7.18: every UP bench has a fresh PASS record, and its
// nova.build is the declared build. A DRIFT on another key names that key's
// check.
func CheckConform(in LineupInput) Line {
	const n, name = "7.18", "conform"
	if !in.BenchesLoaded {
		return verdict(n, name, []string{"bench registry unread (MISSING)"}, "")
	}
	var reds []string
	if in.ConformFail != "" {
		reds = append(reds, "conform publish failed: "+oneline.Escape(in.ConformFail))
	}
	if len(in.Up) == 0 {
		return verdict(n, name, append(reds, "no UP bench"), "")
	}
	for _, b := range in.Up {
		r, why := in.record(b)
		if why != "" {
			reds = append(reds, why)
			continue
		}
		switch r.Fields["verdict"] {
		case "PASS":
			continue
		case "DRIFT":
		default:
			reds = append(reds, oneline.Escape(b)+" verdict "+oneline.Escape(orNone(r.Fields["verdict"], "MISSING")))
			continue
		}
		var others []string
		for _, k := range strings.Fields(r.Fields["keys"]) {
			if checkOf[k] == n || checkOf[k] == "" {
				reds = append(reds, orNone(r.Fields["finding:"+k], "DRIFT "+oneline.Escape(b)+" "+oneline.Escape(k)))
			} else {
				others = append(others, k+" ("+checkOf[k]+")")
			}
		}
		if len(others) > 0 {
			reds = append(reds, oneline.Escape(b)+" DRIFT "+strings.Join(others, ", "))
		}
	}
	return verdict(n, name, reds, fmt.Sprintf("%d UP benches PASS, nova.build as declared", len(in.Up)))
}

// CheckCodingKeys is 7.19: push credential, mirror age, results root and no
// finished job dir left.
func CheckCodingKeys(in LineupInput) Line {
	return keyCheck(in, "7.19", "coding keys",
		[]string{KeyPushCredential, KeyMirrorAge, KeyResultsRoot, KeyFinishedJobdirs},
		"push credential, mirrors under 600s, results root, no job dirs left")
}

// CheckSecretsStore is 7.24: the sealed secrets clone is on main, upstreamed,
// at the declared secrets_rev and clean.
func CheckSecretsStore(in LineupInput) Line {
	return keyCheck(in, "7.24", "secrets store", []string{KeySecretsStore}, "secrets clone on main at the declared rev, clean")
}

// CheckCloneVerb is 7.25: scratch-clone is installed and the four mirrors exist.
func CheckCloneVerb(in LineupInput) Line {
	return keyCheck(in, "7.25", "clone verb", []string{KeyCloneVerb}, "scratch-clone and the "+strings.Join(CloneMirrors, ",")+" mirrors")
}

// CheckProbes is 7.20: a PR-producing sprint's five probe cards have landed.
func CheckProbes(in LineupInput) Line {
	const n, name = "7.20", "probes"
	switch {
	case in.Sprint == "":
		return verdict(n, name, []string{"no sprint named; the probes are per sprint"}, "")
	case !in.ProbesLoaded:
		return verdict(n, name, []string{"probe set unread (MISSING)"}, "")
	case in.NotPRSprint:
		return verdict(n, name, nil, "sprint "+in.Sprint+" produces no PRs")
	}
	var reds, waiting []string
	landed := 0
	for _, p := range in.Probes {
		if in.Landed[p] {
			landed++
		} else {
			waiting = append(waiting, oneline.Escape(p))
		}
	}
	if len(in.Probes) != ProbeCount {
		reds = append(reds, fmt.Sprintf("%d of %d probes cut; lineup --probes cuts them", len(in.Probes), ProbeCount))
	}
	if landed < ProbeCount {
		reds = append(reds, fmt.Sprintf("%d of %d probes landed", landed, ProbeCount))
		if len(waiting) > 0 {
			reds = append(reds, "not landed: "+strings.Join(waiting, ","))
		}
	}
	return verdict(n, name, reds, fmt.Sprintf("%d of %d probes landed", landed, ProbeCount))
}

// CheckGraphQL is 7.21: the binary makes no GraphQL call, and the lander
// login's GraphQL budget covers one hour of lane passes.
func CheckGraphQL(in LineupInput) Line {
	const n, name = "7.21", "no GraphQL"
	var reds []string
	if !in.BinaryScanned {
		reds = append(reds, "binary scan MISSING")
	}
	for _, h := range in.BinaryHits {
		reds = append(reds, "the binary carries "+oneline.Escape(h))
	}
	g := in.GraphQL
	need := 0
	switch {
	case !g.Known:
		reds = append(reds, "GraphQL budget MISSING")
	case g.CallsPerPass < 0 || g.Cadence <= 0:
		reds = append(reds, "lane GraphQL calls per pass or cadence MISSING")
	default:
		need = g.CallsPerPass * int(math.Ceil(float64(time.Hour)/float64(g.Cadence)))
		if g.Remaining < need {
			reds = append(reds, fmt.Sprintf("GraphQL remaining %d < %d (one hour of lane passes at %d per %s)", g.Remaining, need, g.CallsPerPass, g.Cadence))
		}
	}
	return verdict(n, name, reds, fmt.Sprintf("no GraphQL in the binary; remaining %d >= %d for one hour of lane passes", g.Remaining, need))
}

// OpenGate is what `sprint open` asks: nil when every lineup line is GREEN,
// else the refusal naming the red checks.
func OpenGate(lines []Line) error {
	var red []string
	for _, l := range lines {
		if l.Red {
			red = append(red, l.N)
		}
	}
	if len(red) == 0 {
		return nil
	}
	return fmt.Errorf("sprint open refused: %d of %d lineup checks RED (%s)", len(red), len(lines), strings.Join(red, ", "))
}

// RenderLineup prints every DRIFT and MISSING finding of the UP benches, the
// check lines, and the x/y tally.
func RenderLineup(in LineupInput, lines []Line) string {
	var b strings.Builder
	for _, bench := range in.Up {
		r := in.Conform[bench]
		for _, k := range ConformKeys {
			if f := r.Fields["finding:"+k]; f != "" {
				b.WriteString(f + "\n")
			}
		}
	}
	green := 0
	for _, l := range lines {
		b.WriteString(l.String() + "\n")
		if !l.Red {
			green++
		}
	}
	fmt.Fprintf(&b, "LINEUP %d/%d lined up", green, len(lines))
	if err := OpenGate(lines); err != nil {
		b.WriteString("; REFUSED " + err.Error())
	}
	b.WriteString("\n")
	return b.String()
}

// graphQLNeedles are the GraphQL call sites a binary would carry: the
// endpoint path and the v4 client. They are built at run time from their
// upper case so this binary does not carry them itself.
func graphQLNeedles() [][]byte {
	return [][]byte{[]byte(strings.ToLower("/GRAPHQL")), []byte(strings.ToLower("GITHUBV4"))}
}

// ScanBinaryForGraphQL reads a binary for GraphQL call sites.
func ScanBinaryForGraphQL(path string) ([]string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var hits []string
	for _, n := range graphQLNeedles() {
		if bytes.Contains(body, n) {
			hits = append(hits, string(n))
		}
	}
	return hits, nil
}
