package friend

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// Notifications use the one receiver and the bus's pending entries, never a second
// consumer or a card scheduler (docs/SPEC-FRIEND.md, notifications; tla/FriendNotifications.tla).
const (
	NotificationStateFile   = "notifications.json"
	NotificationReadyText   = "NOVA-FRIEND READY QUEUE\nSprint deliveries are available. Read the canonical ready queue and continue the coordinator's existing dispatcher; this notification claims or executes no card.\n"
	NotificationBatchPrefix = "NOVA-FRIEND NOTIFICATION "
	NotificationWindow      = 30 * time.Second
	NotificationRetryMax    = time.Minute
)

// NotificationPolicy selects model notifications. Requests and blockers always keep
// their payloads; report is on by default; transport ack and routine status are audited.
type NotificationPolicy struct {
	Kinds  []string
	Window time.Duration
}

func (p NotificationPolicy) selected(m bus.Message) bool {
	kind := m.KindName()
	if kind == bus.KindRequest || kind == bus.KindBlocker || strings.HasPrefix(m.Body, SessionCheckPrefix) || strings.HasPrefix(m.Subject, "stall wake: friend ") {
		return true
	}
	kinds := p.Kinds
	if len(kinds) == 0 {
		kinds = []string{bus.KindRequest, bus.KindBlocker, bus.KindReport}
	}
	return slices.Contains(kinds, kind)
}

func (p NotificationPolicy) window() time.Duration {
	if p.Window > 0 {
		return p.Window
	}
	return NotificationWindow
}

// cardNotification is the server's delivery courtesy, not a word in someone's prose
// (cmd/nova-sprint wakeFriend and wakeFriendPass; SPEC-FRIEND.md, notifications). Both
// the per-card subject and the batch subject are deal notices. All cards share one wake.
func cardNotification(m bus.Message) bool {
	if m.KindName() != bus.KindStatus {
		return false
	}
	if strings.HasPrefix(m.Subject, "cards dealt: ") {
		return true
	}
	if !strings.HasPrefix(m.Subject, "card ") {
		return false
	}
	return strings.Contains(m.Subject, " dealt: FRIEND-CARD DELIVERED ") || strings.Contains(m.Subject, " dealt: FRIEND-READ DELIVERED ")
}

// dealFriend is the friend a deal notice is keyed by: the friend= field of its subject,
// else the message's first recipient, else fallback (SPEC-FRIEND.md, notifications).
func dealFriend(m bus.Message, fallback string) string {
	if idx := strings.Index(m.Subject, "friend="); idx != -1 {
		if f := strings.Fields(m.Subject[idx+len("friend="):]); len(f) > 0 {
			return f[0]
		}
	}
	if len(m.To) > 0 && m.To[0] != "" {
		return m.To[0]
	}
	return fallback
}

// dealCards are the cards a deal notice names: the ids of a batch subject's parenthesized
// list, or the one card of a per-card subject (SPEC-FRIEND.md, notifications). The batch
// list is cut at ten with a trailing "and N more", which names no card: truncated then
// reports that the list is only a subset of the deal, never the whole of it.
func dealCards(m bus.Message) (cards []string, truncated bool) {
	if strings.HasPrefix(m.Subject, "cards dealt: ") {
		start, end := strings.Index(m.Subject, "("), strings.LastIndex(m.Subject, ")")
		if start != -1 && end > start {
			for _, p := range strings.Split(m.Subject[start+1:end], ",") {
				p = strings.TrimSpace(p)
				if p == "" {
					continue
				}
				if strings.HasPrefix(p, "and ") && strings.HasSuffix(p, " more") {
					truncated = true
					continue
				}
				cards = append(cards, p)
			}
			return cards, truncated
		}
	}
	if strings.HasPrefix(m.Subject, "card ") {
		if cardID, _, ok := strings.Cut(strings.TrimPrefix(m.Subject, "card "), " dealt:"); ok {
			if cardID = strings.TrimSpace(cardID); cardID != "" {
				return []string{cardID}, false
			}
		}
	}
	if idx := strings.Index(m.Subject, "card="); idx != -1 {
		if f := strings.Fields(m.Subject[idx+len("card="):]); len(f) > 0 {
			return []string{f[0]}, false
		}
	}
	return nil, false
}

