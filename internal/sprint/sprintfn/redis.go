package sprintfn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// Redis is the Client of a store: one FCALL ns_sprint_step per step, one
// FCALL_RO ns_sprint_read per read or page, on a connection it owns. It does
// not wrap tset.RedisStore, whose reads stay at ns_tset_read (errata E3).
// Construction neither contacts the store nor loads a library. Its owner
// closes it.
type Redis struct {
	conn   redis.UniversalClient
	prefix string
	close  func() error
	// addr is the store's address, for the library check's words; library
	// is this build's function library (nil: no check).
	addr    string
	library *Library
	// checked says the library check passed; the first call waits for it.
	mu      sync.Mutex
	checked bool
}

// NewRedis is the Client of the store at address, for one deployment's
// prefix (8.0), built as Layer 1's client is (tset.NewRedis, L1 1.5): it
// creates and owns a standalone go-redis client, with no hook and transport
// retries off (effective MaxRetries zero), so nothing can resend a write
// after a lost reply. The password is read from the environment variable
// passwordEnvVar names, so the caller never passes or keeps the secret; an
// empty name is a server without a password, and a name that is not set is
// an error. No caller-supplied client, option, hook or router is accepted.
// The returned Redis must be closed by its owner.
//
// With a Library, the client checks the store's library against it once,
// before its first call (checkLibrary); a nil Library checks nothing.
func NewRedis(address, user, passwordEnvVar string, names sprint.Names, library *Library) (*Redis, error) {
	client, err := newOwnedClient(address, user, passwordEnvVar, os.LookupEnv)
	if err != nil {
		return nil, err
	}
	return &Redis{conn: client, prefix: names.Prefix, close: client.Close, addr: address, library: library}, nil
}

// Library is this build's function library, as the library check judges a
// store's against it (the grammar decisions, 30): its name, its source, and
// the digest that names a version. The caller gives internal/nsprint/fn's
// (fn.Library, the sprint profile's source, fn.Sum), which this package
// cannot import (fn's own tests import this package).
type Library struct {
	Name   string
	Source func() (string, error)
	Sum    func(source string) string
}

// checkLibrary is the library-matches-this-build check (the grammar decisions,
// 30; the present command's libraryMatches): one FUNCTION LIST before the
// client's first call, once. A store that holds no nova_sprint library, or one
// whose digest is not this build's, is refused with nothing sent, naming both
// digests and the command that loads the library: every step through a
// library of another build would be refused or unreadable.
func (r *Redis) checkLibrary(ctx context.Context) error {
	if r.library == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.checked {
		return nil
	}
	lib := r.library
	libs, err := r.conn.FunctionList(ctx, redis.FunctionListQuery{LibraryNamePattern: lib.Name, WithCode: true}).Result()
	if err != nil {
		return fmt.Errorf("list the %s function library at %s: %w", lib.Name, r.addr, err)
	}
	code, found := "", false
	for _, l := range libs {
		if l.Name == lib.Name {
			code, found = l.Code, true
		}
	}
	want, wantErr := lib.Source()
	if err := lib.Matches(r.addr, code, found, want, wantErr); err != nil {
		return err
	}
	r.checked = true
	return nil
}

// Matches judges a store's library against this build's (checkLibrary): nil
// when the store holds this build's; otherwise the refusal, naming the store,
// both digests and the command that loads the library, as the present
// command's libraryMatches does. A build whose own library does not assemble
// (before gate G0, the sprint profile) matches no store.
func (lib *Library) Matches(addr, code string, found bool, want string, wantErr error) error {
	switch {
	case wantErr != nil:
		return fmt.Errorf("this build's %s library does not assemble, so it matches no store (%v); the store at %s cannot run the sprint's steps", lib.Name, wantErr, addr)
	case !found:
		return fmt.Errorf("the store at %s holds no %s function library; run: nova-redis fn load --addr %s", addr, lib.Name, addr)
	case lib.Sum(code) != lib.Sum(want):
		return fmt.Errorf("the store at %s holds %s library %s, and this build is %s; run: nova-redis fn load --addr %s", addr, lib.Name, lib.Sum(code), lib.Sum(want), addr)
	}
	return nil
}

