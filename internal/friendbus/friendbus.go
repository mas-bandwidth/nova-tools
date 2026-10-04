// Package friendbus implements durable Redis-backed note delivery to registered
// native adapters. It does not implement a human inbox or business completion.
package friendbus

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/redis/go-redis/v9"
)

// The publish limits match the bus body ceiling and bound fanout work per script.
const (
	MaxBodyBytes       = 1 << 20
	MaxRecipients      = 256
	MaxClaimCount      = 100
	MaxIdentityBytes   = 128
	defaultConsumerGrp = "harness"
)

var (
	ErrNoSession       = errors.New("friendbus: recipient has no registered adapter session")
	ErrWrongType       = errors.New("friendbus: Redis key has the wrong type")
	ErrConflict        = errors.New("friendbus: message or receipt identity conflicts with stored content")
	ErrNotDurable      = errors.New("friendbus: acceptance must be durable")
	ErrStaleSession    = errors.New("friendbus: acceptance names a stale adapter session")
	ErrDeliveryMissing = errors.New("friendbus: delivery is not present in the recipient stream")
)

// Config selects one Redis namespace. All keys share a hash tag so each Lua
// operation remains in one Redis Cluster slot.
type Config struct {
	Prefix string
	Group  string
	Redis  redis.UniversalClient
}

// Message is an immutable note to publish. CC is retained as message data but
// never receives a delivery stream entry.
type Message struct {
	Actor string   `json:"actor"`
	Op    string   `json:"op"`
	ID    string   `json:"id"`
	From  string   `json:"from"`
	To    []string `json:"to"`
	CC    []string `json:"cc,omitempty"`
	Body  []byte   `json:"body"`
	Date  string   `json:"date,omitempty"`
	Re    string   `json:"re,omitempty"`
}

type PublishResult struct {
	ID     string
	Queued int
}

// Session is a registered, recoverable native adapter route. Revision is an
// opaque adapter-owned route revision, not an ordering counter.
type Session struct {
	ID           string   `json:"id"`
	Adapter      string   `json:"adapter"`
	Revision     string   `json:"revision"`
	Capabilities []string `json:"capabilities"`
}

// Delivery is the complete note supplied to a native adapter. Key remains
// stable across redelivery and process restarts.
type Delivery struct {
	Key       string
	MessageID string
	StreamID  string
	Recipient string
	Consumer  string
	From      string
	To        []string
	CC        []string
	Body      []byte
	Date      string
	Re        string
	AttemptID string
}

type Acceptance struct {
	ReceiptID string `json:"receipt_id"`
	Adapter   string `json:"adapter"`
	Session   string `json:"session_id"`
	Revision  string `json:"session_revision"`
	Durable   bool   `json:"durable"`
}

type Bus struct {
	rdb    redis.UniversalClient
	prefix string
	group  string
}

func New(c Config) (*Bus, error) {
	if c.Redis == nil {
		return nil, errors.New("friendbus: Redis client is required")
	}
	if c.Prefix == "" || strings.ContainsAny(c.Prefix, "{}\r\n") {
		return nil, errors.New("friendbus: prefix must be nonempty and contain no braces or newlines")
	}
	if c.Group == "" {
		c.Group = defaultConsumerGrp
	}
	if !validIdentity(c.Group) {
		return nil, errors.New("friendbus: group must be a nonempty bounded identity")
	}
	return &Bus{rdb: c.Redis, prefix: c.Prefix, group: c.Group}, nil
}

// Publish atomically records one immutable message and creates one queue entry
// for each canonical To recipient. Repeating the same ID and payload is safe.
func (b *Bus) Publish(ctx context.Context, m Message) (PublishResult, error) {
	canonical, payload, fingerprint, err := canonicalMessage(m)
	if err != nil {
		return PublishResult{}, err
	}
	keys := []string{b.messagesKey(), b.sourceKey()}
	for _, recipient := range canonical.To {
		keys = append(keys, b.deliveryStream(recipient))
	}
	args := []any{canonical.ID, fingerprint, string(payload), canonical.From, canonical.Re}
	for _, recipient := range canonical.To {
		args = append(args, recipient, DeliveryKey(canonical.ID, recipient))
	}
	queued, err := publishScript.Run(ctx, b.rdb, keys, args...).Int()
	if err != nil {
		if strings.Contains(err.Error(), "WRONGTYPE") {
			return PublishResult{}, fmt.Errorf("%w: %v", ErrWrongType, err)
		}
		if strings.Contains(err.Error(), "CONFLICT") {
			return PublishResult{}, ErrConflict
		}
		return PublishResult{}, fmt.Errorf("friendbus: publish: %w", err)
	}
	return PublishResult{ID: canonical.ID, Queued: queued}, nil
}

