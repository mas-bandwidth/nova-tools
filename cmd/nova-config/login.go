package main

// nova-config login records which Postgres and where its password lives in
// nova-secrets, in the tool's per-user config file. The file holds no secret.
// A bare verb then opens that Postgres with the password read in this process
// through secrets.ReadLogin, which no environment and no output ever holds
// (docs/SPEC-CONFIG.md, "Connecting").

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/secrets"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// loginFileKey is the name a getenv answers with the login file's path. It has
// spaces in it, so no variable of the environment can be it. An empty answer
// means this process has the login off (a test that did not call withLogin).
const loginFileKey = "nova-config login file"

// loginFileErrKey is why the login file has no place, when loginFileKey is empty.
const loginFileErrKey = "nova-config login file error"

// loginRawActorKey is NOVA_FRIEND as the environment set it, before the recorded
// friend fills an empty one. withLogin answers it; a getenv that does not is
// the login off, and NOVA_FRIEND itself is then the raw value.
const loginRawActorKey = "nova-config login raw actor"

// loginReader reads one login's secret. A test registers one; production uses
// secrets.ReadLogin.
type loginReader func(secrets.Login) (secrets.Secret, error)

// loginReaders is keyed by the login file's path, so two tests can each fake
// the secret of their own file.
var loginReaders sync.Map

// loginRecord is what login writes. It holds no secret and cannot: the password
// is read from nova-secrets when a verb connects, and is never one of these fields.
type loginRecord struct {
	DSN    string `json:"dsn"`
	Friend string `json:"friend"`
	Store  string `json:"store"`
	As     string `json:"as"`
	Key    string `json:"key"`
	Sops   string `json:"sops"`
	Secret string `json:"secret"`
}

func (l loginRecord) secrets() secrets.Login {
	return secrets.Login{Store: l.Store, As: l.As, Key: l.Key, Sops: l.Sops, Name: l.Secret}
}

// line is the login on one line, every field a fact and none a secret.
func (l loginRecord) line() string {
	return fmt.Sprintf("dsn=%s friend=%s store=%s as=%s key=%s sops=%s secret=%s",
		oneline.Field(l.DSN), oneline.Field(l.Friend), oneline.Field(l.Store), oneline.Field(l.As),
		oneline.Field(l.Key), oneline.Field(l.Sops), oneline.Field(l.Secret))
}

// withLogin turns the recorded login on for getenv: the file is the per-user
// config file, an empty NOVA_FRIEND is the recorded friend, and read (when
// non-nil) is how that file's secret is read instead of secrets.ReadLogin.
func withLogin(base func(string) string, read loginReader) func(string) string {
	if base == nil {
		base = func(string) string { return "" }
	}
	path, pathErr := loginPath(base)
	errText := ""
	if pathErr != nil {
		errText = pathErr.Error()
		path = ""
	}
	if read != nil && path != "" {
		loginReaders.Store(path, read)
	}
	return func(k string) string {
		switch k {
		case loginFileKey:
			return path
		case loginFileErrKey:
			return errText
		case loginRawActorKey:
			return base(envActor)
		case envActor:
			if v := base(envActor); v != "" {
				return v
			}
			l, ok, err := readLoginFile(path)
			if err != nil || !ok {
				return ""
			}
			return l.Friend
		default:
			return base(k)
		}
	}
}

// loginPath is $XDG_CONFIG_HOME/nova-config/login.json, else
// ~/.config/nova-config/login.json.
func loginPath(getenv func(string) string) (string, error) {
	if x := getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "nova-config", "login.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "nova-config", "login.json"), nil
}

// loginPathFrom is the file this process records into. The login is off when
// getenv does not answer loginFileKey (a test that did not call withLogin).
func loginPathFrom(getenv func(string) string) (string, error) {
	if getenv == nil || (getenv(loginFileKey) == "" && getenv(loginFileErrKey) == "") {
		return "", errors.New("this nova-config has no login file; run: nova-config login from the command line")
	}
	if msg := getenv(loginFileErrKey); msg != "" && getenv(loginFileKey) == "" {
		return "", errors.New("the login's file has no place: " + msg)
	}
	return getenv(loginFileKey), nil
}