// newOwnedClient is the one client NewRedis builds: Layer 1's rule, with the
// environment's lookup as a seam for tests.
func newOwnedClient(address, user, passwordEnvVar string, lookup func(string) (string, bool)) (*redis.Client, error) {
	if strings.TrimSpace(address) == "" {
		return nil, errors.New("sprintfn: the Redis address is required")
	}
	password := ""
	if passwordEnvVar != "" {
		value, ok := lookup(passwordEnvVar)
		if !ok {
			return nil, fmt.Errorf("sprintfn: the password environment variable %q is not set", passwordEnvVar)
		}
		password = value
	}
	// go-redis reads MaxRetries -1 as an effective zero; zero would select
	// its default of three retries.
	return redis.NewClient(&redis.Options{Addr: address, Username: user, Password: password, MaxRetries: -1}), nil
}

// newRedisWithClient is the tests' seam: a Redis over a client the package's
// own tests build. Production callers cannot hand in a client whose hooks or
// retries the sprint cannot control; preflight still guards this seam.
func newRedisWithClient(conn redis.UniversalClient, names sprint.Names) *Redis {
	return &Redis{conn: conn, prefix: names.Prefix}
}

// Close releases the client NewRedis owns. A Redis built on the tests' seam
// owns none, and Close is a no-op there.
func (r *Redis) Close() error {
	if r == nil || r.close == nil {
		return nil
	}
	return r.close()
}

// preflight refuses a connection that could resend or route a call, before
// anything is sent. It is the check Layer 1's client makes
// (tset.RedisStore.preflight): no client (a typed nil too), a Cluster or Ring
// router, or an effective MaxRetries other than zero, or options that cannot
// be read, is UNSUPPORTEDSTORE. NewRedis builds none of these; the check
// guards the tests' seam.
func (r *Redis) preflight() error {
	if r == nil || nilClient(r.conn) {
		return &tset.ClientError{Code: "UNSUPPORTEDSTORE", Cause: errors.New("no Redis client")}
	}
	switch r.conn.(type) {
	case *redis.ClusterClient, *redis.Ring:
		return &tset.ClientError{Code: "UNSUPPORTEDSTORE", Cause: errors.New("the sprint's functions need a single-node client")}
	}
	options, ok := r.conn.(interface{ Options() *redis.Options })
	if !ok || options.Options() == nil || options.Options().MaxRetries != 0 {
		return &tset.ClientError{Code: "UNSUPPORTEDSTORE", Cause: errors.New("the sprint's client needs effective MaxRetries=0")}
	}
	return nil
}

// nilClient says a client interface holds nothing callable: nil, or a typed
// nil such as a nil *redis.Client.
func nilClient(c redis.UniversalClient) bool {
	if c == nil {
		return true
	}
	v := reflect.ValueOf(c)
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return v.IsNil()
	}
	return false
}

// settledCmd is one FCALL whose reply, once read, stays its own (Layer 1's
// settledRedisCmd, tset/redis.go). After a transport error part way through
// a pipeline's replies, go-redis v9.22 sets that error on every command whose
// error is still nil, the ones whose replies it has already read included, so
// a step that applied would read as an unknown outcome. A command is settled
// when its reply is read: a value, a nil reply, or a server's error reply;
// an error set after that is ignored. Before it, a transport error is kept.
type settledCmd struct {
	*redis.Cmd
	settled bool
}

var _ redis.Cmder = (*settledCmd)(nil)

// SetErr keeps the first read reply's outcome and ignores what follows it.
func (c *settledCmd) SetErr(err error) {
	if c.settled {
		return
	}
	var server redis.Error
	if err == nil || errors.Is(err, redis.Nil) || errors.As(err, &server) {
		c.settled = true
	}
	c.Cmd.SetErr(err)
}

// wireItem is one item encoded, before the pipeline opens.
type wireItem struct {
	step *encodedStep
	read []byte // an atomic read's plan, or a page's
	page bool
	// ntset and nsprint are an atomic read's query counts, by which its
	// answers split.
	ntset, nsprint int
}

