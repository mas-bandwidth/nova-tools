// Friend serve (nova-tools #2938): the loop unit that sits on a friend's seat
// so the seat takes work without the friend being awake in a session. Tonight
// Emma sat at 3 ready / 0 working while "up" because nothing on her seat took
// from her queue. Serve is that thing, at zero model tokens: every second it
// beats (ns_friend_serve_beat, one call), takes ready tasks up to its width
// (task.TakeAvailable, one pipeline plus one call), writes each task's brief
// and dispatches it to the friend's own harness by exec, watches the child,
// and on exit closes the task with the typed line the child wrote or with
// `blocked: <exit reason>`. A task is in working only while its child lives:
// a serve that stops kills its children and gives their tasks back.
package life

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nogh"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/brief"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/read"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
	"github.com/redis/go-redis/v9"
)

// Function names registered by internal/nsprint/fn/lua/friend_serve.lua.
const (
	FunctionServeBeat    = "ns_friend_serve_beat"
	FunctionServeRelease = "ns_friend_serve_release"
)

// ServeLockTTL is the seat lock's and both presence hashes' TTL: five beats at
// BeatInterval, the same window presence.lua gives a friend beat.
const ServeLockTTL = 5 * time.Second

// TaskBeatEvery is how often serve beats each live child's task lease
// (task.Beat's contract: every 60 s, sent by the process that started the
// child). The first beat is the start acknowledgement.
const TaskBeatEvery = 60 * time.Second

// Dispatch argv tokens. Every token is replaced in every argument before exec;
// the same values reach the child in its environment (ServeEnv*).
const (
	TokenBrief  = "@brief"
	TokenOut    = "@out"
	TokenDir    = "@dir"
	TokenSprint = "@sprint"
	TokenID     = "@id"
)

// Environment the child is started with, over the serve's own.
const (
	ServeEnvFriend  = "NOVA_FRIEND"
	ServeEnvSprint  = "NOVA_TASK_SPRINT"
	ServeEnvID      = "NOVA_TASK_ID"
	ServeEnvAttempt = "NOVA_TASK_ATTEMPT"
	ServeEnvToken   = "NOVA_TASK_TOKEN"
	ServeEnvKind    = "NOVA_TASK_KIND"
	ServeEnvHead    = "NOVA_TASK_HEAD"
	ServeEnvBrief   = "NOVA_TASK_BRIEF"
	ServeEnvOut     = "NOVA_TASK_OUT"
	ServeEnvDir     = "NOVA_TASK_DIR"
)

// ShimDirName is the directory under the serve root that holds the refusing
// gh (internal/nogh) every child finds first on its PATH. The dot keeps it
// apart from the per-sprint task dirs beside it.
const ShimDirName = ".shim"

// LogMaxLen bounds friend:<f>:log.
const LogMaxLen = 10000

// outTail is how much of a child's output the close reads for its typed line.
const outTail = 64 * 1024

// ErrSeatHeld is a beat refused because another session serves the seat.
// SeatHeldError carries the holder.
var ErrSeatHeld = errors.New("seat held")

// SeatHeldError names the session that holds the seat.
type SeatHeldError struct{ Friend, Holder string }

func (e *SeatHeldError) Error() string {
	return fmt.Sprintf("friend serve %s: seat held by session %s", e.Friend, e.Holder)
}

func (e *SeatHeldError) Is(target error) bool { return target == ErrSeatHeld }

// ErrUnregistered is a serve for a name not in the friends SET.
var ErrUnregistered = errors.New("unregistered")

// ErrLogin is a start whose login aliases ns_friend_hello refused.
// LoginError carries the refusal words.
var ErrLogin = errors.New("login refused")

// LoginError is ns_friend_hello's refusal of the seat's --login aliases, in
// its own words (LOGIN-TAKEN <alias> <friend>, LOGIN-IS-FRIEND <alias>,
// NAME-IS-LOGIN <friend>, INVALID <alias>).
type LoginError struct{ Friend, Words string }