// Register persists the current adapter route. A route is valid only when it
// declares stable delivery-key deduplication and durable receipt replay.
func (b *Bus) Register(ctx context.Context, recipient string, s Session) error {
	if err := validRecipient(recipient); err != nil {
		return err
	}
	if !validIdentity(s.ID) || !validIdentity(s.Adapter) || !validIdentity(s.Revision) {
		return errors.New("friendbus: session needs bounded ID, adapter and revision")
	}
	capabilities := uniqueSorted(s.Capabilities)
	for _, required := range []string{"durable-delivery-id", "durable-receipt"} {
		if !contains(capabilities, required) {
			return fmt.Errorf("friendbus: session lacks required capability %q", required)
		}
	}
	s.Capabilities = capabilities
	encoded, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("friendbus: encode session: %w", err)
	}
	err = registerScript.Run(ctx, b.rdb, []string{b.sessionKey(recipient)}, string(encoded)).Err()
	if err != nil {
		if strings.Contains(err.Error(), "WRONGTYPE") {
			return fmt.Errorf("%w: %v", ErrWrongType, err)
		}
		return fmt.Errorf("friendbus: register: %w", err)
	}
	return nil
}

func (b *Bus) GetSession(ctx context.Context, recipient string) (Session, bool, error) {
	if err := validRecipient(recipient); err != nil {
		return Session{}, false, err
	}
	raw, err := b.rdb.Get(ctx, b.sessionKey(recipient)).Bytes()
	if errors.Is(err, redis.Nil) {
		return Session{}, false, nil
	}
	if err != nil {
		if strings.Contains(err.Error(), "WRONGTYPE") {
			return Session{}, false, fmt.Errorf("%w: %v", ErrWrongType, err)
		}
		return Session{}, false, fmt.Errorf("friendbus: read session: %w", err)
	}
	var s Session
	if err := json.Unmarshal(raw, &s); err != nil {
		return Session{}, false, fmt.Errorf("friendbus: decode session: %w", err)
	}
	return s, true, nil
}

// Claim obtains new recipient deliveries in the adapter consumer group. The
// stream remains pending until Accept records a durable native receipt.
func (b *Bus) Claim(ctx context.Context, recipient, consumer string, count int, block time.Duration) ([]Delivery, error) {
	if err := b.validateReader(recipient, consumer, count); err != nil {
		return nil, err
	}
	if block < 0 {
		return nil, errors.New("friendbus: block duration cannot be negative")
	}
	if _, ok, err := b.GetSession(ctx, recipient); err != nil {
		return nil, err
	} else if !ok {
		return nil, ErrNoSession
	}
	stream := b.deliveryStream(recipient)
	if err := b.ensureGroup(ctx, stream); err != nil {
		return nil, err
	}
	if block == 0 {
		block = -1
	}
	streams, err := b.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{Group: b.group, Consumer: consumer, Streams: []string{stream, ">"}, Count: int64(count), Block: block}).Result()
	if errors.Is(err, redis.Nil) {
		return []Delivery{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("friendbus: claim: %w", err)
	}
	return decodeStreams(streams, recipient, consumer)
}

// ReclaimPage returns idle pending deliveries and the next Redis cursor. A
// caller persists the cursor if a single call does not drain the pending list.
func (b *Bus) ReclaimPage(ctx context.Context, recipient, consumer string, minIdle time.Duration, count int, cursor string) ([]Delivery, string, error) {
	if err := b.validateReader(recipient, consumer, count); err != nil {
		return nil, "", err
	}
	if minIdle < 0 {
		return nil, "", errors.New("friendbus: minimum idle duration cannot be negative")
	}
	if cursor == "" {
		cursor = "0-0"
	}
	if _, ok, err := b.GetSession(ctx, recipient); err != nil {
		return nil, "", err
	} else if !ok {
		return nil, "", ErrNoSession
	}
	stream := b.deliveryStream(recipient)
	if err := b.ensureGroup(ctx, stream); err != nil {
		return nil, "", err
	}
	msgs, next, err := b.rdb.XAutoClaim(ctx, &redis.XAutoClaimArgs{Stream: stream, Group: b.group, Consumer: consumer, MinIdle: minIdle, Start: cursor, Count: int64(count)}).Result()
	if err != nil {
		return nil, "", fmt.Errorf("friendbus: reclaim: %w", err)
	}
	deliveries, err := decodeMessages(msgs, recipient, consumer)
	return deliveries, next, err
}

