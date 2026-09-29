package sprint

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// ============================================================================
// Section 1: Embedded Lua Scripts & Redis Function Library Packaging
// ============================================================================

// CardLibraryName is the official Redis Function library identifier.
const CardLibraryName = "ns_card"

// LibraryName is an alias for CardLibraryName.
const LibraryName = CardLibraryName

// Embedded Lua script prototypes for packaging into the Redis Function library.
//
//go:embed ns_card_push.lua
var pushLuaScript string

//go:embed ns_card_deal_work.lua
var dealWorkLuaScript string

//go:embed ns_card_land.lua
var landLuaScript string

// GetPushLuaScript returns the embedded Lua script for ns_card_push.
func GetPushLuaScript() string { return pushLuaScript }

// GetDealWorkLuaScript returns the embedded Lua script for ns_card_deal_work.
func GetDealWorkLuaScript() string { return dealWorkLuaScript }

// GetLandLuaScript returns the embedded Lua script for ns_card_land.
func GetLandLuaScript() string { return landLuaScript }

// GetLuaScript returns the corresponding embedded Lua script for the given function name.
func GetLuaScript(fn string) string {
	switch fn {
	case FnCardPush:
		return pushLuaScript
	case FnCardDealWork:
		return dealWorkLuaScript
	case FnCardLand:
		return landLuaScript
	default:
		return ""
	}
}

// EmbeddedHelpers contains the shared Lua helper functions for the ns_card library.
// Defined once at file scope so all registered functions share identical implementations
// without duplication.
const EmbeddedHelpers = `-- Embedded helpers for Layer 2 CardMachine Dual-Table Operations
local function cm_now()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

local function cell_key(table_name, epoch, row, col)
  local prefix = 'table:' .. table_name
  if epoch and epoch ~= '' and epoch ~= '0' then
    prefix = prefix .. ':' .. tostring(epoch)
  end
  return prefix .. ':cell:' .. row .. ':' .. col
end

local function refuse(reason, ...)
  local res = { 'REFUSED', reason }
  local extra = { ... }
  for i = 1, #extra do
    res[#res + 1] = tostring(extra[i])
  end
  return res
end`

// extractRegisterFunction locates and extracts the redis.register_function(...) block
// for the given function name from a source Lua script.
func extractRegisterFunction(script string, name string) (string, error) {
	targetSingle := fmt.Sprintf("redis.register_function('%s'", name)
	targetDouble := fmt.Sprintf("redis.register_function(\"%s\"", name)
	idx := strings.Index(script, targetSingle)
	if idx == -1 {
		idx = strings.Index(script, targetDouble)
	}
	if idx == -1 {
		return "", fmt.Errorf("register_function for %q not found in script", name)
	}
	return strings.TrimSpace(script[idx:]), nil
}

// BuildLibraryPayload packages the three Redis Function Lua scripts
// (ns_card_push.lua, ns_card_deal_work.lua, ns_card_land.lua) into a single
// Redis Function library payload string:
//
//	#!lua name=ns_card
//	<embedded helpers>
//	redis.register_function('ns_card_push', ...)
//	redis.register_function('ns_card_deal_work', ...)
//	redis.register_function('ns_card_land', ...)
func BuildLibraryPayload() (string, error) {
	pushBlock, err := extractRegisterFunction(pushLuaScript, FnCardPush)
	if err != nil {
		return "", fmt.Errorf("extract %s: %w", FnCardPush, err)
	}
	dealBlock, err := extractRegisterFunction(dealWorkLuaScript, FnCardDealWork)
	if err != nil {
		return "", fmt.Errorf("extract %s: %w", FnCardDealWork, err)
	}
	landBlock, err := extractRegisterFunction(landLuaScript, FnCardLand)
	if err != nil {
		return "", fmt.Errorf("extract %s: %w", FnCardLand, err)
	}

	var sb strings.Builder
	sb.WriteString("#!lua name=" + CardLibraryName + "\n\n")
	sb.WriteString(EmbeddedHelpers + "\n\n")
	sb.WriteString(pushBlock + "\n\n")
	sb.WriteString(dealBlock + "\n\n")
	sb.WriteString(landBlock + "\n")

	return sb.String(), nil
}

// MustBuildLibraryPayload returns the built library payload or panics if building fails.
func MustBuildLibraryPayload() string {
	payload, err := BuildLibraryPayload()
	if err != nil {
		panic(fmt.Sprintf("sprint: failed to build library payload: %v", err))
	}
	return payload
}

// LibraryPayload is the packaged Redis Function library payload string.
var LibraryPayload = MustBuildLibraryPayload()