// NotificationBatch is the immutable input kept before enqueue. Accepted means the
// queue took it, not that the model processed it; recovery then retries only receipts.
type NotificationBatch struct {
	Text     string        `json:"text"`
	Entries  []string      `json:"entries,omitempty"`
	Messages []bus.Message `json:"messages,omitempty"`
	Ready    bool          `json:"ready,omitempty"`
	Accepted bool          `json:"accepted,omitempty"`
}

// NotificationState is bounded to one active batch, one deferred nonurgent batch and a ready bit. The
// bus remains the source of every message; filtered entries retain their audit there.
type NotificationState struct {
	Pending *NotificationBatch `json:"pending,omitempty"`
	// Report retains the legacy JSON key for the one deferred report or notice batch.
	Report        *NotificationBatch     `json:"report,omitempty"`
	ReportRetryAt time.Time              `json:"report_retry_at,omitzero"`
	Deals         map[string]*DealNotice `json:"deals,omitempty"`
	Ready         bool                   `json:"ready,omitempty"`
	ReadyDue      time.Time              `json:"ready_due,omitzero"`
	NextReady     time.Time              `json:"next_ready,omitzero"`
	RetryAt       time.Time              `json:"retry_at,omitzero"`
	Failures      int                    `json:"failures,omitempty"`
	Audited       int                    `json:"audited"`
	LastID        string                 `json:"last_id,omitempty"`
}

// DealNotice is the newest deal notice owed a friend: the friend it is keyed by, the cards
// it names, whether that list is a truncated subset, and the bus entry and message it stands
// for (SPEC-FRIEND.md, notifications). A newer notice replaces it; it is withdrawn when all
// its cards are taken, and a truncated list is never withdrawn because a card it omits may
// still be held.
type DealNotice struct {
	Friend    string      `json:"friend"`
	Cards     []string    `json:"cards,omitempty"`
	Truncated bool        `json:"truncated,omitempty"`
	Entry     string      `json:"entry,omitempty"`
	Message   bus.Message `json:"message,omitempty"`
}

// ReadNotificationState and WriteNotificationState use the existing fsynced atomic
// state writer (SPEC-FRIEND.md, notifications), under the notification-only directory.
func ReadNotificationState(dir string) (s NotificationState, err error) {
	_, err = read(filepath.Join(dir, NotificationStateFile), &s)
	if err == nil && s.Pending != nil && (len(s.Pending.Messages) > MaxBatch || len(s.Pending.Entries) != len(s.Pending.Messages)) {
		return NotificationState{}, fmt.Errorf("notification journal holds no bounded batch; preserve it and repair %s", filepath.Join(dir, NotificationStateFile))
	}
	if err == nil && s.Report != nil && (len(s.Report.Messages) > MaxBatch || len(s.Report.Entries) != len(s.Report.Messages)) {
		return NotificationState{}, fmt.Errorf("notification report journal holds no bounded batch")
	}
	return s, err
}

func WriteNotificationState(dir string, s NotificationState) error {
	return write(filepath.Join(dir, NotificationStateFile), s)
}

// notificationReceiver has no presence, inbox, lane, staging, pruning or finish hooks.
// One failed enqueue per due pass backs off, capped at a minute; there is no tight retry
// loop and no give-up ack. Its owed input stays durable across a crash or a long idle.
type notificationReceiver struct {
	d      *Daemon
	b      *bus.Bus
	policy NotificationPolicy
	state  NotificationState
	dir    string
}

func (d *Daemon) runNotifications(ctx context.Context) error {
	dir := d.NotificationStateDir
	if dir == "" {
		dir = filepath.Join(d.Dir, ".nova-friend", "notifications")
	}
	state, err := ReadNotificationState(dir)
	if err != nil {
		return fmt.Errorf("notification recovery: %w", err)
	}
	policy := NotificationPolicy{}
	if d.Notifications != nil {
		policy = *d.Notifications
	}
	if why := bus.CheckKinds(policy.Kinds...); why != "" {
		return fmt.Errorf("notification policy: %s", why)
	}
	n := &notificationReceiver{d: d, b: &bus.Bus{Store: d.Store}, policy: policy, state: state, dir: dir}
	for ctx.Err() == nil {
		now := d.Now()
		if ctx.Err() != nil {
			break
		}
		if err := n.step(ctx, now); err != nil {
			return err
		}
		d.Pause(ctx, BeatEvery)
	}
	return nil
}