// Pipeline encodes every item, then sends them all in one flush and reads one
// reply for each. An item that does not encode refuses the whole pipeline
// with nothing sent. After the flush, a step whose reply is missing or
// unreadable is an *OutcomeUnknownError; the replies that came stay known.
func (r *Redis) Pipeline(ctx context.Context, items []Item) ([]Result, error) {
	if len(items) == 0 {
		return []Result{}, nil
	}
	wire := make([]wireItem, len(items))
	for i, it := range items {
		w, ref := r.encode(it)
		if ref != nil {
			return nil, &ItemError{Index: i, Refusal: ref}
		}
		wire[i] = w
	}
	if err := r.preflight(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := r.checkLibrary(ctx); err != nil {
		return nil, err
	}
	pipe := r.conn.Pipeline()
	cmds := make([]*settledCmd, len(wire))
	for i, w := range wire {
		var cmd *redis.Cmd
		if w.step != nil {
			cmd = redis.NewCmd(ctx, "fcall", fnStep, 0, Version, string(w.step.raw), string(w.step.sprint))
		} else {
			cmd = redis.NewCmd(ctx, "fcall_ro", fnRead, 0, Version, string(w.read))
		}
		cmds[i] = &settledCmd{Cmd: cmd}
		if err := pipe.Process(ctx, cmds[i]); err != nil {
			// Process only queues; an error here is before the flush, and
			// nothing was sent.
			return nil, err
		}
	}
	// One flush. Its aggregate error does not stand for the replies: each
	// command keeps its own, and a command no reply reached holds the
	// transport's error.
	_, _ = pipe.Exec(ctx)
	results := make([]Result, len(wire))
	for i, cmd := range cmds {
		text, err := cmd.Text()
		switch {
		case wire[i].step != nil:
			results[i] = stepResult(wire[i].step, text, err)
		case wire[i].page:
			results[i] = pageResult(text, err)
		default:
			results[i] = readResult(wire[i].ntset, wire[i].nsprint, text, err)
		}
	}
	return results, nil
}

func (r *Redis) encode(it Item) (wireItem, *Refusal) {
	if ref := checkItem(it); ref != nil {
		return wireItem{}, ref
	}
	switch {
	case it.Step != nil:
		enc, ref := encodeStep(r.prefix, it.Step)
		if ref != nil {
			return wireItem{}, ref
		}
		return wireItem{step: &enc}, nil
	case it.Read != nil:
		b, ref := encodeRead(r.prefix, it.Read)
		if ref != nil {
			return wireItem{}, ref
		}
		return wireItem{read: b, ntset: len(it.Read.Tset), nsprint: len(it.Read.Sprint)}, nil
	default:
		plan, ref := checkPage(r.prefix, it.Page)
		if ref != nil {
			return wireItem{}, ref
		}
		b, err := tset.EncodeReadPlan(plan)
		if err != nil {
			return wireItem{}, refusalOf(PhaseOpen, err)
		}
		return wireItem{read: b, page: true}, nil
	}
}

// stepResult reads one step's reply. A server error from before the function
// ran (no such function, an OOM at the start) is known not to have applied; a
// refusal is known not to have applied; anything else after the flush is an
// unknown outcome.
func stepResult(w *encodedStep, text string, err error) Result {
	if err != nil {
		if code := notRunCode(err); code != "" {
			return Result{Err: &tset.ClientError{Code: code, Cause: err}}
		}
		return Result{Err: unknown(w, err)}
	}
	ref, reply, perr := decodeStepReply([]byte(text))
	if perr != nil {
		return Result{Err: unknown(w, perr)}
	}
	if ref != nil {
		return Result{Refusal: ref}
	}
	return Result{Step: reply}
}

func unknown(w *encodedStep, cause error) *OutcomeUnknownError {
	return &OutcomeUnknownError{Step: append([]byte(nil), w.raw...), Sprint: append([]byte(nil), w.sprint...), Cause: cause}
}

// stepEnvelope is ns_sprint_step's success reply: Layer 1's reply, prepared by
// S.prepare, and what each part decided, encoded before the commit.
type stepEnvelope struct {
	Reply json.RawMessage            `json:"reply"`
	Parts map[string]json.RawMessage `json:"parts"`
}

// decodeStepReply reads a refusal, or the envelope of a success.
func decodeStepReply(b []byte) (*Refusal, *StepReply, error) {
	if ref, ok, err := decodeRefusal(b); ok || err != nil {
		return ref, nil, err
	}
	var env stepEnvelope
	if err := json.Unmarshal(b, &env); err != nil || env.Reply == nil {
		return nil, nil, errors.New("sprintfn: malformed step reply")
	}
	reply, err := tset.DecodeReply(env.Reply)
	if err != nil {
		return nil, nil, err
	}
	if env.Parts == nil {
		env.Parts = map[string]json.RawMessage{}
	}
	return nil, &StepReply{Reply: reply, Parts: env.Parts}, nil
}

// decodeRefusal reads a reply whose status is "refused", and says whether it
// was one.
func decodeRefusal(b []byte) (*Refusal, bool, error) {
	var head struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(b, &head) != nil {
		return nil, false, errors.New("sprintfn: malformed reply")
	}
	if head.Status != "refused" {
		return nil, false, nil
	}
	var ref Refusal
	if err := json.Unmarshal(b, &ref); err != nil || ref.Code == "" {
		return nil, true, errors.New("sprintfn: malformed refusal")
	}
	return &ref, true, nil
}

// readResult reads an atomic read's reply: Layer 1's envelope, its answers
// split into the Layer 1 and 2 answers and the sprint's, in plan order. A read
// writes nothing, so a lost read is only an error.
func readResult(ntset, nsprint int, text string, err error) Result {
	if err != nil {
		if code := notRunCode(err); code != "" {
			return Result{Err: &tset.ClientError{Code: code, Cause: err}}
		}
		return Result{Err: err}
	}
	ref, ok, derr := decodeRefusal([]byte(text))
	if derr != nil {
		return Result{Err: derr}
	}
	if ok {
		return Result{Refusal: ref}
	}
	var env struct {
		Status      string            `json:"status"`
		Epoch       tset.Decimal      `json:"epoch"`
		ActiveEpoch tset.Decimal      `json:"active_epoch"`
		TimeMS      tset.Decimal      `json:"time_ms"`
		Answers     []json.RawMessage `json:"answers"`
	}
	if json.Unmarshal([]byte(text), &env) != nil || env.Status != "read" || len(env.Answers) != ntset+nsprint ||
		!tset.ValidDecimal(env.Epoch) || !tset.ValidDecimal(env.ActiveEpoch) || !tset.ValidDecimal(env.TimeMS) {
		return Result{Err: errors.New("sprintfn: malformed read reply")}
	}
	out := &ReadReply{Epoch: env.Epoch, ActiveEpoch: env.ActiveEpoch, TimeMS: env.TimeMS,
		Tset: make([]tset.ReadAnswer, ntset), Sprint: env.Answers[ntset:]}
	for i := 0; i < ntset; i++ {
		if json.Unmarshal(env.Answers[i], &out.Tset[i]) != nil {
			return Result{Err: errors.New("sprintfn: malformed read answer")}
		}
	}
	return Result{Read: out}
}

// pageResult reads a page's reply.
func pageResult(text string, err error) Result {
	if err != nil {
		if code := notRunCode(err); code != "" {
			return Result{Err: &tset.ClientError{Code: code, Cause: err}}
		}
		return Result{Err: err}
	}
	ref, ok, derr := decodeRefusal([]byte(text))
	if derr != nil {
		return Result{Err: derr}
	}
	if ok {
		return Result{Refusal: ref}
	}
	reply, perr := tset.DecodeReadReply([]byte(text))
	if perr != nil {
		return Result{Err: perr}
	}
	return Result{Page: &reply}
}

// notRunCode names a server error that proves the function never ran, so that
// the call is known not to have applied; "" for anything else. Only replies
// the server gives before it enters the function count: no such function and
// an out-of-memory refusal at the start (the two Layer 1's client reads,
// tset/redis.go), an arity error, and an ACL refusal of the FCALL itself.
// Every other reply, and every transport error, is an unknown
// outcome: a script error, a WRONGTYPE from inside the function, BUSY, LOADING
// and READONLY can follow a write that began, and a script error or a
// transport error can quote any of these words, so a match is made on the whole
// reply of a typed server error and never on a substring.
func notRunCode(err error) string {
	var server redis.Error
	if !errors.As(err, &server) {
		return ""
	}
	message := server.Error()
	switch message {
	case "ERR Function not found", "NOSUCHFUNCTION No matching function",
		"NOSCRIPT No matching script. Please use EVAL.":
		return "FUNCTIONMISSING"
	case "OOM command not allowed when used memory > 'maxmemory'", "OOM command not allowed when used memory > 'maxmemory'.":
		return "OOMSTART"
	case "ERR wrong number of arguments for 'fcall' command", "ERR wrong number of arguments for 'fcall_ro' command":
		return "ARITY"
	}
	if noPermFCall.MatchString(message) {
		return "NOPERM"
	}
	return ""
}

// noPermFCall is the ACL refusal of the FCALL command itself, given before
// dispatch. An ACL refusal of a command the function runs is a script error,
// which names another command and is not this reply.
var noPermFCall = regexp.MustCompile(`^NOPERM User \S+ has no permissions to run the 'fcall(_ro)?' command$`)
