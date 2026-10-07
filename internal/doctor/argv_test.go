package doctor

import (
	"net"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// realVerb is one real verb's argv rules, pinned from its source, so the job tests' fake
// shell and fake tools refuse what the real tool refuses. Three attempts at this card
// printed a nova-friend command the real tool refuses and passed, because the fake
// accepted whatever the doctor printed; the table below is what a fake may accept.
//
// The parse is Go's flag package, as every one of these tools uses it (verbflag.Parse,
// internal/tool/tool.go:530-558): flags are read until the first argument that is not a
// flag, and everything from there on is an argument, so `check bob --json` is the friends
// "bob" and "--json". A value flag takes the next word whatever it is.
type realVerb struct {
	values, bools []string
	positionals   bool                // the verb takes arguments after its flags
	rule          func(a argv) string // the verb's own refusals: a required flag, a value it rejects
}

// argv is one parsed command line: the verb, its flags and its arguments.
type argv struct {
	verb  string
	flags map[string]string
	args  []string
}

func (a argv) has(n string) bool { _, ok := a.flags[n]; return ok }

// needs is the rule that each flag named is on the line.
func needs(names ...string) func(a argv) string {
	return func(a argv) string {
		for _, n := range names {
			if !a.has(n) || a.flags[n] == "" {
				return "--" + n + " is required"
			}
		}
		return ""
	}
}

func all(rules ...func(a argv) string) func(a argv) string {
	return func(a argv) string {
		for _, r := range rules {
			if p := r(a); p != "" {
				return p
			}
		}
		return ""
	}
}

// redisLogin is nova-redis's login flags (cmd/nova-redis/main.go, loginFlags: --addr has
// no default and is required).
var redisLogin = []string{"addr", "user", "password-env"}

var realVerbs = map[string]realVerb{
	"nova-redis acl check": {values: redisLogin, rule: needs("addr")},
	"nova-redis acl apply": {values: append(slices.Clone(redisLogin), "password-env-for"), bools: []string{"dry-run"}, rule: needs("addr")},
	"nova-redis fn check":  {values: redisLogin, rule: needs("addr")},
	"nova-redis fn load":   {values: redisLogin, rule: needs("addr")},
	// cmd/nova-redis/serve.go: --bind, --port and --dir required, --dir absolute.
	"nova-redis serve": {values: []string{"bind", "port", "dir", "users", "secrets", "as", "key", "sops", "secret"}, bools: []string{"dry-run"},
		rule: all(needs("bind", "port", "dir"), func(a argv) string {
			number, err := strconv.ParseUint(a.flags["port"], 10, 16)
			if err != nil || number == 0 {
				return "--port wants 1 to 65535"
			}
			if net.ParseIP(a.flags["bind"]) == nil {
				return "--bind wants an IP address, not a hostname"
			}
			if !filepath.IsAbs(a.flags["dir"]) {
				return "--dir " + a.flags["dir"] + " is not absolute"
			}
			return ""
		})},
	// cmd/nova-config/login.go, runLogin: --check takes no other flag; a login records all.
	"nova-config login": {values: []string{"store", "as", "key", "sops", "secret", "dsn", "friend"}, bools: []string{"check", "json"},
		rule: func(a argv) string {
			if a.has("check") {
				if len(a.flags) > 1 {
					return "--check records nothing and takes no other flag"
				}
				return ""
			}
			return needs("dsn", "friend", "store", "as", "key", "secret")(a)
		}},
	"nova-config status": {values: []string{"pg", "file", "redis"}, bools: []string{"json"}},
	// cmd/nova-config/apply.go: a write wants an actor, --as or NOVA_FRIEND, which the
	// test's environment does not set.
	"nova-config apply": {values: []string{"pg", "file", "seat", "redis", "as", "kind"}, bools: []string{"check", "dry-run", "move-seat", "json"},
		rule: func(a argv) string {
			if a.has("check") || a.has("dry-run") {
				return ""
			}
			return needs("as")(a)
		}},
	"nova-config migrate": {values: []string{"pg", "file"}, bools: []string{"print", "dry-run", "json"}},
	"nova-update apply":   {values: []string{"file", "version", "timeout"}, bools: []string{"dry-run", "json"}, positionals: true, rule: needs("file")},
	"nova-swarm doctor":   {values: []string{"path", "local"}},
	// cmd/nova-friend/main.go, check: the friends are its arguments; with --harness it
	// is the delivery check, which wants --as (the friend itself) and --dir
	// (main.go:578-583) and --redis (check.go:46; NOVA_BUS_REDIS, unset here).
	"nova-friend check": {values: []string{"as", "harness", "dir", "session", "config-dir", "model", "within", "to", "since", "shown", "state-dir", "redis"},
		bools: []string{"settings", "json", "dry-run"}, positionals: true,
		rule: func(a argv) string {
			if !a.has("harness") {
				return ""
			}
			if !slices.Contains(friend.Harnesses, a.flags["harness"]) {
				return "--harness " + a.flags["harness"] + " is no harness"
			}
			return needs("as", "dir", "redis")(a)
		}},
	// ping: --as required, --to without --to-friends (wakeping.go), --redis to send (main.go, bus).
	"nova-friend ping": {values: []string{"as", "to", "every", "within", "never-wake", "server", "nonce", "since", "redis"},
		bools: []string{"to-friends", "wake", "json", "dry-run"},
		rule: func(a argv) string {
			if a.has("to-friends") {
				return needs("as", "redis")(a)
			}
			return needs("as", "to", "redis")(a)
		}},
	// install: daemonFlags' three required flags, and a claude friend's config directory
	// (internal/friend/settings_claude.go, ErrNoConfigDir; CLAUDE_CONFIG_DIR is unset here).
	"nova-friend install": {values: []string{"as", "harness", "dir", "session", "server", "width", "silent-stop", "broken-after", "limit-rest",
		"coordinator", "state-dir", "redis", "config-dir", "model", "secrets", "seat", "launchd-log", "within"}, bools: []string{"json", "dry-run"},
		rule: all(needs("as", "harness", "dir"), func(a argv) string {
			if !slices.Contains(friend.Harnesses, a.flags["harness"]) {
				return "--harness " + a.flags["harness"] + " is no harness"
			}
			if a.flags["harness"] == "claude" {
				return needs("config-dir")(a)
			}
			return ""
		})},
	"nova-doctor run": {values: []string{"check", "job", "as", "dir", "harness", "config-dir", "redis", "since"}, bools: []string{"local", "strict", "json"}},
}

// parseArgv reads a nova command as its tool would, or says why the tool refuses it.
func parseArgv(line string) (argv, string) {
	words, err := onboarding.SplitShell(line)
	if err != nil {
		return argv{}, err.Error()
	}
	if len(words) == 0 {
		return argv{}, "an empty command"
	}
	tool, rest := words[0], words[1:]
	var verb string
	for _, n := range []int{2, 1} {
		if len(rest) >= n && !strings.HasPrefix(rest[0], "-") {
			if _, ok := realVerbs[tool+" "+strings.Join(rest[:n], " ")]; ok {
				verb, rest = tool+" "+strings.Join(rest[:n], " "), rest[n:]
				break
			}
		}
	}
	if verb == "" && tool == "nova-doctor" && (len(rest) == 0 || strings.HasPrefix(rest[0], "-")) {
		verb = "nova-doctor run"
	}
	spec, ok := realVerbs[verb]
	if !ok {
		return argv{}, "no verb the table knows: " + line
	}
	a := argv{verb: verb, flags: map[string]string{}}
	for i := 0; i < len(rest); i++ {
		w := rest[i]
		if w == "--" {
			a.args = rest[i+1:]
			break
		}
		if !strings.HasPrefix(w, "-") || w == "-" {
			a.args = rest[i:]
			break
		}
		name, val, hasVal := strings.Cut(strings.TrimLeft(w, "-"), "=")
		switch {
		case slices.Contains(spec.bools, name):
			if !hasVal {
				val = "true"
			}
		case slices.Contains(spec.values, name):
			if !hasVal {
				if i+1 >= len(rest) {
					return a, "flag needs an argument: " + w
				}
				i++
				val = rest[i]
			}
		default:
			return a, "flag provided but not defined: " + w
		}
		a.flags[name] = val
	}
	for _, x := range a.args {
		if strings.HasPrefix(x, "-") {
			return a, "the flag " + x + " comes after the argument " + a.args[0] + ", so the tool reads it as an argument"
		}
	}
	if len(a.args) > 0 && !spec.positionals {
		return a, "takes no positional arguments, got " + a.args[0]
	}
	if spec.rule != nil {
		if p := spec.rule(a); p != "" {
			return a, p
		}
	}
	return a, ""
}

// The table refuses what the real tools refuse: the three commands earlier attempts
// printed, and each verb's required flags.
func TestDoctorFakeShellRefusesWhatTheRealToolRefuses(t *testing.T) {
	t.Parallel()
	for line, want := range map[string]string{
		"nova-friend check bob --json":                                              "comes after the argument bob",
		"nova-friend check --harness claude --as bob":                               "--dir is required",
		"nova-friend check --as bob --harness claude --dir /home/bob":               "--redis is required",
		"nova-friend check --as bob --harness nope --dir /d --redis a:1":            "no harness",
		"nova-friend ping --as ada --to bob":                                        "--redis is required",
		"nova-friend ping --as ada --redis a:1":                                     "--to is required",
		"nova-friend install --as bob --dir /home/bob":                              "--harness is required",
		"nova-friend install --as bob --harness claude --dir /home/bob --redis a:1": "--config-dir is required",
		"nova-redis fn check":                                                       "--addr is required",
		"nova-redis serve --bind 127.0.0.1 --port 6390 --dir ~/nova/stores/redis":   "not absolute",
		"nova-config apply --redis a:1":                                             "--as is required",
		"nova-config login --check --dsn x":                                         "takes no other flag",
		"nova-config status extra":                                                  "takes no positional arguments",
		"nova-swarm doctor --strict":                                                "not defined",
		"nova-friend nope":                                                          "no verb",
	} {
		_, p := parseArgv(line)
		assert.Contains(t, p, want, line)
	}
	for _, line := range []string{
		"nova-friend check --json --since 24h0m0s --redis a:1 bob",
		"nova-friend check --as bob --harness claude --dir /home/bob --redis a:1 --to ada",
		"nova-friend ping --as ada --to bob --redis a:1",
		"nova-friend install --as bob --harness claude --dir /home/bob --redis a:1 --config-dir /home/bob/.claude",
		"nova-redis serve --bind 127.0.0.1 --port 6390 --dir /home/ada/nova/stores/redis",
		"nova-config apply --redis a:1 --as ada",
		"nova-config apply --check --redis a:1",
		"nova-config login --check",
		"nova-doctor --job friend --as bob --dir /home/bob --since 10m0s",
	} {
		_, p := parseArgv(line)
		assert.Empty(t, p, line)
	}
}
