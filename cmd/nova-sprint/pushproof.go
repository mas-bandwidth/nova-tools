package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/redisconn"
)

// The seat's push proof (docs/SPEC-SPRINT.md, "The push proof"; the owner,
// 2026-10-05: "nova-sprint must require them to setup push notifications, it
// won't work until the AI does this"). The rules are sprint.PushRecord's; here
// are the record in the store, the gate every coordinator verb passes
// (coordinatorOnly), seat push and seat pong, and the push loop's round trip:
// a real delivery through nova-friend's Deliverer, or, for a harness with no
// deliver command (Claude Code), one file written into the folder the session
// watches (folderAdapter).

// keySeatPush is name's push record, a key of the sprint's (store.KV).
func keySeatPush(name string) string { return store.SeatPushKey(name) }

// The proof's test seams. pushArmedDefault is false only in this package's test
// binary (TestMain), where a test arms the proof for the names it registers in
// pushTests, each with the Deliverer its push loop delivers through (any other
// value arms the name and leaves it the real adapter); the binary, and every
// name in it, is armed, and delivers through the harness's own adapter or the
// folder adapter.
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
// through: a test's, the folder adapter for a record that names it or a
// harness with no deliver command (nova-friend's adapter is passive), else the
// harness's own.
func pushDeliverer(rec sprint.PushRecord, now func() time.Time) (friend.Deliverer, error) {
	if d, ok := pushTests.Load(rec.Name); ok {
		if d, ok := d.(friend.Deliverer); ok {
			return d, nil
		}
	}
	if rec.Adapter == sprint.AdapterFolder {
		return &folderAdapter{Dir: rec.Target, Now: now}, nil
	}
	d, err := friend.NewDeliverer(rec.Harness, rec.Target, rec.Session, friend.RealExec, nil)
	if err != nil {
		return nil, err
	}
	if passive(d) {
		return &folderAdapter{Dir: rec.Target, Now: now}, nil
	}
	return d, nil
}

// passive says d is the adapter of a harness with no deliver command: nova-friend's
// Stub, or Claude Code's wake file, which puts no turn into a session.
func passive(d friend.Deliverer) bool {
	_, ok := d.(interface{ Passive() })
	return ok
}

// folderAdapter is the seat's adapter for a harness with no deliver command
// (sprint.AdapterFolder; docs/SPEC-SPRINT.md, "The push proof"): Deliver writes
// the text as one file into Dir and answers 0. A push check is written as
// PROOF-<nonce>, and the checks before it are removed, so the folder holds the
// one the session answers; any other text as PUSH-<clock>-<n>.md, the shape the
// push loop writes a judgment in. Each file is written under a dot name and
// renamed, so a watch never sees half of one. A Dir that is not a directory is
// a failure naming it, and nothing is made.
type folderAdapter struct {
	Dir string
	Now func() time.Time
}

func (f *folderAdapter) Deliver(_ context.Context, text string) (int, error) {
	if fi, err := os.Stat(f.Dir); err != nil || !fi.IsDir() {
		return 0, fmt.Errorf("the folder adapter writes into %s, and it is not a directory: make it, or install the seat with the folder the session watches", f.Dir)
	}
	name := "PUSH-" + f.Now().UTC().Format("20060102T150405Z") + "-" + pushNonce()[:8] + ".md"
	nonce, check := strings.CutPrefix(text, sprint.PushCheckPrefix)
	if check {
		nonce, _, _ = strings.Cut(nonce, "\n")
		name = sprint.PushProofFilePrefix + strings.TrimSpace(nonce)
		// Only the filename reveals the nonce; the body explains how to read it.
		text = strings.ReplaceAll(text, strings.TrimSpace(nonce), "<nonce>")
	}
	tmp, err := os.CreateTemp(f.Dir, ".push-*")
	if err != nil {
		return 0, err
	}
	_, err = tmp.WriteString(text)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), filepath.Join(f.Dir, name))
	}
	if err != nil {
		_ = os.Remove(tmp.Name()) // ignored: the write already failed, and that is the error said
		return 0, fmt.Errorf("the folder delivery failed: %s", sprint.HidePushNonces(sprint.PushRecord{Nonce: strings.TrimSpace(nonce)}, err.Error()))
	}
	if check {
		f.dropOldProofs(name)
	}
	return 0, nil
}

