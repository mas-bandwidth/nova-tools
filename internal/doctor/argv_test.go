package doctor

import (
	"net"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/mas-bandwidth/nova-tools/internal/secrets"
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
	verb          string
	flags         map[string]string
	args          []string
	redisPassword bool
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
var redisLogin = []string{"addr", "redis", "user", "password-env"}

var realVerbs = map[string]realVerb{
	"nova-redis acl check": {values: redisLogin, rule: func(a argv) string {
		if a.flags["redis"] != "" || a.flags["addr"] != "" {
			return ""
		}
		return "--redis is required"
	}},
	"nova-redis acl apply": {values: append(slices.Clone(redisLogin), "password-env-for"), bools: []string{"dry-run"}, rule: func(a argv) string {
		if a.flags["redis"] != "" || a.flags["addr"] != "" {
			return ""
		}
		return "--redis is required"
	}},
	"nova-redis fn check": {values: redisLogin, rule: func(a argv) string {
		if a.flags["redis"] != "" || a.flags["addr"] != "" {
			return ""
		}
		return "--redis is required"
	}},
	"nova-redis fn load": {values: redisLogin, rule: func(a argv) string {
		if a.flags["redis"] != "" || a.flags["addr"] != "" {
			return ""
		}
		return "--redis is required"
	}},
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
			if a.flags["dry-run"] != "true" && !a.redisPassword {
				return needs("secrets", "as", "key", "sops", "secret")(a)
			}
			return ""
		})},
	// cmd/nova-config/login.go, runLogin: --check takes no other flag; a login records all.
	"nova-config login": {values: []string{"store", "as", "key", "sops", "secret", "dsn", "actor", "friend"}, bools: []string{"check", "json"},
		rule: func(a argv) string {
			if a.has("check") {
				if len(a.flags) > 1 {
					return "--check records nothing and takes no other flag"
				}
				return ""
			}
			return needs("dsn", "actor", "store", "as", "key", "secret")(a)
		}},
	"nova-config status": {values: []string{"pg", "file", "redis"}, bools: []string{"json"}},
	// cmd/nova-config/apply.go: a write wants an actor, --as or NOVA_FRIEND, which the
	// test's environment does not set.
	"nova-config apply": {values: []string{"pg", "file", "seat", "redis", "as", "kind"}, bools: []string{"check", "dry-run", "move-seat", "json"},
		rule: func(a argv) string {
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
	"nova-doctor run": {values: []string{"check", "job", "as", "dir", "harness", "config-dir", "redis", "since", "redis-secrets", "redis-seat", "redis-key", "redis-sops", "redis-secret", "redis-dir"}, bools: []string{"local", "strict", "json"}},
	// cmd/nova-bus/main.go, names: --timeout and --redis; every bus verb also takes --json.
	// parseArgv does not see NOVA_REDIS_ADDR, so the line carries --redis.
	"nova-bus names": {values: []string{"timeout", "redis"}, bools: []string{"json"}, rule: needs("redis")},
	// cmd/nova-sprint/verbs.go verbSetup, plus the verb's own flags. parseArgv does not see
	// NOVA_SPRINT_ACTOR, so every line the doctor prints carries --actor and --redis.
	"nova-sprint seat": {values: []string{"redis", "actor", "op", "max", "epoch", "reason"}, bools: []string{"json", "repair"},
		rule: all(needs("actor", "redis"), func(a argv) string {
			if a.flags["reason"] != "" && a.flags["repair"] != "true" {
				return "--reason goes with --repair"
			}
			return ""
		})},
	// cmd/nova-sprint/pushproof.go, cmdSeatPush: no words; --sent and --harness are one at a time.
	"nova-sprint seat check": {values: []string{"redis", "actor", "op", "max", "epoch"}, bools: []string{"json"}, rule: needs("actor", "redis")},
	"nova-sprint seat push": {values: []string{"redis", "actor", "op", "max", "epoch", "harness", "target", "session", "sent", "failed"}, bools: []string{"json"},
		rule: all(needs("actor", "redis"), func(a argv) string {
			if a.flags["failed"] != "" && a.flags["sent"] == "" {
				return "--failed goes with --sent"
			}
			if a.flags["sent"] != "" && a.flags["harness"] != "" {
				return "--sent is the push loop's report and --harness the setup: one at a time"
			}
			return ""
		})},
	// cmdSeatPong: one positional, the nonce, after the flags.
	"nova-sprint seat pong": {values: []string{"redis", "actor", "op", "max", "epoch"}, bools: []string{"json"}, positionals: true,
		rule: all(needs("actor", "redis"), func(a argv) string {
			if len(a.args) != 1 || a.args[0] == "" {
				return "wants one word, the nonce the push check carried"
			}
			return ""
		})},
	// cmd/nova-sprint/seatinstall.go and seat.go addConfigSeatFlags: a dry run may omit the
	// push target; a real install wants --harness and --target; the config trio is all or nothing.
	"nova-sprint seat install": {values: []string{"redis", "actor", "op", "max", "epoch", "dir", "log", "harness", "target", "session", "server", "config-seat", "config-dsn", "config-password-env"}, bools: []string{"json", "dry-run"},
		rule: all(needs("actor", "redis"), func(a argv) string {
			if a.flags["dry-run"] != "true" && (a.flags["harness"] == "" || a.flags["target"] == "") {
				return "--harness and --target are required"
			}
			if h := a.flags["harness"]; h != "" && !slices.Contains(friend.Harnesses, h) {
				return "--harness " + h + " is no harness"
			}
			return configSeatLine(a)
		})},
	// cmd/nova-sprint/view.go: --needs takes neither --all nor --since.
	"nova-sprint view coordinator": {values: []string{"redis", "actor", "op", "max", "epoch", "since"}, bools: []string{"json", "all", "needs"},
		rule: all(needs("actor", "redis"), func(a argv) string {
			if a.flags["needs"] == "true" && (a.has("all") || a.flags["since"] != "") {
				return "--needs lists the decisions only and takes neither --all nor --since"
			}
			return ""
		})},
	"nova-sprint view worker": {values: []string{"redis", "actor", "op", "max", "epoch", "as", "since"}, bools: []string{"json"},
		rule: all(needs("actor", "redis", "as"), func(a argv) string {
			if !secrets.IsValidAsName(a.flags["as"]) {
				return "--as names the worker (letters, digits, _ and -)"
			}
			return ""
		})},
	// cmd/nova-sprint/releasecheck.go: no words.
	"nova-sprint release check": {values: []string{"redis", "actor", "op", "max", "epoch", "streams", "window", "merge-p90", "check"}, bools: []string{"json"},
		rule: needs("actor", "redis")},
	// cmd/nova-sprint/reads.go, cmdInbox: --push runs with --wait alone.
	"nova-sprint inbox": {values: []string{"redis", "actor", "op", "max", "epoch", "open", "deadline", "stale", "at-epoch", "timeout", "push"}, bools: []string{"json", "read", "wait"},
		rule: all(needs("actor", "redis"), func(a argv) string {
			if a.flags["push"] != "" && (a.flags["wait"] != "true" || a.flags["read"] == "true" || a.flags["open"] != "" || a.has("at-epoch")) {
				return "--push runs with --wait alone"
			}
			return ""
		})},
}

// configSeatLine is seat install's nova-config profile (seat.go, configSeatFlags.profile):
// none of the three, or all three, and the DSN carries no password.
func configSeatLine(a argv) string {
	seat, dsn, pass := a.flags["config-seat"], a.flags["config-dsn"], a.flags["config-password-env"]
	if seat == "" && dsn == "" && pass == "" {
		return ""
	}
	if seat == "" || dsn == "" || pass == "" {
		return "the nova-config seat profile wants --config-seat, --config-dsn and --config-password-env"
	}
	if !secrets.IsValidAsName(seat) {
		return "--config-seat must match [A-Za-z0-9_-]+"
	}
	if !configEnvName.MatchString(pass) {
		return "--config-password-env must name a variable, [A-Z_][A-Z0-9_]*"
	}
	if _, err := config.ResolveDSN(dsn, func(string) string { return "" }); err != nil {
		return "--config-dsn carries a password or cannot be read"
	}
	return ""
}

var configEnvName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// parseArgv reads a nova command as its tool would, or says why the tool refuses it.
func parseArgv(line string, password ...bool) (argv, string) {
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
	if len(password) > 0 {
		a.redisPassword = password[0]
	}
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
			b, err := strconv.ParseBool(val)
			if err != nil {
				return a, "invalid boolean value for --" + name
			}
			val = strconv.FormatBool(b)
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
		"nova-friend check bob --json":                                                 "comes after the argument bob",
		"nova-friend check --harness claude --as bob":                                  "--dir is required",
		"nova-friend check --as bob --harness claude --dir /home/bob":                  "--redis is required",
		"nova-friend check --as bob --harness nope --dir /d --redis a:1":               "no harness",
		"nova-friend ping --as ada --to bob":                                           "--redis is required",
		"nova-friend ping --as ada --redis a:1":                                        "--to is required",
		"nova-friend install --as bob --dir /home/bob":                                 "--harness is required",
		"nova-friend install --as bob --harness claude --dir /home/bob --redis a:1":    "--config-dir is required",
		"nova-redis fn check":                                                          "--redis is required",
		"nova-redis serve --bind 127.0.0.1 --port 6390 --dir ~/nova/stores/redis":      "not absolute",
		"nova-config apply --check --redis a:1":                                        "--as is required",
		"nova-redis serve --bind 127.0.0.1 --port 6390 --dir /store":                   "--secrets is required",
		"nova-redis serve --dry-run=false --bind 127.0.0.1 --port 6390 --dir /store":   "--secrets is required",
		"nova-redis serve --dry-run=invalid --bind 127.0.0.1 --port 6390 --dir /store": "invalid boolean",
		"nova-config apply --redis a:1":                                                "--as is required",
		"nova-config login --check --dsn x":                                            "takes no other flag",
		"nova-config status extra":                                                     "takes no positional arguments",
		"nova-swarm doctor --strict":                                                   "not defined",
		"nova-friend nope":                                                             "no verb",
		"nova-sprint seat pong check-1 --actor ada --redis a:1":                        "comes after the argument",
		"nova-sprint seat install --actor ada --redis a:1":                             "--harness and --target are required",
		"nova-sprint seat install --dry-run --actor ada --redis a:1 --config-seat ada": "wants --config-seat, --config-dsn and --config-password-env",
		"nova-sprint seat install --dry-run --harness grok --target /t --actor ada --redis a:1 --config-seat ada --config-dsn postgres://u:secret@h/db --config-password-env NOVA_PG_CONFIG_PASSWORD": "carries a password",
		"nova-sprint inbox --push seat --actor ada --redis a:1": "--push runs with --wait alone",
		"nova-bus names": "--redis is required",
	} {
		_, p := parseArgv(line)
		assert.Contains(t, p, want, line)
	}
	for _, line := range []string{
		"nova-friend check --json --since 24h0m0s --redis a:1 bob",
		"nova-friend check --as bob --harness claude --dir /home/bob --redis a:1 --to ada",
		"nova-friend ping --as ada --to bob --redis a:1",
		"nova-friend install --as bob --harness claude --dir /home/bob --redis a:1 --config-dir /home/bob/.claude",
		"nova-redis serve --bind 127.0.0.1 --port 6390 --dir /home/ada/nova/stores/redis --secrets /secrets --as store-seat --key /keys/store --sops /bin/sops --secret NOVA_REDIS_PASSWORD",
		"nova-config apply --redis a:1 --as ada",
		"nova-config apply --check --redis a:1 --as operator",
		"nova-config login --check",
		"nova-config login --store /secrets --as store-seat --key /keys/store --sops /bin/sops --secret NOVA_PG_CONFIG_PASSWORD --dsn postgres://nova_config@127.0.0.1:5432/nova --actor ada",
		"nova-doctor --job friend --as bob --dir /home/bob --since 10m0s",
		"nova-bus names --redis 127.0.0.1:6390",
		"nova-sprint seat --actor ada --redis 127.0.0.1:6390",
		"nova-sprint seat push --json --actor ada --redis 127.0.0.1:6390",
		"nova-sprint seat pong --actor ada --redis 127.0.0.1:6390 check-1",
		"nova-sprint seat install --harness grok --target /home/ada/session --actor ada --redis 127.0.0.1:6390",
		"nova-sprint seat install --dry-run --harness grok --target /home/ada/session --actor ada --redis 127.0.0.1:6390 --config-seat ada --config-dsn postgres://nova_config@127.0.0.1:5432/nova --config-password-env NOVA_PG_CONFIG_PASSWORD",
		"nova-sprint view coordinator --all --json --actor ada --redis 127.0.0.1:6390",
		"nova-sprint view worker --as bob --json --actor ada --redis 127.0.0.1:6390",
		"nova-sprint release check --actor ada --redis 127.0.0.1:6390",
		"nova-sprint inbox --wait --push seat --actor ada --redis 127.0.0.1:6390",
	} {
		_, p := parseArgv(line)
		assert.Empty(t, p, line)
	}
}

func TestDoctorFakeShellAllowsOnlyKnownInjectedAuthentication(t *testing.T) {
	t.Parallel()
	line := "nova-redis serve --bind 127.0.0.1 --port 6390 --dir /store"
	_, noPassword := parseArgv(line)
	assert.Contains(t, noPassword, "--secrets is required")
	_, injected := parseArgv(line, true)
	assert.Empty(t, injected)
}