// GetLibraryPayload returns the packaged Redis Function library payload string.
func GetLibraryPayload() string {
	return LibraryPayload
}

// ErrFunctionsUnsupported is returned by LoadFunctionsStrict when Redis Functions
// are not supported by the target Redis server.
var ErrFunctionsUnsupported = errors.New("redis functions unsupported by server")

// IsCompatibilityError checks whether an error returned by Redis indicates that
// Redis Functions are unsupported or already compatible (e.g. unknown command on Redis < 7,
// command disabled in managed environments, or library already exists).
func IsCompatibilityError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unknown command") ||
		strings.Contains(msg, "disabled") ||
		strings.Contains(msg, "unknown subcommand") ||
		strings.Contains(msg, "not supported") ||
		strings.Contains(msg, "already exists") ||
		strings.Contains(msg, "noperm")
}

func isFallbackableError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "function not found") ||
		strings.Contains(msg, "unknown command") ||
		strings.Contains(msg, "noscript") ||
		strings.Contains(msg, "err loading") ||
		strings.Contains(msg, "fcall")
}

// LoadFunctions executes "FUNCTION LOAD REPLACE <payload>" to register the bundled
// Redis Function library (ns_card) in Redis.
//
// If the server does not support Redis Functions (e.g. Redis < 7.0 or miniredis),
// it gracefully absorbs the error and returns nil, allowing client operations
// to fall back to EVAL automatically.
func LoadFunctions(ctx context.Context, rdb redis.Cmdable) error {
	return LoadFunctionsWithPayload(ctx, rdb, LibraryPayload)
}

// LoadFunctionsWithPayload executes "FUNCTION LOAD REPLACE <payload>" with a custom
// payload string while handling graceful compatibility.
func LoadFunctionsWithPayload(ctx context.Context, rdb redis.Cmdable, payload string) error {
	if rdb == nil {
		return errors.New("redis client is nil")
	}

	cmd := rdb.FunctionLoadReplace(ctx, payload)
	err := cmd.Err()
	if err == nil {
		return nil
	}
	if IsCompatibilityError(err) {
		return nil
	}
	return fmt.Errorf("load functions (%s): %w", CardLibraryName, err)
}

// LoadFunctionsStrict executes "FUNCTION LOAD REPLACE <payload>" but returns
// ErrFunctionsUnsupported if the Redis server does not support Redis Functions.
func LoadFunctionsStrict(ctx context.Context, rdb redis.Cmdable) error {
	if rdb == nil {
		return errors.New("redis client is nil")
	}

	cmd := rdb.FunctionLoadReplace(ctx, LibraryPayload)
	err := cmd.Err()
	if err == nil {
		return nil
	}
	if IsCompatibilityError(err) {
		return fmt.Errorf("%w: %v", ErrFunctionsUnsupported, err)
	}
	return fmt.Errorf("load functions (%s): %w", CardLibraryName, err)
}

// Sum returns the first 16 hex digits of the SHA-256 digest of the library source.
func Sum(source string) string {
	h := sha256.Sum256([]byte(source))
	return hex.EncodeToString(h[:])[:16]
}

// Loaded returns the code of the ns_card library that the server holds, and false when it holds none.
func Loaded(ctx context.Context, rdb redis.Cmdable) (string, bool, error) {
	if rdb == nil {
		return "", false, errors.New("redis client is nil")
	}
	libs, err := rdb.FunctionList(ctx, redis.FunctionListQuery{LibraryNamePattern: CardLibraryName, WithCode: true}).Result()
	if err != nil {
		return "", false, fmt.Errorf("list %s function library: %w", CardLibraryName, err)
	}
	for _, lib := range libs {
		if lib.Name == CardLibraryName {
			return lib.Code, true, nil
		}
	}
	return "", false, nil
}

// LoadMissing installs the embedded ns_card library only when the server does not already hold it.
func LoadMissing(ctx context.Context, rdb redis.Cmdable) error {
	if rdb == nil {
		return errors.New("redis client is nil")
	}
	libs, err := rdb.FunctionList(ctx, redis.FunctionListQuery{LibraryNamePattern: CardLibraryName}).Result()
	if err != nil {
		if strings.Contains(err.Error(), "NOPERM") || IsCompatibilityError(err) {
			return nil
		}
		return fmt.Errorf("list %s function library: %w", CardLibraryName, err)
	}
	for _, lib := range libs {
		if lib.Name == CardLibraryName {
			return nil
		}
	}
	if err := rdb.FunctionLoad(ctx, LibraryPayload).Err(); err != nil {
		if strings.Contains(err.Error(), "already exists") || IsCompatibilityError(err) {
			return nil
		}
		return fmt.Errorf("load %s function library: %w", CardLibraryName, err)
	}
	return nil
}