// dropOldProofs removes every PROOF-* file in Dir but keep: only the last
// check's answer counts (sprint.PushPong), and the folder is not left to fill.
func (f *folderAdapter) dropOldProofs(keep string) {
	entries, _ := os.ReadDir(f.Dir) // ignored: the check was written; an old proof left behind is answered by nothing, and the next write sweeps it
	for _, e := range entries {
		if e.Name() != keep && strings.HasPrefix(e.Name(), sprint.PushProofFilePrefix) && e.Type().IsRegular() {
			_ = os.Remove(filepath.Join(f.Dir, e.Name())) // ignored: a stale proof left behind is answered by nothing
		}
	}
}

// sameDir says a and b are one directory.
func sameDir(a, b string) bool {
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	return err == nil && os.SameFile(fa, fb)
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

// pushSaid is name's push as the seat's status says it, "adapter=<a>
// proven=<RFC3339>", proven=- while the seat has no live proof; "" when name
// is not armed or is no one.
func pushSaid(ctx context.Context, st *store.Store, name string, now time.Time) (string, error) {
	if name == "" || !pushArmed(name) {
		return "", nil
	}
	rec, ok, err := readPush(ctx, st, name)
	if err != nil {
		return "", err
	}
	proven := "-"
	if sprint.PushLive(rec, ok, now) {
		proven = rec.Proven.UTC().Format(time.RFC3339)
	}
	return "adapter=" + oneline.Field(rec.AdapterName()) + " proven=" + proven, nil
}

// pushNonce is a fresh nonce for a push check.
func pushNonce() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b) // ignored: crypto/rand.Read never fails (Go 1.24+)
	return hex.EncodeToString(b)
}

// cmdSeatPush is seat push: name's push record. With --harness and --target it
// records the harness and its deliver target (a harness with no deliver command
// gets the folder adapter, and its target must be a directory); with --sent it records a check delivered,
// or with --failed one that failed (the push loop's own report); with neither
// it prints the record and whether the seat is live.
func (a *app) cmdSeatPush(args []string, stdout, stderr io.Writer) int {
	const name = "seat push"
	fs, c := a.verbSetup(name)
	harness := fs.String("harness", "", "the harness the AI holding the seat runs in: its adapter delivers each push into the session (a harness with no deliver command, claude, gets the folder adapter: each push a file in --target)")
	target := fs.String("target", "", "with --harness, the session's directory, where the adapter delivers (for the folder adapter, the directory the session watches, which must be there)")
	session := fs.String("session", "", "with --harness, the session's id, for a harness that names one (default: the adapter's newest in --target)")
	sent := fs.String("sent", "", "the push loop's report: the nonce of the check it delivered")
	failed := fs.String("failed", "", "with --sent, why the delivery of the check failed")
	dry := fs.Bool("dry-run", false, "check the flags and the record and print what would be recorded, and write nothing")
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
		next, why := seatPushTarget(sprint.PushRecord{Name: c.actor, Harness: *harness, Target: *target, Session: *session})
		if why != "" {
			return refuse(stderr, name, why)
		}
		if *dry {
			fmt.Fprintf(stdout, "SEAT PUSH DRY-RUN name=%s harness=%s target=%s adapter=%s; nothing was written\n", oneline.Field(next.Name), oneline.Field(next.Harness), oneline.Field(next.Target), oneline.Field(next.AdapterName()))
			return 0
		}
		if err := writePush(ctx, st, next); err != nil {
			return a.readFailed(name, err, stderr)
		}
		rec, ok = next, true
	case *sent != "":
		if !ok {
			return refuse(stderr, name, "no push target is recorded for "+c.actor+"; run: "+sprint.PushSetup(c.actor, rec, ok))
		}
		next := sprint.PushSent(rec, *sent, *failed, now)
		if *dry {
			fmt.Fprintf(stdout, "SEAT PUSH DRY-RUN name=%s sent=%s failed=%s; nothing was written\n", oneline.Field(next.Name), oneline.Field(*sent), oneline.Field(orDashStr(*failed, "-")))
			return 0
		}
		rec = next
		if err := writePush(ctx, st, rec); err != nil {
			return a.readFailed(name, err, stderr)
		}
	}
	return a.sayPush(rec, ok, now, c.json, stdout)
}

