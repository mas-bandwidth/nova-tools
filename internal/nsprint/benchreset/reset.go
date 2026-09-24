// Package benchreset implements the fenced, fail-closed reset of one
// nova-sprint bench (#3311). All Redis writes are nova_sprint Function calls;
// tests use only a throwaway Redis and a fake Stopper.
package benchreset

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

const (
	FunctionBegin   = "ns_bench_reset_begin"
	FunctionBeat    = "ns_bench_reset_beat"
	FunctionRequeue = "ns_bench_reset_requeue"
	FunctionEnd     = "ns_bench_reset_end"
	FunctionHold    = "ns_bench_reset_hold"
	FunctionClear   = "ns_bench_reset_clear"
	DefaultGrace    = 5 * time.Second
	BeatInterval    = 30 * time.Second
)

type Card struct {
	Sprint, Label string
	Attempt       int
	State         string
}

func (c Card) Identity() string { return fmt.Sprintf("%s/%s/%d", c.Sprint, c.Label, c.Attempt) }

type StopResult struct {
	Card   Card
	Status string // STOPPED, GONE, ALIVE, or KEPT
}

type Stopper interface {
	Stop(context.Context, deal.Bench, []Card, time.Duration) ([]StopResult, error)
}

type Request struct {
	Bench, Actor, Idem string
	KeepQueue          bool
	Grace              time.Duration
	Stopper            Stopper
	ID                 string // test seam; empty mints 128 random bits
	BeatEvery          time.Duration
}

type CardResult struct {
	Card Card
	From string
	To   string
}

type Result struct {
	Code                           int
	Bench, ID, Why                 string
	Stopped, Requeued, Kept, Alive int
	Took                           time.Duration
	Cards                          []CardResult
}

var ErrFenced = errors.New("FENCED")

func Reset(ctx context.Context, c *redis.Client, req Request) (Result, error) {
	began := time.Now()
	res := Result{Bench: req.Bench}
	if c == nil || req.Bench == "" || req.Actor == "" || req.Stopper == nil {
		return res, fmt.Errorf("bench, actor, redis and stopper are required")
	}
	if req.Grace <= 0 {
		req.Grace = DefaultGrace
	}
	id, err := resetID(req.ID)
	if err != nil {
		return res, err
	}
	res.ID = id
	reply, err := c.FCall(ctx, FunctionBegin, nil, req.Bench, id, req.Actor, req.Idem).StringSlice()
	if err != nil {
		return res, fmt.Errorf("bench reset begin: %w", err)
	}
	if len(reply) == 0 {
		return res, errors.New("bench reset begin: empty reply")
	}
	switch reply[0] {
	case "STARTED":
	case "BUSY", "UNREGISTERED":
		res.Code, res.Why = 2, strings.ToLower(reply[0])
		if len(reply) > 1 {
			res.Why += ":" + reply[1]
		}
		return finish(res, began), nil
	default:
		return res, fmt.Errorf("bench reset begin: %v", reply)
	}

	bench, cards, err := readCards(ctx, c, req.Bench)
	if err != nil {
		return hold(ctx, c, req, res, "ssh:read:"+oneLine(err), began)
	}
	var stopCards []Card
	statuses := map[string]string{}
	for _, card := range cards {
		if req.KeepQueue && card.State == "dealt" {
			statuses[card.Identity()] = "KEPT"
			continue
		}
		stopCards = append(stopCards, card)
	}

	beatEvery := req.BeatEvery
	if beatEvery <= 0 {
		beatEvery = BeatInterval
	}
	stopBeat, beatErr := startBeater(ctx, c, req.Bench, id, beatEvery)
	stopped, stopErr := req.Stopper.Stop(ctx, bench, stopCards, req.Grace)
	stopBeat()
	if err := <-beatErr; err != nil {
		return finish(Result{Code: 1, Bench: req.Bench, ID: id, Why: "fenced"}, began), nil
	}
	if stopErr != nil {
		return hold(ctx, c, req, res, "ssh:"+oneLine(stopErr), began)
	}
	for _, got := range stopped {
		key := got.Card.Identity()
		if _, exists := statuses[key]; exists {
			return hold(ctx, c, req, res, "ssh:duplicate-result", began)
		}
		statuses[key] = got.Status
	}
	for _, card := range stopCards {
		if _, ok := statuses[card.Identity()]; !ok {
			statuses[card.Identity()] = "ALIVE"
		}
	}

	args := []any{req.Bench, id, req.Actor}
	for _, card := range cards {
		args = append(args, card.Sprint, card.Label, strconv.Itoa(card.Attempt), statuses[card.Identity()])
	}
	requeue, err := c.FCall(ctx, FunctionRequeue, nil, args...).StringSlice()
	if err != nil {
		return res, fmt.Errorf("bench reset requeue: %w", err)
	}
	if len(requeue) > 0 && requeue[0] == "FENCED" {
		return finish(Result{Code: 1, Bench: req.Bench, ID: id, Why: "fenced"}, began), nil
	}
	if len(requeue) < 5 || requeue[0] != "RESET" {
		return res, fmt.Errorf("bench reset requeue: %v", requeue)
	}
	res.Stopped, _ = strconv.Atoi(requeue[1])
	res.Requeued, _ = strconv.Atoi(requeue[2])
	res.Kept, _ = strconv.Atoi(requeue[3])
	res.Alive, _ = strconv.Atoi(requeue[4])
	for i := 5; i+4 < len(requeue); i += 5 {
		a, _ := strconv.Atoi(requeue[i+2])
		res.Cards = append(res.Cards, CardResult{Card: Card{Sprint: requeue[i], Label: requeue[i+1], Attempt: a}, From: requeue[i+3], To: requeue[i+4]})
	}
	if res.Alive > 0 {
		return hold(ctx, c, req, res, fmt.Sprintf("alive:%d", res.Alive), began)
	}
	end, err := c.FCall(ctx, FunctionEnd, nil, req.Bench, id, req.Actor,
		res.Stopped, res.Requeued, res.Kept, res.Alive).StringSlice()
	if err != nil {
		return res, fmt.Errorf("bench reset end: %w", err)
	}
	if len(end) > 0 && end[0] == "FENCED" {
		res.Code, res.Why = 1, "fenced"
		return finish(res, began), nil
	}
	if len(end) == 0 || end[0] != "ENDED" {
		return res, fmt.Errorf("bench reset end: %v", end)
	}
	return finish(res, began), nil
}