func (e *LoginError) Error() string {
	return fmt.Sprintf("friend serve %s: login: %s", e.Friend, e.Words)
}

func (e *LoginError) Is(target error) bool { return target == ErrLogin }

// ServeConfig is one seat.
type ServeConfig struct {
	Friend  string
	Session string
	Host    string
	Harness string
	// Sprint limits the take to one sprint; empty takes across sprint:order.
	Sprint string
	// Width is the most children the seat runs at once (at least 1). The
	// take is also capped by friend:<f>:desired, as every take is.
	Width int
	// Dispatch is the harness argv, with the Token* words replaced per task.
	Dispatch []string
	// Dir is the root under which each task gets <sprint>/<id>-<attempt>/
	// holding brief.md and out.log.
	Dir string
	// Actor is the receipts' actor; the friend itself when empty.
	Actor string
	// Env is appended to the child's environment after the ServeEnv* values.
	Env []string
	// Mirror is the git mirror a read task's diff comes from; empty is the
	// bench mirror of the task's repo (~/nova-bench/mirror/<repo>.git).
	Mirror string
	// Out receives one line per event; nil discards.
	Out io.Writer
	// Logins are `--login` aliases (#3797): written to friends:login once on
	// start through ns_friend_hello, the only writer of that hash.
	Logins []string
}

// child is one dispatched task.
type child struct {
	claim    task.Claim
	head     string
	kind     string
	dir      string
	brief    string
	out      string
	briefSrc string
	cmd      *exec.Cmd
	started  time.Time
	lastBeat time.Time
	exited   chan struct{}
	waitErr  error
}

func (c *child) key() string { return c.claim.Sprint + "/" + c.claim.ID }

// Server is one live seat.
type Server struct {
	st       *store.Store
	cfg      ServeConfig
	mu       sync.Mutex
	children map[string]*child
	now      func() time.Time
}

// NewServer checks the config and returns a seat that has not beaten yet.
func NewServer(st *store.Store, cfg ServeConfig) (*Server, error) {
	if st == nil {
		return nil, fmt.Errorf("friend serve: nil store")
	}
	if cfg.Friend == "" || cfg.Session == "" {
		return nil, fmt.Errorf("friend serve: friend and session are required")
	}
	if cfg.Width < 1 {
		return nil, fmt.Errorf("friend serve %s: width must be at least 1", cfg.Friend)
	}
	if len(cfg.Dispatch) == 0 || cfg.Dispatch[0] == "" {
		return nil, fmt.Errorf("friend serve %s: dispatch argv is required", cfg.Friend)
	}
	if cfg.Dir == "" {
		return nil, fmt.Errorf("friend serve %s: dir is required", cfg.Friend)
	}
	if cfg.Actor == "" {
		cfg.Actor = cfg.Friend
	}
	if cfg.Out == nil {
		cfg.Out = io.Discard
	}
	return &Server{st: st, cfg: cfg, children: map[string]*child{}, now: time.Now}, nil
}

// Live is the number of children running now.
func (s *Server) Live() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.children)
}