// readLoginFile is the login at path. ok is false when none is recorded. A file
// that cannot be read or is not a whole login is an error naming it and the
// remedy, never a login with a field missing.
func readLoginFile(path string) (loginRecord, bool, error) {
	if path == "" {
		return loginRecord{}, false, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return loginRecord{}, false, nil
	}
	remedy := "; run: nova-config logout, then nova-config login again"
	if err != nil {
		return loginRecord{}, false, fmt.Errorf("the login %s is unreadable: %v%s", path, err, remedy)
	}
	var l loginRecord
	if err := json.Unmarshal(b, &l); err != nil {
		return loginRecord{}, false, fmt.Errorf("the login %s is not a login: %v%s", path, err, remedy)
	}
	if strings.TrimSpace(l.DSN) == "" || strings.TrimSpace(l.Friend) == "" {
		return loginRecord{}, false, fmt.Errorf("the login %s names no dsn or no friend%s", path, remedy)
	}
	if m := l.secrets().Missing(); m != "" {
		return loginRecord{}, false, fmt.Errorf("the login %s names no %s%s", path, m, remedy)
	}
	return l, true, nil
}

// loadLogin is the recorded login, or ok false when the login is off or no
// file is recorded. A file that is not a whole login is an error.
func loadLogin(getenv func(string) string) (loginRecord, bool, error) {
	if getenv == nil {
		return loginRecord{}, false, nil
	}
	if msg := getenv(loginFileErrKey); msg != "" && getenv(loginFileKey) == "" {
		return loginRecord{}, false, fmt.Errorf("the login's file has no place: %s", msg)
	}
	if getenv(loginFileKey) == "" {
		return loginRecord{}, false, nil
	}
	return readLoginFile(getenv(loginFileKey))
}

func loginOn(getenv func(string) string) bool {
	return getenv != nil && (getenv(loginFileKey) != "" || getenv(loginFileErrKey) != "")
}

// loginSecret reads the password: the reader registered for this file, else
// nova-secrets' own.
func loginSecret(path string, l secrets.Login) (secrets.Secret, error) {
	if path != "" {
		if f, ok := loginReaders.Load(path); ok {
			return f.(loginReader)(l)
		}
	}
	return secrets.ReadLogin(l)
}

// readRecordedSecret reads the login's password in process. Its refusal names
// the file and the remedy; it is never an empty password.
func readRecordedSecret(path string, rec loginRecord) (secrets.Secret, error) {
	s, err := loginSecret(path, rec.secrets())
	if err != nil || !s.Loaded() || s.Empty() {
		if err == nil {
			err = errors.New("the secret is empty, and an empty value is no password")
		}
		return secrets.Secret{}, fmt.Errorf("the login %s (friend %s, dsn %s) reads its password from %s in seat %s of %s, and it does not resolve: %v; run: nova-config login --check, then nova-config login again or nova-config logout",
			path, rec.Friend, config.Redact(rec.DSN), rec.Secret, rec.As, rec.Store, err)
	}
	return s, nil
}

// resolvePG is the Postgres DSN a verb dials. --pg, NOVA_PG_DSN and
// NOVA_PG_PASSWORD_ENV win when set and the recorded secret is not read for
// them. With none of them, and a login recorded, the DSN is that login's and
// the password is read in process. The login off is the old rule exactly.
func resolvePG(pg string, getenv func(string) string) (string, error) {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	if !loginOn(getenv) {
		if pg == "" && getenv(envPG) == "" {
			return "", pgRequired(false)
		}
		return config.ResolveDSN(pg, getenv)
	}
	rec, ok, err := loadLogin(getenv)
	if err != nil {
		return "", err
	}
	explicit := pg != "" || getenv(envPG) != ""
	passNamed := getenv(envPGPassEnv) != ""
	if explicit || passNamed {
		flag := pg
		if flag == "" && getenv(envPG) == "" {
			if !ok {
				return "", pgRequired(true)
			}
			flag = rec.DSN
		}
		return config.ResolveDSN(flag, getenv)
	}
	if !ok {
		return "", pgRequired(true)
	}
	return dsnWithSecret(getenv(loginFileKey), rec)
}