func (n *notificationReceiver) save() error {
	return WriteNotificationState(n.dir, n.state)
}

// cardsKnown is whether the daemon has any card state to answer with: its row (Held), its
// card list (Cards), or the queue file. Without it a notice is never withdrawn, so a state
// that cannot be read never drops a notice that is still owed (SPEC-FRIEND.md, notifications).
func (d *Daemon) cardsKnown() bool {
	if d == nil {
		return false
	}
	if _, ok := d.heldFrom(); ok {
		return true
	}
	if d.Cards != nil {
		return true
	}
	path := filepath.Join(d.Dir, filepath.FromSlash(QueueFile))
	var q Queue
	if found, _ := read(path, &q); found {
		return true
	}
	if found, _ := read(path, &q.Tasks); found {
		return true
	}
	return false
}

// cardsAllTaken is whether every card a notice names has left the friend's row (taken back,
// finished or returned): the daemon's card state d.held() answers it (SPEC-FRIEND.md,
// notifications). A notice whose batch list was truncated at ten names only a subset, so it
// is never withdrawn on that list alone: an omitted card may still be held.
func (n *notificationReceiver) cardsAllTaken(deal *DealNotice) bool {
	if deal == nil || deal.Truncated || !n.d.cardsKnown() || len(deal.Cards) == 0 {
		return false
	}
	held := n.d.held()
	for _, c := range deal.Cards {
		if slices.Contains(held, c) {
			return false
		}
	}
	return true
}

// dealForFriend is the deal notice owed this friend: its keyed entry, or the only deal the
// state holds (SPEC-FRIEND.md, notifications).
func (n *notificationReceiver) dealForFriend() *DealNotice {
	if len(n.state.Deals) == 0 {
		return nil
	}
	if d, ok := n.state.Deals[n.d.Friend]; ok && d != nil {
		return d
	}
	if len(n.state.Deals) == 1 {
		for _, d := range n.state.Deals {
			return d
		}
	}
	return nil
}

// removeDeal drops a deal notice from the state, by identity or by its bus entry.
func (n *notificationReceiver) removeDeal(deal *DealNotice) {
	for k, d := range n.state.Deals {
		if d == deal || (deal != nil && d.Entry == deal.Entry) {
			delete(n.state.Deals, k)
		}
	}
}

// withdrawDeal acknowledges a deal notice the daemon withdrew because every card it named
// is already taken, and clears the ready bit when no deal stands (SPEC-FRIEND.md, notifications).
func (n *notificationReceiver) withdrawDeal(ctx context.Context, deal *DealNotice, entries []string, messages []bus.Message) error {
	if err := n.ack(ctx, entries, messages, "deal notice withdrawn: every card is taken"); err != nil {
		return err
	}
	n.removeDeal(deal)
	if len(n.state.Deals) == 0 {
		n.state.Ready = false
		n.state.ReadyDue = time.Time{}
	}
	return nil
}

