package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// The seat's push proof (docs/SPEC-SPRINT.md, "The push proof"; the owner,
// 2026-10-05: "nova-sprint must require them to setup push notifications, it
// won't work until the AI does this"). The rules are sprint.PushRecord's; here
// are the record in the store, the gate every coordinator verb passes
// (coordinatorOnly), seat push and seat pong, and the push loop's round trip,
// a real delivery through nova-friend's Deliverer, never a file write.

// keySeatPush is name's push record, a key of the sprint's (store.KV).
func keySeatPush(name string) string { return store.SeatPushKey(name) }

// pushAdapterCards is the card that brings a harness its deliver command, for a
// harness whose adapter is still the Stub: the seat cannot be taken from it
// until the card lands.
var pushAdapterCards = map[string]string{"claude": "fg-claude-open-chatb-r"}

// The proof's test seams. pushArmedDefault is false only in this package's test
// binary (TestMain), where a test arms the proof for the names it registers in
// pushTests, each with the Deliverer its push loop delivers through; the
// binary, and every name in it, is armed, and delivers through the harness's
// own adapter.
var (
	pushArmedDefault = true
	pushTests        sync.Map // name -> friend.Deliverer
)

// pushArmed says name's coordinator verbs want a live proof.
func pushArmed(name string) bool {
	if pushArmedDefault {
		return true
	}
	_, ok := pushTests.Load(name)
	return ok
}

// pushDeliverer is the adapter the push loop delivers into rec's session
// through: the harness's own, or a test's.
func pushDeliverer(rec sprint.PushRecord) (friend.Deliverer, error) {
	if d, ok := pushTests.Load(rec.Name); ok {
		return d.(friend.Deliverer), nil
	}
	return friend.NewDeliverer(rec.Harness, rec.Target, rec.Session, friend.RealExec, nil)
}

// stubRefusal is why the seat cannot be pushed to through d, "" when it can:
// a harness whose adapter is the Stub has no deliver command, and the refusal
// names the card that brings it one.
func stubRefusal(harness string, d friend.Deliverer) string {
	s, ok := d.(friend.Stub)
	if !ok {
		return ""
	}
	why := harness + "'s adapter is the Stub, with no deliver command: the push loop cannot reach a " + harness + " session, so the seat cannot be held from one"
	if card, ok := pushAdapterCards[harness]; ok {
		why += " until " + card + " lands"
	} else if s.Reason != "" {
		why += " (" + s.Reason + ")"
	}
	return why + "; nothing was written"
}

// readPush is name's push record; ok false when there is none.
func readPush(ctx context.Context, st *store.Store, name string) (sprint.PushRecord, bool, error) {
	var rec sprint.PushRecord
	kv, ok := st.B.(store.KV)
	if !ok || name == "" {
		return rec, false, nil
	}
	raw, ok, err := kv.GetKey(ctx, keySeatPush(name))
	if err != nil || !ok {
		return rec, false, err
	}
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return rec, false, fmt.Errorf("the push record of %s is not JSON: %w", name, err)
	}
	return rec, true, nil
}

// writePush writes rec as its name's push record.
func writePush(ctx context.Context, st *store.Store, rec sprint.PushRecord) error {
	kv, ok := st.B.(store.KV)
	if !ok {
		return errors.New("this store keeps no keys, and the push record is one")
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if err := kv.SetKey(ctx, keySeatPush(rec.Name), string(b)); err != nil {
		return err
	}
	// the name joins the list teardown deletes the records by
	var names []string
	raw, ok, err := kv.GetKey(ctx, store.KeySeatPushers)
	if err != nil {
		return err
	}
	if ok {
		_ = json.Unmarshal([]byte(raw), &names) // ignored: an unreadable list is written again whole
	}
	if slices.Contains(names, rec.Name) {
		return nil
	}
	l, err := json.Marshal(append(names, rec.Name))
	if err != nil {
		return err
	}
	return kv.SetKey(ctx, store.KeySeatPushers, string(l))
}

// pushGate is the line name's coordinator verbs are refused with while its seat
// has no live proof (sprint.PushDown), "" when it has one or is not armed.
func pushGate(ctx context.Context, st *store.Store, name string, now time.Time) (string, error) {
	if !pushArmed(name) {
		return "", nil
	}
	rec, ok, err := readPush(ctx, st, name)
	if err != nil {
		return "", err
	}
	return sprint.PushDown(name, rec, ok, now), nil
}

// pushNonce is a fresh nonce for a push check.
func pushNonce() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b) // ignored: crypto/rand.Read never fails (Go 1.24+)
	return hex.EncodeToString(b)
}

