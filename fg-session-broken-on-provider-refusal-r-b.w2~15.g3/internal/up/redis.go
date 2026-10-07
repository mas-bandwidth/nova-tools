package up

import (
	"encoding/hex"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
)

// The redis step: the tools with no twin (nova-bus) get one Redis, served by
// nova-redis serve as a supervised loop record of the platform's service
// manager, the same unit loops.yml installs from fleet/templates (a launchd
// agent on darwin, a systemd user unit on linux), on loopback only. Its ACL
// users are the ones nova-redis acl render names, each with a password drawn
// here, sealed into the secrets store and never printed; acl apply and fn
// load reach the store under nova-secrets exec (docs/SPEC-UP.md "Steps", 6;
// docs/SPEC-REDIS.md).
func init() { Register(Step{Name: "redis", Order: 60, Plan: planRedis, Apply: applyRedis}) }

const (
	RedisLoop = "redis-local"
	RedisBind = "127.0.0.1"
	RedisPort = 6390
	RedisDir  = "stores/redis"
	// RedisACLRecord holds the users the last acl apply set; a plan reads it, never the store.
	RedisACLRecord = "stores/redis/acl.applied"
)

// RedisUsers are the ACL users of a store (nova-redis acl render), coordinator first.
var RedisUsers = []string{"coordinator", "bench", "ns-table", "ns-friend"}

// RedisAddr is the store's address.
func RedisAddr() string { return RedisBind + ":" + strconv.Itoa(RedisPort) }

// PasswordName is the secret holding user's password.
func PasswordName(user string) string {
	return "NOVA_UP_REDIS_" + strings.ToUpper(strings.ReplaceAll(user, "-", "_")) + "_PASSWORD"
}

// unitPath is where the service manager reads the loop's unit.
func unitPath(e *Env) string {
	if e.GOOS == "darwin" {
		return filepath.Join(e.Home, "Library", "LaunchAgents", "com.nova.loop."+RedisLoop+".plist")
	}
	return filepath.Join(e.Home, ".config", "systemd", "user", "nova-loop-"+RedisLoop+".service")
}

