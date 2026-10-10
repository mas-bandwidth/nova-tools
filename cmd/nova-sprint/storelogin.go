package main

// The seat's store login, a setting of nova-sprint itself (seat-store-login-built-in):
// `seat login` records which store, which ACL user and where that user's password lives
// in nova-secrets, in the tool's per-user config file; every verb after it opens the
// store with that user and the password read in this process through
// pkg/secrets.ReadLogin, which no environment and no output ever holds. It replaces
// the hand-written wrapper that ran every verb under nova-secrets exec with the
// NOVA_SPRINT_REDIS* variables set (docs/SPEC-SPRINT.md, "The seat's store login").

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/redisauth"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/redisconn"
	"github.com/mas-bandwidth/nova-tools/pkg/secrets"
)

// seatLoginAddr is the name the app's getenv answers with the recorded login's address
// (seatLoginOn): the last place --redis looks, after NOVA_SPRINT_REDIS and
// NOVA_REDIS_ADDR. It has spaces in it, so no variable of the environment can be it.
const seatLoginAddr = "nova-sprint seat login"

// seatLoginPassword names the password of the recorded login to redisconn, which reads
// a password only through the getenv it is handed: the getenv openConn hands it answers
// this name from the value read in process, and the process's environment never holds
// it, so no child of this process can inherit it.
const seatLoginPassword = "NOVA_SPRINT_SEAT_LOGIN_PASSWORD"

// storeLogin is the record seat login writes: the store's address and ACL user, and
// where the user's password is in nova-secrets. It holds no secret and cannot.
type storeLogin struct {
	Redis  string `json:"redis"`
	User   string `json:"user"`
	Store  string `json:"store"`
	As     string `json:"as"`
	Key    string `json:"key"`
	Sops   string `json:"sops"`
	Secret string `json:"secret"`
}

func (l storeLogin) secrets() secrets.Login {
	return secrets.Login{Store: l.Store, As: l.As, Key: l.Key, Sops: l.Sops, Name: l.Secret}
}

// line is the login on one line, every field a fact and none a secret.
func (l storeLogin) line() string {
	return fmt.Sprintf("redis=%s user=%s store=%s as=%s key=%s sops=%s secret=%s", oneline.Field(l.Redis), oneline.Field(l.User),
		oneline.Field(l.Store), oneline.Field(l.As), oneline.Field(l.Key), oneline.Field(l.Sops), oneline.Field(l.Secret))
}

// seatLoginOn turns the recorded login on for this app (main does; a test's app has
// none until its test does): the login's file is the per-user config file, and the
// app's getenv answers seatLoginAddr from it.
func (a *app) seatLoginOn() {
	if a.loginFile == nil {
		a.loginFile = a.defaultLoginFile
	}
	getenv := a.getenv
	a.getenv = func(k string) string {
		if k != seatLoginAddr {
			return getenv(k)
		}
		l, ok, err := a.recordedLogin()
		if err != nil || !ok {
			return ""
		}
		return l.Redis
	}
}

// defaultLoginFile is the per-user config file: $XDG_CONFIG_HOME/nova-sprint/login.json,
// else ~/.config/nova-sprint/login.json, on every system.
func (a *app) defaultLoginFile() (string, error) {
	if x := a.getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "nova-sprint", "login.json"), nil
	}
	home, err := a.home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "nova-sprint", "login.json"), nil
}

// recordedLogin is the login seat login recorded: ok is false when none is (no file,
// or an app with the login off). A file that cannot be read or is not a whole login
// is an error naming it and the remedy, never a login with a field missing.
func (a *app) recordedLogin() (storeLogin, bool, error) {
	if a.loginFile == nil {
		return storeLogin{}, false, nil
	}
	path, err := a.loginFile()
	if err != nil {
		return storeLogin{}, false, fmt.Errorf("the seat login's file has no place: %w", err)
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return storeLogin{}, false, nil
	}
	remedy := "; run: nova-sprint seat logout, then nova-sprint seat login again"
	if err != nil {
		return storeLogin{}, false, fmt.Errorf("the seat login %s is unreadable: %v%s", path, err, remedy)
	}
	var l storeLogin
	if err := json.Unmarshal(b, &l); err != nil {
		return storeLogin{}, false, fmt.Errorf("the seat login %s is not a login: %v%s", path, err, remedy)
	}
	if l.Redis == "" || l.User == "" {
		return storeLogin{}, false, fmt.Errorf("the seat login %s names no redis or no user%s", path, remedy)
	}
	if m := l.secrets().Missing(); m != "" {
		return storeLogin{}, false, fmt.Errorf("the seat login %s names no %s%s", path, m, remedy)
	}
	return l, true, nil
}

