// Package friend: communications and capacity management for friends.
// Issue #4356 Item F:
// 1. friend tell <f> "<text>": sends message over bus (ev:friend / friend:outbox) in one call.
// 2. friend ask <f> --card <id>: deals card <id> to friend and posts brief in one atomic call.
// 3. friend tiers <f> <tiers>: sets advertised model tiers in Redis.
// 4. friend slots <f> <n>: sets capacity slots in Redis.
// 5. friend pause <f> / friend resume <f>: sets/clears paused flag on friend's record in Redis.
package friend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/brief"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/redis/go-redis/v9"
)

// StreamFriendEvents is the Redis stream where friend bus messages are posted.
const StreamFriendEvents = "ev:friend"

// CapLogStream is the capacity audit log stream.
const CapLogStream = "cap:log"

// Valid model tiers as checked by the capacity and worker layers.
var validTiers = map[string]bool{
	"frontier": true,
	"pro":      true,
	"flash":    true,
}

// TellRequest holds arguments for friend tell.
type TellRequest struct {
	Friend string
	Text   string
	Actor  string
	BusDir string
	Now    time.Time
}

// TellResult is the outcome of a successful friend tell.
type TellResult struct {
	Friend  string
	Actor   string
	EventID string
	Text    string
	At      time.Time
}

// Line formats the standard CLI output line for friend tell.
func (r *TellResult) Line() string {
	return fmt.Sprintf("TELL friend=%s from=%s id=%s text=%s", r.Friend, r.Actor, r.EventID, strconv.Quote(r.Text))
}

// Tell sends a message to friend f over the message bus (ev:friend and friend:outbox) in one call.
func Tell(ctx context.Context, c redis.Cmdable, req TellRequest) (*TellResult, error) {
	if c == nil {
		return nil, errors.New("friend tell: nil redis client")
	}
	if strings.TrimSpace(req.Friend) == "" {
		return nil, errors.New("friend tell: friend name is required")
	}
	if strings.TrimSpace(req.Text) == "" {
		return nil, errors.New("friend tell: message text is required")
	}
	actor := req.Actor
	if actor == "" {
		actor = os.Getenv("NOVA_FRIEND")
	}
	if actor == "" {
		actor = "nova-sprint"
	}
	now := req.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	nowMs := strconv.FormatInt(now.UnixMilli(), 10)

	// Append to ev:friend stream
	pipe := c.Pipeline()
	evCmd := pipe.XAdd(ctx, &redis.XAddArgs{
		Stream: StreamFriendEvents,
		MaxLen: 100000,
		Approx: true,
		Values: []any{
			"to", req.Friend,
			"from", actor,
			"kind", "tell",
			"text", req.Text,
			"at", nowMs,
		},
	})
	// Append to friend:outbox stream for bus relay
	pipe.XAdd(ctx, &redis.XAddArgs{
		Stream: OutboxKey,
		MaxLen: 100000,
		Approx: true,
		Values: []any{
			"friend", req.Friend,
			"kind", "tell",
			"detail", req.Text,
			"channel", "bus:To:" + req.Friend,
			"actor", actor,
			"at", nowMs,
		},
	})

	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("friend tell %s: %w", req.Friend, err)
	}

	eventID, err := evCmd.Result()
	if err != nil {
		return nil, fmt.Errorf("friend tell %s: %w", req.Friend, err)
	}

	return &TellResult{
		Friend:  req.Friend,
		Actor:   actor,
		EventID: eventID,
		Text:    req.Text,
		At:      now,
	}, nil
}

// AskRequest holds arguments for friend ask.
type AskRequest struct {
	Friend string
	CardID string
	Actor  string
	BusDir string
	Now    time.Time
}

// AskResult is the outcome of a successful friend ask.
type AskResult struct {
	Friend  string
	CardID  string
	CopyID  string
	Actor   string
	EventID string
	Brief   string
	At      time.Time
}

