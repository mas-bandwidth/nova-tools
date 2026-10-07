package testredis

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The unit tier of the package: what Start decides before it starts anything.
// No test here starts a process.

func TestTheCommandLineIsLoopbackKeepsNothingAndStaysInTheTestsDirectory(t *testing.T) {
	t.Parallel()

	got := arguments("the-dir", "50123", []string{"--user", "default", "off"})
	want := []string{"--bind", "127.0.0.1", "--port", "50123", "--save", "", "--appendonly", "no", "--dir", "the-dir", "--user", "default", "off"}
	if !slices.Equal(got, want) {
		require.Equal(t, want, got, "arguments:\n got %q\nwant %q", got, want)
	}
	if got := arguments("d", "1", nil); len(got) != 10 {
		require.Len(t, got, 10, "with no extra arguments the command line is the ten fixed words; got %q", got)
	}
}

func TestStartRefusesTheArgumentsThatAreItsOwn(t *testing.T) {
	t.Parallel()

	refused := map[string][]string{
		"bind":                  {"--bind", "0.0.0.0"},
		"BIND":                  {"--BIND", "0.0.0.0"},
		"bind after a user":     {"--user", "default", "off", "--bind", "0.0.0.0"},
		"bind and its value":    {"--bind 0.0.0.0"},
		"bind and a tab":        {"--bind\t0.0.0.0"},
		"bind on a second line": {"--maxclients 10\nbind 0.0.0.0"},
		"port on a second line": {"--maxclients 10\r\n\r\n  port 1"},
		"dir after a blank one": {"--maxclients 10\n \t \ndir elsewhere"},
		"port":                  {"--port", "6379"},
		"tls-port":              {"--tls-port", "6380"},
		"unixsocket":            {"--unixsocket", "s"},
		"dir":                   {"--dir", "elsewhere"},
		"include":               {"--include", "redis.conf"},
		"logfile":               {"--logfile", "log"},
		"loglevel":              {"--loglevel", "warning"},
		"daemonize":             {"--daemonize", "yes"},
		"supervised":            {"--supervised", "systemd"},
	}
	for name, extra := range refused {
		err := check(extra)
		option := strings.ToLower(strings.Fields(name)[0])
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "--"+option+" is refused") {
			assert.Failf(t, "", "%s: check(%q) = %v; want a refusal that names --%s", name, extra, err, option)
		}
	}
	for option := range own {
		if err := check([]string{"--" + option, "x"}); err == nil {
			assert.Error(t, err, "--%s is Start's own and check lets it through", option)
		}
	}
	for _, extra := range [][]string{{"redis.conf"}, {"-"}, {"yes", "--appendonly"}} {
		if err := check(extra); err == nil || !strings.Contains(err.Error(), "not an option") {
			assert.Failf(t, "", "check(%q) = %v; want a refusal: the extra arguments open with an option", extra, err)
		}
	}
	accepted := [][]string{
		nil,
		{},
		User("bench", "pw"),
		{"--appendonly", "yes"},
		{"--save", "60", "1"},
		{"--aclfile", "users.acl"},
		{"--maxmemory", "1mb", "--maxmemory-policy", "allkeys-lru"},
		{"--"},
		{"--\n"},
		{"-- \n\t\nmaxclients 10"},
	}
	for _, extra := range accepted {
		if err := check(extra); err != nil {
			assert.NoError(t, err, "check(%q) = %v; want it accepted", extra, err)
		}
	}
}

// A refused argument fails the test before anything is looked up or started.
func TestARefusedArgumentFailsTheTestBeforeAnythingStarts(t *testing.T) {
	t.Parallel()

	l := real
	l.sentry = standing()
	looked := false
	l.look = func(string) (string, error) { looked = true; return "redis-server", nil }
	r := provoke(t, func(tb testing.TB) { l.start(tb, []string{"--bind", "0.0.0.0"}) })
	if !strings.Contains(r.fatal, "--bind is refused") || !strings.Contains(r.fatal, "127.0.0.1") {
		require.Failf(t, "", "Start with --bind: failed with %q; want the refusal and what Start listens on", r.fatal)
	}
	if looked {
		require.False(t, looked, "Start looked for redis-server after it had refused the arguments")
	}
}

