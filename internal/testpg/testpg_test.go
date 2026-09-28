package testpg

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The unit tier of the package: what StartServer decides before it starts
// anything. No test here starts a process or opens a connection.

func TestDSNIsLoopbackTrustAndNoPassword(t *testing.T) {
	t.Parallel()

	s := &Server{User: "postgres", Port: "54321"}
	if got, want := s.DSN("t1_7"), "postgres://postgres@127.0.0.1:54321/t1_7?sslmode=disable"; got != want {
		t.Fatalf("DSN = %q; want %q", got, want)
	}
}

func TestTheChildsEnvironmentCannotPointAtARealCluster(t *testing.T) {
	t.Parallel()

	machine := env("PATH", "the-path", "HOME", "the-home", "TMPDIR", "the-tmp", "SYSTEMROOT", "the-root",
		"PGDATA", "a-real-cluster", "PGHOST", "a-real-host", "PGPORT", "5432", "PGUSER", "a-real-user", "PGPASSWORD", "a-real-password")
	for goos, want := range map[string][]string{
		"darwin":  {"PATH=the-path", "HOME=the-home", "LC_ALL=C", "LANG=C", "TMPDIR=the-tmp"},
		"linux":   {"PATH=the-path", "HOME=the-home", "LC_ALL=C", "LANG=C", "TMPDIR=the-tmp"},
		"windows": {"PATH=the-path", "HOME=the-home", "LC_ALL=C", "LANG=C", "SYSTEMROOT=the-root", "TMPDIR=the-tmp"},
	} {
		if got := cleanEnv(machine, goos); !slices.Equal(got, want) {
			t.Errorf("%s: the child's environment:\n got %q\nwant %q", goos, got, want)
		}
	}
	if got, want := cleanEnv(env("PATH", "p"), "linux"), []string{"PATH=p", "HOME=", "LC_ALL=C", "LANG=C"}; !slices.Equal(got, want) {
		t.Errorf("with no TMPDIR:\n got %q\nwant %q", got, want)
	}
}

func TestBinariesLooksInTheNamedDirectoryThenPathThenTheKnownInstalls(t *testing.T) {
	t.Parallel()

	whole := install(t, programs...)
	other := install(t, programs...)
	partial := install(t, "initdb", "pg_ctl")
	empty := install(t)
	nowhere := func(string) (string, error) { return "", errors.New("not found") }
	onPath := func(dir string) func(string) (string, error) {
		return func(file string) (string, error) {
			if file != "pg_ctl" {
				t.Errorf("looked for %q on PATH; want pg_ctl", file)
			}
			return filepath.Join(dir, file), nil
		}
	}
	// A bin directory of links into the install, the way Homebrew makes one.
	links := t.TempDir()
	linked := true
	for _, program := range programs {
		if err := os.Symlink(filepath.Join(whole, program), filepath.Join(links, program)); err != nil {
			linked = false
		}
	}
	same := func(a, b string) bool {
		x, err1 := os.Stat(a)
		y, err2 := os.Stat(b)
		return err1 == nil && err2 == nil && os.SameFile(x, y)
	}

	for name, c := range map[string]struct {
		l    launch
		want string
		err  []string
	}{
		"the named directory": {
			launch{getenv: env(BinEnv, whole), look: onPath(other), known: []string{other}}, whole, nil},
		"the named directory lacks postgres": {
			launch{getenv: env(BinEnv, partial), look: onPath(whole), known: []string{whole}}, "",
			[]string{BinEnv + "=" + partial + " holds no postgres"}},
		"the named directory is empty": {
			launch{getenv: env(BinEnv, empty), look: onPath(whole), known: []string{whole}}, "",
			[]string{BinEnv + "=" + empty + " holds no initdb"}},
		"PATH": {
			launch{getenv: env(), look: onPath(whole), known: []string{other}}, whole, nil},
		"PATH, an install that is not whole": {
			launch{getenv: env(), look: onPath(partial), known: []string{empty, other}}, other, nil},
		"PATH, a program that is not there": {
			launch{getenv: env(), look: onPath(filepath.Join(empty, "gone")), known: []string{other}}, other, nil},
		"a known install": {
			launch{getenv: env(), look: nowhere, known: []string{empty, partial, other, whole}}, other, nil},
		"nowhere": {
			launch{getenv: env(), look: nowhere, known: []string{empty, partial}}, "",
			[]string{"pg_ctl (with initdb and postgres) is not on PATH", BinEnv, empty + ", " + partial, "install-postgres.sh"}},
	} {
		got, err := c.l.binaries()
		if c.err == nil && (err != nil || !same(got, c.want)) {
			t.Errorf("%s: Binaries = %q, %v; want %q", name, got, err, c.want)
		}
		for _, want := range c.err {
			if got != "" || err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("%s: Binaries = %q, %v; want an error with %q", name, got, err, want)
			}
		}
	}
	if linked {
		l := launch{getenv: env(), look: onPath(links), known: nil}
		if got, err := l.binaries(); err != nil || !same(got, whole) {
			t.Errorf("PATH, links into an install: Binaries = %q, %v; want the install %q, not the links %q", got, err, whole, links)
		}
	}
	for program, want := range map[string]string{"initdb": "initdb", "pg_ctl": "pg_ctl", "postgres": "postgres"} {
		var rest []string
		for _, p := range programs {
			if p != program {
				rest = append(rest, p)
			}
		}
		if got := lacks(install(t, rest...)); got != want {
			t.Errorf("an install without %s lacks %q", program, got)
		}
	}
}