func Clear(ctx context.Context, c *redis.Client, bench, actor, why string) (Result, error) {
	res := Result{Bench: bench, Why: why}
	if c == nil || bench == "" || actor == "" || strings.TrimSpace(why) == "" {
		return res, errors.New("bench, actor, why and redis are required")
	}
	reply, err := c.FCall(ctx, FunctionClear, nil, bench, actor, why).StringSlice()
	if err != nil {
		return res, fmt.Errorf("bench reset clear: %w", err)
	}
	if len(reply) == 0 {
		return res, errors.New("bench reset clear: empty reply")
	}
	switch reply[0] {
	case "CLEARED":
		if len(reply) > 1 {
			res.ID = reply[1]
		}
	case "BUSY":
		res.Code, res.Why = 2, "busy"
	case "NOT-HELD":
		res.Code, res.Why = 2, "not-held"
	default:
		return res, fmt.Errorf("bench reset clear: %v", reply)
	}
	return res, nil
}

func hold(ctx context.Context, c *redis.Client, req Request, res Result, why string, began time.Time) (Result, error) {
	res.Code, res.Why = 1, why
	reply, err := c.FCall(ctx, FunctionHold, nil, req.Bench, res.ID, req.Actor, why,
		res.Stopped, res.Requeued, res.Kept, res.Alive).StringSlice()
	if err != nil {
		return res, fmt.Errorf("bench reset hold: %w", err)
	}
	if len(reply) > 0 && reply[0] == "FENCED" {
		res.Why = "fenced"
		return finish(res, began), nil
	}
	if len(reply) == 0 || reply[0] != "HELD" {
		return res, fmt.Errorf("bench reset hold: %v", reply)
	}
	return finish(res, began), nil
}

func finish(res Result, began time.Time) Result { res.Took = time.Since(began); return res }