// Accept stores the native adapter's durable acceptance receipt and XACKs the
// stable delivery in one Redis script. It accepts a late genuine receipt after
// ownership changes; see redis-contract.md, “Accept validates delivery association”.
func (b *Bus) Accept(ctx context.Context, recipient string, d Delivery, a Acceptance) error {
	if err := validRecipient(recipient); err != nil {
		return err
	}
	if !a.Durable {
		return ErrNotDurable
	}
	if d.Recipient != recipient || d.Key != DeliveryKey(d.MessageID, recipient) || d.StreamID == "" {
		return errors.New("friendbus: delivery identity does not match recipient")
	}
	if !validIdentity(a.ReceiptID) || !validIdentity(a.Adapter) || !validIdentity(a.Session) || !validIdentity(a.Revision) {
		return errors.New("friendbus: acceptance needs bounded receipt, adapter, session and revision")
	}
	stream := b.deliveryStream(recipient)
	if err := acceptScript.Run(ctx, b.rdb, []string{stream, b.receiptsKey(), b.sessionKey(recipient), b.failuresKey()}, d.StreamID, d.Key, d.MessageID, recipient, a.ReceiptID, a.Adapter, a.Session, a.Revision, b.group).Err(); err != nil {
		switch {
		case strings.Contains(err.Error(), "CONFLICT"):
			return ErrConflict
		case strings.Contains(err.Error(), "STALE"):
			return ErrStaleSession
		case strings.Contains(err.Error(), "MISSING"):
			return ErrDeliveryMissing
		case strings.Contains(err.Error(), "WRONGTYPE"):
			return fmt.Errorf("%w: %v", ErrWrongType, err)
		default:
			return fmt.Errorf("friendbus: accept: %w", err)
		}
	}
	return nil
}

// RecordFailure records no acceptance and deliberately leaves the PEL entry
// pending so a later retry/reclaim can recover it.
func (b *Bus) RecordFailure(ctx context.Context, recipient string, d Delivery, reason string) error {
	if err := validRecipient(recipient); err != nil {
		return err
	}
	if d.Recipient != recipient || d.Key != DeliveryKey(d.MessageID, recipient) {
		return errors.New("friendbus: delivery identity does not match recipient")
	}
	if reason == "" || len(reason) > 1024 || !utf8.ValidString(reason) {
		return errors.New("friendbus: failure reason must be valid UTF-8 of 1 to 1024 bytes")
	}
	if !validIdentity(d.Consumer) {
		return errors.New("friendbus: delivery consumer is missing")
	}
	if err := failureScript.Run(ctx, b.rdb, []string{b.deliveryStream(recipient), b.failuresKey(), b.receiptsKey()}, d.StreamID, d.Key, d.MessageID, recipient, reason, time.Now().UTC().Format(time.RFC3339Nano), b.group, d.Consumer).Err(); err != nil {
		if strings.Contains(err.Error(), "WRONGTYPE") {
			return fmt.Errorf("%w: %v", ErrWrongType, err)
		}
		if strings.Contains(err.Error(), "MISSING") {
			return ErrDeliveryMissing
		}
		return fmt.Errorf("friendbus: record failure: %w", err)
	}
	return nil
}

func (b *Bus) validateReader(recipient, consumer string, count int) error {
	if err := validRecipient(recipient); err != nil {
		return err
	}
	if !validIdentity(consumer) {
		return errors.New("friendbus: consumer must be a bounded identity")
	}
	if count < 1 || count > MaxClaimCount {
		return fmt.Errorf("friendbus: claim count must be between 1 and %d", MaxClaimCount)
	}
	return nil
}

func (b *Bus) ensureGroup(ctx context.Context, stream string) error {
	err := b.rdb.XGroupCreateMkStream(ctx, stream, b.group, "0").Err()
	if err == nil || strings.Contains(err.Error(), "BUSYGROUP") {
		return nil
	}
	return fmt.Errorf("friendbus: create consumer group: %w", err)
}