// Beat takes or renews the seat and writes presence: friend:<f>:serve (the
// lock), friend:<f>:beat under its TTL, and friend:<f>:last (where friend:<f>
// is not a row, friend:<f> also carries the beat TTL).
// A seat another session holds is a SeatHeldError (ErrSeatHeld).
func (s *Server) Beat(ctx context.Context) error {
	stamp := s.now().UTC().Format(time.RFC3339)
	reply, err := s.st.Client().FCall(ctx, FunctionServeBeat, nil,
		s.cfg.Friend, s.cfg.Session, stamp, strconv.Itoa(s.Live()), strconv.Itoa(s.cfg.Width),
		s.cfg.Harness, s.cfg.Host, s.cfg.Actor).Slice()
	if err != nil {
		return fmt.Errorf("friend serve %s: beat: %w", s.cfg.Friend, err)
	}
	if len(reply) == 0 {
		return fmt.Errorf("friend serve %s: beat: empty reply", s.cfg.Friend)
	}
	switch fmt.Sprint(reply[0]) {
	case "OK":
		if len(reply) > 1 && fmt.Sprint(reply[1]) == "0" {
			s.receipt(ctx, "serve-up", nil, "width="+strconv.Itoa(s.cfg.Width))
		}
		return nil
	case "BUSY":
		holder := ""
		if len(reply) > 1 {
			holder = fmt.Sprint(reply[1])
		}
		return &SeatHeldError{Friend: s.cfg.Friend, Holder: holder}
	case "UNREGISTERED":
		return fmt.Errorf("friend serve %s: %w: nova-sprint capacity friend", s.cfg.Friend, ErrUnregistered)
	default:
		return fmt.Errorf("friend serve %s: beat: %s", s.cfg.Friend, fmt.Sprint(reply[0]))
	}
}

// Start is the seat's first step: beat (take the seat), then bind the
// --login aliases through ns_friend_hello under the same session. A refused
// login releases the seat, so a clashing alias never leaves a seat held.
func (s *Server) Start(ctx context.Context) error {
	if err := s.Beat(ctx); err != nil {
		return err
	}
	if err := s.login(ctx); err != nil {
		_ = s.Release(context.WithoutCancel(ctx))
		return err
	}
	return nil
}

// login writes the seat's aliases to friends:login through ns_friend_hello
// (#3092 rev 6), one call. The beat already holds friend:<f>:beat under this
// session, so the hello is a renewal: no second friend-up, no take.
func (s *Server) login(ctx context.Context) error {
	if len(s.cfg.Logins) == 0 {
		return nil
	}
	fargs := []any{s.cfg.Friend, -1, s.cfg.Harness, s.cfg.Host, s.cfg.Session, "", s.cfg.Actor, ""}
	for _, alias := range s.cfg.Logins {
		fargs = append(fargs, alias)
	}
	reply, err := s.st.Client().FCall(ctx, FunctionHello, nil, fargs...).Slice()
	if err != nil {
		return fmt.Errorf("friend serve %s: login: %w", s.cfg.Friend, err)
	}
	if len(reply) == 0 {
		return fmt.Errorf("friend serve %s: login: empty reply", s.cfg.Friend)
	}
	switch status := fmt.Sprint(reply[0]); status {
	case "UP":
		s.receipt(ctx, "login", nil, "logins="+strings.Join(s.cfg.Logins, ","))
		return nil
	case "UNREGISTERED":
		return fmt.Errorf("friend serve %s: %w: nova-sprint capacity friend", s.cfg.Friend, ErrUnregistered)
	default:
		words := make([]string, 0, len(reply))
		for _, v := range reply {
			words = append(words, fmt.Sprint(v))
		}
		return &LoginError{Friend: s.cfg.Friend, Words: strings.Join(words, " ")}
	}
}

// Release is the seat's bye: only the serving session releases.
func (s *Server) Release(ctx context.Context) error {
	reply, err := s.st.Client().FCall(ctx, FunctionServeRelease, nil,
		s.cfg.Friend, s.cfg.Session, s.cfg.Actor).Slice()
	if err != nil {
		return fmt.Errorf("friend serve %s: release: %w", s.cfg.Friend, err)
	}
	if len(reply) == 0 {
		return fmt.Errorf("friend serve %s: release: empty reply", s.cfg.Friend)
	}
	switch fmt.Sprint(reply[0]) {
	case "DOWN":
		s.receipt(ctx, "serve-down", nil, "")
		return nil
	case "BUSY":
		holder := ""
		if len(reply) > 1 {
			holder = fmt.Sprint(reply[1])
		}
		return &SeatHeldError{Friend: s.cfg.Friend, Holder: holder}
	default:
		return fmt.Errorf("friend serve %s: release: %s", s.cfg.Friend, fmt.Sprint(reply[0]))
	}
}

