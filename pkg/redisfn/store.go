package redisfn

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// State is what Check found on a store.
type State int

const (
	// Unknown is the state when there is no answer: the store could not be
	// read, or the library was refused. It comes with the error that says why.
	Unknown State = iota
	// Same: the store holds a library of this name whose code is the source,
	// byte for byte.
	Same
	// Different: the store holds a library of this name with other code.
	Different
	// Absent: the store holds no library of this name.
	Absent
)

// MismatchError is Check's error for Different and Absent: the store does not
// hold the library this binary was built with.
type MismatchError struct {
	Library string // the library's name
	Want    string // the digest of this binary's library
	Loaded  string // the digest of the code the store holds; "" when it holds none
	Remedy  string // what to do about it
}

// Error is one line that names the library, both digests (loaded=none when
// the store holds none) and the remedy.
func (e *MismatchError) Error() string {
	if e.Loaded == "" {
		return fmt.Sprintf("redisfn: library %s is absent from the store: loaded=none want=%s; remedy: %s",
			oneline.Field(e.Library), oneline.Field(e.Want), oneline.Escape(e.Remedy))
	}
	return fmt.Sprintf("redisfn: library %s on the store is not the one this binary was built with: loaded=%s want=%s; remedy: %s",
		oneline.Field(e.Library), oneline.Field(e.Loaded), oneline.Field(e.Want), oneline.Escape(e.Remedy))
}

// Held is one function name and the library that registers it on the store.
type Held struct {
	Function string // the name, as the store's error or this library spells it
	Holder   string // the library on the store that registers it; "" when none was found
}

// CollisionError is the load's error when the store refused the library because
// another library on it registers one of this library's function names. A
// function name belongs to one library, compared without case, so while two
// libraries carry the same function one of them cannot be loaded.
type CollisionError struct {
	// Library is the library the store refused.
	Library string
	// Held is the function the store named, and with it every function name
	// read from this library's files that another library registers, sorted
	// by name. It is never empty.
	Held []Held
	// Unread is why the store's libraries could not be listed to find the
	// holders, and nil when they were. When it is set, Held is the one
	// function the store named, with no holder.
	Unread error
}

// Error is one line that names each function with the library that holds it,
// and the remedy.
func (e *CollisionError) Error() string {
	var held []string
	for _, h := range e.Held {
		switch {
		case h.Holder != "":
			held = append(held, fmt.Sprintf("%s is registered by library %s", oneline.Field(h.Function), oneline.Field(h.Holder)))
		case e.Unread != nil:
			held = append(held, fmt.Sprintf("%s is registered by another library (which one could not be read: %s)", oneline.Field(h.Function), oneline.Err(e.Unread)))
		default:
			held = append(held, fmt.Sprintf("%s was registered by another library when the store refused, and by none a moment later", oneline.Field(h.Function)))
		}
	}
	library := oneline.Field(e.Library)
	return fmt.Sprintf("redisfn: load %s: the store refused it and holds what it held before: function %s; remedy: a function name belongs to one library: load the version of the other library that no longer registers it, then load %s again",
		library, strings.Join(held, ", function "), library)
}

// Unwrap is Unread, so errors.Is sees why the holders could not be read.
func (e *CollisionError) Unwrap() error { return e.Unread }

// Outcome is what Ensure or LoadMissing did to the store.
type Outcome int

const (
	// Failed is the outcome beside an error: what the store holds is not
	// known from this call.
	Failed Outcome = iota
	// Unchanged: nothing was written, because the store held the library
	// already. For Ensure that is this code; for LoadMissing it is a library
	// of this name, whatever its code, and it may have been put there by
	// another loader between LoadMissing's read and its load.
	Unchanged
	// Loaded: the store held no library of this name and holds this one now.
	Loaded
	// Replaced (Ensure only): the store held other code under this name and
	// holds this library now.
	Replaced
	// Skipped (LoadMissing only): nothing was written, and the store may not
	// hold the library, because the store refused this caller the read of
	// its libraries (NOPERM: the caller is not the deployer) or refused the
	// load for a function name another library holds. Receipt.Why is the
	// store's reply. The caller's FCALL answers for the store.
	Skipped
)