// WrapLuaForEval wraps a Redis Function Lua source code so that it can be evaluated
// directly via EVAL with global KEYS and ARGV.
func WrapLuaForEval(rawLua string) string {
	s := rawLua
	const regFn = "redis.register_function("
	idx := strings.Index(s, regFn)
	if idx != -1 {
		searchStart := idx + len(regFn)
		fnIdx := strings.Index(s[searchStart:], "function(")
		if fnIdx != -1 {
			targetFn := searchStart + fnIdx
			s = s[:idx] + "local function __handler(" + s[targetFn+len("function("):]
		}
	}
	lastEndParen := strings.LastIndex(s, "end)")
	if lastEndParen != -1 {
		s = s[:lastEndParen] + "end" + s[lastEndParen+len("end)"):]
	}
	return s + "\nreturn __handler(KEYS, ARGV)\n"
}

// executeScriptOrFCall executes a function via FCALL if possible, falling back to EVAL
// if the command is unsupported or the function is not loaded.
func executeScriptOrFCall(
	ctx context.Context,
	rdb redis.Cmdable,
	fn string,
	rawScript string,
	keys []string,
	ro bool,
	callArgs []any,
	forceEval bool,
) ([]any, error) {
	if !forceEval {
		var cmd *redis.Cmd
		if ro {
			cmd = rdb.FCallRO(ctx, fn, keys, callArgs...)
		} else {
			cmd = rdb.FCall(ctx, fn, keys, callArgs...)
		}
		reply, err := cmd.Slice()
		if err == nil {
			return reply, nil
		}
		if !isFallbackableError(err) || rawScript == "" {
			return nil, err
		}
	}

	// Fallback: evaluate the raw script via EVAL
	wrapped := WrapLuaForEval(rawScript)
	cmd := rdb.Eval(ctx, wrapped, keys, callArgs...)
	reply, err := cmd.Slice()
	if err != nil {
		return nil, fmt.Errorf("eval fallback for %s failed: %w", fn, err)
	}
	return reply, nil
}

// ============================================================================
// Section 2: Monotonic Sequence Ratchet
// ============================================================================

// DefaultSequenceRatchetKey is the default Redis key used for tracking monotonic sequences.
const DefaultSequenceRatchetKey = "table:sprint:seq"

var (
	// ErrMonotonicSequenceViolation is returned when a sequence ratchet check detects
	// a non-monotonic sequence (target sequence <= current sequence).
	ErrMonotonicSequenceViolation = errors.New("monotonic sequence violation: sequence must strictly increase")

	// ErrCRC32Violation is returned when a transaction payload fails IEEE CRC-32 verification.
	ErrCRC32Violation = errors.New("crc32 integrity violation: payload checksum mismatch")

	// ErrStateHashMismatch is returned when post-transaction state hash verification fails.
	ErrStateHashMismatch = errors.New("state hash mismatch: post-mutation state verification failed")

	// ErrTransactionRolledBack is returned when a transaction has been aborted and rolled back.
	ErrTransactionRolledBack = errors.New("transaction rolled back")
)

// SequenceRatchet manages an atomic, strictly monotonic sequence counter in Redis.
type SequenceRatchet struct {
	rdb redis.Cmdable
	key string
}

// NewSequenceRatchet creates a new SequenceRatchet for the given Redis client and key.
func NewSequenceRatchet(rdb redis.Cmdable, key ...string) *SequenceRatchet {
	k := DefaultSequenceRatchetKey
	if len(key) > 0 && key[0] != "" {
		k = key[0]
	}
	return &SequenceRatchet{
		rdb: rdb,
		key: k,
	}
}

// Key returns the underlying Redis key for this sequence ratchet.
func (sr *SequenceRatchet) Key() string {
	return sr.key
}

// Get returns the current sequence from Redis (0 if unset).
func (sr *SequenceRatchet) Get(ctx context.Context) (uint64, error) {
	val, err := sr.rdb.Get(ctx, sr.key).Result()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	seq, err := strconv.ParseUint(val, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse sequence ratchet value %q: %w", val, err)
	}
	return seq, nil
}

// Set unconditionally sets the sequence value (used during initialization or rollback).
func (sr *SequenceRatchet) Set(ctx context.Context, seq uint64) error {
	return sr.rdb.Set(ctx, sr.key, strconv.FormatUint(seq, 10), 0).Err()
}