func pgRequired(withLoginHint bool) error {
	msg := "--pg is required: postgres://user@host:5432/db (or " + envPG + "), or --file <path> for a local file with no database"
	if withLoginHint {
		msg += ", or a login recorded by nova-config login"
	}
	return errors.New(msg)
}

// dsnWithSecret applies the recorded password in memory. The getenv handed to
// ResolveDSN answers only that password, so the process environment is not
// the source and is not changed.
func dsnWithSecret(path string, rec loginRecord) (string, error) {
	s, err := readRecordedSecret(path, rec)
	if err != nil {
		return "", err
	}
	var pw string
	if err := s.Use(func(p string) error { pw = p; return nil }); err != nil {
		return "", err
	}
	return config.ResolveDSN(rec.DSN, func(k string) string {
		if k == config.DefaultPassEnv {
			return pw
		}
		return ""
	})
}

// runLoginTool dispatches login and logout through pkg/tool.
func runLoginTool(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	return loginTool(d).RunContext(ctx, args, os.Stdin, stdout, stderr)
}

func loginTool(d deps) *tool.Tool {
	return &tool.Tool{
		Name:      toolName,
		What:      "a fleet's machines and AI friends as rows in PostgreSQL, applied into Redis",
		ExitTable: "0 done, 1 refused (the verb ran and the store said no), 2 could not run (usage, or a store that did not answer)",
		How: "each kind is a table in PostgreSQL schema config, applied into Redis.\n" +
			"login records the PostgreSQL DSN and where the password is in nova-secrets.\n" +
			"logout removes that recorded login.\n" +
			"The password is read in this process, never recorded and never put in an environment.",
		Verbs: []tool.Verb{
			loginVerb(d),
			logoutVerb(d),
		},
	}
}

func loginVerb(d deps) tool.Verb {
	return tool.Verb{
		Name:      "login",
		Usage:     "login --store <dir> --as <seat> --key <file> --secret <NAME> --dsn <dsn> --actor <name> [--sops <path>]\nlogin --check",
		Detail:    "records the DSN and where the password is; never the password; --dry-run checks the same and resolves the secret, and records nothing",
		Effect:    tool.LocalWrite + ": the login file (0600) under the per-user config directory; --check and --dry-run write nothing",
		ExitTable: "0 done, 1 refused (the verb ran and the secret did not resolve), 2 could not run (usage, or a store that did not answer)",
		DryRun:    true,
		Flags:     loginFlags,
		Run:       func(c *tool.Call) *tool.Out { return runLogin(c, d) },
	}
}

func logoutVerb(d deps) tool.Verb {
	return tool.Verb{
		Name:      "logout",
		Usage:     "logout",
		Detail:    "removes the recorded login; --dry-run says whether one is recorded and removes nothing",
		Effect:    tool.LocalWrite + ": removes the login file; --dry-run writes nothing",
		ExitTable: "0 done, 2 could not run",
		DryRun:    true,
		Flags:     func(f *tool.Flags) { f.Prints() },
		Run:       func(c *tool.Call) *tool.Out { return runLogout(c, d) },
	}
}