// String is the outcome in one upper-case word, the first word of a
// Receipt's line: FAILED, UNCHANGED, LOADED, REPLACED, SKIPPED.
func (o Outcome) String() string {
	switch o {
	case Unchanged:
		return "UNCHANGED"
	case Loaded:
		return "LOADED"
	case Replaced:
		return "REPLACED"
	case Skipped:
		return "SKIPPED"
	}
	return "FAILED"
}

// Receipt is what one Ensure or LoadMissing did.
type Receipt struct {
	Library string  // the library's name
	Outcome Outcome // what was done
	Digest  string  // the digest of this binary's library
	Was     string  // the digest of the code the store held before; "" when it held none or was not read (LoadMissing never reads it)
	Why     string  // the store's reply when the outcome is Skipped; "" otherwise
}

// String is the receipt on one line, "LOADED my_library sha=<digest>", with
// was=<digest> after it when code was replaced and why="<the store's reply>"
// when the load was skipped.
func (r Receipt) String() string {
	line := r.Outcome.String() + " " + oneline.Field(r.Library) + " sha=" + r.Digest
	switch r.Outcome {
	case Replaced:
		line += " was=" + r.Was
	case Skipped:
		line += " why=" + oneline.Quote(r.Why)
	}
	return line
}

// Query is the FUNCTION LIST that reads this library's code, the command
// Check and Ensure send. The store matches the name as a pattern and without
// case, so the reply may hold other libraries as well; judge takes this one
// by its exact name.
func (l Library) Query() redis.FunctionListQuery {
	return redis.FunctionListQuery{LibraryNamePattern: l.Name, WithCode: true}
}

// judge is the verdict on a FUNCTION LIST reply, and the digest of the code
// the store holds ("" for none). The state is Same only when the reply holds a
// library of exactly this name whose code is this binary's source byte for
// byte; the digests are for the error's line and decide nothing. The error is
// nil for Same, a *MismatchError for Different and Absent.
func (b *built) judge(reply []redis.Library) (State, string, error) {
	for _, lib := range reply {
		if lib.Name != b.name {
			continue
		}
		if lib.Code == b.source {
			return Same, b.digest, nil
		}
		loaded := DigestOf(lib.Code)
		remedy := b.remedy
		if remedy == "" {
			remedy = "load this binary's library over it (Ensure), or run the binary the store's library came from"
		}
		return Different, loaded, &MismatchError{Library: b.name, Want: b.digest, Loaded: loaded, Remedy: remedy}
	}
	remedy := b.remedy
	if remedy == "" {
		remedy = "load this binary's library (Ensure)"
	}
	return Absent, "", &MismatchError{Library: b.name, Want: b.digest, Remedy: remedy}
}

// Check answers whether the library on the store is the one this binary was
// built with, and changes nothing. It sends one FUNCTION LIST (Query) and
// judges the reply (judge): Same with a nil error, Different or Absent with a
// *MismatchError whose one line names the library, both digests and the
// remedy, Unknown with the error of a store that could not be read or of a
// library that the source refuses.
//
// The identity it reads is the library's code as the store holds it. The
// store keeps that code with the functions it compiled from it, and changes
// the two together or not at all, so nothing can change what Check reads and
// leave the library as it was, and nothing can change the library and leave
// what Check reads. The answer is true of the moment the store replied;
// another loader may act after it.
//
// It returns within the Bound, whatever the client's own timeouts are, and a
// context that has ended already sends nothing.
func (l Library) Check(ctx context.Context, client redis.UniversalClient) (State, error) {
	b, err := l.ready(client)
	if err != nil {
		return Unknown, err
	}
	reply, err := within(ctx, l.Bound, func(ctx context.Context) ([]redis.Library, error) {
		return client.FunctionList(ctx, l.Query()).Result()
	})
	if err != nil {
		return Unknown, b.failed("check", err)
	}
	state, _, err := b.judge(reply)
	return state, err
}

