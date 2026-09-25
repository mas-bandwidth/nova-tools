// Package seatcred resolves a seat's fleet Redis user and password in the
// process that needs them (nova-tools#4052). nova-sprint, nova-card,
// nova-swarm and nova-wake take `--seat <name>` (or NOVA_SEAT) and read the
// seat's file through internal/secrets -- the same store, key and sops, and
// the same checks, `nova-secrets exec` uses -- so no shell wrapper stands
// between a coordinator and its own store.
//
// The password is held as a secrets.Secret: never printed, never logged, never
// an argument. It is handed to the Redis client in memory and, for the one
// child that needs it (`nova-sprint redis-cli`), to that child's environment
// only; it is never set in this process's own environment, so no other child
// this process starts inherits it.
package seatcred

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mas-bandwidth/nova-tools/internal/secrets"
)

const (
	// SeatEnv names the seat when no --seat flag does.
	SeatEnv = "NOVA_SEAT"
	// StoreEnv overrides the store directory, DefaultStore under $HOME.
	StoreEnv = "NOVA_SECRETS_STORE"
	// KeyEnv overrides the seat's age key, DefaultKeyDir/<seat>.key under $HOME.
	KeyEnv = "NOVA_SECRETS_KEY"
	// SopsEnv overrides the sops binary, else sops on PATH.
	SopsEnv = "NOVA_SECRETS_SOPS"
	// UserEnv picks the Redis ACL user when the seat's file holds more than
	// one password; it is the variable internal/nsprint/store already reads.
	UserEnv = "NOVA_SPRINT_REDIS_USER"

	// DefaultStore and DefaultKeyDir are the fleet layout under $HOME.
	DefaultStore  = "nova-bench/secrets"
	DefaultKeyDir = ".config/nova-secrets"
)

// Users is the order a seat's Redis user is chosen in when UserEnv is unset:
// the first whose password the seat's file holds.
var Users = []string{"coordinator", "bench"}

// PasswordKey is the key in a seat's file that holds user's Redis password.
func PasswordKey(user string) string {
	return "NOVA_REDIS_" + strings.ToUpper(user) + "_PASSWORD"
}

// Cred is one seat's Redis login. String names the seat, user and key; the
// password formats as secrets.Redacted under every verb.
type Cred struct {
	Seat     string
	User     string
	Key      string
	Password secrets.Secret
}

func (c Cred) String() string {
	return fmt.Sprintf("seat=%s user=%s key=%s", c.Seat, c.User, c.Key)
}

// Paths is where a seat's file, key and sops are.
type Paths struct{ Store, Key, Sops string }

// PathsFor resolves the store, the seat's key and sops from getenv: the
// override variables when set, else the fleet layout under HOME and sops on
// PATH.
func PathsFor(seat string, getenv func(string) string) (Paths, error) {
	home := getenv("HOME")
	p := Paths{Store: getenv(StoreEnv), Key: getenv(KeyEnv), Sops: getenv(SopsEnv)}
	if (p.Store == "" || p.Key == "") && home == "" {
		return Paths{}, fmt.Errorf("seat %s: HOME is unset and %s or %s does not name the store and key", seat, StoreEnv, KeyEnv)
	}
	if p.Store == "" {
		p.Store = filepath.Join(home, DefaultStore)
	}
	if p.Key == "" {
		p.Key = filepath.Join(home, DefaultKeyDir, seat+".key")
	}
	if p.Sops == "" {
		s, err := exec.LookPath("sops")
		if err != nil {
			return Paths{}, fmt.Errorf("seat %s: no sops on PATH; install it or set %s", seat, SopsEnv)
		}
		p.Sops = s
	}
	return p, nil
}