// Advance atomically increments the sequence ratchet by 1 (INCR) and returns the new sequence.
func (sr *SequenceRatchet) Advance(ctx context.Context) (uint64, error) {
	n, err := sr.rdb.Incr(ctx, sr.key).Result()
	if err != nil {
		return 0, err
	}
	return uint64(n), nil
}

const ratchetScript = `
local key = KEYS[1]
local target = tonumber(ARGV[1])
local current = tonumber(redis.call('GET', key) or '0')
if target <= current then
    return { 'ERR', 'MONOTONIC_VIOLATION', tostring(current), tostring(target) }
end
redis.call('SET', key, target)
return { 'OK', tostring(target) }
`

// Ratchet atomically checks that targetSeq > currentSeq. If valid, it updates
// the sequence to targetSeq. If targetSeq <= currentSeq, it returns ErrMonotonicSequenceViolation.
func (sr *SequenceRatchet) Ratchet(ctx context.Context, targetSeq uint64) (uint64, error) {
	cmd := sr.rdb.Eval(ctx, ratchetScript, []string{sr.key}, targetSeq)
	res, err := cmd.Slice()
	if err != nil {
		return 0, fmt.Errorf("ratchet script failed: %w", err)
	}
	if len(res) == 0 {
		return 0, errors.New("empty response from ratchet script")
	}
	status := fmt.Sprint(res[0])
	if status != "OK" {
		currentVal := ""
		if len(res) > 2 {
			currentVal = fmt.Sprint(res[2])
		}
		return 0, fmt.Errorf("%w: target %d <= current %s", ErrMonotonicSequenceViolation, targetSeq, currentVal)
	}
	return targetSeq, nil
}

// ============================================================================
// Section 3: Transaction Model, Key Snapshots & Rollback
// ============================================================================

// CardTransaction models an atomic transaction dispatched to Redis.
type CardTransaction struct {
	Seq               uint64                `json:"seq"`
	Action            journalAction         `json:"action"`
	Payload           []byte                `json:"payload"`
	PayloadCRC        uint32                `json:"crc32"`
	ExpectedStateHash [32]byte              `json:"expected_state_hash,omitempty"`
	Event             *JournalMutationEvent `json:"event,omitempty"`
}

// NewCardTransaction constructs a CardTransaction from a JournalMutationEvent,
// automatically serializing the event payload and calculating its IEEE CRC-32 checksum.
func NewCardTransaction(seq uint64, event JournalMutationEvent, expectedStateHash [32]byte) (*CardTransaction, error) {
	payload, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("marshal event payload: %w", err)
	}
	checksum := crc32.ChecksumIEEE(payload)
	return &CardTransaction{
		Seq:               seq,
		Action:            event.Action,
		Payload:           payload,
		PayloadCRC:        checksum,
		ExpectedStateHash: expectedStateHash,
		Event:             &event,
	}, nil
}

// TransactionResult summarizes the committed outcome of an executed CardTransaction.
type TransactionResult struct {
	Seq       uint64   `json:"seq"`
	Receipt   *Receipt `json:"receipt,omitempty"`
	StateHash [32]byte `json:"state_hash,omitempty"`
	Frame     *Frame   `json:"frame,omitempty"`
}

type keyBackup struct {
	Exists     bool
	KeyType    string
	HashData   map[string]string
	ZSetData   []redis.Z
	StringData string
}

type keySnapshot struct {
	ratchetSeq uint64
	backups    map[string]keyBackup
}

func captureKeySnapshot(ctx context.Context, rdb redis.Cmdable, sr *SequenceRatchet, keys []string) (*keySnapshot, error) {
	snap := &keySnapshot{
		backups: make(map[string]keyBackup, len(keys)),
	}

	if sr != nil {
		seq, err := sr.Get(ctx)
		if err != nil {
			return nil, fmt.Errorf("snapshot sequence ratchet: %w", err)
		}
		snap.ratchetSeq = seq
	}

	for _, k := range keys {
		if k == "" {
			continue
		}
		t, err := rdb.Type(ctx, k).Result()
		if err != nil {
			return nil, fmt.Errorf("snapshot type for %s: %w", k, err)
		}

		switch t {
		case "none":
			snap.backups[k] = keyBackup{Exists: false, KeyType: "none"}
		case "string":
			val, err := rdb.Get(ctx, k).Result()
			if err != nil && !errors.Is(err, redis.Nil) {
				return nil, fmt.Errorf("snapshot string for %s: %w", k, err)
			}
			snap.backups[k] = keyBackup{Exists: true, KeyType: "string", StringData: val}
		case "hash":
			hdata, err := rdb.HGetAll(ctx, k).Result()
			if err != nil {
				return nil, fmt.Errorf("snapshot hash for %s: %w", k, err)
			}
			snap.backups[k] = keyBackup{Exists: true, KeyType: "hash", HashData: hdata}
		case "zset":
			zdata, err := rdb.ZRangeWithScores(ctx, k, 0, -1).Result()
			if err != nil {
				return nil, fmt.Errorf("snapshot zset for %s: %w", k, err)
			}
			snap.backups[k] = keyBackup{Exists: true, KeyType: "zset", ZSetData: zdata}
		default:
			// If unsupported key type, preserve empty
			snap.backups[k] = keyBackup{Exists: true, KeyType: t}
		}
	}

	return snap, nil
}