func loginFlags(f *tool.Flags) {
	f.Prints()
	f.String("store", "", "the nova-secrets store's working copy the password is read from")
	f.String("as", "", "the seat of that store whose file holds the password")
	f.String("key", "", "the seat's age key file")
	f.String("sops", "", "the sops binary; empty is the sops on PATH, recorded as its path")
	f.String("secret", "", "the NAME of the password in the seat's file; never the password")
	f.String("dsn", "", "the PostgreSQL `dsn` with no password, postgres://user@host:port/db")
	actor := f.String("actor", "", "the `name` a bare write is recorded under when --as and NOVA_FRIEND are unset")
	f.StringVar(actor, "friend", "", "the old spelling of --actor, kept for one release; it sets the same `name`")
	f.Bool("check", false, "record nothing: print the recorded login and whether its secret resolves (exit 1 when it does not); the password is never shown")
}

// runLogin records the store login, or with --check says what a bare verb
// would use and whether the secret resolves.
func runLogin(c *tool.Call, d deps) *tool.Out {
	const verb = "login"
	check, dry := c.Bool("check"), c.DryRun()
	path, err := loginPathFrom(d.getenv)
	if err != nil {
		refuse(c.Stderr, verb, err.Error())
		return tool.Exit(2)
	}
	if check {
		store := c.Str("store")
		as := c.Str("as")
		key := c.Str("key")
		sops := c.Str("sops")
		secret := c.Str("secret")
		dsnFlag := c.Str("dsn")
		actor := c.Str("actor")
		if store+as+key+sops+secret+dsnFlag+actor != "" {
			refuse(c.Stderr, verb, "--check records nothing and takes no other flag")
			return tool.Exit(2)
		}
		return tool.Exit(loginCheck(path, d.getenv, c.Stdout, c.Stderr))
	}
	l := loginRecord{
		DSN:    strings.TrimSpace(c.Str("dsn")),
		Friend: strings.TrimSpace(c.Str("actor")),
		Store:  c.Str("store"),
		As:     strings.TrimSpace(c.Str("as")),
		Key:    c.Str("key"),
		Sops:   c.Str("sops"),
		Secret: strings.TrimSpace(c.Str("secret")),
	}
	if l.Sops == "" {
		if p, err := exec.LookPath("sops"); err == nil {
			l.Sops = p
		}
	}
	var missing []string
	for _, f := range []struct{ v, flag string }{{l.DSN, "--dsn <dsn>"}, {l.Friend, "--actor <name>"}} {
		if f.v == "" {
			missing = append(missing, f.flag)
		}
	}
	if m := l.secrets().Missing(); m != "" {
		missing = append(missing, m)
	}
	if len(missing) > 0 {
		refuse(c.Stderr, verb, "missing "+strings.Join(missing, ", ")+" (no sops on PATH names --sops)")
		return tool.Exit(2)
	}
	if err := config.ValidateName(l.Friend); err != nil {
		refuse(c.Stderr, verb, "--actor: "+err.Error())
		return tool.Exit(2)
	}
	for _, p := range []*string{&l.Store, &l.Key, &l.Sops} {
		if abs, err := filepath.Abs(*p); err == nil {
			*p = abs
		}
	}
	if _, err := config.ResolveDSN(l.DSN, func(string) string { return "" }); err != nil {
		refuse(c.Stderr, verb, "nothing was recorded: --dsn: "+err.Error())
		return tool.Exit(2)
	}
	s, err := loginSecret(path, l.secrets())
	if err != nil || !s.Loaded() || s.Empty() {
		if err == nil {
			err = errors.New("the secret is empty, and an empty value is no password")
		}
		refuse(c.Stderr, verb, "nothing was recorded: the password of "+l.Friend+" does not resolve from "+l.Secret+": "+err.Error()+"; run: nova-config login again with the store, seat, key and secret that hold it")
		return tool.Exit(2)
	}
	if dry {
		// every check the real run makes, the secret resolved and dropped, and no write
		fmt.Fprintf(c.Stdout, "LOGIN DRY-RUN file=%s %s resolves=yes dry_run=true; nothing was recorded\n", oneline.Field(path), l.line())
		return tool.Exit(0)
	}
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		refuse(c.Stderr, verb, "the login was not recorded: "+err.Error())
		return tool.Exit(2)
	}
	if err := writePrivate(path, append(b, '\n')); err != nil {
		refuse(c.Stderr, verb, "the login was not recorded: "+err.Error())
		return tool.Exit(2)
	}
	fmt.Fprintf(c.Stdout, "LOGIN RECORDED file=%s %s resolves=yes\n", oneline.Field(path), l.line())
	if c.Given("friend") {
		fmt.Fprintln(c.Stdout, "NOTE --friend is --actor")
	}
	return tool.Exit(0)
}

