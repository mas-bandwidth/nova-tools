package fn

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests reach the loader's store paths at the unit tier, where no
// redis-server may be started (STANDARD section 8): the store is fnStoreFake,
// a go-redis hook that answers FUNCTION LIST, FUNCTION LOAD and FCALL ns_ping
// from its fields and records what was sent, so the client dials nothing.
// Nothing in the package's helpers or in testredis answers those commands
// (OnlyFCALL judges a command and RoundTrips counts one; neither replies), so
// this file holds its own fake.

// fnStoreFake is the store behind a loader test's client: the reply each of
// the loader's three commands gets, and the record of what the loader sent.
type fnStoreFake struct {
	libs    []redis.Library // the FUNCTION LIST reply, when listErr is nil
	listErr error           // the FUNCTION LIST error, else the reply answers
	loadErr error           // the FUNCTION LOAD error, else the load lands
	ping    any             // the FCALL ns_ping reply, when pingErr is nil
	pingErr error           // the FCALL ns_ping error, else the reply answers
	loads   []string        // the code of every FUNCTION LOAD sent
	pattern string          // the libraryname pattern of the last FUNCTION LIST
	pings   int             // the FCALL ns_ping sends
}

// DialHook fails a dial: the fake answers every command, so a connection is a
// test failure, not a wait.
func (s *fnStoreFake) DialHook(redis.DialHook) redis.DialHook {
	return func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("the fake store is not dialed")
	}
}

// ProcessPipelineHook passes the batch on: the loader sends no pipeline.
func (s *fnStoreFake) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

// ProcessHook answers the three commands the loader sends and passes nothing
// on, so no command reaches a connection.
func (s *fnStoreFake) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		args := cmd.Args()
		switch {
		case len(args) > 2 && args[0] == "function" && args[1] == "list":
			list := cmd.(*redis.FunctionListCmd)
			if pattern, ok := args[3].(string); ok {
				s.pattern = pattern
			}
			if s.listErr != nil {
				list.SetErr(s.listErr)
				return s.listErr
			}
			list.SetVal(s.libs)
			return nil
		case len(args) > 2 && args[0] == "function" && args[1] == "load":
			load := cmd.(*redis.StringCmd)
			s.loads = append(s.loads, args[len(args)-1].(string))
			if s.loadErr != nil {
				load.SetErr(s.loadErr)
				return s.loadErr
			}
			load.SetVal(Library)
			return nil
		case len(args) > 0 && args[0] == "fcall":
			call := cmd.(*redis.Cmd)
			s.pings++
			if s.pingErr != nil {
				call.SetErr(s.pingErr)
				return s.pingErr
			}
			call.SetVal(s.ping)
			return nil
		}
		return next(ctx, cmd)
	}
}

// fakeClient is the client a loader test hands the code: every command goes
// to the fake and none is dialled.
func fakeClient(t *testing.T, store *fnStoreFake) *redis.Client {
	t.Helper()
	c := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0", MaxRetries: -1})
	c.AddHook(store)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// TestLoaderCoverSum: Sum names a source by the first 16 hex digits of its
// SHA-256, and two sources that differ name two Sums.
func TestLoaderCoverSum(t *testing.T) {
	t.Parallel()

	h := sha256.Sum256([]byte("code"))
	digest := hex.EncodeToString(h[:])
	assert.Equal(t, digest[:16], Sum("code"), "Sum is not the first 16 hex digits of the source's SHA-256")
	assert.NotEqual(t, Sum("code"), Sum("code 2"), "two sources that differ name one Sum")
}

// TestLoaderCoverStateOK: OK holds only when the state is not missing, the
// loaded Sum is the wanted one and the ping answered PONG; each of the three
// refusals alone turns it false.
func TestLoaderCoverStateOK(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		st   State
		want bool
	}{
		{"the loaded library that answers PONG", State{Want: "a", Loaded: "a", Ping: "PONG"}, true},
		{"a missing library", State{Want: "a", Loaded: "a", Ping: "PONG", Missing: true}, false},
		{"a stale library", State{Want: "a", Loaded: "b", Ping: "PONG"}, false},
		{"a skipped ping", State{Want: "a", Loaded: "a", Ping: PingSkipped}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.st.OK(), "State %+v", tt.st)
		})
	}
}

// TestLoaderCoverJudge: judge names a state ours only when the store holds
// the embedded source byte for byte; a store that holds none is marked
// Missing and one that holds another body is marked by its own Sum, and both
// skip the FCALL the caller would run on its own library.
func TestLoaderCoverJudge(t *testing.T) {
	t.Parallel()

	const source = "code"
	tests := []struct {
		name      string
		code      string
		found     bool
		wantState State
		wantOurs  bool
	}{
		{"the embedded source is ours", source, true, State{Want: Sum(source), Loaded: Sum(source)}, true},
		{"no library is missing and skipped", "", false, State{Want: Sum(source), Missing: true, Ping: PingSkipped}, false},
		{"another body is skipped", "other", true, State{Want: Sum(source), Loaded: Sum("other"), Ping: PingSkipped}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, ours := judge(source, tt.code, tt.found)
			assert.Equal(t, tt.wantState, st)
			assert.Equal(t, tt.wantOurs, ours, "judge(source, %q, %v)", tt.code, tt.found)
		})
	}
}

