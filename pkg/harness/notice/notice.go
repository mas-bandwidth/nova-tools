// Package notice is the one delivery rule every nova-hook harness shares
// (docs/SPEC-HOOK.md, the harness notice; TLA+ model owed: tla/HarnessNotice.tla,
// TLC on a bench later). A harness
// is a Shim: only its mechanism, how text gets into a running turn, how an
// idle session is woken, and how consumption is observed. The Core owns the
// rules, once:
//
//  1. At most one pending notice per friend. It points at a saved file the
//     Core rewrites in place while it is pending: the newest valid challenge
//     and a count of the ordinary messages.
//  2. A valid challenge has the canonical body, and the hook record's active
//     sum and epoch. A stale or lookalike challenge never replaces or hides
//     the current one; it counts as an ordinary message. A proved, superseded
//     or consumed challenge is cleared.
//  3. A new push happens only after the previous notice is consumed, and each
//     is consumed exactly once. Consumption is the shim's own turn boundary or
//     the model's own act on the bus (a proof, an answer, a read).
//     3a. Liveness: when a newer valid challenge is held than the pending notice
//     showed, and the pending notice has gone one challenge interval (store
//     clock) without consumption, the Core re-pushes that same notice (same
//     id and file, rewritten in place). There is never a second pending one.
//  4. Only the model proves. The Core never runs prove and never writes a proof.
//  5. A push whose native outcome is unknown (codexdesktop.ErrOutcomeUnknown)
//     may have reached the session: the notice is pending as if accepted, the
//     route keeps running and the record keeps its epoch. Only rule 3a pushes
//     it again; an unknown outcome never stops the route, so it never lets
//     the heartbeat unhook the name (tla/HarnessNoticeUnknown.tla).
//  6. A shim whose session can move (Follower: a friend who starts a new
//     chat) is asked where it is once a FollowEvery. When it moved, the
//     pending notice, told to the place it left, is pushed once into the new
//     one (same id and file); there is still one pending notice
//     (tla/HarnessNoticeUnknown.tla, Follow).
package notice

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/codexdesktop"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// MaxSaved bounds one saved notice file, as the native receivers check it.
const MaxSaved = 64 << 10

// challengePrefix is the subject of every heartbeat challenge.
const challengePrefix = "HOOK CHALLENGE nonce="

// wireMisses is the historical body's count. New readers accept this exact
// old body during rollout but the server emits the current silence wording.
const wireMisses = 2

// legacyChallengeText retains the exact old wire form for backward reading.
func legacyChallengeText(name, nonce string, every time.Duration) string {
	return fmt.Sprintf("HOOK CHALLENGE nonce=%s\nThe hook's heartbeat asks whether the model is here. Answer from inside this turn, before anything else, within %s, with: nova-hook prove --as %s --nonce %s\nOnly the model answers: no daemon, loop or script may. %d unanswered in a row unhook %s.\n", nonce, every, name, nonce, wireMisses, name)
}

// ChallengeText is the current canonical body. Receivers also accept the old
// body during rollout; a returning old receiver updates through adopt.
func ChallengeText(name, nonce string, bound time.Duration) string {
	return fmt.Sprintf("HOOK CHALLENGE nonce=%s\nThe hook's heartbeat asks whether the model is here. Answer from inside this turn, before anything else, within %s, with: nova-hook prove --as %s --nonce %s\nOnly the model answers: no daemon, loop or script may. An unanswered challenge past its deadline and continued silence can unhook %s.\n", nonce, bound, name, nonce, name)
}

// Canonical is the nonce of m when m has the server's whole challenge shape
// for recipient: the subject, a safe nonce and the body byte for byte. It
// never grants a proof; only the record's active sum makes it current.
func Canonical(m bus.Message, recipient string) (string, bool) {
	nonce, _, ok := canonical(m, recipient)
	return nonce, ok
}

// canonical is Canonical and the challenge interval the body names.
func canonical(m bus.Message, recipient string) (string, time.Duration, bool) {
	nonce, ok := strings.CutPrefix(m.Subject, challengePrefix)
	if !ok {
		return "", 0, false
	}
	if _, err := codexdesktop.SavedPath("/validated", nonce); err != nil {
		return "", 0, false
	}
	_, rest, ok := strings.Cut(m.Body, ", before anything else, within ")
	if !ok {
		return "", 0, false
	}
	interval, _, ok := strings.Cut(rest, ", with: ")
	if !ok {
		return "", 0, false
	}
	every, err := time.ParseDuration(interval)
	if err != nil || every < time.Second || (m.Body != legacyChallengeText(recipient, nonce, every) && m.Body != ChallengeText(recipient, nonce, every)) {
		return "", 0, false
	}
	return nonce, every, true
}