// unit renders the loop record redis-local as fleet/templates renders a kept-alive loop.
func unit(e *Env) []byte {
	argv := []string{e.path("nova-redis"), "serve", "--bind", RedisBind, "--port", strconv.Itoa(RedisPort), "--dir", e.Path(RedisDir)}
	path := filepath.Dir(e.path("nova-redis")) + ":" + filepath.Dir(e.path("redis-server")) + ":/usr/bin:/bin"
	log := e.Path(LogsDir, RedisLoop+".log")
	if e.GOOS == "darwin" {
		log = filepath.Join(e.Home, "Library", "Logs", "nova-loop-"+RedisLoop+".log")
	}
	var b strings.Builder
	if e.GOOS == "darwin" {
		fmt.Fprintf(&b, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n")
		fmt.Fprintf(&b, "<!-- %s: written by nova-up --local from the loop record %s -->\n<plist version=\"1.0\">\n<dict>\n", unitPath(e), RedisLoop)
		fmt.Fprintf(&b, "<key>Label</key>\n<string>com.nova.loop.%s</string>\n<key>ProgramArguments</key>\n<array>\n", RedisLoop)
		for _, w := range argv {
			fmt.Fprintf(&b, "<string>%s</string>\n", xmlEscape(w))
		}
		fmt.Fprintf(&b, "</array>\n<key>EnvironmentVariables</key>\n<dict>\n<key>HOME</key>\n<string>%s</string>\n<key>PATH</key>\n<string>%s</string>\n</dict>\n",
			xmlEscape(e.Home), xmlEscape(path))
		fmt.Fprintf(&b, "<key>WorkingDirectory</key>\n<string>%s</string>\n<key>RunAtLoad</key>\n<true/>\n<key>KeepAlive</key>\n<true/>\n<key>ThrottleInterval</key>\n<integer>10</integer>\n", xmlEscape(e.Root))
		fmt.Fprintf(&b, "<key>StandardOutPath</key>\n<string>%s</string>\n<key>StandardErrorPath</key>\n<string>%s</string>\n</dict>\n</plist>\n", xmlEscape(log), xmlEscape(log))
		return []byte(b.String())
	}
	fmt.Fprintf(&b, "# %s: written by nova-up --local from the loop record %s\n[Unit]\nDescription=nova loop %s\nAfter=network-online.target\nStartLimitIntervalSec=0\n\n", unitPath(e), RedisLoop, RedisLoop)
	fmt.Fprintf(&b, "[Service]\nType=simple\nRestart=always\nRestartSec=10\nWorkingDirectory=%s\nEnvironment=\"HOME=%s\"\nEnvironment=\"PATH=%s\"\nExecStart=", e.Root, e.Home, path)
	for i, w := range argv {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(strconv.Quote(strings.NewReplacer("%", "%%", "$", "$$").Replace(w)))
	}
	fmt.Fprintf(&b, "\nStandardOutput=append:%s\nStandardError=append:%s\n\n[Install]\nWantedBy=default.target\n", log, log)
	return []byte(b.String())
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

// sealed is the names the seat's file holds (nova-secrets names: one
// `SECRETS NAME key=<NAME>` line each, never a value), empty before the store is there.
func sealed(e *Env) (map[string]bool, error) {
	have := map[string]bool{}
	if !exists(e.Path(SecretsDir, ".git")) {
		return have, nil
	}
	out, err := e.Run(Cmd{Name: e.path("nova-secrets"), Args: []string{"names", "--store", e.Path(SecretsDir), "--as", Seat, "--max", "0"}})
	if err != nil {
		return nil, err
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "SECRETS NAME ") {
			have[field(l, "key")] = true
		}
	}
	return have, nil
}

// redisTodo is what the store lacks: the unit's state, the passwords not yet sealed, and whether acl apply is owed.
func redisTodo(e *Env) (State, []string, bool, error) {
	have, err := sealed(e)
	var unsealed []string
	for _, u := range RedisUsers {
		if !have[PasswordName(u)] {
			unsealed = append(unsealed, u)
		}
	}
	acl := fileState(e.Path(RedisACLRecord), []byte(strings.Join(RedisUsers, " ")+"\n")) != OK
	return fileState(unitPath(e), unit(e)), unsealed, acl, err
}

func planRedis(e *Env) Finding {
	where := fmt.Sprintf("%s loop=%s for nova-bus", RedisAddr(), RedisLoop)
	u, unsealed, acl, err := redisTodo(e)
	if err != nil {
		return Finding{Change, where + ": the seat's names could not be read (" + err.Error() + "); apply seals what is not there"}
	}
	var todo []string
	if u != OK {
		todo = append(todo, "unit "+string(u))
	}
	if len(unsealed) > 0 {
		todo = append(todo, fmt.Sprintf("passwords to seal=%d", len(unsealed)))
	}
	if acl {
		todo = append(todo, "acl apply, fn load")
	}
	switch {
	case len(todo) == 0:
		return Finding{OK, where}
	case u == Create:
		return Finding{Create, where + ": " + strings.Join(todo, ", ")}
	}
	return Finding{Change, where + ": " + strings.Join(todo, ", ")}
}

func applyRedis(e *Env) error {
	u, unsealed, _, err := redisTodo(e)
	if err != nil {
		return err
	}
	if u != OK {
		if err := writeFile(unitPath(e), unit(e), 0o644); err != nil {
			return err
		}
		if err := loadUnit(e, u == Change); err != nil {
			return err
		}
	}
	for _, user := range unsealed {
		if err := seal(e, PasswordName(user)); err != nil {
			return err
		}
	}
	var names []string
	acl := []string{e.path("nova-redis"), "acl", "apply", "--addr", RedisAddr()}
	for _, user := range RedisUsers {
		names = append(names, PasswordName(user))
		acl = append(acl, "--password-env-for", user+"="+PasswordName(user))
	}
	fn := []string{e.path("nova-redis"), "fn", "load", "--addr", RedisAddr(), "--user", RedisUsers[0], "--password-env", PasswordName(RedisUsers[0])}
	for _, argv := range [][]string{acl, fn} {
		if _, err := e.Run(secretsExec(e, names, argv)); err != nil {
			return err
		}
	}
	return writeFile(e.Path(RedisACLRecord), []byte(strings.Join(RedisUsers, " ")+"\n"), 0o644)
}

// loadUnit has the service manager run the unit, again after a change.
func loadUnit(e *Env, reload bool) error {
	var runs []Cmd
	if e.GOOS == "darwin" {
		domain := "gui/" + strconv.Itoa(e.UID)
		if reload {
			runs = append(runs, Cmd{Name: "launchctl", Args: []string{"bootout", domain, unitPath(e)}})
		}
		runs = append(runs, Cmd{Name: "launchctl", Args: []string{"bootstrap", domain, unitPath(e)}})
	} else {
		name := filepath.Base(unitPath(e))
		runs = append(runs, Cmd{Name: "systemctl", Args: []string{"--user", "daemon-reload"}},
			Cmd{Name: "systemctl", Args: []string{"--user", "enable", "--now", name}})
		if reload {
			runs = append(runs, Cmd{Name: "systemctl", Args: []string{"--user", "restart", name}})
		}
	}
	for i, c := range runs {
		// ignored: on darwin a bootout of a unit launchd no longer holds fails, and the bootstrap after it is what loads the new unit
		if _, err := e.Run(c); err != nil && !(reload && i == 0 && e.GOOS == "darwin") {
			return err
		}
	}
	return nil
}

// seal draws a password and seals it into the seat's file on stdin
// (nova-secrets seal --stdin --no-pr), then brings the seal's branch onto
// main and pushes it, so nova-secrets exec reads it. The password is in no
// argument and no line nova-up prints.
func seal(e *Env, name string) error {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(e.Rand, raw); err != nil {
		return fmt.Errorf("drawing the password for %s: %w", name, err)
	}
	store := e.Path(SecretsDir)
	out, err := e.Run(Cmd{Name: e.path("nova-secrets"), Args: []string{"seal", "--store", store, "--as", Seat,
		"--key", e.Path(KeyFile()), "--sops", e.path("sops"), "--name", name, "--stdin", "--no-pr"}, Stdin: hex.EncodeToString(raw)})
	if err != nil {
		return err
	}
	branch := field(out, "branch")
	if branch == "" {
		return fmt.Errorf("nova-secrets seal named no branch= for %s", name)
	}
	for _, c := range []Cmd{
		{Name: e.path("git"), Args: []string{"-C", store, "merge", "-q", "--ff-only", branch}},
		{Name: e.path("git"), Args: []string{"-C", store, "push", "-q", "origin", "main"}},
	} {
		if _, err := e.Run(c); err != nil {
			return err
		}
	}
	return nil
}

// secretsExec is argv run under nova-secrets exec with the named secrets in its environment.
func secretsExec(e *Env, names, argv []string) Cmd {
	return Cmd{Name: e.path("nova-secrets"), Args: append([]string{"exec", "--store", e.Path(SecretsDir), "--as", Seat,
		"--key", e.Path(KeyFile()), "--sops", e.path("sops"), "--only", strings.Join(names, ","), "--"}, argv...)}
}
