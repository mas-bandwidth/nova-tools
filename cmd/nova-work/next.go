package main

// `nova-work next` is the "what do I do next" verb, and it is the first place
// where all three halves of the work language answer one question together.
//
//	the graph   whose needs are closed          (WorkSet.Ready)
//	the kernel  and whose resources are free    (internal/jobs Admission, #1391)
//	the ladder  and which mind does it          (nova-decide route)
//
// Asking only the first is what let the loop hand out two cards in one lane and
// two units one file. Asking only the first two answers "these may run", which
// is a set and not an answer: a mind wants ONE unit.
//
// The four gates, in order, and each one is a reading rather than a judgment:
//
//  1. READY (the language). Not done, and every need done.
//  2. OWNED (A13). The unit's :owner is this mind, or "all", or -- for a child
//     of a coordinating window -- the generic child spelling or nothing at all.
//     A friend's unit is never handed to a child, because the machinery routes
//     to friends and a friend's work is not a card.
//  3. FREE (A4 to A9). Every unit already :live or :uncertain holds its
//     reservation FIRST -- that is what A4 means by "uncertain keeps its
//     reservation", and the clock never frees it -- and the candidates are then
//     admitted against what is left. One live unit per lane (A6), no two
//     intersecting :writes (A7), atomically over the vector (A8), with no
//     barrier and no wait (A9).
//  4. ROUTED (the ladder). Each survivor goes through nova-decide's own route,
//     which vetoes a unit whose last attempt has not proved it terminated
//     (Stella's lease rule: a rung that may still be running is not a rung to
//     step off) and says with what confidence its first attempt is right. The
//     answer is the candidate with the highest confidence, ties in the order
//     the author wrote them: deterministic, so the same file and the same mind
//     give the same unit every time.
//
// `rung=` on the NEXT line is the ladder's answer, not the asker: --for names
// the OWNER of the work and the rung names the mind the ladder would put on it.
// The two are different axes and the line says both, because a child that reads
// `rung=johnny` on a unit it owns has learned something a merged field would
// have hidden.
//
// --take makes the answer an action: the attempt is opened on the unit, the
// unit goes :live, and the reservation is charged to it from that instant. The
// set's own lock is held across the read and the write, and a lock already held
// is a refusal naming the path rather than a wait.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/jobs"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/worklang"
)

// router is the seam onto nova-decide. Production is the route verb's own two
// paths, in this process: the rules alone, or the rules with Jev among the
// eligible rungs. A test replaces it with a fake, so no unit test dials the
// provider, needs a key on disk or depends on the ladder's arithmetic.
var router = func(ctx context.Context, reg *decide.Registry, u decide.Unit, floor float64, ask bool, baseURL, keyEnv string) (decide.RouteResult, error) {
	if !ask {
		return decide.RouteRules(reg, u, floor)
	}
	client, err := decide.New(baseURL, keyEnv)
	if err != nil {
		return decide.RouteResult{}, err
	}
	return decide.RouteJev(ctx, client, reg, u, floor)
}

// defaultKind is the decide kind a unit of a work set is routed as when it
// carries no :kind of its own. It is a stated convention and not a guess a
// reader has to discover: a unit of a pit-stop set is a verb to build unless
// its author says otherwise, and --kind replaces it for a set that is not.
const defaultKind = decide.KindNewVerb