// PassResult is what one pass did.
type PassResult struct {
	Taken  int
	Closed int
	Live   int
}

// Pass is one tick: beat, close the children that exited, beat the leases
// that are due, take up to the free width and dispatch each claim.
func (s *Server) Pass(ctx context.Context) (PassResult, error) {
	var res PassResult
	if err := s.Beat(ctx); err != nil {
		return res, err
	}
	res.Closed = s.reap(ctx)
	s.beatLeases(ctx)
	free := s.cfg.Width - s.Live()
	if free > 0 {
		claims, err := task.TakeAvailable(ctx, s.st, s.cfg.Friend, s.cfg.Sprint, "", free, s.cfg.Actor, "")
		if err != nil {
			return res, fmt.Errorf("friend serve %s: take: %w", s.cfg.Friend, err)
		}
		for _, c := range claims {
			res.Taken++
			s.receipt(ctx, "take", &c, "kind="+c.Kind)
			if err := s.start(ctx, c); err != nil {
				s.closeBlocked(ctx, c, "", "", "dispatch: "+err.Error())
				res.Closed++
			}
		}
		// the start-ack of every child just spawned: one call (#3915)
		s.beatLeases(ctx)
	}
	res.Live = s.Live()
	return res, nil
}

// Watch is a pass without a take: beat, close what exited, beat the leases.
func (s *Server) Watch(ctx context.Context) (int, error) {
	if err := s.Beat(ctx); err != nil {
		return 0, err
	}
	closed := s.reap(ctx)
	s.beatLeases(ctx)
	return closed, nil
}

// Wait watches at BeatInterval until every child has exited and is closed.
func (s *Server) Wait(ctx context.Context) (int, error) {
	closed := 0
	for s.Live() > 0 {
		select {
		case <-ctx.Done():
			return closed, ctx.Err()
		case <-time.After(BeatInterval):
		}
		n, err := s.Watch(ctx)
		closed += n
		if err != nil {
			return closed, err
		}
	}
	return closed, nil
}

// Stop kills every live child, gives its task back (task.Cancel, so the next
// serve retakes it) and releases the seat. A task is never left in working
// without a live child.
func (s *Server) Stop(ctx context.Context, reason string) error {
	s.mu.Lock()
	live := make([]*child, 0, len(s.children))
	for _, c := range s.children {
		live = append(live, c)
	}
	s.mu.Unlock()
	var first error
	for _, c := range live {
		killGroup(c.cmd)
		<-c.exited
		status, err := task.Cancel(ctx, s.st, task.CancelRequest{
			Sprint: c.claim.Sprint, ID: c.claim.ID, Token: c.claim.Token,
			Reason: "serve-stopped: " + reason, Actor: s.cfg.Actor,
		})
		if err != nil && first == nil {
			first = err
		}
		s.receipt(ctx, "cancel", &c.claim, "status="+string(status)+" reason="+reason)
		s.mu.Lock()
		delete(s.children, c.key())
		s.mu.Unlock()
	}
	if err := s.Release(ctx); err != nil && first == nil {
		first = err
	}
	return first
}

// Serve runs passes at BeatInterval until ctx is done, then stops.
func Serve(ctx context.Context, st *store.Store, cfg ServeConfig) error {
	s, err := NewServer(st, cfg)
	if err != nil {
		return err
	}
	if err := s.Start(ctx); err != nil {
		return err
	}
	if _, err := s.Pass(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(BeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return s.Stop(context.WithoutCancel(ctx), "signal")
		case <-ticker.C:
			if _, err := s.Pass(ctx); err != nil {
				if errors.Is(err, ErrSeatHeld) || errors.Is(err, ErrUnregistered) {
					_ = s.Stop(context.WithoutCancel(ctx), err.Error())
					return err
				}
				fmt.Fprintf(s.cfg.Out, "SERVE %s pass: %v\n", cfg.Friend, err)
			}
		}
	}
}