func TestStartWithNoPostgresFailsTheTestAndNamesTheBinary(t *testing.T) {
	t.Parallel()

	l := real
	l.getenv = env(BinEnv, install(t, "initdb"))
	l.port = func() (string, error) {
		t.Error("a port was taken for a server that has no binaries")
		return "", errors.New("unreachable")
	}
	if s, err := l.startServer(t.TempDir()); s != nil || err == nil || !strings.Contains(err.Error(), "holds no pg_ctl") {
		t.Fatalf("StartServer with no pg_ctl = %v, %v; want an error that names it", s, err)
	}
	r := provoke(t, func(tb testing.TB) { l.start(tb) })
	if !strings.Contains(r.fatal, "throwaway postgres: "+BinEnv+"=") || !strings.Contains(r.fatal, "holds no pg_ctl") {
		t.Fatalf("Start with no pg_ctl failed with %q; want one line naming the binary", r.fatal)
	}
}

func TestThePackageRefusesToRunOutsideATestBinary(t *testing.T) {
	t.Parallel()

	l := real
	l.inTest = func() bool { return false }
	l.getenv = func(string) string {
		t.Error("the environment was read outside a test binary")
		return ""
	}
	for name, call := range map[string]func(){
		"StartServer": func() { _, _ = l.startServer(t.TempDir()) },
		"Start":       func() { l.start(t) },
	} {
		said, _ := panics(call).(string)
		if !strings.Contains(said, "outside a test binary") {
			t.Errorf("%s outside a test binary panicked with %q; want the refusal", name, said)
		}
	}
	if !real.inTest() {
		t.Fatal("this is a test binary and the package does not know it")
	}
}

func TestAFreePortIsALoopbackPortTheKernelChose(t *testing.T) {
	t.Parallel()

	var asked []string
	port, err := freePort(func(network, address string) (net.Listener, error) {
		asked = append(asked, network+" "+address)
		return net.Listen(network, address)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(asked, []string{"tcp 127.0.0.1:0"}) {
		t.Fatalf("the port was asked for as %q; want tcp 127.0.0.1:0, loopback and the kernel's choice", asked)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		t.Fatalf("freePort = %q; want a port", port)
	}
	if port, err := real.port(); err != nil || port == "" {
		t.Fatalf("the port StartServer takes = %q, %v", port, err)
	}
	full := errors.New("no descriptors left")
	if _, err := freePort(func(string, string) (net.Listener, error) { return nil, full }); err == nil || !strings.Contains(err.Error(), full.Error()) {
		t.Fatalf("freePort with no listener = %v; want the cause", err)
	}
}

// A server whose address is not one: the connection string does not parse,
// and nothing is dialled.
func TestAServerWithNoAddressIsAnErrorBeforeAnythingIsDialled(t *testing.T) {
	t.Parallel()

	s := &Server{User: "postgres", Port: "not a port"}
	if err := s.ready(time.Minute); err == nil {
		t.Fatal("ready on a server with no port = nil; want the connection string refused")
	}
	if r := provoke(t, func(tb testing.TB) { s.Database(tb) }); r.fatal == "" {
		t.Fatal("Database on a server with no port returned; want the test failed")
	}
}
