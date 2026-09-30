package sprintfn

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// Conn is the connection Redis sends on: a go-redis client the caller owns.
// It must be a single-node client whose effective MaxRetries is zero
// (construct it with MaxRetries: -1), so that no transport retry can resend a
// write after a lost reply (L1 1.5).
type Conn = redis.UniversalClient

// Redis is the Client of a store: one FCALL ns_sprint_step per step, one
// FCALL_RO ns_sprint_read per read or page, on its own connection. It does
// not wrap tset.RedisStore, whose reads stay at ns_tset_read (errata E3).
// Construction neither contacts the store nor loads a library.
type Redis struct {
	conn   Conn
	prefix string
}

// NewRedis is the Client of the store conn reaches, for one deployment's
// prefix (8.0).
func NewRedis(conn Conn, names sprint.Names) *Redis {
	return &Redis{conn: conn, prefix: names.Prefix}
}

// preflight refuses a connection that could resend or route a call, before
// anything is sent. It is the check Layer 1's client makes
// (tset.RedisStore.preflight): no client, a Cluster or Ring router, or an
// effective MaxRetries other than zero, or options that cannot be read, is
// UNSUPPORTEDSTORE.
func (r *Redis) preflight() error {
	if r == nil || r.conn == nil {
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
	pipe := r.conn.Pipeline()
	cmds := make([]*redis.Cmd, len(wire))
	for i, w := range wire {
		if w.step != nil {
			cmds[i] = pipe.FCall(ctx, fnStep, nil, Version, string(w.step.raw), string(w.step.sprint))
		} else {
			cmds[i] = pipe.FCallRO(ctx, fnRead, nil, Version, string(w.read))
		}
	}
	// One flush. Its aggregate error does not stand for the replies: each
	// command keeps its own.
	_, execErr := pipe.Exec(ctx)
	results := make([]Result, len(wire))
	for i, cmd := range cmds {
		text, err := cmd.Text()
		if err != nil && execErr != nil && errors.Is(err, redis.Nil) {
			err = execErr
		}
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

// notRunCode names a server error the store gives before a function runs, so
// that the call is known not to have applied; "" for anything else. A script
// error can carry the same words after a write began, so only these exact
// replies count (as Layer 1's client reads them, tset/redis.go).
func notRunCode(err error) string {
	var server redis.Error
	if !errors.As(err, &server) {
		return ""
	}
	switch server.Error() {
	case "ERR Function not found", "NOSUCHFUNCTION No matching function":
		return "FUNCTIONMISSING"
	case "OOM command not allowed when used memory > 'maxmemory'", "OOM command not allowed when used memory > 'maxmemory'.":
		return "OOMSTART"
	}
	return ""
}