// Line formats the standard CLI output line for friend ask.
func (r *AskResult) Line() string {
	copyInfo := r.CopyID
	if copyInfo == "" {
		copyInfo = "-"
	}
	return fmt.Sprintf("ASK friend=%s card=%s copy=%s from=%s id=%s", r.Friend, r.CardID, copyInfo, r.Actor, r.EventID)
}

// Ask deals card id to friend f and posts the bus message with the card's brief in one call.
func Ask(ctx context.Context, c redis.Cmdable, req AskRequest) (*AskResult, error) {
	if c == nil {
		return nil, errors.New("friend ask: nil redis client")
	}
	if strings.TrimSpace(req.Friend) == "" {
		return nil, errors.New("friend ask: friend name is required")
	}
	if strings.TrimSpace(req.CardID) == "" {
		return nil, errors.New("friend ask: card id is required")
	}
	actor := req.Actor
	if actor == "" {
		actor = os.Getenv("NOVA_FRIEND")
	}
	if actor == "" {
		actor = "nova-sprint"
	}
	now := req.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	nowMs := strconv.FormatInt(now.UnixMilli(), 10)

	// Step 1: Deal the card to friend
	dealReq := taskcard.DealRequest{
		To:  taskcard.Consumer{Kind: "friend", Name: req.Friend},
		IDs: []string{req.CardID},
		By:  actor,
	}
	dealt, err := taskcard.Deal(ctx, c, dealReq)
	if err != nil {
		return nil, fmt.Errorf("friend ask: deal card %s to %s: %w", req.CardID, req.Friend, err)
	}
	if len(dealt) == 0 {
		return nil, fmt.Errorf("friend ask: card %s was not dealt to %s", req.CardID, req.Friend)
	}

	copyID := dealt[0].Copy
	if copyID == "" {
		copyID = dealt[0].Primary
	}

	// Step 2: Read or render the card's brief
	cardKey := "task:" + req.CardID
	rec, err := c.HGetAll(ctx, cardKey).Result()
	if err != nil || len(rec) == 0 {
		// Fallback to card:<id>
		rec, _ = c.HGetAll(ctx, "card:"+req.CardID).Result()
	}

	briefText := rec["brief"]
	if briefText == "" {
		if b, rerr := brief.RenderCard(req.CardID, rec, ""); rerr == nil {
			briefText = string(b)
		} else if title := rec["title"]; title != "" {
			briefText = fmt.Sprintf("CARD %s: %s", req.CardID, title)
		} else {
			briefText = fmt.Sprintf("CARD %s", req.CardID)
		}
	}

	// Step 3: Post the bus message with the card's brief
	pipe := c.Pipeline()
	evCmd := pipe.XAdd(ctx, &redis.XAddArgs{
		Stream: StreamFriendEvents,
		MaxLen: 100000,
		Approx: true,
		Values: []any{
			"to", req.Friend,
			"from", actor,
			"kind", "ask",
			"card", req.CardID,
			"copy", copyID,
			"brief", briefText,
			"text", briefText,
			"at", nowMs,
		},
	})
	pipe.XAdd(ctx, &redis.XAddArgs{
		Stream: OutboxKey,
		MaxLen: 100000,
		Approx: true,
		Values: []any{
			"friend", req.Friend,
			"kind", "ask",
			"card", req.CardID,
			"copy", copyID,
			"detail", briefText,
			"channel", "bus:To:" + req.Friend,
			"actor", actor,
			"at", nowMs,
		},
	})

	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("friend ask %s: %w", req.Friend, err)
	}

	eventID, err := evCmd.Result()
	if err != nil {
		return nil, fmt.Errorf("friend ask %s: %w", req.Friend, err)
	}

	return &AskResult{
		Friend:  req.Friend,
		CardID:  req.CardID,
		CopyID:  copyID,
		Actor:   actor,
		EventID: eventID,
		Brief:   briefText,
		At:      now,
	}, nil
}

// TiersRequest holds arguments for friend tiers.
type TiersRequest struct {
	Friend string
	Tiers  string
	Actor  string
	Now    time.Time
}

// TiersResult is the outcome of setting friend tiers.
type TiersResult struct {
	Friend string
	Tiers  string
}