// ServeOnce is `friend wake --as <f>`: one pass, then watch the children it
// started until they close, then release the seat.
func ServeOnce(ctx context.Context, st *store.Store, cfg ServeConfig) (PassResult, error) {
	s, err := NewServer(st, cfg)
	if err != nil {
		return PassResult{}, err
	}
	if err := s.Start(ctx); err != nil {
		return PassResult{}, err
	}
	res, err := s.Pass(ctx)
	if err != nil {
		return res, err
	}
	closed, err := s.Wait(ctx)
	res.Closed += closed
	res.Live = s.Live()
	if err != nil {
		_ = s.Stop(context.WithoutCancel(ctx), err.Error())
		return res, err
	}
	return res, s.Release(ctx)
}

// start writes the brief, starts the harness and sends the start-ack beat.
func (s *Server) start(ctx context.Context, c task.Claim) error {
	fields, err := s.st.Client().HGetAll(ctx, task.Key(c.Sprint, c.ID)).Result()
	if err != nil {
		return fmt.Errorf("read task: %w", err)
	}
	ch := &child{claim: c, head: fields["head"], kind: fields["kind"], exited: make(chan struct{})}
	if ch.kind == "" {
		ch.kind = c.Kind
	}
	ch.dir = filepath.Join(s.cfg.Dir, c.Sprint, c.ID+"-"+strconv.Itoa(c.Attempt))
	if err := os.MkdirAll(ch.dir, 0o755); err != nil {
		return err
	}
	ch.brief = filepath.Join(ch.dir, "brief.md")
	ch.out = filepath.Join(ch.dir, "out.log")
	if err := s.writeBrief(ctx, ch, fields); err != nil {
		return err
	}
	argv := make([]string, len(s.cfg.Dispatch))
	replacer := strings.NewReplacer(TokenBrief, ch.brief, TokenOut, ch.out, TokenDir, ch.dir,
		TokenSprint, c.Sprint, TokenID, c.ID)
	for i, a := range s.cfg.Dispatch {
		argv[i] = replacer.Replace(a)
	}
	// The refusing gh goes first on the child's PATH (nova-tools #3600): a
	// friend child reaches GitHub through nova-sprint verbs and git only. A
	// shim that cannot be written starts no child.
	shimDir := filepath.Join(s.cfg.Dir, ShimDirName)
	if shim, err := nogh.Install(shimDir); err != nil {
		return err
	} else if shim == "" {
		shimDir = "" // no shim on this OS: PATH stays as it is
	}
	outFile, err := os.Create(ch.out)
	if err != nil {
		return err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = ch.dir
	cmd.Stdout, cmd.Stderr = outFile, outFile
	cmd.Env = append(os.Environ(),
		ServeEnvFriend+"="+s.cfg.Friend, ServeEnvSprint+"="+c.Sprint, ServeEnvID+"="+c.ID,
		ServeEnvAttempt+"="+strconv.Itoa(c.Attempt), ServeEnvToken+"="+c.Token,
		ServeEnvKind+"="+ch.kind, ServeEnvHead+"="+ch.head, ServeEnvBrief+"="+ch.brief,
		ServeEnvOut+"="+ch.out, ServeEnvDir+"="+ch.dir)
	cmd.Env = append(cmd.Env, s.cfg.Env...)
	cmd.Env = nogh.PathFirst(cmd.Env, shimDir)
	ownGroup(cmd)
	if err := cmd.Start(); err != nil {
		_ = outFile.Close()
		return fmt.Errorf("start %s: %w", argv[0], err)
	}
	ch.cmd = cmd
	ch.started = s.now()
	go func() {
		ch.waitErr = cmd.Wait()
		_ = outFile.Close()
		close(ch.exited)
	}()
	s.mu.Lock()
	s.children[ch.key()] = ch
	s.mu.Unlock()
	s.receipt(ctx, "start", &c, fmt.Sprintf("pid=%d brief=%s argv=%s", cmd.Process.Pid, ch.briefSrc, argv[0]))
	return nil
}

// writeBrief renders the task's brief through the one producer (brief.Render)
// and, when render refuses the record (a kind without a template, a title
// without PATHS), gives a read task the read brief from Redis and the mirror
// (read.BriefTask: the PR record, lines and CI, and the diff saved beside the
// brief, zero GitHub calls, nova-tools#3599); anything else, or a read the
// mirror cannot brief yet, gets the record itself so the child is still
// pointed at the work; the receipt says which.
func (s *Server) writeBrief(ctx context.Context, ch *child, fields map[string]string) error {
	var stdout, stderr bytes.Buffer
	if code := brief.Render(ctx, s.st, ch.claim.Sprint+"/"+ch.claim.ID, ch.brief, &stdout, &stderr); code == 0 {
		ch.briefSrc = "render"
		return nil
	} else if code == 2 {
		return fmt.Errorf("brief render: %s", strings.TrimSpace(stderr.String()))
	}
	if isReview(ch.kind) && filepath.Base(ch.brief) == "brief.md" {
		var rout, rerr bytes.Buffer
		rf := fields
		if rf["kind"] == "" {
			rf = make(map[string]string, len(fields)+1)
			for k, v := range fields {
				rf[k] = v
			}
			rf["kind"] = ch.kind
		}
		if read.BriefTask(ctx, s.st.Client(), ch.claim.ID, rf, s.cfg.Mirror, filepath.Dir(ch.brief), &rout, &rerr) == 0 {
			ch.briefSrc = "read"
			return nil
		}
		stderr.Reset()
		stderr.WriteString(strings.TrimSpace(rerr.String()))
	}
	ch.briefSrc = "record"
	var b strings.Builder
	fmt.Fprintf(&b, "TASK: %s/%s\n", ch.claim.Sprint, ch.claim.ID)
	fmt.Fprintf(&b, "BRIEF: record (%s)\n\n", strings.TrimSpace(strings.SplitN(stderr.String(), "\n", 2)[0]))
	keys := make([]string, 0, len(fields))
	for k := range fields {
		if k == "token" || k == "token_sha" || k == "payload_sha" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if fields[k] != "" {
			fmt.Fprintf(&b, "%s: %s\n", k, fields[k])
		}
	}
	b.WriteString("\nWrite one typed line on stdout when you are done: SCORE who=<you> head=<sha> score=<n>/10 for a read, DONE <what> for a build, BLOCKED <why> when you cannot.\n")
	return os.WriteFile(ch.brief, []byte(b.String()), 0o644)
}

// beatLeases beats every child's task lease that is due, in one call
// (task.BeatMany, #3915): working is exactly the children alive.
func (s *Server) beatLeases(ctx context.Context) {
	s.mu.Lock()
	due := make([]*child, 0, len(s.children))
	for _, c := range s.children {
		if s.now().Sub(c.lastBeat) >= TaskBeatEvery-BeatInterval {
			due = append(due, c)
		}
	}
	s.mu.Unlock()
	if len(due) == 0 {
		return
	}
	sort.Slice(due, func(i, j int) bool { return due[i].key() < due[j].key() })
	reqs := make([]task.BeatRequest, len(due))
	for i, c := range due {
		reqs[i] = task.BeatRequest{Sprint: c.claim.Sprint, ID: c.claim.ID, Token: c.claim.Token, Actor: s.cfg.Actor}
	}
	statuses, err := task.BeatMany(ctx, s.st, reqs)
	now := s.now()
	for _, c := range due {
		c.lastBeat = now
	}
	if err != nil {
		fmt.Fprintf(s.cfg.Out, "SERVE %s beat n=%d: %v\n", s.cfg.Friend, len(due), err)
		return
	}
	for i, c := range due {
		s.beatStatus(ctx, c, statuses[i])
	}
}

// beatStatus acts on one child's beat: a fenced lease stops the child.
func (s *Server) beatStatus(ctx context.Context, c *child, status task.BeatStatus) {
	if status == task.BeatFenced {
		// The lease moved under us (the reconciler superseded the attempt);
		// the child's work no longer has a task. Stop it.
		s.receipt(ctx, "fenced", &c.claim, "")
		killGroup(c.cmd)
	}
}

// reap closes every child that has exited.
func (s *Server) reap(ctx context.Context) int {
	s.mu.Lock()
	var exited []*child
	for _, c := range s.children {
		select {
		case <-c.exited:
			exited = append(exited, c)
		default:
		}
	}
	for _, c := range exited {
		delete(s.children, c.key())
	}
	s.mu.Unlock()
	sort.Slice(exited, func(i, j int) bool { return exited[i].key() < exited[j].key() })
	for _, c := range exited {
		s.close(ctx, c)
	}
	return len(exited)
}

// close runs done for one exited child: the typed line it wrote when it exited
// 0 and wrote one, `blocked: <reason>` otherwise.
func (s *Server) close(ctx context.Context, c *child) {
	rc, reason := exitReason(c.cmd, c.waitErr)
	output := readTail(c.out)
	line := TypedLine(output)
	secs := int(s.now().Sub(c.started).Seconds())
	s.receipt(ctx, "exit", &c.claim, fmt.Sprintf("rc=%d secs=%d typed=%t", rc, secs, line != ""))
	if rc == 0 && line != "" {
		verdict, score := "", ""
		if isReview(c.kind) {
			verdict, score = VerdictOf(line)
		}
		s.done(ctx, c.claim, c.kind, c.head, line, verdict, score)
		return
	}
	if line == "" {
		reason += "; no typed line"
		if last := lastLine(output); last != "" {
			reason += ": " + last
		}
	} else {
		reason += "; wrote " + line
	}
	s.closeBlocked(ctx, c.claim, c.kind, c.head, reason)
}

// closeBlocked closes a task with blocked: evidence. A review closes with
// verdict BLOCKED and score 0, which no read counter reads as a read.
func (s *Server) closeBlocked(ctx context.Context, c task.Claim, kind, head, reason string) {
	if kind == "" {
		kind = c.Kind
	}
	verdict, score := "", ""
	if isReview(kind) {
		verdict, score = "BLOCKED", "0"
	}
	s.done(ctx, c, kind, head, "blocked: "+oneLine(reason), verdict, score)
}

func (s *Server) done(ctx context.Context, c task.Claim, kind, head, evidence, verdict, score string) {
	req := task.DoneRequest{Sprint: c.Sprint, ID: c.ID, Token: c.Token, Evidence: evidence, Actor: s.cfg.Actor}
	if isReview(kind) {
		req.Verdict, req.Score, req.Head = verdict, score, head
	}
	res, err := task.DoneTyped(ctx, s.st, req)
	if err != nil {
		s.receipt(ctx, "done", &c, "error="+oneLine(err.Error()))
		fmt.Fprintf(s.cfg.Out, "SERVE %s done %s/%s: %v\n", s.cfg.Friend, c.Sprint, c.ID, err)
		return
	}
	detail := "status=" + string(res.Status)
	if res.Why != "" {
		detail += " why=" + res.Why
	}
	s.receipt(ctx, "done", &c, detail+" evidence="+evidence)
}

// receipt appends one entry to friend:<f>:log and prints its line.
func (s *Server) receipt(ctx context.Context, kind string, c *task.Claim, detail string) {
	at := strconv.FormatInt(s.now().UnixMilli(), 10)
	values := []any{"kind", kind, "friend", s.cfg.Friend, "session", s.cfg.Session, "at", at}
	line := "SERVE " + s.cfg.Friend + " " + kind
	if c != nil {
		values = append(values, "sprint", c.Sprint, "id", c.ID, "attempt", strconv.Itoa(c.Attempt))
		line += " " + c.Sprint + "/" + c.ID + " attempt=" + strconv.Itoa(c.Attempt)
	}
	if detail != "" {
		values = append(values, "detail", detail)
		line += " " + detail
	}
	err := s.st.Client().XAdd(ctx, &redis.XAddArgs{
		Stream: LogKey(s.cfg.Friend), MaxLen: LogMaxLen, Approx: true, Values: values,
	}).Err()
	if err != nil {
		line += " (log: " + oneLine(err.Error()) + ")"
	}
	fmt.Fprintln(s.cfg.Out, line)
}

// LogKey is the serve receipt stream of a friend.
func LogKey(friend string) string { return "friend:" + friend + ":log" }

// LockKey is the seat lock of a friend.
func LockKey(friend string) string { return "friend:" + friend + ":serve" }

// typedWords are the first words of the lines a child may end on. The
// contract's own words (DONE, ABSTAIN, BLOCKED and DISPOSITION) come from
// typedrec, the one typed parser (#2506), so they have one source; the rest
// are the review and spec words that are not RESULT or DISPOSITION records.
var typedWords = func() map[string]bool {
	w := map[string]bool{
		"SCORE": true, "HOLD": true, "REPAIR": true, "SPEC": true, "SPEC-WRITTEN": true, "CLOSE": true,
	}
	for _, s := range typedrec.Contract.StatusWords() {
		w[s] = true
	}
	return w
}()

// TypedLine is the last line of output whose first word is a typed word, or
// "" when the child wrote none. A harness's own trailer (a PASS, a cost
// line) after the typed line does not hide it.
func TypedLine(output string) string {
	lines := strings.Split(output, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		f := strings.Fields(l)
		if len(f) > 0 && typedWords[f[0]] {
			return l
		}
	}
	return ""
}

// VerdictOf reads a review's typed line into ns_task_done's verdict and
// score: SCORE ... score=N[/10] is APPROVE N; a DISPOSITION line goes through
// typedrec.ParseDisposition, the one typed parser, and carries its verdict=
// with score=; HOLD is HOLD with its score or 0; any other typed word
// (including a DISPOSITION that names no verdict) is itself with score 0.
// Whether an APPROVE counts toward landing is the lander's reading
// (land/stream ReadAt), not this one.
func VerdictOf(line string) (verdict, score string) {
	f := strings.Fields(line)
	if len(f) == 0 {
		return "", ""
	}
	kv := map[string]string{}
	for _, w := range f[1:] {
		if k, v, ok := strings.Cut(w, "="); ok {
			kv[strings.ToLower(k)] = strings.TrimRight(v, ":,;")
		}
	}
	score = strings.TrimSuffix(kv["score"], "/10")
	if _, err := strconv.Atoi(score); err != nil {
		score = "0"
	}
	if c, ok := typedrec.ParseDisposition(line); ok {
		return c.Verdict, score
	}
	switch f[0] {
	case "SCORE":
		return "APPROVE", score
	case "HOLD":
		return "HOLD", score
	default:
		return f[0], score
	}
}

func isReview(kind string) bool { return kind == "read" || kind == "review" }

// exitReason is the child's exit as one phrase.
func exitReason(cmd *exec.Cmd, waitErr error) (int, string) {
	if cmd == nil || cmd.ProcessState == nil {
		if waitErr != nil {
			return -1, "exit=? " + oneLine(waitErr.Error())
		}
		return -1, "exit=?"
	}
	rc := cmd.ProcessState.ExitCode()
	if rc == -1 {
		return -1, "exit=signal " + oneLine(cmd.ProcessState.String())
	}
	return rc, "exit=" + strconv.Itoa(rc)
}

// readTail reads the last outTail bytes of a file, or "" when it cannot.
func readTail(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return ""
	}
	off := info.Size() - outTail
	if off < 0 {
		off = 0
	}
	b, err := io.ReadAll(io.NewSectionReader(f, off, outTail))
	if err != nil {
		return ""
	}
	return string(b)
}

func lastLine(output string) string {
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			if len(l) > 200 {
				l = l[:200]
			}
			return l
		}
	}
	return ""
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
