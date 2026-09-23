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

// Source builds one library from all verb files. A verb is added by placing a
// Lua file in lua/; no central Lua registry needs to change.
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
	for _, name := range names {
		fragment, err := sources.ReadFile(name)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", name, err)
		}
		if strings.TrimSpace(string(fragment)) == "" {
			return "", fmt.Errorf("empty function file %s", name)
		}
		b.WriteString("\n-- " + name + "\n")
		b.Write(fragment)
		b.WriteByte('\n')
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

// Check reads the loaded library and calls FCALL ns_ping 0 (ns_ping carries
// no no-writes flag, so FCALL_RO would refuse it); it changes nothing.
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
	reply, err := client.FCall(ctx, "ns_ping", nil).Result()
	if err != nil {
		st.Ping = err.Error()
	} else {
		st.Ping = fmt.Sprint(reply)
	}
	return st, nil
}