// Line formats the standard CLI output line for friend tiers.
func (r *TiersResult) Line() string {
	t := r.Tiers
	if t == "" {
		t = "-"
	}
	return fmt.Sprintf("FRIEND TIERS friend=%s tiers=%s", r.Friend, t)
}

// NormalizeTiers validates and formats model tiers.
func NormalizeTiers(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return "", nil
	}
	parts := strings.Split(s, ",")
	seen := make(map[string]bool, len(parts))
	var cleaned []string
	for _, p := range parts {
		t := strings.ToLower(strings.TrimSpace(p))
		if t == "" {
			continue
		}
		if !validTiers[t] {
			return "", fmt.Errorf("invalid tier %q; want comma-separated list of frontier, pro, flash", t)
		}
		if !seen[t] {
			seen[t] = true
			cleaned = append(cleaned, t)
		}
	}
	if len(cleaned) == 0 {
		return "", nil
	}
	return strings.Join(cleaned, ","), nil
}

// SetTiers sets the advertised model tiers of friend f in Redis.
func SetTiers(ctx context.Context, c redis.Cmdable, req TiersRequest) (*TiersResult, error) {
	if c == nil {
		return nil, errors.New("friend tiers: nil redis client")
	}
	if strings.TrimSpace(req.Friend) == "" {
		return nil, errors.New("friend tiers: friend name is required")
	}
	normTiers, err := NormalizeTiers(req.Tiers)
	if err != nil {
		return nil, err
	}
	actor := req.Actor
	if actor == "" {
		actor = os.Getenv("NOVA_FRIEND")
	}
	if actor == "" {
		actor = "nova-sprint"
	}
	now := req.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	nowMs := strconv.FormatInt(now.UnixMilli(), 10)

	desiredKey := "friend:" + req.Friend + ":desired"
	pipe := c.Pipeline()
	if normTiers == "" {
		pipe.HDel(ctx, desiredKey, "tiers")
	} else {
		pipe.HSet(ctx, desiredKey, "tiers", normTiers)
	}
	pipe.HSet(ctx, desiredKey, "at", nowMs)
	pipe.SAdd(ctx, "friends", req.Friend)
	pipe.XAdd(ctx, &redis.XAddArgs{
		Stream: CapLogStream,
		MaxLen: 100000,
		Approx: true,
		Values: []any{
			"kind", "friend:tiers",
			"target", "friend:" + req.Friend,
			"tiers", normTiers,
			"actor", actor,
			"at", nowMs,
		},
	})

	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("friend tiers %s: %w", req.Friend, err)
	}

	return &TiersResult{
		Friend: req.Friend,
		Tiers:  normTiers,
	}, nil
}

// SlotsRequest holds arguments for friend slots.
type SlotsRequest struct {
	Friend string
	Slots  int
	Actor  string
	Now    time.Time
}

// SlotsResult is the outcome of setting friend slots.
type SlotsResult struct {
	Friend string
	Slots  int
}

// Line formats the standard CLI output line for friend slots.
func (r *SlotsResult) Line() string {
	return fmt.Sprintf("FRIEND SLOTS friend=%s slots=%d", r.Friend, r.Slots)
}

// SetSlots sets friend f's capacity slots in Redis.
func SetSlots(ctx context.Context, c redis.Cmdable, req SlotsRequest) (*SlotsResult, error) {
	if c == nil {
		return nil, errors.New("friend slots: nil redis client")
	}
	if strings.TrimSpace(req.Friend) == "" {
		return nil, errors.New("friend slots: friend name is required")
	}
	if req.Slots < 0 {
		return nil, fmt.Errorf("friend slots %s: slots must be non-negative, got %d", req.Friend, req.Slots)
	}
	actor := req.Actor
	if actor == "" {
		actor = os.Getenv("NOVA_FRIEND")
	}
	if actor == "" {
		actor = "nova-sprint"
	}
	now := req.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	nowMs := strconv.FormatInt(now.UnixMilli(), 10)

	desiredKey := "friend:" + req.Friend + ":desired"
	pipe := c.Pipeline()
	pipe.HSet(ctx, desiredKey, "slots", strconv.Itoa(req.Slots), "at", nowMs)
	pipe.SAdd(ctx, "friends", req.Friend)
	pipe.XAdd(ctx, &redis.XAddArgs{
		Stream: CapLogStream,
		MaxLen: 100000,
		Approx: true,
		Values: []any{
			"kind", "capacity friend",
			"target", "friend:" + req.Friend,
			"slots", strconv.Itoa(req.Slots),
			"actor", actor,
			"at", nowMs,
		},
	})

	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("friend slots %s: %w", req.Friend, err)
	}

	return &SlotsResult{
		Friend: req.Friend,
		Slots:  req.Slots,
	}, nil
}