func (n *notificationReceiver) step(ctx context.Context, now time.Time) error {
	// A failed urgent batch stays pending; a failed nonurgent batch has its own slot. A ready wake is only a bit until due, so
	// requests and blockers never wait behind its coalescing or retry window.
	if n.state.Pending == nil {
		if err := n.receive(ctx, now); err != nil {
			return err
		}
	}
	if n.state.Pending == nil && n.state.Ready && !now.Before(n.state.ReadyDue) && !now.Before(n.state.RetryAt) {
		deal := n.dealForFriend()
		if n.cardsAllTaken(deal) {
			if err := n.withdrawDeal(ctx, deal, []string{deal.Entry}, []bus.Message{deal.Message}); err != nil {
				return err
			}
			return n.save()
		}
		text := NotificationReadyText
		var entries []string
		var messages []bus.Message
		if deal != nil {
			text = NotificationReadyText + "\n" + BatchFor(n.d.Coordinator, []bus.Message{deal.Message}, "", "")
			entries = []string{deal.Entry}
			messages = []bus.Message{deal.Message}
		}
		n.state.Pending = &NotificationBatch{Text: text, Entries: entries, Messages: messages, Ready: true}
		if err := n.save(); err != nil {
			return err
		}
	}
	if n.state.Pending == nil && n.state.Report != nil && !now.Before(n.state.ReportRetryAt) {
		n.state.Pending, n.state.Report = n.state.Report, nil
		n.state.RetryAt = time.Time{}
		if err := n.save(); err != nil {
			return err
		}
	}
	p := n.state.Pending
	if p == nil || (!p.Accepted && now.Before(n.state.RetryAt)) {
		return nil
	}
	if !p.Accepted {
		if p.Ready {
			deal := n.dealForFriend()
			if n.cardsAllTaken(deal) {
				if err := n.withdrawDeal(ctx, deal, p.Entries, p.Messages); err != nil {
					return err
				}
				n.state.Pending = nil
				return n.save()
			}
		}
		exit, err := n.d.Deliver.Deliver(ctx, p.Text)
		if ctx.Err() != nil {
			return nil
		}
		if exit != 0 || err != nil {
			n.state.Failures = min(n.state.Failures+1, 6)
			wait := min(RecheckEvery*time.Duration(1<<uint(n.state.Failures-1)), NotificationRetryMax)
			n.state.RetryAt = now.Add(wait)
			// A failed ready wake goes back to its bit so urgent messages can still enter.
			if p.Ready {
				n.state.Pending = nil
			}
			nonurgent := slices.Contains([]string{"report", "notice"}, NotificationCategory(p.Text))
			if nonurgent {
				n.state.Report, n.state.Pending = p, nil
				n.state.ReportRetryAt = n.state.RetryAt
				n.state.RetryAt = time.Time{}
			}
			retryAt := n.state.RetryAt
			if nonurgent {
				retryAt = n.state.ReportRetryAt
			}
			n.d.Record(fmt.Sprintf("%s notification pending: enqueue exit=%d error=%v; next attempt no earlier than %s", now.UTC().Format(time.RFC3339), exit, err, retryAt.UTC().Format(time.RFC3339)))
			return n.save()
		}
		p.Accepted = true
		// Commit queue acceptance before receipts: a crash here never silently loses the
		// input, and a crash after this save cannot enqueue the accepted batch twice.
		if err := n.save(); err != nil {
			return err
		}
	}
	if err := n.ack(ctx, p.Entries, p.Messages, "queue accepted, not processed"); err != nil {
		return err
	}
	if p.Ready {
		n.removeDeal(n.dealForFriend())
		if len(n.state.Deals) == 0 {
			n.state.Ready = false
			n.state.ReadyDue = time.Time{}
		}
		n.state.NextReady = now.Add(n.policy.window())
	}
	n.state.Pending = nil
	n.state.RetryAt = time.Time{}
	n.state.Failures = 0
	return n.save()
}