// loginCheck prints the recorded login, which environment source would win,
// and whether the secret resolves. The password is read and dropped.
func loginCheck(path string, getenv func(string) string, stdout, stderr io.Writer) int {
	l, ok, err := readLoginFile(path)
	if err != nil {
		return refuse(stderr, "login", err.Error())
	}
	if !ok {
		return refuse(stderr, "login", "no login is recorded at "+path+"; run: nova-config login --store <dir> --as <seat> --key <file> --secret <NAME> --dsn <dsn> --actor <name>")
	}
	line := fmt.Sprintf("LOGIN file=%s %s%s", oneline.Field(path), l.line(), loginWins(getenv))
	if _, err := readRecordedSecret(path, l); err != nil {
		fmt.Fprintln(stdout, line+" resolves=no")
		fmt.Fprintf(stderr, "nova-config login: %s\n", oneline.Escape(err.Error()))
		return 1
	}
	fmt.Fprintln(stdout, line+" resolves=yes")
	return 0
}

// loginWins names an environment source that a bare verb would use instead of
// the recorded login. Values that are not safe to print are not printed.
func loginWins(getenv func(string) string) string {
	if getenv == nil {
		return ""
	}
	var b strings.Builder
	if v := getenv(envPG); v != "" {
		b.WriteString(" dsn-wins=env:" + oneline.Field(config.Redact(v)))
	}
	if v := getenv(envPGPassEnv); v != "" {
		name := envPGPassEnv
		if isEnvWord(v) {
			name = v
		}
		b.WriteString(" password-wins=env:" + oneline.Field(name))
	}
	if v := rawActor(getenv); v != "" {
		b.WriteString(" friend-wins=env:" + oneline.Field(v))
	}
	return b.String()
}

func rawActor(getenv func(string) string) string {
	if v := getenv(loginRawActorKey); v != "" {
		return v
	}
	if getenv(loginFileKey) != "" {
		return "" // the probe is how withLogin reports the raw actor; empty means unset
	}
	return getenv(envActor)
}

func isEnvWord(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '_' && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// runLogout removes the recorded login.
func runLogout(c *tool.Call, d deps) *tool.Out {
	const verb = "logout"
	dry := c.DryRun()
	path, err := loginPathFrom(d.getenv)
	if err != nil {
		refuse(c.Stderr, verb, err.Error())
		return tool.Exit(2)
	}
	if dry {
		was := "recorded"
		if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
			was = "none"
		}
		fmt.Fprintf(c.Stdout, "LOGOUT DRY-RUN file=%s was=%s dry_run=true; nothing was removed\n", oneline.Field(path), was)
		return tool.Exit(0)
	}
	switch err := os.Remove(path); {
	case errors.Is(err, os.ErrNotExist):
		fmt.Fprintf(c.Stdout, "LOGOUT file=%s was=none\n", oneline.Field(path))
	case err != nil:
		refuse(c.Stderr, verb, "the login "+path+" was not removed: "+err.Error()+"; run: rm "+path)
		return tool.Exit(2)
	default:
		fmt.Fprintf(c.Stdout, "LOGOUT file=%s was=recorded\n", oneline.Field(path))
	}
	return tool.Exit(0)
}

// writePrivate writes b to path for its user alone (0600, its directory 0700),
// whole or not at all: a temporary file beside it, renamed over it.
func writePrivate(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".login-*.json")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }() // ignored: temp file cleanup
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close() // ignored: the chmod error is the one returned
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close() // ignored: the write error is the one returned
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