func TestUserIsTheDefaultUserOffAndOneUserWithAPassword(t *testing.T) {
	t.Parallel()

	got := User("bench", "the-password")
	want := []string{"--user", "default", "off", "--user", "bench", "on", ">the-password", "~*", "&*", "+@all"}
	if !slices.Equal(got, want) {
		require.Equal(t, want, got, "User:\n got %q\nwant %q", got, want)
	}
	if err := check(got); err != nil {
		require.NoError(t, err, "Start refuses what User returns: %v", err)
	}
}

func TestAFailureShowsTheArgumentsAndNoPassword(t *testing.T) {
	t.Parallel()

	args := append(arguments("d", "1", User("bench", "secret-one")),
		"<secret-two", "#secret-three", "!secret-four",
		"--requirepass", "secret-five", "--masterauth", "secret-six",
		"--requirepass secret-seven", "--masteruser\tsecret-eight",
		"--maxmemory", "1mb")
	before := slices.Clone(args)
	got := commandLine(redact(args))
	want := `"--bind" "127.0.0.1" "--port" "1" "--save" "" "--appendonly" "no" "--dir" "d" ` +
		`"--user" "default" "off" "--user" "bench" "on" "***" "~*" "&*" "+@all" ` +
		`"***" "***" "***" "--requirepass" "***" "--masterauth" "***" ` +
		`"--requirepass ***" "--masteruser ***" "--maxmemory" "1mb"`
	if got != want {
		require.Equal(t, want, got, "the arguments of a failure:\n got %s\nwant %s", got, want)
	}
	if strings.Contains(got, "secret") {
		require.NotContains(t, got, "secret", "a password is in the arguments of a failure: %s", got)
	}
	if !slices.Equal(args, before) {
		require.Equal(t, before, args, "redact changed the arguments it was given")
	}
}

func TestTheOutputIsReadForTheReadyLineAndItsEndIsKept(t *testing.T) {
	t.Parallel()

	open := func(w *tail) bool {
		select {
		case <-w.ready:
			return false
		default:
			return true
		}
	}

	// The line split over two writes, then printed again.
	w := &tail{ready: make(chan struct{})}
	write := func(part string) {
		t.Helper()
		if n, err := w.Write([]byte(part)); n != len(part) || err != nil {
			require.Failf(t, "", "Write = %d, %v", n, err)
		}
	}
	write("1:M * Server initialized\n1:M * Ready to acc")
	if !open(w) {
		require.Failf(t, "", "ready before the line was whole; the output so far: %q", w)
	}
	write("ept connections tcp\n")
	if open(w) {
		require.Failf(t, "", "the ready line is whole and ready is not closed; the output: %q", w)
	}
	write("1:M * Ready to accept connections tcp\n")
	if open(w) {
		require.Fail(t, "ready opened again")
	}
	if got, want := w.String(), "1:M * Server initialized\n1:M * Ready to accept connections tcp\n1:M * Ready to accept connections tcp\n"; got != want {
		require.Equal(t, want, got, "the output kept:\n got %q\nwant %q", got, want)
	}

	// More output than is kept: the end is kept, and a ready line that the
	// same write carried past the cut was still read.
	long := &tail{ready: make(chan struct{})}
	if _, err := long.Write([]byte(readyLine + strings.Repeat("x", tailSize) + "the end")); err != nil {
		require.NoError(t, err, err)
	}
	if open(long) {
		require.Fail(t, "the ready line at the head of a long write was not read")
	}
	if got := long.String(); len(got) != tailSize || !strings.HasSuffix(got, "xthe end") || strings.Contains(got, readyLine) {
		require.Failf(t, "", "kept %d bytes ending %q; want the last %d", len(got), got[len(got)-8:], tailSize)
	}

	// Output that never says ready.
	mute := &tail{ready: make(chan struct{})}
	if _, err := mute.Write([]byte("unit tier: redis-server is functional-only\n")); err != nil {
		require.NoError(t, err, err)
	}
	if !open(mute) {
		require.Fail(t, "ready without the ready line")
	}
}