// receive is one bounded native read pass, including muted traffic: no competing
// filtered RecvKinds consumer can release and re-claim these same entries.
func (n *notificationReceiver) receive(ctx context.Context, now time.Time) error {
	var entries, silent []string
	var messages, audited []bus.Message
	pong := ""
	size := 0
	for i := 0; i < MaxBatch; i++ {
		block := time.Duration(0)
		if i == 0 && !n.state.Ready {
			block = BeatEvery
		}
		e, ok, err := n.b.Recv(ctx, n.d.Friend, block)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if !ok {
			break
		}
		m := e.Message()
		n.state.Audited++
		n.state.LastID = m.ID
		if e.Stage == bus.Acted {
			silent = append(silent, e.Entry)
			audited = append(audited, m)
			continue
		}
		if nonce, _, _, ping := ParsePing(m.Body); ping {
			n.d.daemonPong(ctx, n.b, m, nonce, now)
			if IsWake(m.Body) && n.d.PongCommand != nil {
				entries = append(entries, e.Entry)
				messages = append(messages, m)
				pong = n.d.PongCommand(nonce)
				break
			}
			silent = append(silent, e.Entry)
			audited = append(audited, m)
		} else if cardNotification(m) {
			// A deal notice is state, not an event: one pending notice per friend, the
			// newer replacing the older, which is acknowledged here as coalesced.
			friend := dealFriend(m, n.d.Friend)
			if n.state.Deals == nil {
				n.state.Deals = make(map[string]*DealNotice)
			}
			if old := n.state.Deals[friend]; old != nil {
				silent = append(silent, old.Entry)
				audited = append(audited, old.Message)
			}
			cards, truncated := dealCards(m)
			n.state.Deals[friend] = &DealNotice{Friend: friend, Cards: cards, Truncated: truncated, Entry: e.Entry, Message: m}
			if !n.state.Ready {
				n.state.Ready = true
				n.state.ReadyDue = now.Add(n.policy.window())
				if n.state.NextReady.After(n.state.ReadyDue) {
					n.state.ReadyDue = n.state.NextReady
				}
			}
		} else if n.policy.selected(m) && n.state.Report != nil && m.KindName() != bus.KindRequest && m.KindName() != bus.KindBlocker {
			// Backpressured nonurgent input remains bus-pending, never released or acknowledged;
			// the same bounded receiver can still reach later requests and blockers.
			n.d.Record(fmt.Sprintf("notification capacity pending id=%s kind=%s", m.ID, m.KindName()))
		} else if n.policy.selected(m) {
			entries = append(entries, e.Entry)
			messages = append(messages, m)
			size += len(m.Body)
			if size >= BatchBytes {
				break
			}
		} else {
			silent = append(silent, e.Entry)
			audited = append(audited, m)
		}
	}
	if n.state.Pending == nil && len(messages) > 0 {
		ids := make([]string, len(messages))
		for i, m := range messages {
			ids[i] = m.ID
		}
		hash := sha256.Sum256([]byte(strings.Join(ids, "\n")))
		category := "report"
		for _, m := range messages {
			if m.KindName() == bus.KindRequest || m.KindName() == bus.KindBlocker {
				category = "urgent"
				break
			}
			if m.KindName() != bus.KindReport {
				category = "notice"
			}
		}
		text := fmt.Sprintf("%s%s %x\n%s", NotificationBatchPrefix, category, hash, BatchFor(n.d.Coordinator, messages, "", pong))
		if len(messages) == 1 && pong != "" {
			text = WakeTurnText(pong)
		}
		n.state.Pending = &NotificationBatch{Text: text, Entries: entries, Messages: messages}
		// Genuine input bypasses a failed ready wake's retry, without clearing the owed bit.
		n.state.RetryAt = time.Time{}
	}
	// Ready intent and audit counts are durable BEFORE any muted status is acked.
	if err := n.save(); err != nil {
		return err
	}
	return n.ack(ctx, silent, audited, "notification filtered or globally coalesced")
}

func (n *notificationReceiver) ack(ctx context.Context, entries []string, messages []bus.Message, why string) error {
	if len(entries) == 0 {
		return nil
	}
	ids := make([]string, len(messages))
	for i, m := range messages {
		ids[i] = m.ID
	}
	if _, err := n.b.Stamp(ctx, n.d.Friend, bus.Delivered, ids...); err != nil {
		return err
	}
	if _, err := n.d.Store.Ack(ctx, bus.StreamOf(n.d.Friend), n.d.Friend, entries...); err != nil {
		return err
	}
	for _, m := range messages {
		n.d.Record(fmt.Sprintf("notification audit id=%s kind=%s: %s; acked=true", m.ID, m.KindName(), why))
	}
	return nil
}

// NotificationKey is the durable enqueue family: one global ready wake, or a batch's
// immutable message-id fingerprint. Only exact notifier prefixes participate.
func NotificationKey(text string) string {
	if text == NotificationReadyText || strings.HasPrefix(text, NotificationReadyText) {
		return "ready queue"
	}
	if first, _, ok := strings.Cut(text, "\n"); ok && strings.HasPrefix(first, NotificationBatchPrefix) {
		return first
	}
	return ""
}

// NotificationCategory bounds unread useful input to one batch of each category;
// report backpressure cannot consume the urgent category's capacity.
func NotificationCategory(text string) string {
	if text == NotificationReadyText || strings.HasPrefix(text, NotificationReadyText) {
		return "ready"
	}
	first, _, ok := strings.Cut(text, "\n")
	if !ok || !strings.HasPrefix(first, NotificationBatchPrefix) {
		return ""
	}
	parts := strings.Fields(first)
	if len(parts) == 4 && slices.Contains([]string{"urgent", "report", "notice"}, parts[2]) {
		return parts[2]
	}
	return "urgent" // the earlier immutable marker format remains recognizable on recovery
}