// Ensure is the deployer's load: it puts this library on the store unless
// the store holds exactly this code, replacing other code under the name. It
// reads the store as Check does, and when the state is Absent or Different it
// loads with one FUNCTION LOAD REPLACE. The receipt says which happened:
// Unchanged (the store held this code; nothing was written), Loaded (the name
// was free) or Replaced (other code was under the name, whose digest is Was).
// Beside an error the outcome is Failed, and the error is Check's for the
// read or the load's for the load.
//
// The load is one command, FUNCTION LOAD REPLACE, and the store never holds
// half of the library, which is Redis's doing, not this package's: inside
// that one command it makes the new library of the whole text and, when
// anything fails (a syntax error, a Lua error when the library's own text
// runs, a function name another library holds, a library that registers
// nothing), has the library it held before in place again when it replies. No
// other client's command runs in between, so no client sees a store between
// the two; after a load, error or not, the store holds the whole library it
// held before or the whole of this one (the functional tests hold each of
// those refusals against a redis-server; tla/RedisFn.tla, NoGap, shows what
// FUNCTION DELETE followed by FUNCTION LOAD would lose). A nil outcome says
// the store answered the load with this library's name; a reply of the store
// itself (errors.As finds a redis.Error) says the store holds what it held
// before; any other error, a lost connection or the Bound passing, says
// neither: the command may have run, whole, after the caller stopped waiting,
// and Check says which. When the store refuses the library because another
// library registers one of its function names, the error is a
// *CollisionError that names the function and the library that holds it, and
// an error whose text names a line of the library carries the line's file in
// the note after it.
//
// The read and the load are two commands, and another loader may act between
// them; the load replaces whatever is there by then, so a receipt that says
// Loaded may have replaced a library loaded in that moment. Two binaries
// built with different libraries that both call Ensure on one store replace
// each other's library for as long as both run (tla/RedisFn.tla,
// MCRedisFnTwoDeployers). Call it from the one place that deploys; a tool on
// its way to an FCALL calls LoadMissing, which never replaces, or Check.
//
// It returns within the Bound, the read and the load together, whatever the
// client's own timeouts are, and a context that has ended already sends
// nothing.
func (l Library) Ensure(ctx context.Context, client redis.UniversalClient) (Receipt, error) {
	b, err := l.ready(client)
	if err != nil {
		return Receipt{Library: l.Name}, err
	}
	receipt, err := within(ctx, l.Bound, func(ctx context.Context) (Receipt, error) {
		r := Receipt{Library: b.name, Digest: b.digest}
		reply, err := client.FunctionList(ctx, l.Query()).Result()
		if err != nil {
			return r, b.failed("check", err)
		}
		state, was, _ := b.judge(reply)
		if state == Same {
			r.Outcome = Unchanged
			return r, nil
		}
		r.Was = was
		if err := b.load(ctx, client); err != nil {
			return r, b.failed("load", err)
		}
		r.Outcome = Loaded
		if state == Different {
			r.Outcome = Replaced
		}
		return r, nil
	})
	if err != nil {
		// The call that was left behind keeps its own receipt; this one is
		// made here of what came back, which is nothing when the wait ended.
		return Receipt{Library: b.name, Digest: b.digest, Was: receipt.Was}, b.failed("load", err)
	}
	return receipt, nil
}