func TestAMissingRedisServerSkipsOnALaptopAndFailsUnderCI(t *testing.T) {
	t.Parallel()

	cause := errors.New(`exec: "redis-server": executable file not found in $PATH`)
	env := func(ci string) func(string) string {
		return func(key string) string {
			if key == CIEnv {
				return ci
			}
			return ""
		}
	}
	for ci, skips := range map[string]bool{"": true, "0": true, "true": true, "1": false} {
		r := provoke(t, func(tb testing.TB) { absent(tb, cause, env(ci)) })
		switch {
		case skips && (r.fatal != "" || !strings.Contains(r.skipped, "redis-server unavailable") || !strings.Contains(r.skipped, cause.Error())):
			assert.Failf(t, "", "NOVA_CI=%q: skipped with %q, failed with %q; want a skip that carries the cause", ci, r.skipped, r.fatal)
		case !skips && (r.skipped != "" || !strings.Contains(r.fatal, "redis-server is required under NOVA_CI=1") || !strings.Contains(r.fatal, cause.Error())):
			assert.Failf(t, "", "NOVA_CI=%q: skipped with %q, failed with %q; want a failure that carries the cause", ci, r.skipped, r.fatal)
		}
	}
	r := provoke(t, func(tb testing.TB) { absent(tb, nil, env("")) })
	if !strings.Contains(r.fatal, "Absent requires the error") || r.skipped != "" {
		require.Failf(t, "", "Absent with no cause: skipped with %q, failed with %q; want a failure", r.skipped, r.fatal)
	}

	// Program and Start give the same answer, from the same lookup.
	l := real
	l.sentry = standing()
	l.look = func(file string) (string, error) {
		if file != "redis-server" {
			assert.Equal(t, "redis-server", file, "looked for %q; want redis-server", file)
		}
		return "", cause
	}
	for name, call := range map[string]func(testing.TB){
		"Program": func(tb testing.TB) { l.program(tb) },
		"Start":   func(tb testing.TB) { l.start(tb, nil) },
	} {
		l.getenv = env("")
		if r := provoke(t, call); r.fatal != "" || !strings.Contains(r.skipped, "redis-server unavailable") {
			assert.Failf(t, "", "%s with no redis-server: skipped with %q, failed with %q; want a skip", name, r.skipped, r.fatal)
		}
		l.getenv = env("1")
		if r := provoke(t, call); r.skipped != "" || !strings.Contains(r.fatal, "redis-server is required under NOVA_CI=1") {
			assert.Failf(t, "", "%s with no redis-server under NOVA_CI=1: skipped with %q, failed with %q; want a failure", name, r.skipped, r.fatal)
		}
	}
	l.look = func(string) (string, error) { return "the-program", nil }
	if got := l.program(t); got != "the-program" {
		require.Equal(t, "the-program", got, "Program = %q; want what the lookup found", got)
	}
}

// The exported Absent and Program read the real environment and the real
// PATH. Whatever this machine has, the answer is one of the documented three.
func TestAbsentAndProgramAnswerOnThisMachine(t *testing.T) {
	t.Parallel()

	r := provoke(t, func(tb testing.TB) { Absent(tb, errors.New("not found")) })
	if (r.fatal == "") == (r.skipped == "") {
		require.Failf(t, "", "Absent skipped with %q and failed with %q; want exactly one", r.skipped, r.fatal)
	}
	var bin string
	r = provoke(t, func(tb testing.TB) { bin = Program(tb) })
	if found := bin != ""; found == (r.fatal != "" || r.skipped != "") {
		require.Failf(t, "", "Program = %q, skipped with %q, failed with %q; want a program or one answer", bin, r.skipped, r.fatal)
	}
}