func cmdNext(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("next", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	file := fs.String("file", "", "the work set to read (required)")
	forMind := fs.String("for", "", "the mind asking: the owner the unit must belong to (required)")
	lanesFile := fs.String("lanes", "", "the lanes file every :lane must name (required)")
	machines := fs.String("machines", "", "the registry of minds the ladder routes over; the embedded one when absent")
	doneList := fs.String("done", "", "comma-separated unit ids that are done, beside what the file says")
	kind := fs.String("kind", defaultKind, "the decide kind a unit carrying no :kind is routed as")
	floor := fs.Float64("floor", decide.DefaultFloor, "confidence floor; below it the ladder steps UP a rung")
	useJev := fs.Bool("jev", true, "ask Jev among the eligible rungs")
	noJev := fs.Bool("no-jev", false, "answer by the rules alone: no key, no network, deterministic")
	baseURL := fs.String("base-url", decide.DefaultBaseURL, "Jev endpoint")
	keyEnv := fs.String("key-env", decide.DefaultKeyEnv, "environment variable holding the key; never a file, never argv")
	usagePath := fs.String("usage", "", "append what a provider call spent to this usage TSV")
	logPath := fs.String("log", "", "append the routing decisions to this log (JSON lines)")
	take := fs.Bool("take", false, "open the attempt on the unit chosen: :state :live, the reservation charged")
	by := fs.String("by", "", "--take: the mind the attempt is filed under; --for when absent")
	started := fs.String("started", "", "--take: when the attempt started; this run's clock when absent")
	limits, bounds := boundFlags(fs)
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, " next", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, " next", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	for name, value := range map[string]string{"--file": *file, "--for": *forMind, "--lanes": *lanesFile} {
		if strings.TrimSpace(value) == "" {
			return refuse(stderr, " next", name+" is required; refusing to guess")
		}
	}
	// The floor is refused here, before anything reads it: NaN compares false
	// against every bound, so a bare range check lets it through and a decision
	// is gated on a number that is not one. The refusal is the ladder's own.
	if err := decide.ValidFloor(*floor); err != nil {
		return refuse(stderr, " next", oneline.Err(err)+"; such as --floor 0.9")
	}
	ask := *useJev && !*noJev
	// The same rule the route verb holds: token spend reporting is an
	// obligation and every decision is logged, so a call nobody can account for
	// is refused rather than made and then forgotten.
	if ask {
		var missing []string
		if strings.TrimSpace(*usagePath) == "" {
			missing = append(missing, "--usage")
		}
		if strings.TrimSpace(*logPath) == "" {
			missing = append(missing, "--log")
		}
		if len(missing) > 0 {
			return refuse(stderr, " next", fmt.Sprintf(
				"a jev call must be accounted for: %s missing; pass them, or --no-jev to answer by the rules alone with no call to account for",
				oneline.Escape(strings.Join(missing, " and "))))
		}
	}
	if !decide.KnownKind(*kind) {
		return refuse(stderr, " next", fmt.Sprintf(
			"--kind %q is not one of %s", *kind, oneline.Escape(strings.Join(decide.Kinds, ", "))))
	}
	lanes, err := readLanes(*lanesFile)
	if err != nil {
		return refuse(stderr, " next", oneline.Err(err))
	}
	reg, err := decide.LoadRegistry(*machines)
	if err != nil {
		return refuse(stderr, " next", oneline.Err(err))
	}

	// --take holds the set's lock across the read, the decision and the write:
	// the unit a mind is told to do and the unit it is recorded as doing are
	// one decision, and another writer between them would make them two.
	if *take {
		release, err := lockSet(*file)
		if err != nil {
			return refuse(stderr, " next", oneline.Err(err))
		}
		defer release()
	}

	data, err := os.ReadFile(*file)
	if err != nil {
		return refuse(stderr, " next", oneline.Err(err))
	}
	ws, err := worklang.ParseWorkSet(*file, data, bounds(limits))
	if err != nil {
		return refuse(stderr, " next", oneline.Err(err))
	}

	picked, none := pickNext(ws, nextOpts{
		Mind: *forMind, Lanes: lanes, LanesFile: *lanesFile, Done: splitIDs(*doneList),
		Registry: reg, Kind: *kind, Floor: *floor, Ask: ask,
		BaseURL: *baseURL, KeyEnv: *keyEnv, Log: *logPath, Usage: *usagePath,
	})
	if none != "" {
		// Every value inside the sentence was escaped where it was interpolated,
		// so the quote here makes it one pasteable token and nothing more.
		fmt.Fprintf(stdout, "NEXT NONE reason=%s\n", oneline.Quote(none))
		return 0
	}

	took := "-"
	if *take {
		owner := strings.TrimSpace(*by)
		if owner == "" {
			owner = *forMind
		}
		stamp := strings.TrimSpace(*started)
		if stamp == "" {
			stamp = time.Now().UTC().Format(time.RFC3339)
		} else if _, err := worklang.ParseStamp(stamp); err != nil {
			return refuse(stderr, " next", oneline.Err(err))
		}
		out, rec, err := worklang.Take(*file, data, picked.Unit.ID, worklang.NewAttempt{
			Rung: picked.Route.Rung.Name, Owner: owner, Started: stamp,
		}, bounds(limits))
		if err != nil {
			return refuse(stderr, " next", oneline.Err(err))
		}
		if err := writeInPlace(*file, out); err != nil {
			return refuse(stderr, " next", oneline.Err(err))
		}
		took = fmt.Sprintf("%d", rec.N)
	}
	fmt.Fprintf(stdout, "NEXT unit=%s lane=%s rung=%s conf=%.2f take=%s reason=%s\n",
		oneline.Field(picked.Unit.ID), field(picked.Unit.Lane()),
		field(picked.Route.Rung.Name), picked.Route.Confidence, oneline.Field(took),
		oneline.Quote(oneline.Escape(picked.Route.Reason)))
	return 0
}

// nextOpts is everything the four gates read. It is a struct so the decision is
// one function a test can drive without a flag set or a file.
type nextOpts struct {
	Mind      string
	Lanes     map[string]bool
	LanesFile string
	Done      map[string]bool
	Registry  *decide.Registry
	Kind      string
	Floor     float64
	Ask       bool
	BaseURL   string
	KeyEnv    string
	Log       string
	Usage     string
}

// candidate is one unit that survived the gates, with the ladder's answer.
type candidate struct {
	Unit  worklang.Unit
	Route decide.RouteResult
	Order int
}

// pickNext runs the four gates and returns the one answer, or the reason there
// is none. The reason is never "nothing to do": it names WHICH gate emptied the
// set, so a mind reading it knows whether to wait, to ask, or to look at a lane
// somebody else is holding.
func pickNext(ws *worklang.WorkSet, opts nextOpts) (candidate, string) {
	ready := ws.Ready(opts.Done)
	if len(ready) == 0 {
		return candidate{}, "no unit of the set is ready: every open unit is waiting on a need"
	}

	var owned []worklang.Unit
	for _, u := range ready {
		if ownerMatches(u.Owner(), opts.Mind) {
			owned = append(owned, u)
		}
	}
	if len(owned) == 0 {
		return candidate{}, fmt.Sprintf(
			"%d units are ready and none of them is %s's: a unit goes to its :owner, and an unowned one only to a child of the coordinating window",
			len(ready), oneline.Escape(opts.Mind))
	}

	// A4 first: what is already running holds its reservation, and the clock
	// never frees it. Only then is anything admitted against what is left.
	a := jobs.New(nil)
	defer a.Close()
	var held []string
	for _, u := range ws.Units {
		if u.ID == "" {
			continue
		}
		if state := u.State(); state == worklang.StateLive || state == "uncertain" {
			if _, err := a.Grant(u.Request()); err == nil {
				held = append(held, u.ID)
			}
		}
	}

	var free []worklang.Unit
	var firstHeld string
	for i, ad := range a.Admit(worklang.Requests(owned)) {
		u := owned[i]
		if lane := u.Lane(); lane != "" && !opts.Lanes[lane] {
			if firstHeld == "" {
				firstHeld = fmt.Sprintf("%s names lane %q, which %s does not",
					oneline.Escape(u.ID), oneline.Escape(lane), oneline.Escape(opts.LanesFile))
			}
			continue
		}
		if !ad.Go {
			if firstHeld == "" {
				firstHeld = fmt.Sprintf("%s is held on %s by %s",
					oneline.Escape(u.ID), oneline.Escape(ad.On()), oneline.Escape(ad.By()))
			}
			continue
		}
		free = append(free, u)
	}
	if len(free) == 0 {
		return candidate{}, fmt.Sprintf(
			"%d units are %s's and ready, and every one of them is held: %s (%d unit(s) already hold a reservation: %s)",
			len(owned), oneline.Escape(opts.Mind), oneline.Escape(firstHeld),
			len(held), oneline.Escape(strings.Join(held, ", ")))
	}

	var picks []candidate
	var waiting string
	for i, u := range free {
		ev, err := evidence(u, opts.Kind)
		if err != nil {
			if waiting == "" {
				waiting = err.Error()
			}
			continue
		}
		res, err := router(context.Background(), opts.Registry, ev, opts.Floor, opts.Ask, opts.BaseURL, opts.KeyEnv)
		if err != nil {
			if waiting == "" {
				waiting = fmt.Sprintf("the ladder could not route %s: %s", oneline.Escape(u.ID), oneline.Err(err))
			}
			continue
		}
		if !res.Dispatchable() {
			// Stella's lease rule: an attempt whose termination is unproved
			// leaves its rung occupied, and a rung that may still be running is
			// not a rung to step off. The unit is an answer about WHO owns the
			// work, not permission to start it again.
			if waiting == "" {
				waiting = fmt.Sprintf("%s waits on %s: %s",
					oneline.Escape(u.ID), oneline.Escape(res.Rung.Name), oneline.Escape(res.Reason))
			}
			continue
		}
		picks = append(picks, candidate{Unit: u, Route: res, Order: i})
	}
	if len(picks) == 0 {
		return candidate{}, fmt.Sprintf(
			"%d units are ready, owned and free, and the ladder dispatches none of them: %s", len(free), oneline.Escape(waiting))
	}
	// Highest confidence first -- the ladder's own number for "the first
	// attempt is right" -- and ties in the order the author wrote them, so one
	// file and one mind always give one answer.
	sort.SliceStable(picks, func(i, j int) bool {
		if picks[i].Route.Confidence != picks[j].Route.Confidence {
			return picks[i].Route.Confidence > picks[j].Route.Confidence
		}
		return picks[i].Order < picks[j].Order
	})
	return picks[0], ""
}

// ownerMatches is A13's rule as a reading. A unit goes to the mind its :owner
// names; "all" goes to anyone; and a CHILD of the coordinating window also
// takes the generic child spelling and a unit nobody owns, because those are
// the coordinator's own work and a child is the coordinator's hand. A friend's
// unit matches nothing but that friend: the machinery routes to friends, and a
// friend's work is an ask rather than a card.
func ownerMatches(owner, mind string) bool {
	owner, mind = strings.TrimSpace(owner), strings.TrimSpace(mind)
	switch {
	case strings.EqualFold(owner, mind):
		return true
	case strings.EqualFold(owner, "all"):
		return true
	case !isChild(mind):
		return false
	case owner == "":
		return true
	default:
		return isChild(owner)
	}
}

// isChild reports whether a name is a child of a coordinating window rather
// than a friend: the generic spellings the sets on the bench use, and the
// `child:<rung>` form the registry's ask field names.
func isChild(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return name == "rowan-child" || name == "stella-child" || strings.HasPrefix(name, "child:")
}

// evidence turns a unit into the ladder's bounded, public evidence: its kind,
// its size read off :writes, the lane it belongs to and its prior attempts. No
// title, no path and no branch name crosses: what the ladder is offered is the
// enumerated projection SPEC-DECIDE fixes, and this is where that boundary is
// drawn for a work set.
func evidence(u worklang.Unit, kind string) (decide.Unit, error) {
	if own := u.Fields["kind"]; own.Kind == worklang.String || own.Kind == worklang.Keyword || own.Kind == worklang.Symbol {
		if text := strings.TrimSpace(own.Value); text != "" {
			if !decide.KnownKind(text) {
				return decide.Unit{}, fmt.Errorf(
					"unit %s carries :kind %s, which is not one of %s", u.ID, text, strings.Join(decide.Kinds, ", "))
			}
			kind = text
		}
	}
	ev := decide.Unit{ID: u.ID, Kind: kind, Files: len(u.Writes()), LaneOwner: u.Lane()}
	if ev.Files > 0 {
		ev.Lanes = 1
	}
	for _, a := range u.Attempts() {
		ev.Attempts = append(ev.Attempts, decide.Attempt{
			Rung: a.Rung, Outcome: ladderOutcome(a.Outcome), Terminated: a.HasProof,
		})
	}
	return ev, nil
}

// ladderOutcome maps A3's outcomes onto the ladder's four. They are two
// vocabularies for the same events and the map is stated once: `uncertain` is
// the ladder's `timeout`, which is exactly what it means -- an attempt with no
// proof that it stopped -- and Terminated carries the proof across, so a
// `uncertain` record that DID prove termination moves the ladder on.
func ladderOutcome(outcome string) string {
	switch outcome {
	case "green":
		return decide.OutcomeOK
	case "red":
		return decide.OutcomeFailed
	case "abandoned", "refused":
		return decide.OutcomeAbandoned
	default:
		return decide.OutcomeTimeout
	}
}