// LoadMissing puts the library on the store only when the store holds no
// library of its name, and never replaces one, whatever its code. It is for
// every caller that is not the deployer: a tool on its way to an FCALL runs
// whatever binary its host has, and an older binary can replace the
// deployed library and remove every function added since.
// Putting a newer build over an older one is Ensure's job, in the one place
// that deploys.
//
// It sends FUNCTION LIST for the name (without the code) and, only when no
// library of exactly this name is there, FUNCTION LOAD, without REPLACE, so
// a library another loader puts there between the two is refused by the
// store and left as it is. The receipt says what happened:
//
//   - Unchanged: a library of this name was on the store, at the read or at
//     the load; nothing was written and its code was not read;
//   - Loaded: the name was free and this library is on the store now;
//   - Skipped: the store refused the read with NOPERM (the caller is not the
//     deployer), or refused the load because another library holds one of
//     this library's function names; nothing was written, Why is the store's
//     reply, and the caller's FCALL answers for the store.
//
// Each of those comes with a nil error. Any other failure is an error with
// the outcome Failed: a store that could not be reached or read, or a load
// the store refused for the library itself (a syntax error, a name
// registered twice), after which the store holds what it held before.
//
// It returns within the Bound, the read and the load together, whatever the
// client's own timeouts are, and a context that has ended already sends
// nothing.
func (l Library) LoadMissing(ctx context.Context, client redis.UniversalClient) (Receipt, error) {
	b, err := l.ready(client)
	if err != nil {
		return Receipt{Library: l.Name}, err
	}
	receipt, err := within(ctx, l.Bound, func(ctx context.Context) (Receipt, error) {
		r := Receipt{Library: b.name, Digest: b.digest}
		reply, err := client.FunctionList(ctx, redis.FunctionListQuery{LibraryNamePattern: b.name}).Result()
		if err != nil {
			if isReply(err) && strings.Contains(err.Error(), "NOPERM") {
				r.Outcome, r.Why = Skipped, err.Error()
				return r, nil
			}
			return r, b.failed("check", err)
		}
		for _, lib := range reply {
			if lib.Name == b.name {
				r.Outcome = Unchanged
				return r, nil
			}
		}
		err = b.put(ctx, client, false)
		switch {
		case err == nil:
			r.Outcome = Loaded
			return r, nil
		case isReply(err) && libraryExists.MatchString(err.Error()):
			r.Outcome = Unchanged // another loader put a library of this name there after the read
			return r, nil
		case isReply(err) && named.MatchString(err.Error()):
			r.Outcome, r.Why = Skipped, err.Error()
			return r, nil
		}
		return r, b.failed("load", err)
	})
	if err != nil {
		return Receipt{Library: b.name, Digest: b.digest}, b.failed("load", err)
	}
	return receipt, nil
}

// isReply is true when err is a reply of the store itself, not a failure to
// reach it.
func isReply(err error) bool {
	var reply redis.Error
	return errors.As(err, &reply)
}

// ready is the library assembled and a client to send it with, or the reason
// there is neither; nothing has been sent.
func (l Library) ready(client redis.UniversalClient) (*built, error) {
	b, err := l.build()
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, fmt.Errorf("redisfn: library %s: no client to reach a store with", l.Name)
	}
	return b, nil
}

// named is the function name in the store's refusal of a library that
// registers a name another library holds.
var named = regexp.MustCompile(`Function (\S+) already exists$`)

// libraryExists is the store's refusal of a FUNCTION LOAD, without REPLACE,
// of a library whose name it holds.
var libraryExists = regexp.MustCompile(`Library '[^']*' already exists$`)

// put sends the library, with FUNCTION LOAD REPLACE when replace is true and
// FUNCTION LOAD when it is not, and reads the store's answer.
func (b *built) put(ctx context.Context, client redis.UniversalClient, replace bool) error {
	var answer string
	var err error
	if replace {
		answer, err = client.FunctionLoadReplace(ctx, b.source).Result()
	} else {
		answer, err = client.FunctionLoad(ctx, b.source).Result()
	}
	if err != nil {
		return err
	}
	if answer != b.name {
		return &failure{line: fmt.Sprintf("redisfn: load %s: the store answered %s and not the library's name, so nothing says the library was loaded; Check says what the store holds",
			b.name, oneline.Quote(answer))}
	}
	return nil
}

// load sends the library with FUNCTION LOAD REPLACE and names the holder of
// a function the store refuses it for.
func (b *built) load(ctx context.Context, client redis.UniversalClient) error {
	err := b.put(ctx, client, true)
	if err == nil {
		return nil
	}
	if m := named.FindStringSubmatch(err.Error()); m != nil && isReply(err) {
		return b.collision(ctx, client, m[1])
	}
	return err
}