// cmdSeatPush is seat push: name's push record. With --harness and --target it
// records the harness and its deliver target (a harness whose adapter is the
// Stub is refused, naming its card); with --sent it records a check delivered,
// or with --failed one that failed (the push loop's own report); with neither
// it prints the record and whether the seat is live.
func (a *app) cmdSeatPush(args []string, stdout, stderr io.Writer) int {
	const name = "seat push"
	fs, c := a.verbSetup("seat")
	harness := fs.String("harness", "", "the harness the AI holding the seat runs in: its adapter delivers each push into the session")
	target := fs.String("target", "", "with --harness, the session's directory, where the adapter delivers")
	session := fs.String("session", "", "with --harness, the session's id, for a harness that names one (default: the adapter's newest in --target)")
	sent := fs.String("sent", "", "the push loop's report: the nonce of the check it delivered")
	failed := fs.String("failed", "", "with --sent, why the delivery of the check failed")
	if pos, err := parse(fs, args); err != nil || len(pos) > 0 {
		return refuse(stderr, name, argErr("takes no words ", err, pos...))
	}
	if c.actor == "" {
		return refuse(stderr, name, "seat push wants --actor <name> (or NOVA_SPRINT_ACTOR): the seat whose push record it is; nothing was changed")
	}
	if *failed != "" && *sent == "" {
		return refuse(stderr, name, "--failed goes with --sent")
	}
	if *sent != "" && *harness != "" {
		return refuse(stderr, name, "--sent is the push loop's report and --harness the setup: one at a time")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	ctx := context.Background()
	rec, ok, err := readPush(ctx, st, c.actor)
	if err != nil {
		return a.readFailed(name, err, stderr)
	}
	rec.Name = c.actor
	now := a.now()
	switch {
	case *harness != "":
		next := sprint.PushRecord{Name: c.actor, Harness: *harness, Target: *target, Session: *session}
		if why := a.pushTargetRefusal(next); why != "" {
			return refuse(stderr, name, why)
		}
		if err := writePush(ctx, st, next); err != nil {
			return a.readFailed(name, err, stderr)
		}
		rec, ok = next, true
	case *sent != "":
		if !ok {
			return refuse(stderr, name, "no push target is recorded for "+c.actor+"; run: "+sprint.PushSetup(c.actor, rec, ok))
		}
		rec = sprint.PushSent(rec, *sent, *failed, now)
		if err := writePush(ctx, st, rec); err != nil {
			return a.readFailed(name, err, stderr)
		}
	}
	return a.sayPush(rec, ok, now, c.json, stdout)
}

// pushTargetRefusal is why rec may not be recorded, "" is may: a name, a known
// harness, a target, and an adapter that is no Stub.
func (a *app) pushTargetRefusal(rec sprint.PushRecord) string {
	if why := sprint.NotPushTarget(rec); why != "" {
		return why
	}
	d, err := friend.NewDeliverer(rec.Harness, rec.Target, rec.Session, friend.RealExec, nil)
	if err != nil {
		return err.Error()
	}
	return stubRefusal(rec.Harness, d)
}

// sayPush prints the record: PUSH OK while the seat is live, else PUSH DOWN with
// why and the setup (exit 1).
func (a *app) sayPush(rec sprint.PushRecord, ok bool, now time.Time, asJSON bool, stdout io.Writer) int {
	why := sprint.PushWhy(rec.Name, rec, ok, now)
	if asJSON {
		b, _ := json.Marshal(map[string]any{"record": rec, "recorded": ok, "live": why == "", "why": why}) // ignored: a record of strings and times always encodes
		fmt.Fprintln(stdout, string(b))
	} else {
		line := fmt.Sprintf("name=%s harness=%s target=%s", oneline.Field(rec.Name), oneline.Field(orDashStr(rec.Harness, "-")), oneline.Field(orDashStr(rec.Target, "-")))
		if why == "" {
			fmt.Fprintf(stdout, "PUSH OK %s proven=%s\n", line, rec.Proven.UTC().Format(time.RFC3339))
		} else {
			fmt.Fprintf(stdout, "PUSH DOWN %s why=%s remedy=%s\n", line, oneline.Quote(why), oneline.Quote(sprint.PushSetup(rec.Name, rec, ok)))
		}
	}
	if why != "" {
		return 1
	}
	return 0
}

// cmdSeatPong is seat pong <nonce>: the session's answer to the push check, run
// from inside the session the check reached. Only the last check delivered
// counts, and once.
func (a *app) cmdSeatPong(args []string, stdout, stderr io.Writer) int {
	const name = "seat pong"
	fs, c := a.verbSetup("seat")
	pos, err := parse(fs, args)
	if err != nil || len(pos) != 1 {
		return refuse(stderr, name, argErr("wants one word, the nonce the push check carried, ", err, pos...))
	}
	if c.actor == "" {
		return refuse(stderr, name, "seat pong wants --actor <name> (or NOVA_SPRINT_ACTOR): the seat the check was pushed to; nothing was changed")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	ctx := context.Background()
	rec, ok, err := readPush(ctx, st, c.actor)
	if err != nil {
		return a.readFailed(name, err, stderr)
	}
	rec.Name = c.actor
	now := a.now()
	next, why := sprint.PushPong(rec, ok, strings.TrimSpace(pos[0]), now)
	if why != "" {
		return refuse(stderr, name, why)
	}
	if err := writePush(ctx, st, next); err != nil {
		return a.readFailed(name, err, stderr)
	}
	sayOK(stdout, c.json, name, fmt.Sprintf("SEAT PONG OK name=%s nonce=%s proven=%s", oneline.Field(next.Name), oneline.Field(next.PongOf), next.Proven.UTC().Format(time.RFC3339)),
		map[string]any{"name": next.Name, "nonce": next.PongOf, "proven": next.Proven})
	return 0
}

// pushProver is a source the push loop proves the seat through: it reads the
// holder's push record and reports a check delivered. A source without it
// (a test's) proves nothing.
type pushProver interface {
	pushRecord(ctx context.Context, name string) (sprint.PushRecord, bool, error)
	pushSent(ctx context.Context, name, nonce, why string) error
}

func (s *storeSource) pushRecord(ctx context.Context, name string) (sprint.PushRecord, bool, error) {
	return readPush(ctx, s.st, name)
}

func (s *storeSource) pushSent(ctx context.Context, name, nonce, why string) error {
	rec, ok, err := readPush(ctx, s.st, name)
	if err != nil || !ok {
		return err
	}
	return writePush(ctx, s.st, sprint.PushSent(rec, nonce, why, s.st.Now()))
}

func (s *serverSource) pushRecord(ctx context.Context, name string) (sprint.PushRecord, bool, error) {
	res, err := s.a.ask(ctx, s.addr, []string{"seat"}, []string{"push", "--json", "--actor", name})
	if err != nil {
		return sprint.PushRecord{}, false, err
	}
	var out struct {
		Record   sprint.PushRecord `json:"record"`
		Recorded bool              `json:"recorded"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &out); err != nil {
		return sprint.PushRecord{}, false, fmt.Errorf("the server's seat push is not JSON (exit %d): %s", res.Code, strings.TrimSpace(res.Stderr))
	}
	return out.Record, out.Recorded, nil
}

func (s *serverSource) pushSent(ctx context.Context, name, nonce, why string) error {
	words := []string{"push", "--json", "--actor", name, "--sent", nonce}
	if why != "" {
		words = append(words, "--failed", why)
	}
	res, err := s.a.ask(ctx, s.addr, []string{"seat"}, words)
	if err != nil {
		return err
	}
	if res.Code == 2 {
		return errors.New(strings.TrimSpace(res.Stderr))
	}
	return nil
}

// prove is the push loop's round trip for the holder, at each look: when a
// check is due (sprint.PushDue) a fresh nonce goes into the session through
// the harness's adapter and the delivery, or its failure, is recorded; the
// session's seat pong proves it. It says each check on stdout.
func (a *app) prove(ctx context.Context, src inboxSource, holder string, asJSON bool, stdout io.Writer) {
	say := pushSayer(asJSON, stdout)
	pr, ok := src.(pushProver)
	if !ok || holder == "" {
		return
	}
	rec, found, err := pr.pushRecord(ctx, holder)
	if err != nil {
		say("DOWN", holder, "", "the push record: "+err.Error())
		return
	}
	if !found || !sprint.PushDue(rec, a.now()) {
		return
	}
	nonce := pushNonce()
	why := a.deliverPush(ctx, rec, sprint.PushCheckText(holder, nonce))
	if err := pr.pushSent(ctx, holder, nonce, why); err != nil {
		say("DOWN", holder, nonce, "the check went out and could not be recorded: "+err.Error())
		return
	}
	if why != "" {
		say("DOWN", holder, nonce, why)
		return
	}
	say("CHECK", holder, nonce, "")
}

// deliverPush delivers text into rec's session through its adapter, bounded by
// sprint.PushAnswerBound: "" when it went in, else why not.
func (a *app) deliverPush(ctx context.Context, rec sprint.PushRecord, text string) string {
	d, err := pushDeliverer(rec)
	if err != nil {
		return err.Error()
	}
	if why := stubRefusal(rec.Harness, d); why != "" {
		return why
	}
	dctx, cancel := context.WithTimeout(ctx, sprint.PushAnswerBound)
	defer cancel()
	exit, err := d.Deliver(dctx, text)
	var deferred friend.Deferred
	switch {
	case errors.As(err, &deferred):
		return deferred.Error()
	case err != nil:
		return err.Error()
	case exit != 0:
		return fmt.Sprintf("%s's deliver command exited %d", rec.Harness, exit)
	}
	return ""
}

// pushJudgments delivers the groups just written for holder into the holder's
// session as one turn, through the same adapter as the proof: the files are the
// record of what was pushed, and the session is where it is read.
func (a *app) pushJudgments(ctx context.Context, src inboxSource, holder string, texts []string, asJSON bool, stdout io.Writer) {
	say := pushSayer(asJSON, stdout)
	pr, ok := src.(pushProver)
	if !ok || holder == "" || len(texts) == 0 {
		return
	}
	rec, found, err := pr.pushRecord(ctx, holder)
	if err != nil || !found {
		return // the proof says PUSH DOWN for it at the next look
	}
	if why := a.deliverPush(ctx, rec, "NOVA SPRINT INBOX: new for the coordinator\n"+strings.Join(texts, "\n")); why != "" {
		say("DOWN", holder, "", "the judgments were written and not delivered: "+why)
		return
	}
	say("OK", holder, "", "")
}

// pushSayer is how the push loop says a step of the proof: PUSH <what> name=
// [nonce=] [why=] on a line, or one JSON object under --json.
func pushSayer(asJSON bool, stdout io.Writer) func(what, name, nonce, why string) {
	return func(what, name, nonce, why string) {
		if asJSON {
			b, _ := json.Marshal(map[string]any{"push": strings.ToLower(what), "name": name, "nonce": nonce, "why": why}) // ignored: strings always encode
			fmt.Fprintln(stdout, string(b))
			return
		}
		line := "PUSH " + what + " name=" + oneline.Field(name)
		if nonce != "" {
			line += " nonce=" + oneline.Field(nonce)
		}
		if why != "" {
			line += " why=" + oneline.Quote(why)
		}
		fmt.Fprintln(stdout, line)
	}
}