// Activity is the model's own acts on the bus, read in one round trip. It
// moves when the model proves, answers a message, or reads its group; a new
// message arriving never moves it.
type Activity struct {
	AnsweredAt, Proof, Group string
	Answers, Owed            int64
}

// Since reports whether the model acted on the bus after a was read.
func (b Activity) Since(a Activity) bool {
	return (b.AnsweredAt != "" && b.AnsweredAt != a.AnsweredAt) || (b.Proof != "" && b.Proof != a.Proof) ||
		b.Answers > a.Answers || b.Owed < a.Owed || b.Group != a.Group
}

// Store is what the Core reads of the hook record and the bus, and the one
// write a failed route makes.
type Store interface {
	// Active is name's outstanding challenge: its nonce sum and epoch, or "" and 0.
	Active(ctx context.Context, name string) (sum string, epoch int64, err error)
	// Answered is the subset of ids the model already answered with nova-bus send --re.
	Answered(ctx context.Context, name string, ids []string) (map[string]bool, error)
	// Activity is the model's own acts on the bus now.
	Activity(ctx context.Context, name string) (Activity, error)
	// Now is the store's clock, stamped on a saved notice.
	Now(ctx context.Context) (time.Time, error)
	// Unprove drops the bus push's proof when the route can no longer deliver.
	Unprove(ctx context.Context, name string, role hook.Role, epoch int64) error
}

// Recovery is the startup backlog a native route carries in its first notice.
type Recovery struct {
	Messages []bus.Message
	Since    string
	Coverage string
}

// Notice is the one pending notice: what the saved file holds.
type Notice struct {
	ID        string
	Path      string // the saved file the pushed text names
	Challenge *bus.Message
	Nonce     string
	Count     int
	Senders   []string
	Recovery  *Recovery
}

// Empty reports whether the notice holds nothing to tell.
func (n Notice) Empty() bool { return n.Challenge == nil && n.Count == 0 && n.Recovery == nil }

// Text is the closed one-line text a line harness puts in front of the model.
// It names the saved file only; no subject or body enters it.
func (n Notice) Text() string {
	return "nova-hook notice: read " + n.Path + " (it holds the current challenge and the message count); then read the bus"
}

// Shim is one harness's mechanism, and nothing else.
type Shim interface {
	// Name is the harness word, as a PUSH line spells it.
	Name() string
	// Dir is the existing private directory the saved notice files live in.
	Dir() string
	// Push tells the session a notice is at n.Path: into a running turn, or
	// waking an idle session. It answers the receipt (a turn id, a kind).
	Push(ctx context.Context, n Notice) (string, error)
	// Consumed reports whether the session reached its own turn boundary
	// since n was pushed. The Core asks only while n is pending.
	Consumed(ctx context.Context, n Notice) (bool, error)
}

// Follower is a shim whose session can move: a friend who starts a new chat.
// Follow points the shim at where the session is now.
type Follower interface {
	Follow(ctx context.Context) (from, to string, moved bool, err error)
}

// FollowEvery is how often, on the store clock, the Core asks a Follower
// where the session is.
const FollowEvery = hook.RenewEvery

// Clearer is a shim that removes its in-turn surface once a notice is consumed.
type Clearer interface {
	Clear(ctx context.Context, n Notice) error
}

// Files writes a saved notice: create refuses an existing file, replace
// rewrites the pending one in place.
type Files interface {
	Save(path string, data []byte, replace bool) error
}

// Core is the one pending notice of one friend.
type Core struct {
	As    string
	Role  hook.Role
	Epoch int64
	Redis string // the store address the receiver instructions name
	Store Store
	Shim  Shim
	Files Files
	NewID func() string
	Say   func(*tool.Out) // one PUSH line per push; the Monitor harnesses take it as their mechanism

	held   Notice    // everything since the last consumption
	out    *Notice   // pushed and not consumed
	shown  string    // the nonce the pending notice held when it was last pushed
	mark   Activity  // the model's bus activity when it was pushed
	pushed time.Time // the store clock at the pending notice's last push
	looked time.Time // the store clock when a Follower was last asked
}