// collision names the holder of the function the store refused the library
// for, and of every other function name of the library that another library
// on the store registers.
func (b *built) collision(ctx context.Context, client redis.UniversalClient, function string) error {
	e := &CollisionError{Library: b.name}
	reply, err := client.FunctionList(ctx, redis.FunctionListQuery{}).Result()
	if err != nil {
		e.Held, e.Unread = []Held{{Function: function}}, err
		return e
	}
	ours := map[string]string{strings.ToLower(function): function}
	for key, name := range b.names {
		if _, ok := ours[key]; !ok {
			ours[key] = name
		}
	}
	found := map[string]bool{}
	for _, lib := range reply {
		if lib.Name == b.name {
			continue
		}
		for _, fn := range lib.Functions {
			key := strings.ToLower(fn.Name)
			if name, ok := ours[key]; ok && !found[key] {
				found[key] = true
				e.Held = append(e.Held, Held{Function: name, Holder: lib.Name})
			}
		}
	}
	if !found[strings.ToLower(function)] {
		e.Held = append(e.Held, Held{Function: function})
	}
	sort.Slice(e.Held, func(i, j int) bool { return e.Held[i].Function < e.Held[j].Function })
	return e
}

// failed is the error of one call, on one line: what was being done to which
// library, the cause, the origin of any line the cause names, and what the
// store holds after it. An error that is already this package's is returned
// as it is.
func (b *built) failed(doing string, err error) error {
	var mine *failure
	var collision *CollisionError
	if errors.As(err, &mine) || errors.As(err, &collision) {
		return err
	}
	var early *unsent
	if errors.As(err, &early) {
		return &failure{fmt.Sprintf("redisfn: %s %s: the caller's wait had ended before anything was sent: %s; nothing was sent, so the store holds what it held",
			doing, b.name, oneline.Err(early.err)), err}
	}
	refused := isReply(err)
	what := "the store did not answer"
	switch {
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled):
		what = "no answer from the store before the wait ended"
	case refused:
		what = "the store refused"
	}
	holds := "the store holds the whole library it held before or the whole of this one, and Check says which"
	switch {
	case doing == "check":
		holds = "nothing was changed"
	case refused:
		holds = "the store holds what it held before"
	}
	text := oneline.Err(err)
	if note := b.explain(err.Error()); note != "" {
		text += " " + note
	}
	return &failure{fmt.Sprintf("redisfn: %s %s: %s: %s; %s", doing, b.name, what, text, holds), err}
}

// failure is an error of this package, on one line, that wraps its cause
// when it has one.
type failure struct {
	line string
	err  error
}

func (f *failure) Error() string { return f.line }

func (f *failure) Unwrap() error { return f.err }

// unsent is within's error when the caller's context had ended before the
// call was made: nothing was sent. It wraps the context's error.
type unsent struct{ err error }

func (u *unsent) Error() string { return u.err.Error() }

func (u *unsent) Unwrap() error { return u.err }

// within runs one call against the store and returns when it answers or when
// the bound passes (or ctx ends), whichever is first; when ctx has ended
// already, the call is not made. The client's own timeouts do not decide the
// wait: go-redis does not put a context's deadline on its reads unless the
// client was built to, so a call that is still waiting is left behind with
// its context cancelled, and ends by the client's timeouts or with its
// connection.
func within[T any](ctx context.Context, bound time.Duration, call func(context.Context) (T, error)) (T, error) {
	if err := ctx.Err(); err != nil {
		var none T
		return none, &unsent{err} // the caller's wait has ended already: nothing is sent
	}
	if bound <= 0 {
		bound = DefaultBound
	}
	ctx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	type answer struct {
		value T
		err   error
	}
	done := make(chan answer, 1)
	go func() {
		value, err := call(ctx)
		done <- answer{value, err}
	}()
	select {
	case a := <-done:
		return a.value, a.err
	case <-ctx.Done():
		var none T
		return none, ctx.Err()
	}
}