// readLoginSecret reads the login's password in process: the test's reader when one is
// set, else nova-secrets' own (secrets.ReadLogin). Its refusal names the setting and the
// remedy; it is never an empty password.
func (a *app) readLoginSecret(path string, l storeLogin) (secrets.Secret, error) {
	s, err := a.secretReader()(l.secrets())
	if err != nil {
		return secrets.Secret{}, fmt.Errorf("the seat login %s (user %s at %s) reads its password from %s in seat %s of %s, and it does not resolve: %v; run: nova-sprint seat login --check, then nova-sprint seat login again or nova-sprint seat logout",
			path, l.User, l.Redis, l.Secret, l.As, l.Store, err)
	}
	return s, nil
}

// secretReader is how a login's secret is read: the test's reader when one is set, else
// nova-secrets' own.
func (a *app) secretReader() func(secrets.Login) (secrets.Secret, error) {
	if a.loginSecret != nil {
		return a.loginSecret
	}
	return secrets.ReadLogin
}

// storeOptions is how the store at addr is logged in to, and the getenv that login's
// password is read through. The environment's login wins: NOVA_SPRINT_REDIS_USER set is
// that user and the variable NOVA_SPRINT_REDIS_PASSWORD_ENV names, as nova-table dials.
// Else the recorded seat login, when addr is its address, is its user with the password
// read in process from nova-secrets. Else the store's default user.
func (a *app) storeOptions(addr string) (redisconn.Options, func(string) string, error) {
	o := redisconn.Options{Addr: addr, Env: redisconn.Env{User: redisauth.UserEnv}}
	if a.getenv(redisauth.UserEnv) != "" {
		o.Env.PasswordEnv = redisauth.PasswordEnvEnv
		if a.getenv(redisauth.PasswordEnvEnv) == "" {
			o.PasswordEnv = redisauth.DefaultPasswordEnv
		}
		return o, a.getenv, nil
	}
	l, ok, err := a.recordedLogin()
	if err != nil {
		return redisconn.Options{}, nil, err
	}
	if !ok || l.Redis != addr {
		return o, a.getenv, nil
	}
	path, _ := a.loginFile() // ignored: recordedLogin read the file at this path
	s, err := a.readLoginSecret(path, l)
	if err != nil {
		return redisconn.Options{}, nil, err
	}
	o.User, o.PasswordEnv = l.User, seatLoginPassword
	return o, func(k string) string {
		if k != seatLoginPassword {
			return a.getenv(k)
		}
		v := ""
		_ = s.Use(func(p string) error { v = p; return nil }) // ignored: the function never fails
		return v
	}, nil
}