// Pending is the pushed notice not yet consumed, if any.
func (c *Core) Pending() (Notice, bool) {
	if c.out == nil {
		return Notice{}, false
	}
	return *c.out, true
}

// Rearm moves the Core to a new epoch: a held challenge of the old one is
// cleared at the next step, as any no longer active is.
func (c *Core) Rearm(epoch int64) { c.Epoch = epoch }

// Recover sets the startup backlog the next notice carries.
func (c *Core) Recover(r *Recovery) {
	if r != nil && (len(r.Messages) > 0 || r.Coverage != "" || r.Since != "") {
		c.held.Recovery = r
	}
}

// Offer folds one read of the bus into the held notice, then rewrites the
// pending file in place. Messages the model already answered are skipped.
func (c *Core) Offer(ctx context.Context, ms []bus.Message) error {
	if len(ms) == 0 {
		return nil
	}
	ids := make([]string, 0, len(ms))
	for _, m := range ms {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	if err := c.settle(ctx); err != nil {
		return err
	}
	answered, err := c.Store.Answered(ctx, c.As, ids)
	if err != nil {
		return fmt.Errorf("read model answers: %w", err)
	}
	var sum string
	var epoch int64
	read := false
	for _, m := range ms {
		if m.ID != "" && answered[m.ID] {
			continue
		}
		if nonce, ok := Canonical(m, c.As); ok {
			if !read {
				if sum, epoch, err = c.Store.Active(ctx, c.As); err != nil {
					return fmt.Errorf("read the active challenge: %w", err)
				}
				read = true
			}
			if sum != "" && sum == hook.Sum(nonce) && epoch == c.Epoch {
				cp := m
				c.held.Challenge, c.held.Nonce = &cp, nonce
				continue
			}
		}
		c.held.Count++
		from := cmp.Or(m.From, "unknown")
		if !slices.Contains(c.held.Senders, from) {
			c.held.Senders = append(c.held.Senders, from)
		}
	}
	if c.out == nil {
		return nil
	}
	n := c.held
	n.ID, n.Path = c.out.ID, c.out.Path
	if err := c.save(ctx, n, true); err != nil {
		return err
	}
	c.out = &n
	return nil
}

// Step observes consumption, clears what no longer stands, and pushes the
// held notice when none is pending, or re-pushes the pending one when a newer
// challenge has waited behind it for one interval (rule 3a). A certain push
// failure stops the route: it is never retried here. An unknown outcome keeps
// the notice pending (rule 5).
func (c *Core) Step(ctx context.Context) error {
	if f, ok := c.Shim.(Follower); ok {
		if err := c.follow(ctx, f); err != nil {
			return err
		}
	}
	if err := c.settle(ctx); err != nil {
		return err
	}
	if c.out != nil {
		return c.repush(ctx)
	}
	if err := c.current(ctx); err != nil {
		return err
	}
	if c.held.Empty() {
		return nil
	}
	mark, err := c.Store.Activity(ctx, c.As)
	if err != nil {
		return fmt.Errorf("read bus activity: %w", err)
	}
	n := c.held
	n.ID = c.NewID()
	path, err := codexdesktop.SavedPath(c.Shim.Dir(), n.ID)
	if err != nil {
		return err
	}
	n.Path = path
	if err := c.save(ctx, n, false); err != nil {
		return err
	}
	at, err := c.Store.Now(ctx)
	if err != nil {
		return fmt.Errorf("read the store clock: %w", err)
	}
	receipt, err := c.Shim.Push(ctx, n)
	if err != nil && !errors.Is(err, codexdesktop.ErrOutcomeUnknown) {
		return err
	}
	// An unknown outcome may have reached the session, so the notice is
	// pending exactly as an accepted one (rule 5): it is never pushed again
	// blindly, only by rule 3a, and the route and the record stand.
	c.out, c.shown, c.mark, c.pushed = &n, n.Nonce, mark, at
	c.sayUnknown(n, receipt, false, err)
	return nil
}

// follow is rule 6: once a FollowEvery, the Core asks where the session is.
// When it moved (a friend who started a new chat), a pending notice was told
// to the chat it left, so the same notice (same id and file) is pushed once
// into the new one. A failed look keeps the route where it is and says so: it
// never stops the route.
func (c *Core) follow(ctx context.Context, f Follower) error {
	now, err := c.Store.Now(ctx)
	if err != nil {
		return fmt.Errorf("read the store clock: %w", err)
	}
	if !c.looked.IsZero() && now.Sub(c.looked) < FollowEvery {
		return nil
	}
	c.looked = now
	from, to, moved, err := f.Follow(ctx)
	if err != nil {
		if c.Say != nil {
			c.Say(tool.Done().As("STATE").Fact("state", "follow-unknown").Fact("at", from).Note("where the session is now could not be read: " + err.Error() + "; the route stays where it is and looks again in " + FollowEvery.String()))
		}
		return nil
	}
	if !moved {
		return nil
	}
	if c.Say != nil {
		c.Say(tool.Done().As("STATE").Fact("state", "moved").Fact("from", from).Fact("to", to).Note("the session moved: the route follows it"))
	}
	if c.out == nil {
		return nil
	}
	receipt, err := c.Shim.Push(ctx, *c.out)
	if err != nil && !errors.Is(err, codexdesktop.ErrOutcomeUnknown) {
		return err
	}
	c.shown, c.pushed = c.out.Nonce, now
	c.sayUnknown(*c.out, receipt, true, err)
	return nil
}

// current clears a held challenge the record no longer holds as active.
func (c *Core) current(ctx context.Context) error {
	if c.held.Challenge == nil {
		return nil
	}
	sum, epoch, err := c.Store.Active(ctx, c.As)
	if err != nil {
		return fmt.Errorf("read the active challenge: %w", err)
	}
	if sum == "" || sum != hook.Sum(c.held.Nonce) || epoch != c.Epoch {
		c.held.Challenge, c.held.Nonce = nil, ""
	}
	return nil
}

// repush is rule 3a: the pending notice, unconsumed for one challenge
// interval, is pushed again when it now holds a newer valid challenge than
// its last push showed. Its file was rewritten in place by Offer; the id and
// path stay, so there is still one pending notice.
func (c *Core) repush(ctx context.Context) error {
	if c.held.Challenge == nil || c.held.Nonce == c.shown {
		return nil
	}
	_, every, ok := canonical(*c.held.Challenge, c.As)
	if !ok {
		return nil
	}
	now, err := c.Store.Now(ctx)
	if err != nil {
		return fmt.Errorf("read the store clock: %w", err)
	}
	if now.Sub(c.pushed) < every {
		return nil
	}
	if err := c.current(ctx); err != nil || c.held.Challenge == nil {
		return err
	}
	receipt, err := c.Shim.Push(ctx, *c.out)
	if err != nil && !errors.Is(err, codexdesktop.ErrOutcomeUnknown) {
		return err
	}
	c.shown, c.pushed = c.held.Nonce, now
	c.sayUnknown(*c.out, receipt, true, err)
	return nil
}

// sayUnknown is say, or, for an unknown outcome (err non-nil), the PUSH line
// that says so: the notice stays pending and only rule 3a pushes it again.
func (c *Core) sayUnknown(n Notice, receipt string, again bool, err error) {
	if err == nil {
		c.say(n, receipt, again)
		return
	}
	if c.Say == nil {
		return
	}
	o := tool.Done().As("PUSH").Fact("push", c.Shim.Name()).Fact("id", n.ID).Fact("receipt", "unknown").Fact("native", "unknown").Fact("messages", n.Count)
	if again {
		o.Fact("repush", true)
	}
	c.Say(o.Note("native outcome unknown: " + err.Error() + "; the notice stays pending and the hook stays as it is; it is pushed again only when a newer challenge has waited one interval behind it"))
}

// say is the one PUSH line a push prints.
func (c *Core) say(n Notice, receipt string, again bool) {
	if c.Say == nil {
		return
	}
	o := tool.Done().As("PUSH").Fact("push", c.Shim.Name()).Fact("id", n.ID).Fact("receipt", receipt).Fact("messages", n.Count)
	if n.Nonce != "" {
		o.Fact("nonce", n.Nonce)
	}
	if again {
		o.Fact("repush", true)
	}
	c.Say(o.Fact("notice", tool.Text(n.Text())).Note("one notice; only the model proves"))
}

// settle observes whether the pending notice was consumed and, once it is,
// clears what it told: the count, the recovery, and the challenge it held
// when pushed. It runs before an arrival folds, so a message that arrives
// with the consumption is never cleared with it.
func (c *Core) settle(ctx context.Context) error {
	if c.out == nil {
		return nil
	}
	done, err := c.consumed(ctx)
	if err != nil || !done {
		return err
	}
	consumed := *c.out
	c.out = nil
	c.held.Count, c.held.Senders, c.held.Recovery = 0, nil, nil
	if c.held.Nonce == c.shown {
		c.held.Challenge, c.held.Nonce = nil, ""
	}
	if cl, ok := c.Shim.(Clearer); ok {
		return cl.Clear(ctx, consumed)
	}
	return nil
}

func (c *Core) consumed(ctx context.Context) (bool, error) {
	done, err := c.Shim.Consumed(ctx, *c.out)
	if err != nil || done {
		return done, err
	}
	now, err := c.Store.Activity(ctx, c.As)
	if err != nil {
		return false, fmt.Errorf("read bus activity: %w", err)
	}
	return now.Since(c.mark), nil
}

// Fail is a stopped route's one result: the bus push's proof is dropped
// (never the record or its epoch), and the delivery is never retried.
func (c *Core) Fail(ctx context.Context, err error) *tool.Out {
	dropErr := c.Store.Unprove(ctx, c.As, c.Role, c.Epoch)
	outcome := "failed"
	if errors.Is(err, codexdesktop.ErrOutcomeUnknown) {
		outcome = "unknown"
	}
	o := tool.Fail(c.Shim.Name()+" local delivery "+outcome+": "+err.Error()).Fact("native", outcome)
	o.Remedy = "inspect the saved notice and the model's bus answers before restarting the route; never blindly retry an accepted or unknown delivery"
	if dropErr != nil {
		o.Note("the route proof could not be revoked: " + dropErr.Error() + "; this client stops renewing its lease")
	}
	return o
}

// Envelope is the saved notice file: bounded external data, never liveness.
type Envelope struct {
	LocalReceiver struct {
		Identity    string `json:"identity"`
		Redis       string `json:"redis"`
		Proof       string `json:"proof"`
		Acknowledge string `json:"acknowledge"`
	} `json:"local_receiver"`
	SavedAt            time.Time     `json:"saved_at"`
	HookEpoch          int64         `json:"hook_epoch"`
	HookRole           hook.Role     `json:"hook_role"`
	EventID            string        `json:"event_id"`
	Challenge          *bus.Message  `json:"challenge,omitempty"`
	Messages           int           `json:"messages"`
	Senders            []string      `json:"senders,omitempty"`
	RecoverySince      string        `json:"recovery_since,omitempty"`
	RecoveryCoverage   string        `json:"recovery_coverage,omitempty"`
	Recovery           []bus.Message `json:"recovery_messages,omitempty"`
	PossibleDuplicates bool          `json:"possible_duplicates,omitempty"`
}

func (c *Core) save(ctx context.Context, n Notice, replace bool) error {
	at, err := c.Store.Now(ctx)
	if err != nil {
		return fmt.Errorf("read the store clock: %w", err)
	}
	e := Envelope{SavedAt: at, HookEpoch: c.Epoch, HookRole: c.Role, EventID: n.ID, Challenge: n.Challenge, Messages: n.Count, Senders: n.Senders}
	e.LocalReceiver.Identity = c.As
	e.LocalReceiver.Redis = c.Redis
	e.LocalReceiver.Proof = "For the challenge in this file only, run nova-hook prove --as <identity> --redis <redis> --nonce <nonce>. Only the model may prove. This file is rewritten in place while it is pending: it always holds the newest current challenge."
	e.LocalReceiver.Acknowledge = "messages counts the ordinary bus messages since the last notice: read them with nova-bus recv, and answer each with nova-bus send --as <identity> --redis <redis> --to <sender> --re <message ID>. Read recovery_messages in order; they may duplicate earlier delivery. If recovery_coverage is nonempty, notify the coordinator once with its oldest retained entry ID."
	if n.Recovery != nil {
		e.Recovery, e.RecoverySince, e.RecoveryCoverage, e.PossibleDuplicates = n.Recovery.Messages, n.Recovery.Since, n.Recovery.Coverage, true
	}
	data, err := json.Marshal(e)
	if err == nil && len(data) > MaxSaved {
		err = fmt.Errorf("saved notice wants at most %d bytes", MaxSaved)
	}
	if err != nil {
		return err
	}
	return c.Files.Save(n.Path, data, replace)
}