// Resolve opens seat's file through secrets.OpenSeatFile and picks its Redis
// login: UserEnv's user when set, else the first of Users whose password the
// file holds. A refusal names the seat and what to do, never a value.
func Resolve(seat string, getenv func(string) string) (Cred, error) {
	if !secrets.IsValidAsName(seat) {
		return Cred{}, fmt.Errorf("seat %q: must match [A-Za-z0-9_-]+", seat)
	}
	p, err := PathsFor(seat, getenv)
	if err != nil {
		return Cred{}, err
	}
	sf, err := secrets.OpenSeatFile(p.Store, seat, p.Key, p.Sops)
	if err != nil {
		return Cred{}, fmt.Errorf("seat %s: %w", seat, err)
	}
	users := Users
	if u := strings.TrimSpace(getenv(UserEnv)); u != "" {
		users = []string{u}
	}
	for _, u := range users {
		key := PasswordKey(u)
		if pw, ok := sf.Secrets[key]; ok && pw.Loaded() && !pw.Empty() {
			return Cred{Seat: seat, User: u, Key: key, Password: pw}, nil
		}
	}
	want := make([]string, len(users))
	for i, u := range users {
		want[i] = PasswordKey(u)
	}
	return Cred{}, fmt.Errorf("seat %s: %s holds no %s; seal it with nova-secrets seal --as %s --name %s", seat, sf.Path, strings.Join(want, " or "), seat, want[0])
}

// The process's seat. Select records it; Active resolves it once.
var (
	mu       sync.Mutex
	selected string
	resolved bool
	cred     Cred
	credErr  error
	resolver = func(seat string) (Cred, error) { return Resolve(seat, os.Getenv) }
)

// Select makes seat this process's seat ("" is none) and forgets any earlier
// resolution. Nothing is decrypted until Active is first asked.
func Select(seat string) {
	mu.Lock()
	defer mu.Unlock()
	selected, resolved, cred, credErr = strings.TrimSpace(seat), false, Cred{}, nil
}

// Selected is the seat Select recorded, "" when none.
func Selected() string {
	mu.Lock()
	defer mu.Unlock()
	return selected
}

// Active is the selected seat's login, resolved on first use and kept for the
// life of the process. ok is false when no seat is selected: the caller then
// authenticates as it did before (its own environment variables).
func Active() (c Cred, ok bool, err error) {
	mu.Lock()
	defer mu.Unlock()
	if selected == "" {
		return Cred{}, false, nil
	}
	if !resolved {
		cred, credErr = resolver(selected)
		resolved = true
	}
	return cred, true, credErr
}

// FromArgs takes --seat <name> or --seat=<name> out of args (only before a
// "--", which ends a tool's own flags), selects that seat or else getenv's
// NOVA_SEAT, and returns the rest. A --seat with no name is an error.
func FromArgs(args []string, getenv func(string) string) ([]string, error) {
	seat, flagged := "", false
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		switch {
		case a == "--seat" || a == "-seat":
			if i+1 >= len(args) || args[i+1] == "" || strings.HasPrefix(args[i+1], "-") {
				return nil, fmt.Errorf("--seat wants a seat name, for example --seat studio")
			}
			seat, flagged = args[i+1], true
			i++
		case strings.HasPrefix(a, "--seat=") || strings.HasPrefix(a, "-seat="):
			seat, flagged = a[strings.IndexByte(a, '=')+1:], true
			if seat == "" {
				return nil, fmt.Errorf("--seat wants a seat name, for example --seat studio")
			}
		default:
			rest = append(rest, a)
		}
	}
	if !flagged && getenv != nil {
		seat = getenv(SeatEnv)
	}
	Select(seat)
	return rest, nil
}

// ChildEnv is environ with the password variables a Redis child could confuse
// for its own removed and REDISCLI_AUTH set to c's password: the environment of
// the one child that is handed the password, never this process's.
func ChildEnv(environ []string, c Cred) []string {
	out := make([]string, 0, len(environ)+1)
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if name == "REDISCLI_AUTH" || name == c.Key || (strings.HasPrefix(name, "NOVA_REDIS_") && strings.HasSuffix(name, "_PASSWORD")) {
			continue
		}
		out = append(out, kv)
	}
	_ = c.Password.Use(func(pw string) error {
		out = append(out, "REDISCLI_AUTH="+pw)
		return nil
	})
	return out
}