func (b *Bus) messagesKey() string        { return b.prefix + ":{friendbus}:messages" }
func (b *Bus) sourceKey() string          { return b.prefix + ":{friendbus}:source" }
func (b *Bus) receiptsKey() string        { return b.prefix + ":{friendbus}:accepted" }
func (b *Bus) failuresKey() string        { return b.prefix + ":{friendbus}:failures" }
func (b *Bus) sessionKey(r string) string { return b.prefix + ":{friendbus}:session:" + encodePart(r) }
func (b *Bus) deliveryStream(r string) string {
	return b.prefix + ":{friendbus}:delivery:" + encodePart(r)
}

func DeliveryKey(id, recipient string) string { return encodePart(id) + "." + encodePart(recipient) }

func encodePart(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

func canonicalMessage(m Message) (Message, []byte, string, error) {
	if !validIdentity(m.Actor) || !validIdentity(m.Op) || !validIdentity(m.From) {
		return Message{}, nil, "", errors.New("friendbus: message needs bounded actor, op and sender")
	}
	derivedID := OperationID(m.Actor, m.Op)
	if m.ID != "" && m.ID != derivedID {
		return Message{}, nil, "", errors.New("friendbus: message ID must be derived from actor and op")
	}
	m.ID = derivedID
	if len(m.Body) > MaxBodyBytes {
		return Message{}, nil, "", fmt.Errorf("friendbus: body exceeds %d bytes", MaxBodyBytes)
	}
	if !utf8.Valid(m.Body) {
		return Message{}, nil, "", errors.New("friendbus: body must be valid UTF-8")
	}
	if (m.Date != "" && !validIdentity(m.Date)) || (m.Re != "" && !validIdentity(m.Re)) {
		return Message{}, nil, "", errors.New("friendbus: Date and Re must be bounded valid text")
	}
	if len(m.To) > MaxRecipients || len(m.CC) > MaxRecipients {
		return Message{}, nil, "", fmt.Errorf("friendbus: To and Cc input lists are each limited to %d entries", MaxRecipients)
	}
	to := uniqueSorted(m.To)
	cc := uniqueSorted(m.CC)
	if len(to) < 1 || len(to) > MaxRecipients || len(cc) > MaxRecipients {
		return Message{}, nil, "", fmt.Errorf("friendbus: To must contain 1 to %d recipients and Cc at most %d", MaxRecipients, MaxRecipients)
	}
	for _, r := range append(append([]string{}, to...), cc...) {
		if err := validRecipient(r); err != nil {
			return Message{}, nil, "", err
		}
	}
	m.To, m.CC = to, cc
	payload, err := json.Marshal(m)
	if err != nil {
		return Message{}, nil, "", fmt.Errorf("friendbus: encode message: %w", err)
	}
	stable := struct {
		Actor string   `json:"actor"`
		Op    string   `json:"op"`
		ID    string   `json:"id"`
		From  string   `json:"from"`
		To    []string `json:"to"`
		CC    []string `json:"cc,omitempty"`
		Body  []byte   `json:"body"`
		Re    string   `json:"re,omitempty"`
	}{m.Actor, m.Op, m.ID, m.From, m.To, m.CC, m.Body, m.Re}
	canonical, err := json.Marshal(stable)
	if err != nil {
		return Message{}, nil, "", fmt.Errorf("friendbus: encode canonical message: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return m, payload, hex.EncodeToString(sum[:]), nil
}

// OperationID binds a retry-safe message ID to the caller's stable actor/op pair.
func OperationID(actor, op string) string {
	sum := sha256.Sum256([]byte(actor + "\x00" + op))
	return "op-" + hex.EncodeToString(sum[:])
}

func validIdentity(s string) bool {
	return s != "" && len(s) <= MaxIdentityBytes && utf8.ValidString(s) && !strings.ContainsAny(s, "\x00\r\n")
}
func validRecipient(s string) error {
	if !validIdentity(s) {
		return errors.New("friendbus: recipient must be a nonempty bounded identity")
	}
	return nil
}
func uniqueSorted(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}
func contains(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}

func decodeStreams(streams []redis.XStream, recipient, consumer string) ([]Delivery, error) {
	out := make([]Delivery, 0)
	for _, stream := range streams {
		decoded, err := decodeMessages(stream.Messages, recipient, consumer)
		if err != nil {
			return nil, err
		}
		out = append(out, decoded...)
	}
	return out, nil
}
func decodeMessages(msgs []redis.XMessage, recipient, consumer string) ([]Delivery, error) {
	out := make([]Delivery, 0, len(msgs))
	for _, msg := range msgs {
		var m Message
		raw, ok := streamString(msg.Values["payload"])
		if !ok {
			return nil, fmt.Errorf("friendbus: stream %s has no string payload", msg.ID)
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("friendbus: stream %s has invalid payload: %w", msg.ID, err)
		}
		key := stringField(msg.Values, "delivery_key")
		if key != DeliveryKey(m.ID, recipient) || stringField(msg.Values, "message_id") != m.ID || stringField(msg.Values, "recipient") != recipient {
			return nil, fmt.Errorf("friendbus: stream %s has inconsistent delivery identity", msg.ID)
		}
		out = append(out, Delivery{Key: key, MessageID: m.ID, StreamID: msg.ID, Recipient: recipient, Consumer: consumer, From: m.From, To: m.To, CC: m.CC, Body: m.Body, Date: m.Date, Re: m.Re, AttemptID: attemptID(consumer, msg.ID)})
	}
	return out, nil
}
func stringField(m map[string]any, k string) string {
	b, ok := streamString(m[k])
	if !ok {
		return ""
	}
	return string(b)
}
func streamString(v any) ([]byte, bool) {
	switch value := v.(type) {
	case string:
		return []byte(value), true
	case []byte:
		return value, true
	default:
		return nil, false
	}
}
func attemptID(consumer, streamID string) string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return consumer + ":" + streamID
	}
	return hex.EncodeToString(buf)
}

var publishScript = redis.NewScript(`
if type(redis.acl_check_cmd) ~= 'function' then return redis.error_reply('ERR friendbus writes require redis.acl_check_cmd permission preflight') end
local function keytype(k)
  local t = redis.call('TYPE', k).ok
  return t
end
if keytype(KEYS[1]) ~= 'none' and keytype(KEYS[1]) ~= 'hash' then return redis.error_reply('WRONGTYPE messages') end
if keytype(KEYS[2]) ~= 'none' and keytype(KEYS[2]) ~= 'stream' then return redis.error_reply('WRONGTYPE source') end
for i=3,#KEYS do if keytype(KEYS[i]) ~= 'none' and keytype(KEYS[i]) ~= 'stream' then return redis.error_reply('WRONGTYPE delivery') end end
local old = redis.call('HGET', KEYS[1], ARGV[1])
if old then if old ~= ARGV[2] then return redis.error_reply('CONFLICT message id has different content') end; return 0 end
if not redis.acl_check_cmd('XADD', KEYS[2], '*', 'id', ARGV[1], 'fingerprint', ARGV[2], 'payload', ARGV[3]) then return redis.error_reply('NOPERM source XADD') end
local argi = 6
for i=3,#KEYS do
  if not redis.acl_check_cmd('XADD', KEYS[i], '*', 'delivery_key', ARGV[argi+1], 'message_id', ARGV[1], 'recipient', ARGV[argi], 'payload', ARGV[3]) then return redis.error_reply('NOPERM delivery XADD') end
  argi = argi + 2
end
if not redis.acl_check_cmd('HSET', KEYS[1], ARGV[1], ARGV[2]) then return redis.error_reply('NOPERM message HSET') end
redis.call('XADD', KEYS[2], '*', 'id', ARGV[1], 'fingerprint', ARGV[2], 'payload', ARGV[3])
argi = 6
for i=3,#KEYS do
  redis.call('XADD', KEYS[i], '*', 'delivery_key', ARGV[argi+1], 'message_id', ARGV[1], 'recipient', ARGV[argi], 'payload', ARGV[3])
  argi = argi + 2
end
redis.call('HSET', KEYS[1], ARGV[1], ARGV[2])
return #KEYS - 2
`)

var registerScript = redis.NewScript(`
if type(redis.acl_check_cmd) ~= 'function' then return redis.error_reply('ERR friendbus writes require redis.acl_check_cmd permission preflight') end
local t = redis.call('TYPE', KEYS[1]).ok
if t ~= 'none' and t ~= 'string' then return redis.error_reply('WRONGTYPE session') end
if not redis.acl_check_cmd('SET', KEYS[1], ARGV[1]) then return redis.error_reply('NOPERM session SET') end
redis.call('SET', KEYS[1], ARGV[1])
return 1
`)

var acceptScript = redis.NewScript(`
if type(redis.acl_check_cmd) ~= 'function' then return redis.error_reply('ERR friendbus writes require redis.acl_check_cmd permission preflight') end
local function keytype(k) return redis.call('TYPE', k).ok end
if keytype(KEYS[1]) ~= 'stream' then return redis.error_reply('MISSING stream') end
if keytype(KEYS[2]) ~= 'none' and keytype(KEYS[2]) ~= 'hash' then return redis.error_reply('WRONGTYPE receipts') end
if keytype(KEYS[4]) ~= 'none' and keytype(KEYS[4]) ~= 'hash' then return redis.error_reply('WRONGTYPE failures') end
if keytype(KEYS[3]) ~= 'string' then return redis.error_reply('STALE session missing') end
local raw = redis.call('GET', KEYS[3])
local ok, session = pcall(cjson.decode, raw)
if not ok or session.id ~= ARGV[7] or session.adapter ~= ARGV[6] or session.revision ~= ARGV[8] then return redis.error_reply('STALE session') end
local rows = redis.call('XRANGE', KEYS[1], ARGV[1], ARGV[1])
if #rows ~= 1 then return redis.error_reply('MISSING delivery') end
local fields = rows[1][2]
local entry = {}
for i=1,#fields,2 do entry[fields[i]] = fields[i+1] end
if entry.delivery_key ~= ARGV[2] or entry.message_id ~= ARGV[3] or entry.recipient ~= ARGV[4] then return redis.error_reply('MISSING delivery identity') end
local old = redis.call('HGET', KEYS[2], ARGV[2])
local encoded = cjson.encode({receipt=ARGV[5],adapter=ARGV[6],session=ARGV[7],revision=ARGV[8]})
if old then
  local oldok, oldreceipt = pcall(cjson.decode, old)
  if not oldok or oldreceipt.receipt ~= ARGV[5] or oldreceipt.adapter ~= ARGV[6] or oldreceipt.session ~= ARGV[7] or oldreceipt.revision ~= ARGV[8] then return redis.error_reply('CONFLICT acceptance') end
else
  if not redis.acl_check_cmd('HSET', KEYS[2], ARGV[2], encoded) then return redis.error_reply('NOPERM acceptance HSET') end
end
if not redis.acl_check_cmd('XACK', KEYS[1], ARGV[9], ARGV[1]) then return redis.error_reply('NOPERM delivery XACK') end
if not redis.acl_check_cmd('HDEL', KEYS[4], ARGV[2]) then return redis.error_reply('NOPERM failure HDEL') end
if not old then redis.call('HSET', KEYS[2], ARGV[2], encoded) end
redis.call('XACK', KEYS[1], ARGV[9], ARGV[1])
redis.call('HDEL', KEYS[4], ARGV[2])
return 1
`)

var failureScript = redis.NewScript(`
if type(redis.acl_check_cmd) ~= 'function' then return redis.error_reply('ERR friendbus writes require redis.acl_check_cmd permission preflight') end
local function keytype(k) return redis.call('TYPE', k).ok end
if keytype(KEYS[1]) ~= 'stream' then return redis.error_reply('MISSING stream') end
if keytype(KEYS[2]) ~= 'none' and keytype(KEYS[2]) ~= 'hash' then return redis.error_reply('WRONGTYPE failures') end
if keytype(KEYS[3]) ~= 'none' and keytype(KEYS[3]) ~= 'hash' then return redis.error_reply('WRONGTYPE receipts') end
local rows = redis.call('XRANGE', KEYS[1], ARGV[1], ARGV[1])
if #rows ~= 1 then return redis.error_reply('MISSING delivery') end
local fields = rows[1][2]
local entry = {}
for i=1,#fields,2 do entry[fields[i]] = fields[i+1] end
if entry.delivery_key ~= ARGV[2] or entry.message_id ~= ARGV[3] or entry.recipient ~= ARGV[4] then return redis.error_reply('MISSING delivery identity') end
if redis.call('HEXISTS', KEYS[3], ARGV[2]) == 1 then return 0 end
local pending = redis.call('XPENDING', KEYS[1], ARGV[7], ARGV[1], ARGV[1], 1)
if #pending ~= 1 or pending[1][2] ~= ARGV[8] then return redis.error_reply('MISSING pending delivery') end
local value = cjson.encode({reason=ARGV[5],at=ARGV[6]})
if not redis.acl_check_cmd('HSET', KEYS[2], ARGV[2], value) then return redis.error_reply('NOPERM failure HSET') end
redis.call('HSET', KEYS[2], ARGV[2], value)
return 1
`)