// PauseResult is the outcome of pausing or resuming a friend.
type PauseResult struct {
	Friend  string
	Paused  bool
	Changed bool
}

// Line formats the standard CLI output line for friend pause/resume.
func (r *PauseResult) Line() string {
	if r.Paused {
		return fmt.Sprintf("PAUSED friend:%s", r.Friend)
	}
	return fmt.Sprintf("RESUMED friend:%s", r.Friend)
}

// setPaused sets or clears the paused flag on friend f.
func setPaused(ctx context.Context, c redis.Cmdable, friend string, paused bool, actor string) (*PauseResult, error) {
	if c == nil {
		return nil, errors.New("friend pause/resume: nil redis client")
	}
	if strings.TrimSpace(friend) == "" {
		return nil, errors.New("friend pause/resume: friend name is required")
	}
	if actor == "" {
		actor = os.Getenv("NOVA_FRIEND")
	}
	if actor == "" {
		actor = "nova-sprint"
	}
	flag := "0"
	if paused {
		flag = "1"
	}

	// Try Lua Function ns_worker_pause if available
	res, err := c.FCall(ctx, "ns_worker_pause", nil, "friend", friend, flag, actor, "").Result()
	if err == nil {
		if vals, ok := res.([]any); ok && len(vals) >= 2 {
			word := fmt.Sprint(vals[0])
			if word == "UNKNOWN" {
				return nil, fmt.Errorf("unknown friend %q", friend)
			}
			changed := len(vals) > 2 && fmt.Sprint(vals[2]) == "1"
			return &PauseResult{
				Friend:  friend,
				Paused:  paused,
				Changed: changed,
			}, nil
		}
	}

	// Direct Redis fallback
	desiredKey := "friend:" + friend + ":desired"
	isMember, _ := c.SIsMember(ctx, "friends", friend).Result()
	exists, _ := c.Exists(ctx, desiredKey).Result()
	if !isMember && exists == 0 {
		return nil, fmt.Errorf("unknown friend %q", friend)
	}

	curPaused, _ := c.HGet(ctx, desiredKey, "paused").Result()
	if curPaused == "" {
		curPaused = "0"
	}
	changed := (curPaused != flag)
	now := time.Now().UTC()
	nowMs := strconv.FormatInt(now.UnixMilli(), 10)

	pipe := c.Pipeline()
	pipe.HSet(ctx, desiredKey, "paused", flag, "at", nowMs)
	action := "resume"
	if paused {
		action = "pause"
	}
	pipe.XAdd(ctx, &redis.XAddArgs{
		Stream: CapLogStream,
		MaxLen: 100000,
		Approx: true,
		Values: []any{
			"kind", "worker " + action,
			"target", "friend:" + friend,
			"actor", actor,
			"at", nowMs,
		},
	})
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("friend pause/resume %s: %w", friend, err)
	}

	return &PauseResult{
		Friend:  friend,
		Paused:  paused,
		Changed: changed,
	}, nil
}

// Pause pauses friend f in Redis.
func Pause(ctx context.Context, c redis.Cmdable, friend, actor string) (*PauseResult, error) {
	return setPaused(ctx, c, friend, true, actor)
}

// Resume resumes friend f in Redis.
func Resume(ctx context.Context, c redis.Cmdable, friend, actor string) (*PauseResult, error) {
	return setPaused(ctx, c, friend, false, actor)
}