func restoreKeySnapshot(ctx context.Context, rdb redis.Cmdable, sr *SequenceRatchet, snap *keySnapshot) error {
	if snap == nil {
		return nil
	}

	// 1. Restore sequence ratchet
	if sr != nil {
		if err := sr.Set(ctx, snap.ratchetSeq); err != nil {
			return fmt.Errorf("restore sequence ratchet to %d: %w", snap.ratchetSeq, err)
		}
	}

	// 2. Restore keys
	for k, b := range snap.backups {
		if !b.Exists {
			_ = rdb.Del(ctx, k).Err()
			continue
		}

		switch b.KeyType {
		case "string":
			_ = rdb.Set(ctx, k, b.StringData, 0).Err()
		case "hash":
			_ = rdb.Del(ctx, k).Err()
			if len(b.HashData) > 0 {
				fields := make([]any, 0, len(b.HashData)*2)
				for field, val := range b.HashData {
					fields = append(fields, field, val)
				}
				_ = rdb.HSet(ctx, k, fields...).Err()
			}
		case "zset":
			_ = rdb.Del(ctx, k).Err()
			if len(b.ZSetData) > 0 {
				_ = rdb.ZAdd(ctx, k, b.ZSetData...).Err()
			}
		}
	}

	return nil
}

// ============================================================================
// Section 4: RedisCardClient & Transaction Execution Engine
// ============================================================================

// RedisCardClient provides atomic card machine operations over Redis with sequence
// ratchets, Lua script packaging, CRC integrity checks, and state hash verification rollback.
type RedisCardClient struct {
	rdb             redis.Cmdable
	client          *Client
	ratchet         *SequenceRatchet
	monotoneRatchet *MonotoneRatchet
	journalWriter   *JournalWriter
	forceEval       bool

	mu       sync.Mutex
	verifier *MemoryCardMachine // optional shadow state machine for state hash calculation & verification
}

// RedisOption configures a RedisCardClient.
type RedisOption func(*RedisCardClient)

// WithForceEval forces the client to use EVAL rather than FCALL.
func WithForceEval(force bool) RedisOption {
	return func(c *RedisCardClient) {
		c.forceEval = force
	}
}

// WithRatchetKey overrides the sequence ratchet key.
func WithRatchetKey(key string) RedisOption {
	return func(c *RedisCardClient) {
		c.ratchet = NewSequenceRatchet(c.rdb, key)
	}
}

// WithShadowVerifier attaches a shadow MemoryCardMachine for post-transaction
// state hash verification.
func WithShadowVerifier(verifier *MemoryCardMachine) RedisOption {
	return func(c *RedisCardClient) {
		c.verifier = verifier
	}
}

// WithMonotoneRatchet attaches an in-memory MonotoneRatchet for strict step sequence
// and state hash chaining.
func WithMonotoneRatchet(r *MonotoneRatchet) RedisOption {
	return func(c *RedisCardClient) {
		c.monotoneRatchet = r
	}
}

// WithJournalWriter attaches a JournalWriter to record and frame all committed
// transactions directly into the journal log.
func WithJournalWriter(w *JournalWriter) RedisOption {
	return func(c *RedisCardClient) {
		c.journalWriter = w
	}
}