// TestLoaderCoverLoad: Load sends the embedded source in one FUNCTION LOAD
// REPLACE, and a load the store refuses is named with the library's own line.
func TestLoaderCoverLoad(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	source, err := Source()
	require.NoError(t, err, err)
	refused := errors.New("ERR library refused")
	tests := []struct {
		name    string
		loadErr error
		wantErr string
	}{
		{"the embedded source loads", nil, ""},
		{"a refused load is named", refused, "load nova_sprint function library: " + refused.Error()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fnStoreFake{loadErr: tt.loadErr}
			err := Load(ctx, fakeClient(t, store))
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tt.wantErr)
			}
			assert.Equal(t, []string{source}, store.loads, "Load sends the embedded source, byte for byte")
		})
	}
}

// TestLoaderCoverLoadMissing: LoadMissing loads an empty store once, leaves a
// library the store holds exactly as it is, and answers nil for the two
// refusals the record holds it to: a seat whose ACL refuses FUNCTION LIST,
// and a load another caller won between the list and the load. Any other list
// or load error is named with the library's own line.
func TestLoaderCoverLoadMissing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	source, err := Source()
	require.NoError(t, err, err)
	noperm := errors.New("NOPERM this user has no permissions to run the 'function|list' command")
	exists := errors.New("ERR Function already exists")
	other := errors.New("ERR store down")
	tests := []struct {
		name     string
		libs     []redis.Library
		listErr  error
		loadErr  error
		wantErr  string
		wantLoad bool
	}{
		{"an empty store is loaded", nil, nil, nil, "", true},
		{"a held library is left alone", []redis.Library{{Name: Library, Code: "held"}}, nil, nil, "", false},
		{"a seat without FUNCTION loads nothing", nil, noperm, nil, "", false},
		{"another list error is named", nil, other, nil, "list nova_sprint function library: " + other.Error(), false},
		{"a load another caller won is no error", nil, nil, exists, "", true},
		{"another load error is named", nil, nil, other, "load nova_sprint function library: " + other.Error(), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fnStoreFake{libs: tt.libs, listErr: tt.listErr, loadErr: tt.loadErr}
			err := LoadMissing(ctx, fakeClient(t, store))
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tt.wantErr)
			}
			if tt.wantLoad {
				assert.Equal(t, []string{source}, store.loads, "LoadMissing sends the embedded source, byte for byte")
			} else {
				assert.Empty(t, store.loads, "LoadMissing sends no load here")
			}
			assert.Equal(t, Library, store.pattern, "LoadMissing asks the store for the nova_sprint library by name")
		})
	}
}

// TestLoaderCoverLoaded: Loaded reads the nova_sprint library's code out of a
// FUNCTION LIST reply, answers false for a store that holds none, and names a
// list error with the library's own line.
func TestLoaderCoverLoaded(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	other := errors.New("ERR store down")
	tests := []struct {
		name      string
		libs      []redis.Library
		listErr   error
		wantCode  string
		wantFound bool
		wantErr   string
	}{
		{"the held library's code", []redis.Library{{Name: "other"}, {Name: Library, Code: "code"}}, nil, "code", true, ""},
		{"a store that holds none", nil, nil, "", false, ""},
		{"a list error is named", nil, other, "", false, "list nova_sprint function library: " + other.Error()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fnStoreFake{libs: tt.libs, listErr: tt.listErr}
			code, found, err := Loaded(ctx, fakeClient(t, store))
			assert.Equal(t, tt.wantCode, code)
			assert.Equal(t, tt.wantFound, found)
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tt.wantErr)
			}
		})
	}
}

// TestLoaderCoverCheck: Check reads the loaded library, FCALLs ns_ping only
// when the store holds exactly the embedded source, carries the error text of
// a failed ping, and names a list error while keeping the wanted Sum.
func TestLoaderCoverCheck(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	source, err := Source()
	require.NoError(t, err, err)
	other := errors.New("ERR store down")
	tests := []struct {
		name      string
		libs      []redis.Library
		listErr   error
		ping      any
		pingErr   error
		wantState State
		wantErr   string
		wantPings int
	}{
		{
			"the embedded library answers PONG",
			[]redis.Library{{Name: Library, Code: source}}, nil, "PONG", nil,
			State{Want: Sum(source), Loaded: Sum(source), Ping: "PONG"}, "", 1,
		},
		{
			"a store that holds none is skipped",
			nil, nil, nil, nil,
			State{Want: Sum(source), Missing: true, Ping: PingSkipped}, "", 0,
		},
		{
			"another body is skipped",
			[]redis.Library{{Name: Library, Code: "#!lua name=other"}}, nil, nil, nil,
			State{Want: Sum(source), Loaded: Sum("#!lua name=other"), Ping: PingSkipped}, "", 0,
		},
		{
			"a failed ping is the error text",
			[]redis.Library{{Name: Library, Code: source}}, nil, nil, other,
			State{Want: Sum(source), Loaded: Sum(source), Ping: other.Error()}, "", 1,
		},
		{
			"a list error is named",
			nil, other, nil, nil,
			State{Want: Sum(source)}, "list nova_sprint function library: " + other.Error(), 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fnStoreFake{libs: tt.libs, listErr: tt.listErr, ping: tt.ping, pingErr: tt.pingErr}
			st, err := Check(ctx, fakeClient(t, store))
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tt.wantErr)
			}
			assert.Equal(t, tt.wantState, st)
			assert.Equal(t, tt.wantPings, store.pings, "Check sends ns_ping only on its own library")
		})
	}
}