func TestThePackageRefusesToRunOutsideATestBinary(t *testing.T) {
	t.Parallel()

	l := real
	l.inTest = func() bool { return false }
	l.sentry = standing()
	l.look = func(string) (string, error) {
		assert.Fail(t, "looked for redis-server outside a test binary")
		return "", errors.New("unreachable")
	}
	for name, call := range map[string]func(){
		"Start":          func() { l.start(t, nil) },
		"Program":        func() { l.program(t) },
		"FreePort":       func() { l.freePort(t) },
		"CommandCounter": func() { l.counter(t, net.Listen) },
	} {
		said, _ := panics(call).(string)
		if !strings.Contains(said, "outside a test binary") {
			assert.Contains(t, said, "outside a test binary", "%s outside a test binary panicked with %q; want the refusal", name, said)
		}
	}
	if !real.inTest() {
		require.Fail(t, "this is a test binary and the package does not know it")
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
		require.NoError(t, err, err)
	}
	if !slices.Equal(asked, []string{"tcp 127.0.0.1:0"}) {
		require.Failf(t, "", "the port was asked for as %q; want tcp 127.0.0.1:0, loopback and the kernel's choice", asked)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		require.Failf(t, "", "freePort = %q; want a port", port)
	}
	if n, err := strconv.Atoi(FreePort(t)); err != nil || n < 1 || n > 65535 {
		require.Failf(t, "", "FreePort = %d, %v; want a port", n, err)
	}

	full := errors.New("no descriptors left")
	if _, err := freePort(func(string, string) (net.Listener, error) { return nil, full }); !errors.Is(err, full) {
		require.Failf(t, "", "freePort with no listener = %v; want the cause", err)
	}
	l := real
	l.sentry = standing()
	l.look = func(string) (string, error) { return "redis-server", nil }
	l.port = func() (string, error) { return "", full }
	for name, call := range map[string]func(testing.TB){
		"FreePort": func(tb testing.TB) { l.freePort(tb) },
		"Start":    func(tb testing.TB) { l.start(tb, nil) },
	} {
		if r := provoke(t, call); !strings.Contains(r.fatal, full.Error()) {
			assert.Failf(t, "", "%s with no port failed with %q; want the cause", name, r.fatal)
		}
	}
}

func TestNoServerIsStartedWithoutItsSentry(t *testing.T) {
	t.Parallel()

	// A sentry that cannot be enlisted: asked once, and the answer stands.
	asked := 0
	none := &sentry{enlist: func() (*post, error) { asked++; return nil, errors.New("the test binary was not found") }}
	for i := 0; i < 3; i++ {
		if _, err := none.group(); err == nil || !strings.Contains(err.Error(), "the test binary was not found") {
			require.Failf(t, "", "group() with no sentry = %v; want the cause", err)
		}
	}
	if asked != 1 {
		require.EqualValues(t, 1, asked, "the sentry was enlisted %d times; want once", asked)
	}

	// A sentry that stands: enlisted once, however many tests start at once.
	asked = 0
	stands := &sentry{enlist: func() (*post, error) { asked++; return &post{group: 4242, gone: make(chan struct{})}, nil }}
	var wg sync.WaitGroup
	groups := make([]int, 50)
	for i := range groups {
		wg.Add(1)
		go func() {
			defer wg.Done()
			groups[i], _ = stands.group()
		}()
	}
	wg.Wait()
	if asked != 1 || slices.ContainsFunc(groups, func(g int) bool { return g != 4242 }) {
		require.Failf(t, "", "enlisted %d times, groups %v; want once and 4242 for every test", asked, groups)
	}

	// A sentry that has ended since.
	gone := make(chan struct{})
	close(gone)
	ended := &sentry{enlist: func() (*post, error) { return &post{group: 4242, gone: gone}, nil }}
	if _, err := ended.group(); err == nil || !strings.Contains(err.Error(), "the sentry has ended") {
		require.Failf(t, "", "group() with a sentry that has ended = %v; want a refusal", err)
	}

	// Start says so, and looks up the program first: a machine with no
	// redis-server skips, it does not fail on a sentry.
	l := real
	l.sentry = ended
	l.look = func(string) (string, error) { return "redis-server", nil }
	if r := provoke(t, func(tb testing.TB) { l.start(tb, nil) }); !strings.Contains(r.fatal, "no server is started without its sentry") || !strings.Contains(r.fatal, "the sentry has ended") {
		require.Failf(t, "", "Start with a sentry that has ended failed with %q; want the refusal", r.fatal)
	}
}

// held is a standard input that tells the test when it is being read and
// ends when the test says so.
type held struct {
	reading chan struct{}
	end     chan struct{}
	once    sync.Once
}