// cmdSeatLogin is seat login: record the store login every verb after it uses, or with
// --check say the login it would use and whether its secret resolves.
func (a *app) cmdSeatLogin(args []string, stdout, stderr io.Writer) int {
	const verb = "seat login"
	flags, c := a.verbSetup(verb) // its --redis is the store's address the login is for
	store := flags.String("store", "", "the nova-secrets store's working copy the password is read from (nova-secrets exec --store)")
	as := flags.String("as", "", "the seat of the store whose file holds the password (nova-secrets exec --as)")
	key := flags.String("key", "", "the seat's age key file (nova-secrets exec --key)")
	sops := flags.String("sops", "", "the sops binary (nova-secrets exec --sops); empty is the sops on PATH, recorded as its path")
	secret := flags.String("secret", "", "the NAME of the password in the seat's file, e.g. NOVA_REDIS_COORDINATOR_PASSWORD; never the password")
	user := flags.String("user", "", "the store's ACL user the verbs log in as, e.g. coordinator")
	check := flags.Bool("check", false, "record nothing: print the recorded login and whether its secret resolves (exit 1 when it does not); the password is never shown")
	if helpAsked(args) {
		seatLoginHelp(stdout, verb, flags)
		return 0
	}
	if pos, err := parse(flags, args); err != nil || len(pos) > 0 {
		return refuse(stderr, verb, argErr("takes no words ", err, pos...))
	}
	if a.loginFile == nil {
		return refuse(stderr, verb, "this nova-sprint has no seat login file; run: nova-sprint seat login from the command line")
	}
	path, err := a.loginFile()
	if err != nil {
		return refuse(stderr, verb, "the seat login's file has no place: "+err.Error())
	}
	if *check {
		if *store+*as+*key+*sops+*secret+*user != "" {
			return refuse(stderr, verb, "--check records nothing and takes no other flag")
		}
		return a.seatLoginCheck(path, stdout, stderr)
	}
	l := storeLogin{Redis: strings.TrimSpace(c.redis), User: strings.TrimSpace(*user), Store: *store, As: *as, Key: *key, Sops: *sops, Secret: strings.TrimSpace(*secret)}
	if l.Sops == "" {
		if p, err := exec.LookPath("sops"); err == nil {
			l.Sops = p
		}
	}
	var missing []string
	for _, f := range []struct{ v, flag string }{{l.Redis, "--redis <addr>"}, {l.User, "--user <name>"}} {
		if f.v == "" {
			missing = append(missing, f.flag)
		}
	}
	if m := l.secrets().Missing(); m != "" {
		missing = append(missing, m)
	}
	if len(missing) > 0 {
		return refuse(stderr, verb, "missing "+strings.Join(missing, ", ")+" (no sops on PATH names --sops)")
	}
	if isTwin(l.Redis) {
		return refuse(stderr, verb, "--redis "+l.Redis+" is the in-memory twin, which has no users and wants no login")
	}
	if _, err := redisconn.Resolve(redisconn.Options{Addr: l.Redis}, nil); err != nil {
		return refuse(stderr, verb, "--redis: "+err.Error())
	}
	for _, p := range []*string{&l.Store, &l.Key, &l.Sops} {
		if abs, err := filepath.Abs(*p); err == nil {
			*p = abs // recorded whole, so a verb run from any directory reads the same files
		}
	}
	if _, err := a.secretReader()(l.secrets()); err != nil {
		return refuse(stderr, verb, "nothing was recorded: the password of "+l.User+" does not resolve from "+l.Secret+": "+err.Error()+"; run: nova-sprint seat login again with the store, seat, key and secret that hold it")
	}
	b, _ := json.MarshalIndent(l, "", "  ") // ignored: a struct of strings always encodes
	if err := writePrivate(path, append(b, '\n')); err != nil {
		return refuse(stderr, verb, "the seat login was not recorded: "+err.Error())
	}
	fmt.Fprintf(stdout, "SEAT LOGIN RECORDED file=%s %s resolves=yes\n", oneline.Field(path), l.line())
	return 0
}

// noSeatLogin is the sentence seat login --check prints when none is recorded.
func noSeatLogin(path string) string {
	return "no seat login is recorded at " + path + "; run: nova-sprint seat login --store <dir> --as <seat> --key <file> --secret <NAME> --user <name> --redis <addr>"
}

// seatLoginCheck is seat login --check: the login a bare verb would use, which login
// wins when the environment names one, and whether the secret resolves. The password is
// read and dropped, never shown.
func (a *app) seatLoginCheck(path string, stdout, stderr io.Writer) int {
	l, ok, err := a.recordedLogin()
	if err != nil {
		return refuse(stderr, "seat login", err.Error())
	}
	if !ok {
		return refuse(stderr, "seat login", noSeatLogin(path))
	}
	line := fmt.Sprintf("SEAT LOGIN file=%s %s", oneline.Field(path), l.line())
	if u := a.getenv(redisauth.UserEnv); u != "" {
		line += " wins=env:" + redisauth.UserEnv // the environment's login is used in its place
	}
	if addr := firstEnv(a.getenv, "NOVA_SPRINT_REDIS", "NOVA_REDIS_ADDR"); addr != "" && addr != l.Redis {
		line += " redis-wins=env:" + oneline.Field(addr)
	}
	if _, err := a.readLoginSecret(path, l); err != nil {
		fmt.Fprintln(stdout, line+" resolves=no")
		fmt.Fprintf(stderr, "nova-sprint seat login: %s\n", oneline.Escape(err.Error()))
		return 1
	}
	fmt.Fprintln(stdout, line+" resolves=yes")
	return 0
}