// seatPushTarget is rec as it is recorded, with its adapter, and why it may not
// be, "" is may: a name, a known harness and a target; a harness with no
// deliver command (nova-friend's adapter is passive) gets the folder adapter,
// and its target must be a directory that is there.
func seatPushTarget(rec sprint.PushRecord) (sprint.PushRecord, string) {
	rec.Adapter = ""
	if why := sprint.NotPushTarget(rec); why != "" {
		return rec, why
	}
	target, err := filepath.Abs(rec.Target)
	if err != nil {
		return rec, "--target cannot be resolved to an absolute directory: " + err.Error() + "; give an absolute --target <dir>; nothing was written"
	}
	rec.Target = target
	d, err := friend.NewDeliverer(rec.Harness, rec.Target, rec.Session, friend.RealExec, nil)
	if err != nil {
		return rec, err.Error()
	}
	if !passive(d) {
		return rec, ""
	}
	rec.Adapter = sprint.AdapterFolder
	if fi, err := os.Stat(rec.Target); err != nil || !fi.IsDir() {
		return rec, rec.Harness + " has no deliver command, so the push loop writes each check and judgment as a file into --target, the folder the session watches, and " + rec.Target + " is not a directory; nothing was written"
	}
	return rec, ""
}

// sayPush prints the record: PUSH OK while the seat is live, else PUSH DOWN with
// why and the setup (exit 1).
func (a *app) sayPush(rec sprint.PushRecord, ok bool, now time.Time, asJSON bool, stdout io.Writer) int {
	why := sprint.PushWhy(rec.Name, rec, ok, now)
	if asJSON {
		proof := sprint.PushProofState(rec)
		rec = sprint.PublicPushRecord(rec)
		b, _ := json.Marshal(map[string]any{"record": rec, "recorded": ok, "live": why == "", "proof": proof, "why": why}) // ignored: a record of strings and times always encodes
		fmt.Fprintln(stdout, string(b))
	} else {
		line := fmt.Sprintf("name=%s harness=%s target=%s adapter=%s", oneline.Field(rec.Name), oneline.Field(orDashStr(rec.Harness, "-")), oneline.Field(orDashStr(rec.Target, "-")), oneline.Field(rec.AdapterName()))
		if why == "" {
			fmt.Fprintf(stdout, "PUSH OK %s proven=%s\n", line, rec.Proven.UTC().Format(time.RFC3339))
		} else {
			remedy := sprint.PushSetup(rec.Name, rec, ok)
			if ok && rec.Adapter == sprint.AdapterFolder {
				remedy += "; then, " + sprint.FolderSteps(rec)
			}
			fmt.Fprintf(stdout, "PUSH DOWN %s why=%s remedy=%s\n", line, oneline.Quote(why), oneline.Quote(remedy))
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
	fs, c := a.verbSetup(name)
	dry := fs.Bool("dry-run", false, "check the nonce against the seat's push record and say whether it would prove the seat, and write nothing")
	pos, err := parse(fs, args)
	if err != nil || len(pos) != 1 {
		return refuse(stderr, name, "wants one word, the nonce read from the delivered check; run: nova-sprint seat pong <nonce> --actor <name>")
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
	if *dry {
		fmt.Fprintf(stdout, "SEAT PONG DRY-RUN name=%s would-prove=%s; nothing was written\n", oneline.Field(next.Name), next.Proven.UTC().Format(time.RFC3339))
		return 0
	}
	if err := writePush(ctx, st, next); err != nil {
		return a.readFailed(name, err, stderr)
	}
	sayOK(stdout, c.json, name, fmt.Sprintf("SEAT PONG OK name=%s proven=%s", oneline.Field(next.Name), next.Proven.UTC().Format(time.RFC3339)),
		map[string]any{"name": next.Name, "proven": next.Proven})
	return 0
}

// pushProver is a source the push loop proves the seat through: it reads the
// holder's push record and reports a check delivered. A source without it
// (a test's) proves nothing.
type pushProver interface {
	pushRecord(ctx context.Context, name string) (sprint.PushRecord, bool, error)
	pushSent(ctx context.Context, name, nonce, why string) error
	pushReply(name, nonce string) (string, error)
}

func (s *storeSource) pushReply(name, nonce string) (string, error) {
	if s.redis == "" {
		return "", errors.New("the push source has no direct store address; restart inbox --wait --push seat with --redis <address>")
	}
	if !isTwin(s.redis) {
		if _, err := redisconn.Resolve(redisconn.Options{Addr: s.redis}, nil); err != nil {
			return "", err
		}
	}
	return sprint.PushPongCommand(name, nonce, s.redis, ""), nil
}

func (s *serverSource) pushReply(name, nonce string) (string, error) {
	if s.addr == "" {
		return "", errors.New("the push source has no server address; restart inbox --wait --push seat with NOVA_SPRINT_SERVER set")
	}
	if _, err := redisconn.Resolve(redisconn.Options{Addr: s.addr}, nil); err != nil {
		return "", err
	}
	return sprint.PushPongCommand(name, nonce, "", s.addr), nil
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
	res, err := s.a.ask(ctx, s.addr, []string{"seat", "push"}, []string{"--json", "--actor", name})
	if err != nil {
		return sprint.PushRecord{}, false, err
	}
	var out struct {
		Record   sprint.PushRecord `json:"record"`
		Recorded bool              `json:"recorded"`
		Proof    string            `json:"proof"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &out); err != nil {
		return sprint.PushRecord{}, false, fmt.Errorf("the server's seat push is not JSON (exit %d): %s", res.Code, strings.TrimSpace(res.Stderr))
	}
	return pushScheduleRecord(out.Record, out.Proof), out.Recorded, nil
}

func (s *serverSource) pushSent(ctx context.Context, name, nonce, why string) error {
	words := []string{"--json", "--actor", name, "--sent", nonce}
	if why != "" {
		words = append(words, "--failed", why)
	}
	res, err := s.a.ask(ctx, s.addr, []string{"seat", "push"}, words)
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
	reply, err := pr.pushReply(holder, nonce)
	if err != nil {
		say("DOWN", holder, "", err.Error())
		return
	}
	why := a.deliverPush(ctx, rec, sprint.PushCheckText(nonce, reply))
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
	d, err := pushDeliverer(rec, a.now)
	if err != nil {
		return err.Error()
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
// record of what was pushed, and the session is where it is read. A folder
// adapter whose folder is the holder's inbox has them already: the files the
// loop wrote are the delivery, and nothing is written twice.
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
	if inbox, _, ok := a.seatInbox(holder); ok && rec.Adapter == sprint.AdapterFolder && sameDir(rec.Target, inbox) {
		say("OK", holder, "", "")
		return
	}
	if why := a.deliverPush(ctx, rec, "NOVA SPRINT INBOX: new for the coordinator\n"+strings.Join(texts, "\n")); why != "" {
		say("DOWN", holder, "", "the judgments were written and not delivered: "+why)
		return
	}
	say("OK", holder, "", "")
}

// pushSayer is how the push loop says a step of the proof: PUSH <what> name=
// [proof=pending] [why=] on a line, or one JSON object under --json. The nonce
// is never a status field, even on delivery failure.
func pushSayer(asJSON bool, stdout io.Writer) func(what, name, nonce, why string) {
	return func(what, name, nonce, why string) {
		why = sprint.HidePushNonces(sprint.PushRecord{Nonce: nonce}, why)
		proof := ""
		if nonce != "" {
			proof = "pending"
		}
		if asJSON {
			b, _ := json.Marshal(map[string]any{"push": strings.ToLower(what), "name": name, "proof": proof, "why": why}) // ignored: strings always encode
			fmt.Fprintln(stdout, string(b))
			return
		}
		line := "PUSH " + what + " name=" + oneline.Field(name)
		if nonce != "" {
			line += " proof=pending"
		}
		if why != "" {
			line += " why=" + oneline.Quote(why)
		}
		fmt.Fprintln(stdout, line)
	}
}

// pushScheduleRecord restores only equality/presence for PushDue and PushLive from
// the public state (SPEC-SPRINT, "The push proof"). It never restores a real nonce.
func pushScheduleRecord(rec sprint.PushRecord, proof string) sprint.PushRecord {
	if proof == "" { // an older server's record already carries its scheduler state
		return rec
	}
	rec.Nonce, rec.PongOf = "", ""
	if !rec.Proven.IsZero() {
		rec.PongOf = "previous"
	}
	if proof != "none" {
		rec.Nonce = "current"
	}
	if proof == "proven" {
		rec.PongOf = rec.Nonce
	}
	return rec
}