func (h *held) Read([]byte) (int, error) {
	h.once.Do(func() { close(h.reading) })
	<-h.end
	return 0, io.EOF
}

type failing struct{ err error }

func (f failing) Write([]byte) (int, error) { return 0, f.err }

func (f failing) Read([]byte) (int, error) { return 0, f.err }

func TestTheSentryKillsWhenItsInputEndsAndNotBefore(t *testing.T) {
	t.Parallel()

	in := &held{reading: make(chan struct{}), end: make(chan struct{})}
	var out bytes.Buffer
	var mu sync.Mutex
	kills := 0
	kill := func() error {
		mu.Lock()
		defer mu.Unlock()
		kills++
		return nil
	}
	killed := func() int {
		mu.Lock()
		defer mu.Unlock()
		return kills
	}
	code := make(chan int, 1)
	go func() { code <- stand(in, &out, kill) }()
	<-in.reading
	// The sentry is inside its read: it has said it stands, and the test
	// binary is alive.
	if out.String() != sentryStands {
		require.Failf(t, "", "a sentry that reads has said %q; want %q", out.String(), sentryStands)
	}
	if n := killed(); n != 0 {
		require.Zero(t, n, "the sentry killed %d times while its input was open", n)
	}
	close(in.end)
	if got := <-code; got != 0 || killed() != 1 {
		require.Failf(t, "", "input ended: stand = %d after %d kills; want 0 after one", got, killed())
	}

	// An input that breaks is an input that ended.
	kills = 0
	if got := stand(failing{errors.New("the pipe broke")}, io.Discard, kill); got != 0 || kills != 1 {
		require.Failf(t, "", "input broke: stand = %d after %d kills; want 0 after one", got, kills)
	}
	// A kill that fails, and a sentry nobody listens to, are reported.
	if got := stand(strings.NewReader(""), io.Discard, func() error { return errors.New("not permitted") }); got != 1 {
		require.EqualValues(t, 1, got, "the kill failed: stand = %d; want 1", got)
	}
	kills = 0
	if got := stand(strings.NewReader(""), failing{errors.New("closed")}, kill); got != 1 || kills != 0 {
		require.Failf(t, "", "nobody reads the sentry: stand = %d after %d kills; want 1 after none", got, kills)
	}
}

// answering is a loopback listener that reads one line from every client and
// answers with reply; an empty reply hangs up.
func answering(t *testing.T, reply string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		require.NoError(t, err, err)
	}
	done := make(chan struct{})
	t.Cleanup(func() {
		_ = ln.Close()
		<-done
	})
	go func() {
		defer close(done)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			if line, err := bufio.NewReader(c).ReadString('\n'); err != nil || line != "PING\r\n" {
				assert.Failf(t, "", "the client sent %q, %v; want one PING", line, err)
			}
			_, _ = io.WriteString(c, reply)
			_ = c.Close()
		}
	}()
	return ln.Addr().String()
}

func TestPingTakesPongAndNoauthForAServer(t *testing.T) {
	t.Parallel()

	until := time.Now().Add(time.Minute)
	for reply, want := range map[string]string{
		"+PONG\r\n":                             "",
		"-NOAUTH Authentication required.\r\n":  "",
		"-ERR unknown command 'PING'\r\n":       `it answered PING with "-ERR unknown command 'PING'"`,
		"-DENIED Redis is running in protected": "EOF",
		"":                                      "EOF",
	} {
		err := ping(answering(t, reply), until)
		switch {
		case want == "" && err != nil:
			assert.Failf(t, "", "answer %q: ping = %v; want a server", reply, err)
		case want != "" && (err == nil || !strings.Contains(err.Error(), want)):
			assert.Failf(t, "", "answer %q: ping = %v; want an error with %q", reply, err, want)
		}
	}
	// Nothing can listen there: port 0 is no port to connect to, so no
	// parallel test can take it and answer, as it can a port freed a moment
	// ago. The dial's own error is ping's.
	if err := ping("127.0.0.1:0", until); err == nil || !strings.HasPrefix(err.Error(), "dial tcp 127.0.0.1:0: ") {
		require.Failf(t, "", "ping to port 0 = %v; want the dial's error", err)
	}
}
