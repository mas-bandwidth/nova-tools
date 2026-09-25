// Package fn assembles and installs the nova-sprint Redis Function library.
package fn

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/redis/go-redis/v9"
)

const Library = "nova_sprint"

//go:embed lua/*.lua
var sources embed.FS

// Prelude is the one chunk-level local of the assembled library: NS, the
// table through which a file hands helpers to a file that sorts after it
// (friend.lua -> redistribute*.lua as NS.friend, redistribute.lua ->
// redistribute_assign.lua as NS.redistribute, task_claim.lua -> task_queue.lua
// as NS.DEP).
const Prelude = "local NS = {}\n"

// MaxLocals is the most active locals the library's main function may hold.
// Lua (and so Redis) refuses a function with more than 200; the headroom is
// for the next merge. TestLibraryLocalsUnderLimit enforces it.
const MaxLocals = 180

// Each file is emitted as its header line ("-- lua/<name>.lua", which the
// one-writer tests split sections on), a line "do", the file, and a line
// "end -- lua/<name>.lua".
const (
	fileHeader = "-- "
	blockOpen  = "do"
	blockClose = "end -- "
)

// Source builds one library from all verb files. A verb is added by placing a
// Lua file in lua/; no central Lua registry needs to change. Each file is
// wrapped in its own do-block, so its top-level locals leave scope at its end:
// the main function holds len(Prelude locals) + the largest file's locals at
// once, not the sum over every file (Lua's limit is 200 active locals; the sum
// passed it at #3487). A file shares nothing by bare local; what a later file
// needs goes through NS.
func Source() (string, error) {
	names, err := fs.Glob(sources, "lua/*.lua")
	if err != nil {
		return "", fmt.Errorf("list sprint functions: %w", err)
	}
	if len(names) == 0 {
		return "", fmt.Errorf("nova_sprint has no function files")
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("#!lua name=" + Library + "\n")
	b.WriteString(Prelude)
	for _, name := range names {
		fragment, err := sources.ReadFile(name)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", name, err)
		}
		if strings.TrimSpace(string(fragment)) == "" {
			return "", fmt.Errorf("empty function file %s", name)
		}
		b.WriteString("\n" + fileHeader + name + "\n" + blockOpen + "\n")
		b.Write(fragment)
		b.WriteString("\n" + blockClose + name + "\n")
	}
	return b.String(), nil
}

// Load installs the complete library atomically. REPLACE permits an updated
// binary to deploy its exact embedded version without a delete/load gap.
func Load(ctx context.Context, client *redis.Client) error {
	source, err := Source()
	if err != nil {
		return err
	}
	if err := client.FunctionLoadReplace(ctx, source).Err(); err != nil {
		return fmt.Errorf("load %s function library: %w", Library, err)
	}
	return nil
}

// LoadMissing installs the embedded library only when the server holds no
// nova_sprint library, and never replaces one it holds (#3620). A verb that
// loads on the way to its FCALL (card push, drain and release, the
// reconciler's calls and expire duty) runs whatever binary its host has; with
// Load, an older binary REPLACEd the deployed library with its own and every
// function added since vanished from the store ("ERR Function not found" from
// the reconciler's ns_fleet_step, #3620). Upgrading the library is the
// deploy's job (`nova-sprint fn load`, which uses Ensure). A caller whose ACL
// refuses FUNCTION LIST is not the deployer: it loads nothing and its FCALL
// answers for the store.
func LoadMissing(ctx context.Context, client *redis.Client) error {
	libs, err := client.FunctionList(ctx, redis.FunctionListQuery{LibraryNamePattern: Library}).Result()
	if err != nil {
		if strings.Contains(err.Error(), "NOPERM") {
			return nil
		}
		return fmt.Errorf("list %s function library: %w", Library, err)
	}
	for _, lib := range libs {
		if lib.Name == Library {
			return nil
		}
	}
	source, err := Source()
	if err != nil {
		return err
	}
	if err := client.FunctionLoad(ctx, source).Err(); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			return nil // another caller loaded it between the list and the load
		}
		return fmt.Errorf("load %s function library: %w", Library, err)
	}
	return nil
}

// Sum names one library version: the first 16 hex digits of the SHA-256 of
// its source. fn load prints it; fn check compares the loaded code's Sum with
// the embedded one.
func Sum(source string) string {
	h := sha256.Sum256([]byte(source))
	return hex.EncodeToString(h[:])[:16]
}

// Loaded returns the code of the nova_sprint library the server holds, and
// false when it holds none.
func Loaded(ctx context.Context, client *redis.Client) (string, bool, error) {
	libs, err := client.FunctionList(ctx, redis.FunctionListQuery{LibraryNamePattern: Library, WithCode: true}).Result()
	if err != nil {
		return "", false, fmt.Errorf("list %s function library: %w", Library, err)
	}
	for _, lib := range libs {
		if lib.Name == Library {
			return lib.Code, true, nil
		}
	}
	return "", false, nil
}

// Ensure loads the embedded library only when the server does not already
// hold that exact source, so a converge that runs it every pass is a no-op
// once the version matches. It returns the embedded Sum and whether it loaded.
func Ensure(ctx context.Context, client *redis.Client) (string, bool, error) {
	source, err := Source()
	if err != nil {
		return "", false, err
	}
	sum := Sum(source)
	code, found, err := Loaded(ctx, client)
	if err != nil {
		return sum, false, err
	}
	if found && code == source {
		return sum, false, nil
	}
	if err := client.FunctionLoadReplace(ctx, source).Err(); err != nil {
		return sum, false, fmt.Errorf("load %s function library: %w", Library, err)
	}
	return sum, true, nil
}

// State is what fn check found on a server.
type State struct {
	Want    string // Sum of the embedded source
	Loaded  string // Sum of the loaded code, "" when missing
	Missing bool
	Ping    string // the FCALL ns_ping 0 reply, or the error text
}

// OK is true when the loaded library is the embedded one and ns_ping answers PONG.
func (s State) OK() bool { return !s.Missing && s.Loaded == s.Want && s.Ping == "PONG" }

// PingSkipped is State.Ping when Check did not call ns_ping because the
// server does not hold the embedded source.
const PingSkipped = "skipped"

// Check reads the loaded library and, only when the server holds exactly the
// embedded source, calls FCALL ns_ping 0 (ns_ping carries no no-writes flag,
// so FCALL_RO would refuse it). When the library is missing or stale, the
// ns_ping the server would run is not ours (another library, or an older
// body, may write), so Check skips the call and reports Ping=PingSkipped; it
// changes nothing.
func Check(ctx context.Context, client *redis.Client) (State, error) {
	source, err := Source()
	if err != nil {
		return State{}, err
	}
	st := State{Want: Sum(source)}
	code, found, err := Loaded(ctx, client)
	if err != nil {
		return st, err
	}
	if !found {
		st.Missing = true
	} else {
		st.Loaded = Sum(code)
	}
	if !found || code != source {
		st.Ping = PingSkipped
		return st, nil
	}
	reply, err := client.FCall(ctx, "ns_ping", nil).Result()
	if err != nil {
		st.Ping = err.Error()
	} else {
		st.Ping = fmt.Sprint(reply)
	}
	return st, nil
}