// cmdSeatLogout is seat logout: the recorded login removed, so every verb after it
// opens the store as the environment and its flags say.
func (a *app) cmdSeatLogout(args []string, stdout, stderr io.Writer) int {
	const verb = "seat logout"
	flags, _ := a.verbSetup(verb)
	if helpAsked(args) {
		seatLoginHelp(stdout, verb, flags)
		return 0
	}
	if pos, err := parse(flags, args); err != nil || len(pos) > 0 {
		return refuse(stderr, verb, argErr("takes no words ", err, pos...))
	}
	if a.loginFile == nil {
		return refuse(stderr, verb, "this nova-sprint has no seat login file; run: nova-sprint seat logout from the command line")
	}
	path, err := a.loginFile()
	if err != nil {
		return refuse(stderr, verb, "the seat login's file has no place: "+err.Error())
	}
	switch err := os.Remove(path); {
	case errors.Is(err, fs.ErrNotExist):
		fmt.Fprintf(stdout, "SEAT LOGOUT file=%s was=none\n", oneline.Field(path))
	case err != nil:
		return refuse(stderr, verb, "the seat login "+path+" was not removed: "+err.Error()+"; run: rm "+path)
	default:
		fmt.Fprintf(stdout, "SEAT LOGOUT file=%s was=recorded\n", oneline.Field(path))
	}
	return 0
}

// helpAsked says a word of args asks for help (before a --, where words are taken as
// they are).
func helpAsked(args []string) bool {
	for _, w := range args {
		if w == "--" {
			return false
		}
		if verbflag.IsHelp(w) {
			return true
		}
	}
	return false
}

// seatLoginHelp is seat login's and seat logout's -h: their usage, what they do and
// every flag, on stdout.
func seatLoginHelp(w io.Writer, verb string, flags *flag.FlagSet) {
	fmt.Fprint(w, seatLoginWords(verb))
	if verb == "seat login" {
		fmt.Fprintln(w, "\nflags:")
		flags.SetOutput(w)
		flags.PrintDefaults()
	}
	fmt.Fprint(w, "\nexit: 0 done; 1 --check found the secret does not resolve; 2 usage, or refused (nothing recorded)\n")
}

func seatLoginWords(verb string) string {
	usage := `usage:
  nova-sprint seat login --store <secrets dir> --as <seat> --key <keyfile> --secret <NAME> --user <redis user> --redis <addr> [--sops <path>]
  nova-sprint seat login --check
  nova-sprint seat logout

`
	if verb == "seat logout" {
		return usage + `seat logout removes the recorded seat login (the per-user config file
$XDG_CONFIG_HOME/nova-sprint/login.json, else ~/.config/nova-sprint/login.json).
Every verb after it opens the store as its --redis, NOVA_SPRINT_REDIS* and
NOVA_REDIS_ADDR say. Nothing recorded is not a refusal: it says was=none.
`
	}
	return usage + `seat login records the store login every verb after it uses, in the per-user
config file $XDG_CONFIG_HOME/nova-sprint/login.json (else
~/.config/nova-sprint/login.json), mode 0600: the address, the ACL user, and
where the user's password is in nova-secrets (store, seat, key, sops, the
secret's NAME). The password is never recorded, printed or put in an
environment: each verb reads it in its own process through nova-secrets'
checks (the ones nova-secrets exec makes) when it opens the store, so no
wrapper script runs nova-sprint. A login is recorded only when its secret
resolves.

Which login a verb uses: --redis, else NOVA_SPRINT_REDIS, else NOVA_REDIS_ADDR,
else the recorded address. NOVA_SPRINT_REDIS_USER set is the environment's
login (that user, the variable NOVA_SPRINT_REDIS_PASSWORD_ENV names), which
wins; else the recorded user and secret, when the store opened is the recorded
address. A recorded login whose secret does not resolve, or a file that is not
a login, is a refusal naming the file and the remedy, never a login without a
password.
`
}

// writePrivate writes b to path for its user alone (0600, its directory 0700), whole or
// not at all: a temporary file beside it, renamed over it.
func writePrivate(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".login-*.json")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }() // ignored: gone after the rename; a failed write leaves nothing behind
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close() // ignored: the chmod's error is the one returned
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close() // ignored: the write's error is the one returned
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