func resetID(given string) (string, error) {
	if given != "" {
		return given, nil
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("reset id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func readCards(ctx context.Context, c *redis.Client, bench string) (deal.Bench, []Card, error) {
	pipe := c.Pipeline()
	member := pipe.SIsMember(ctx, "benches", bench)
	beat := pipe.HGetAll(ctx, "bench:"+bench+":beat")
	starting := pipe.ZRange(ctx, "bench:"+bench+":starting", 0, -1)
	living := pipe.ZRange(ctx, "bench:"+bench+":living", 0, -1)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return deal.Bench{}, nil, err
	}
	if !member.Val() {
		return deal.Bench{}, nil, fmt.Errorf("bench %s is not registered", bench)
	}
	b := deal.Bench{Name: bench, Host: beat.Val()["host"], User: beat.Val()["user"]}
	seen := map[string]bool{}
	var cards []Card
	for _, identity := range append(starting.Val(), living.Val()...) {
		if seen[identity] {
			continue
		}
		seen[identity] = true
		parts := strings.Split(identity, "/")
		if len(parts) != 3 {
			return b, nil, fmt.Errorf("invalid card identity %q", identity)
		}
		a, err := strconv.Atoi(parts[2])
		if err != nil || a < 1 {
			return b, nil, fmt.Errorf("invalid card identity %q", identity)
		}
		cards = append(cards, Card{Sprint: parts[0], Label: parts[1], Attempt: a})
	}
	sort.Slice(cards, func(i, j int) bool { return cards[i].Identity() < cards[j].Identity() })
	pipe = c.Pipeline()
	cmds := make([]*redis.SliceCmd, len(cards))
	for i, card := range cards {
		cmds[i] = pipe.HMGet(ctx, "s:"+card.Sprint+":card:"+card.Label, "state", "bench", "attempt")
	}
	if len(cards) > 0 {
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return b, nil, err
		}
	}
	out := cards[:0]
	for i, card := range cards {
		v := cmds[i].Val()
		if len(v) < 3 {
			continue
		}
		state := asString(v[0])
		if asString(v[1]) != bench || asString(v[2]) != strconv.Itoa(card.Attempt) {
			continue
		}
		if state != "dealt" && state != "launched" && state != "running" {
			continue
		}
		card.State = state
		out = append(out, card)
	}
	return b, out, nil
}

func asString(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func startBeater(ctx context.Context, c *redis.Client, bench, id string, every time.Duration) (func(), <-chan error) {
	stop := make(chan struct{})
	done := make(chan error, 1)
	var once sync.Once
	go func() {
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				done <- nil
				return
			case <-ctx.Done():
				done <- ctx.Err()
				return
			case <-ticker.C:
				reply, err := c.FCall(ctx, FunctionBeat, nil, bench, id).StringSlice()
				if err != nil {
					done <- err
					return
				}
				if len(reply) == 0 || reply[0] != "BEAT" {
					done <- ErrFenced
					return
				}
			}
		}
	}()
	return func() { once.Do(func() { close(stop) }) }, done
}

func oneLine(err error) string {
	s := strings.TrimSpace(err.Error())
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

// RemoteStopper uses a deal.Opener-shaped single-use session for the whole
// bench. The session captures the bench-side stop protocol's stdout.
type RemoteStopper struct{ Program string }

func (r RemoteStopper) Stop(ctx context.Context, b deal.Bench, cards []Card, grace time.Duration) ([]StopResult, error) {
	if len(cards) == 0 {
		return nil, nil
	}
	ss := &stopSession{program: r.Program, bench: b, grace: grace}
	opened := false
	open := deal.Opener(func(context.Context) (deal.Session, error) {
		if opened {
			return nil, deal.ErrSessionPerCard
		}
		opened = true
		return ss, nil
	})
	session, err := open(ctx)
	if err != nil {
		return nil, err
	}
	var in bytes.Buffer
	for _, card := range cards {
		fmt.Fprintf(&in, "%s %s %d\n", card.Sprint, card.Label, card.Attempt)
	}
	if err := session.Run(ctx, in.Bytes()); err != nil {
		return nil, err
	}
	byID := map[string]Card{}
	for _, card := range cards {
		byID[card.Identity()] = card
	}
	var result []StopResult
	seen := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(ss.stdout))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 2 {
			return nil, fmt.Errorf("invalid stop reply %q", sc.Text())
		}
		card, ok := byID[f[1]]
		if !ok || seen[f[1]] {
			return nil, fmt.Errorf("invalid stop identity %q", f[1])
		}
		if f[0] != "STOPPED" && f[0] != "GONE" && f[0] != "ALIVE" {
			return nil, fmt.Errorf("invalid stop status %q", f[0])
		}
		seen[f[1]] = true
		result = append(result, StopResult{Card: card, Status: f[0]})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

type stopSession struct {
	program string
	bench   deal.Bench
	grace   time.Duration
	stdout  []byte
}

func (s *stopSession) Run(ctx context.Context, stdin []byte) error {
	program := s.program
	if program == "" {
		program = "ssh"
	}
	command := fmt.Sprintf("exec bash -lc 'exec nova-sprint card stop --stdin --grace %s'", s.grace.String())
	args := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=5", s.bench.Target(), command}
	testguard.RefuseHosts(program, args...)
	runCtx, cancel := context.WithTimeout(ctx, s.grace+30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, program, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	s.stdout = append([]byte(nil), out.Bytes()...)
	return nil
}