// NewRedisCardClient creates a new RedisCardClient.
func NewRedisCardClient(rdb redis.Cmdable, opts ...RedisOption) *RedisCardClient {
	c := &RedisCardClient{
		rdb:     rdb,
		client:  NewClient(rdb),
		ratchet: NewSequenceRatchet(rdb),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// RDB returns the underlying Redis client.
func (c *RedisCardClient) RDB() redis.Cmdable {
	return c.rdb
}

// Ratchet returns the underlying SequenceRatchet.
func (c *RedisCardClient) Ratchet() *SequenceRatchet {
	return c.ratchet
}

// MonotoneRatchet returns the attached MonotoneRatchet, if configured.
func (c *RedisCardClient) MonotoneRatchet() *MonotoneRatchet {
	return c.monotoneRatchet
}

// JournalWriter returns the attached JournalWriter, if configured.
func (c *RedisCardClient) JournalWriter() *JournalWriter {
	return c.journalWriter
}

// ShadowVerifier returns the attached shadow verifier, if configured.
func (c *RedisCardClient) ShadowVerifier() *MemoryCardMachine {
	return c.verifier
}

// Client returns the underlying *Client.
func (c *RedisCardClient) Client() *Client {
	return c.client
}

// LoadFunctions loads the bundled ns_card library into Redis.
func (c *RedisCardClient) LoadFunctions(ctx context.Context) error {
	return LoadFunctions(ctx, c.rdb)
}

// SetConsumerCapacity sets the slot capacity for a consumer in Redis.
func (c *RedisCardClient) SetConsumerCapacity(ctx context.Context, consumer ConsumerID, slots int) error {
	return c.rdb.HSet(ctx, "table:fleet:consumer:"+string(consumer), "slots", strconv.Itoa(slots)).Err()
}

// GetConsumerCapacity retrieves the slot capacity for a consumer.
func (c *RedisCardClient) GetConsumerCapacity(ctx context.Context, consumer ConsumerID) (int, error) {
	val, err := c.rdb.HGet(ctx, "table:fleet:consumer:"+string(consumer), "slots").Result()
	if errors.Is(err, redis.Nil) {
		return 4, nil // default slots
	}
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(val)
}

// Push executes Action 1 (Push) directly over Redis.
func (c *RedisCardClient) Push(ctx context.Context, card CardID, stream string, opts ...WriteOptions) (*Receipt, error) {
	keys := []string{"table:streams", MemberKey(string(card))}
	reply, receipt, err := executeCardFCallWithFallback(ctx, c.rdb, FnCardPush, keys, false, c.forceEval, opts, string(card), stream)
	_ = reply
	return receipt, err
}

// DealWork executes Action 3 (DealWork) directly over Redis.
func (c *RedisCardClient) DealWork(ctx context.Context, params DealWorkParams, opts ...WriteOptions) (*DealWorkResult, error) {
	keys := []string{"table:streams", "table:fleet", MemberKey(string(params.Card))}
	reply, receipt, err := executeCardFCallWithFallback(ctx, c.rdb, FnCardDealWork, keys, false, c.forceEval, opts, string(params.Card), string(params.Consumer), params.Score)
	if err != nil {
		return nil, err
	}
	if len(reply) < 2 {
		return nil, fmt.Errorf("%s: missing copy ID in reply", FnCardDealWork)
	}
	copyID, err := ParseCopyID(fmt.Sprint(reply[1]))
	if err != nil {
		return nil, fmt.Errorf("%s: malformed copy ID %v: %w", FnCardDealWork, reply[1], err)
	}
	return &DealWorkResult{
		Receipt: *receipt,
		Copy:    copyID,
	}, nil
}

// Land executes Action 11 (Land) directly over Redis.
func (c *RedisCardClient) Land(ctx context.Context, card CardID, opts ...WriteOptions) (*Receipt, error) {
	keys := []string{"table:streams", "table:fleet", MemberKey(string(card))}
	reply, receipt, err := executeCardFCallWithFallback(ctx, c.rdb, FnCardLand, keys, false, c.forceEval, opts, string(card))
	_ = reply
	return receipt, err
}

// ExecuteTransaction executes a single CardTransaction with strict sequence ratchet,
// CRC payload verification, Lua action dispatch, and state hash rollback protection.
func (c *RedisCardClient) ExecuteTransaction(ctx context.Context, tx *CardTransaction) (*TransactionResult, error) {
	if tx == nil {
		return nil, errors.New("nil transaction")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// 1. Verify CRC32 Checksum
	actualCRC := crc32.ChecksumIEEE(tx.Payload)
	if actualCRC != tx.PayloadCRC {
		return nil, fmt.Errorf("%w: expected 0x%08x, got 0x%08x", ErrCRC32Violation, tx.PayloadCRC, actualCRC)
	}

	// 2. Verify Monotonic Sequence Ratchet Pre-condition
	currentSeq, err := c.ratchet.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("read sequence ratchet: %w", err)
	}
	if tx.Seq <= currentSeq {
		return nil, fmt.Errorf("%w: tx.Seq=%d <= currentSeq=%d", ErrMonotonicSequenceViolation, tx.Seq, currentSeq)
	}

	// 3. Deserialize Event if not present
	var ev JournalMutationEvent
	if tx.Event != nil {
		ev = *tx.Event
	} else {
		if err := json.Unmarshal(tx.Payload, &ev); err != nil {
			return nil, fmt.Errorf("unmarshal transaction event: %w", err)
		}
	}

	// 4. Capture Pre-Transaction Key Snapshot for Rollback
	candidateKeys := candidateKeysForEvent(ev)
	snapshot, err := captureKeySnapshot(ctx, c.rdb, c.ratchet, candidateKeys)
	if err != nil {
		return nil, fmt.Errorf("capture transaction snapshot: %w", err)
	}

	// Helper to rollback state on any post-check violation
	rollback := func(triggerErr error) error {
		rbErr := restoreKeySnapshot(ctx, c.rdb, c.ratchet, snapshot)
		if rbErr != nil {
			return fmt.Errorf("%w (rollback failed: %v)", triggerErr, rbErr)
		}
		return triggerErr
	}

	// 5. Dispatch Action via Lua Script to Redis
	opts := WriteOptions{Epoch: ev.Epoch, Actor: ev.Actor}
	var receipt *Receipt

	switch ev.Action {
	case actSetCapacity:
		err = c.SetConsumerCapacity(ctx, ev.Consumer, ev.Slots)
		if err != nil {
			return nil, rollback(fmt.Errorf("set capacity failed: %w", err))
		}
	case actPush:
		receipt, err = c.Push(ctx, ev.CardID, ev.Stream, opts)
		if err != nil {
			return nil, rollback(fmt.Errorf("push failed: %w", err))
		}
	case actDealWork:
		res, dealErr := c.DealWork(ctx, DealWorkParams{Card: ev.CardID, Consumer: ev.Consumer, Score: ev.Score}, opts)
		if dealErr != nil {
			return nil, rollback(fmt.Errorf("deal work failed: %w", dealErr))
		}
		receipt = &res.Receipt
	case actLand:
		receipt, err = c.Land(ctx, ev.CardID, opts)
		if err != nil {
			return nil, rollback(fmt.Errorf("land failed: %w", err))
		}
	default:
		// Other actions: dispatch through Client if supported
		return nil, rollback(fmt.Errorf("unsupported transaction action: %s", ev.Action))
	}

	// 6. Update Sequence Ratchet in Redis
	_, err = c.ratchet.Ratchet(ctx, tx.Seq)
	if err != nil {
		return nil, rollback(err)
	}

	// 7. Post-Mutation State Hash Verification
	var stateHash [32]byte
	if c.verifier != nil {
		// Update shadow state machine
		if err := applyJournalEvent(ctx, c.verifier, ev); err != nil {
			return nil, rollback(fmt.Errorf("shadow state apply failed: %w", err))
		}
		stateHash = c.verifier.StateHash()

		// Verify against ExpectedStateHash if specified
		var emptyHash [32]byte
		if tx.ExpectedStateHash != emptyHash && stateHash != tx.ExpectedStateHash {
			return nil, rollback(fmt.Errorf("%w: expected %x, got %x", ErrStateHashMismatch, tx.ExpectedStateHash, stateHash))
		}
	}

	// 8. In-memory MonotoneRatchet step if configured
	if c.monotoneRatchet != nil {
		var newHash [32]byte
		if stateHash != [32]byte{} {
			newHash = stateHash
		} else if tx.ExpectedStateHash != [32]byte{} {
			newHash = tx.ExpectedStateHash
		} else {
			newHash = sha256.Sum256(append(tx.Payload, byte(tx.Seq)))
		}
		_, err := c.monotoneRatchet.Step(RatchetStep{
			Seq:            tx.Seq,
			Timestamp:      time.Now(),
			PriorStateHash: c.monotoneRatchet.CurrentStateHash(),
			NewStateHash:   newHash,
			Payload:        tx.Payload,
		})
		if err != nil {
			return nil, rollback(fmt.Errorf("monotone ratchet step failed: %w", err))
		}
	}

	// 9. Frame into Journal if JournalWriter is configured
	var journalFrame *Frame
	if c.journalWriter != nil {
		var appendErr error
		journalFrame, appendErr = c.journalWriter.AppendWithSeq(tx.Seq, time.Now(), tx.Payload)
		if appendErr != nil {
			return nil, rollback(fmt.Errorf("journal write failed: %w", appendErr))
		}
	}

	return &TransactionResult{
		Seq:       tx.Seq,
		Receipt:   receipt,
		StateHash: stateHash,
		Frame:     journalFrame,
	}, nil
}

// candidateKeysForEvent determines the set of Redis keys affected by an event.
func candidateKeysForEvent(ev JournalMutationEvent) []string {
	keys := []string{
		"table:streams:revision",
		"table:fleet:revision",
		"table:streams:changes",
		"table:streams:identity",
		"table:fleet:identity",
	}

	if ev.CardID != "" {
		keys = append(keys, MemberKey(string(ev.CardID)))
		epochStr := strconv.FormatUint(uint64(ev.Epoch), 10)
		streamStr := ev.Stream
		if streamStr == "" {
			streamStr = "main"
		}
		keys = append(keys,
			fmt.Sprintf("table:streams:%s:cell:%s:waiting", epochStr, streamStr),
			fmt.Sprintf("table:streams:%s:cell:%s:ready", epochStr, streamStr),
			fmt.Sprintf("table:streams:%s:cell:%s:working", epochStr, streamStr),
			fmt.Sprintf("table:streams:%s:cell:%s:review", epochStr, streamStr),
			fmt.Sprintf("table:streams:%s:cell:%s:merging", epochStr, streamStr),
			fmt.Sprintf("table:streams:%s:cell:%s:landed", epochStr, streamStr),
		)
	}

	if ev.Consumer != "" {
		keys = append(keys, "table:fleet:consumer:"+string(ev.Consumer))
		epochStr := strconv.FormatUint(uint64(ev.Epoch), 10)
		cStr := string(ev.Consumer)
		keys = append(keys,
			fmt.Sprintf("table:fleet:%s:cell:%s:working", epochStr, cStr),
			fmt.Sprintf("table:fleet:%s:cell:%s:ready", epochStr, cStr),
			fmt.Sprintf("table:fleet:%s:cell:%s:review", epochStr, cStr),
			fmt.Sprintf("table:fleet:%s:cell:%s:ok", epochStr, cStr),
			fmt.Sprintf("table:fleet:%s:cell:%s:fail", epochStr, cStr),
		)
	}

	if ev.CopyID.Card != "" {
		keys = append(keys, MemberKey(ev.CopyID.String()))
	}

	return keys
}

// executeCardFCallWithFallback executes a card Redis function via executeScriptOrFCall,
// falling back to EVAL when FCALL is not supported.
func executeCardFCallWithFallback(
	ctx context.Context,
	rdb redis.Cmdable,
	fn string,
	keys []string,
	ro bool,
	forceEval bool,
	options []WriteOptions,
	args ...any,
) ([]any, *Receipt, error) {
	var opts WriteOptions
	if len(options) > 0 {
		opts = options[0]
	}

	headerJSON, err := json.Marshal(struct {
		Epoch string `json:"epoch"`
		Actor string `json:"actor"`
		Fence string `json:"fence"`
		Idem  string `json:"idem"`
	}{
		Epoch: strconv.FormatUint(uint64(opts.Epoch), 10),
		Actor: opts.Actor,
		Fence: opts.Fence,
		Idem:  opts.Idem,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("marshal write options: %w", err)
	}

	callArgs := append([]any{string(headerJSON)}, args...)
	rawScript := GetLuaScript(fn)

	reply, err := executeScriptOrFCall(ctx, rdb, fn, rawScript, keys, ro, callArgs, forceEval)
	if err != nil {
		return nil, nil, fmt.Errorf("execute %s failed: %w", fn, err)
	}

	if len(reply) == 0 {
		return nil, nil, fmt.Errorf("%s returned empty reply", fn)
	}

	// Check for REFUSED protocol reply
	if status := fmt.Sprint(reply[0]); status == "REFUSED" {
		return nil, nil, parseRefusal(fn, reply)
	}

	if ro {
		return reply, nil, nil
	}

	if len(reply) < 2 {
		return nil, nil, fmt.Errorf("%s: missing committed receipt", fn)
	}

	lastIdx := len(reply) - 1
	wireReceipt, ok := reply[lastIdx].([]any)
	if !ok || len(wireReceipt) < 6 || fmt.Sprint(wireReceipt[0]) != "RECEIPT" {
		return nil, nil, fmt.Errorf("%s: malformed receipt element: %v", fn, reply[lastIdx])
	}

	r := Receipt{
		ID:      fmt.Sprint(wireReceipt[1]),
		Outcome: fmt.Sprint(wireReceipt[5]),
	}
	if ep, err := strconv.ParseUint(fmt.Sprint(wireReceipt[2]), 10, 64); err == nil {
		r.Epoch = EpochID(ep)
	}
	if b, err := strconv.ParseUint(fmt.Sprint(wireReceipt[3]), 10, 64); err == nil {
		r.Before = b
	}
	if a, err := strconv.ParseUint(fmt.Sprint(wireReceipt[4]), 10, 64); err == nil {
		r.After = a
	}

	if opts.Receipt != nil {
		*opts.Receipt = r
	}

	return reply[:lastIdx], &r, nil
}
