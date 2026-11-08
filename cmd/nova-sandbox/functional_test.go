//go:build functional

package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// childArg gates the helper child: the test binary re-executed with it as its only
// argument exits 0 at once, standing in for /bin/true so no test needs a host binary.
const childArg = "nova-sandbox-test-child"

// child is the path of the test binary itself, run inside the wall as a no-op command.
var child = os.Args[0]

// withChild appends the no-op child to a wall's arguments, granting --read of the
// test binary's directory, which is under no system root (a build cache on CI).
func withChild(args ...string) []string {
	return append(slices.Clone(args), "--read", filepath.Dir(child), "--", child, childArg)
}

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == childArg {
		os.Exit(0)
	}
	os.Exit(m.Run())
}

var (
	buildOnce sync.Once
	binary    string
	buildErr  error
)

// bin builds nova-sandbox once for the package's functional tests.
func bin(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "nova-sandbox-bin-")
		if err != nil {
			buildErr = err
			return
		}
		binary = filepath.Join(dir, "nova-sandbox")
		out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput()
		if err != nil {
			buildErr = &buildError{string(out)}
		}
	})
	require.NoError(t, buildErr)
	return binary
}

type buildError struct{ out string }

func (e *buildError) Error() string { return e.out }

// sandbox runs nova-sandbox with args from dir; returns exit code, stdout, stderr.
func sandbox(t *testing.T, dir string, args ...string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(bin(t), args...)
	cmd.Env, cmd.Dir = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "TMPDIR=" + dir}, dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return code, out.String(), errb.String()
}

// scratch is a resolved directory with job/ (the write) and out/ beside it.
func scratch(t *testing.T) (job, out string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	job, out = filepath.Join(root, "job"), filepath.Join(root, "out")
	require.NoError(t, os.Mkdir(job, 0o700))
	require.NoError(t, os.Mkdir(out, 0o700))
	return
}

// linuxOnly skips a test or step that checks the Landlock and seccomp wall: the
// macOS sandbox-exec profile fences other things (README.md, Reference).
func linuxOnly(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("checks the Linux wall; this is " + runtime.GOOS)
	}
}

func TestCheckAndProbe(t *testing.T) {
	t.Parallel()
	code, out, _ := sandbox(t, os.TempDir(), "check")
	require.Equal(t, 0, code, out)
	assert.Contains(t, out, "CHECK OK backend=")
	assert.Contains(t, out, " note=")
	code, out, _ = sandbox(t, os.TempDir(), "probe")
	require.Equal(t, 0, code, out)
	assert.Contains(t, out, "PROBE OK")
	assert.Contains(t, out, "name=write_outside expect=deny got=deny")
	assert.Contains(t, out, "name=read_named expect=allow got=allow")
	assert.Contains(t, out, "name=unix_deny expect=deny got=deny")
	steps := []string{"net_deny expect=deny got=deny", "tcp_allow expect=allow got=allow",
		"udp_control expect=allow got=allow", "udp_deny expect=deny got=deny", "tcp_other_deny expect=deny got=deny",
		"udp_deny_allowmode expect=deny got=deny"}
	if runtime.GOOS == "linux" { // the macOS probe has no unix_allow or listen steps
		steps = append(steps, "unix_allow expect=allow got=allow", "listen_control expect=allow got=allow", "listen_deny expect=deny got=deny")
	}
	for _, step := range steps {
		assert.Contains(t, out, "name="+step+"\n")
	}
	if !strings.Contains(out, "name=dns_deny expect=deny got=untested") {
		assert.Contains(t, out, "name=dns_control expect=allow got=allow\n")
		assert.Contains(t, out, "name=dns_deny expect=deny got=deny\n")
	}
}

func TestWriteInsideAllowedOutsideRefused(t *testing.T) {
	t.Parallel()
	job, out := scratch(t)
	code, _, stderr := sandbox(t, job, "--write", job, "--", "/bin/sh", "-c", "echo hi > "+filepath.Join(job, "ok"))
	require.Equal(t, 0, code, stderr)
	assert.FileExists(t, filepath.Join(job, "ok"))

	code, _, _ = sandbox(t, job, "--write", job, "--", "/bin/sh", "-c", "echo hi > "+filepath.Join(out, "bad"))
	assert.NotEqual(t, 0, code)
	assert.NoFileExists(t, filepath.Join(out, "bad"), "the kernel refused the write")
}

func TestReadOutsideRefusedUnlessNamed(t *testing.T) {
	t.Parallel()
	job, out := scratch(t)
	secret := filepath.Join(out, "secret")
	require.NoError(t, os.WriteFile(secret, []byte("s"), 0o600))
	code, stdout, _ := sandbox(t, job, "--write", job, "--", "/bin/cat", secret)
	assert.NotEqual(t, 0, code)
	assert.Empty(t, stdout)
	code, stdout, stderr := sandbox(t, job, "--write", job, "--read", out, "--", "/bin/cat", secret)
	require.Equal(t, 0, code, stderr)
	assert.Equal(t, "s", stdout)
	// --read is not --write
	code, _, _ = sandbox(t, job, "--write", job, "--read", out, "--", "/bin/sh", "-c", "echo x > "+filepath.Join(out, "w"))
	assert.NotEqual(t, 0, code)
}

func TestExitStatusPassesThrough(t *testing.T) {
	t.Parallel()
	job, _ := scratch(t)
	code, _, _ := sandbox(t, job, "--write", job, "--", "/bin/sh", "-c", "exit 7")
	assert.Equal(t, 7, code)
	code, _, stderr := sandbox(t, job, "--write", job, "--", "/bin/sh", "-c", "kill -9 $$")
	assert.Equal(t, 137, code, stderr)
	assert.Contains(t, stderr, "nova-sandbox: command killed by signal 9 (exit 137)")
}

func TestWallLineCwdAndMkdir(t *testing.T) {
	t.Parallel()
	job, out := scratch(t)
	dir, err := filepath.EvalSymlinks(filepath.Dir(child)) // the wall line names resolved paths (macOS: /var is /private/var)
	require.NoError(t, err)
	code, _, stderr := sandbox(t, "/usr", withChild()...)
	require.Equal(t, 0, code, stderr)
	assert.Contains(t, stderr, "WALL read="+dir+" net=open\n")

	code, _, stderr = sandbox(t, out, withChild("--write", job)...)
	assert.Equal(t, 125, code, stderr)
	assert.Contains(t, stderr, "WALL read="+dir+" write="+job+" net=open\n")
	assert.Contains(t, stderr, "SANDBOX REFUSED reason=cwd")
	assert.Contains(t, stderr, "cd "+job+" or add --read "+out)
	assert.NotContains(t, stderr, "; run:")

	code, _, stderr = sandbox(t, job, withChild("--write", job, "--net-deny")...)
	require.Equal(t, 0, code, stderr)
	assert.Contains(t, stderr, "WALL read="+dir+" write="+job+" net=denied\n")

	missing := filepath.Join(job, "missing")
	code, _, stderr = sandbox(t, job, withChild("--write", missing)...)
	assert.Equal(t, 2, code, stderr)
	assert.Contains(t, stderr, "mkdir -p "+missing)
	file := filepath.Join(job, "f")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	code, _, stderr = sandbox(t, job, withChild("--write", file)...)
	assert.Equal(t, 2, code, stderr)
	assert.NotContains(t, stderr, "mkdir")
}

func TestRefusalsExit2And126And127(t *testing.T) {
	t.Parallel()
	job, out := scratch(t)
	code, _, stderr := sandbox(t, job, withChild("--write", "relative")...)
	assert.Equal(t, 2, code, "a malformed invocation is 2")
	assert.Contains(t, stderr, "nova-sandbox: bad_write: ")
	script := filepath.Join(out, "x.sh")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\necho ran\n"), 0o755))
	code, _, stderr = sandbox(t, job, "--write", job, "--", script)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "nova-sandbox: bad_read: ", "a command outside every grant is refused before the wall")
	code, stdout, stderr := sandbox(t, job, "--write", job, "--read", out, "--", script)
	require.Equal(t, 0, code, stderr)
	assert.Equal(t, "ran\n", stdout)
	code, _, _ = sandbox(t, job, "--write", job, "--", "no-such-command-here")
	assert.Equal(t, 127, code)
	nox := filepath.Join(job, "noexec")
	require.NoError(t, os.WriteFile(nox, []byte("x"), 0o600))
	code, _, stderr = sandbox(t, job, "--write", job, "--", nox)
	assert.Equal(t, 126, code, "a file that is not executable is 126: "+stderr)
}

func TestEnvironmentAndDirectoryPassThrough(t *testing.T) {
	t.Parallel()
	job, _ := scratch(t)
	cmd := exec.Command(bin(t), "--write", job, "--env", "TMPDIR", "--", "/bin/sh", "-c", `pwd; printf '%s\n' "$TMPDIR" "$TOKEN"; mktemp`)
	cmd.Dir = job
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + job, "TMPDIR=" + job, "TOKEN=secret"}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	require.NoError(t, cmd.Run(), errb.String())
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.Len(t, lines, 4)
	assert.Equal(t, []string{job, job}, lines[:2], "the caller's cwd passes through, and TMPDIR passes because --env names it")
	assert.Empty(t, lines[2], "a variable that is not named does not arrive")
	assert.True(t, strings.HasPrefix(lines[3], job+"/"), "a temp file lands in the TMPDIR the caller pointed inside a --write: "+lines[3])
}

func TestArgv0IsVerbatim(t *testing.T) {
	t.Parallel()
	job, out := scratch(t)
	// A symlink to /bin/sh invoked by the link's name: `sh -c` with no operand sets $0
	// to argv[0], so the command sees the name it was invoked as, not the resolved path.
	// (A shebang script cannot show this: the kernel sets $0 to the script file.)
	link := filepath.Join(out, "mysh")
	require.NoError(t, os.Symlink("/bin/sh", link))
	code, stdout, stderr := sandbox(t, job, "--write", job, "--read", out, "--", link, "-c", "echo $0")
	require.Equal(t, 0, code, stderr)
	assert.Equal(t, link+"\n", stdout, "argv[0] is the caller's, not the resolved /bin/sh target")
}

func TestSymlinkedScriptSeesItsLinkName(t *testing.T) {
	t.Parallel()
	job, out := scratch(t)
	// The kernel sets a #! script's $0 to the path it was exec'd by, so the wall must
	// exec the caller's spelling, not the resolved target.
	who := filepath.Join(out, "who.sh")
	require.NoError(t, os.WriteFile(who, []byte("#!/bin/sh\necho $0\n"), 0o755))
	link := filepath.Join(out, "link.sh")
	require.NoError(t, os.Symlink(who, link))
	code, stdout, stderr := sandbox(t, job, "--write", job, "--read", out, "--", link)
	require.Equal(t, 0, code, stderr)
	assert.Equal(t, link+"\n", stdout)
}

// TestUnixFenceCost runs what the fence must still allow (a connected socketpair, as
// every event loop makes) and what it must refuse (a datagram socket to /dev/log, an
// agent socket), each skipped where the tool is not installed.
func TestUnixFenceCost(t *testing.T) {
	t.Parallel()
	job, _ := scratch(t)
	run := func(t *testing.T, tool string, args ...string) (int, string) {
		t.Helper()
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " is not installed here")
		}
		code, stdout, stderr := sandbox(t, job, append([]string{"--write", job, "--"}, append([]string{tool}, args...)...)...)
		return code, stdout + stderr
	}
	t.Run("asyncio", func(t *testing.T) {
		code, out := run(t, "python3", "-c", "import asyncio\nasync def f(): return 7\nprint(asyncio.run(f()))")
		require.Equal(t, 0, code, out)
		assert.Contains(t, out, "7")
	})
	t.Run("socketpair types", func(t *testing.T) {
		linuxOnly(t)
		code, out := run(t, "python3", "-c", `import socket
for k in ("SOCK_STREAM", "SOCK_SEQPACKET", "SOCK_DGRAM"):
    try:
        socket.socketpair(socket.AF_UNIX, getattr(socket, k)); print(k, "ok")
    except PermissionError: print(k, "eperm")
try:
    socket.socket(socket.AF_UNIX, socket.SOCK_STREAM); print("socket ok")
except PermissionError: print("socket eperm")`)
		require.Equal(t, 0, code, out)
		assert.Contains(t, out, "SOCK_STREAM ok")
		assert.Contains(t, out, "SOCK_SEQPACKET eperm", "a seqpacket pair can sendto a datagram path")
		assert.Contains(t, out, "SOCK_DGRAM eperm")
		assert.Contains(t, out, "socket eperm")
	})
	t.Run("logger", func(t *testing.T) {
		linuxOnly(t)
		code, out := run(t, "logger", "--socket-errors=on", "-u", "/dev/log", "walled")
		assert.NotEqual(t, 0, code, out)
		assert.Contains(t, out, "Operation not permitted")
	})
	t.Run("gpg-connect-agent", func(t *testing.T) {
		linuxOnly(t)
		code, out := run(t, "gpg-connect-agent", "--no-autostart", "/bye")
		assert.Contains(t, out, "Operation not permitted", "rc=%d", code)
	})
}

func TestUnixSocketIsFencedByDefault(t *testing.T) {
	t.Parallel()
	linuxOnly(t)
	if _, out, _ := sandbox(t, os.TempDir(), "check"); strings.Contains(out, "seccomp UNAVAILABLE") {
		t.Skip("no seccomp on this kernel")
	}
	job, out := scratch(t)
	sock := filepath.Join(out, "s")
	l, err := net.Listen("unix", sock)
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	self := bin(t)
	read := []string{"--read", filepath.Dir(self)}
	args := func(extra ...string) []string {
		return append(append(append([]string{}, read...), extra...), "--write", job, "--", self, clientVerb, "dial", sock)
	}
	code, _, stderr := sandbox(t, job, args()...)
	assert.NotEqual(t, 0, code, "AF_UNIX is refused, so the connect cannot be made: "+stderr)
	code, _, stderr = sandbox(t, job, args("--unix")...)
	assert.Equal(t, 0, code, "--unix reopens AF_UNIX: "+stderr)
}

// TestSystemdRunEscapeIsClosed reproduces the reported escape: systemd-run --user hands
// the command off to a service that runs outside the wall. The hand-off travels over a
// unix socket, which the default fence refuses, so the command cannot start the service.
func TestSystemdRunEscapeIsClosed(t *testing.T) {
	t.Parallel()
	sr, err := exec.LookPath("systemd-run")
	if err != nil {
		t.Skip("no systemd-run on this host")
	}
	if out, err := exec.Command(sr, "--user", "--wait", "--pipe", child, childArg).CombinedOutput(); err != nil {
		t.Skip("systemd --user is not available here: " + string(out))
	}
	job, out := scratch(t)
	secret := filepath.Join(out, "secret")
	require.NoError(t, os.WriteFile(secret, []byte("leak"), 0o600))
	code, stdout, _ := sandbox(t, job, "--read", filepath.Dir(sr), "--write", job, "--net-deny", "--",
		sr, "--user", "--wait", "--pipe", "/bin/cat", secret)
	assert.NotEqual(t, 0, code, "the hand-off socket is refused, so the service never runs")
	assert.NotContains(t, stdout, "leak", "the file outside the wall was not read")
}

func TestKeepFDsLeavesAnInheritedDescriptorOpen(t *testing.T) {
	t.Parallel()
	job, out := scratch(t)
	f, err := os.Create(filepath.Join(out, "inherited"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	_, err = f.WriteString("data")
	require.NoError(t, err)
	// Pass the open descriptor to the child as fd 3 and have it read from /dev/fd/3.
	run := func(extra ...string) (int, string) {
		cmd := exec.Command(bin(t), append(append([]string{}, extra...), "--write", job, "--", "/bin/sh", "-c", "cat <&3 2>/dev/null; true")...)
		cmd.Env, cmd.Dir = []string{"PATH=/usr/bin:/bin", "HOME=" + job}, job
		rf, _ := os.Open(f.Name())
		defer rf.Close()
		cmd.ExtraFiles = []*os.File{rf} // becomes fd 3 in the child
		var b strings.Builder
		cmd.Stdout = &b
		_ = cmd.Run()
		return 0, b.String()
	}
	_, withClose := run()
	assert.NotContains(t, withClose, "data", "by default the inherited fd 3 is closed before exec")
	_, withKeep := run("--keep-fds")
	assert.Contains(t, withKeep, "data", "--keep-fds leaves it open")
}

func TestNetDenyRefusesAConnect(t *testing.T) {
	t.Parallel()
	job, _ := scratch(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	dial := []string{"--read", filepath.Dir(bin(t)), "--write", job}
	args := func(extra ...string) []string {
		return append(append(append([]string{}, dial...), extra...), "--", bin(t), clientVerb, "dial", l.Addr().String())
	}
	code, _, stderr := sandbox(t, job, args()...)
	require.Equal(t, 0, code, "without --net-deny the connect works: "+stderr)
	code, _, stderr = sandbox(t, job, args("--net-deny")...)
	if code == 125 {
		t.Skip("--net-deny is not enforceable on this machine: " + stderr)
	}
	assert.Equal(t, 1, code, "the kernel refused the connect")
	code, _, stderr = sandbox(t, job, args("--net-deny", "--net-allow", l.Addr().String())...)
	assert.Equal(t, 0, code, "--net-allow opens the named loopback port back up: "+stderr)
}

// udpListener is a UDP socket outside the wall; got says whether a datagram arrived.
func udpListener(t *testing.T) (addr string, got func(time.Duration) bool) {
	t.Helper()
	u, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = u.Close() })
	return u.LocalAddr().String(), func(wait time.Duration) bool {
		buf := make([]byte, 64)
		_ = u.SetReadDeadline(time.Now().Add(wait))
		_, _, err := u.ReadFrom(buf)
		return err == nil
	}
}

// TestNetDenyClosesUDP is the reported hole: a UDP datagram, which Landlock does not
// fence, must not leave a --net-deny wall, with or without --net-allow.
func TestNetDenyClosesUDP(t *testing.T) {
	t.Parallel()
	job, _ := scratch(t)
	addr, got := udpListener(t)
	self := bin(t)
	args := func(extra ...string) []string {
		return append(append([]string{"--read", filepath.Dir(self), "--write", job}, extra...), "--", self, clientVerb, "udp", addr)
	}
	code, _, stderr := sandbox(t, job, args()...)
	require.Equal(t, 0, code, stderr)
	require.True(t, got(3*time.Second), "without --net-deny the datagram arrives")
	code, _, stderr = sandbox(t, job, args("--net-deny")...)
	assert.Equal(t, 1, code, "socket(AF_INET, SOCK_DGRAM) is refused: "+stderr)
	assert.False(t, got(500*time.Millisecond))
	if runtime.GOOS != "linux" {
		return // macOS --net-allow opens UDP to localhost on the port too (README)
	}
	code, _, stderr = sandbox(t, job, args("--net-deny", "--net-allow", addr)...)
	assert.Equal(t, 1, code, "--net-allow reopens TCP only, even on the datagram's own port: "+stderr)
	assert.False(t, got(500*time.Millisecond))
}

// TestNetDenyClosesEverySocketKind tries each kind of socket from Python inside the
// wall: under --net-deny none but AF_UNIX-stream pairs and netlink may be made, and
// under --net-allow TCP comes back and nothing else does.
func TestNetDenyClosesEverySocketKind(t *testing.T) {
	t.Parallel()
	linuxOnly(t)
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed here")
	}
	job, _ := scratch(t)
	script := `import socket as s
kinds = [("tcp", s.AF_INET, s.SOCK_STREAM, 0), ("tcp6", s.AF_INET6, s.SOCK_STREAM, 0),
  ("udp", s.AF_INET, s.SOCK_DGRAM, 0), ("udp6", s.AF_INET6, s.SOCK_DGRAM, 0),
  ("ping", s.AF_INET, s.SOCK_DGRAM, s.IPPROTO_ICMP), ("raw", s.AF_INET, s.SOCK_RAW, s.IPPROTO_UDP),
  ("sctp", s.AF_INET, s.SOCK_STREAM, 132), ("mptcp", s.AF_INET, s.SOCK_STREAM, 262),
  ("packet", 17, s.SOCK_RAW, 0), ("netlink", s.AF_NETLINK, s.SOCK_RAW, 0)]
for name, f, t, p in kinds:
    try:
        s.socket(f, t, p).close(); print(name, "ok")
    except PermissionError: print(name, "eperm")
    except OSError as e: print(name, "oserror", e.errno)
`
	run := func(extra ...string) string {
		code, out, stderr := sandbox(t, job, append(append([]string{"--write", job}, extra...), "--", "python3", "-c", script)...)
		require.Equal(t, 0, code, stderr)
		return out
	}
	closed := run("--net-deny")
	for _, k := range []string{"tcp", "tcp6", "udp", "udp6", "ping", "raw", "sctp", "mptcp", "packet"} {
		assert.Contains(t, closed, k+" eperm\n", "--net-deny: "+closed)
	}
	assert.NotContains(t, closed, "netlink eperm", "netlink stays open: "+closed)
	allow := run("--net-deny", "--net-allow", "127.0.0.1:1")
	assert.Contains(t, allow, "tcp ok\n")
	assert.Contains(t, allow, "tcp6 ok\n")
	for _, k := range []string{"udp", "udp6", "ping", "raw", "sctp", "mptcp", "packet"} {
		assert.Contains(t, allow, k+" eperm\n", "--net-allow: "+allow)
	}
}

// TestNetDenyClosesDNS: a name outside /etc/hosts does not resolve under --net-deny,
// through Go's resolver and through glibc getaddrinfo (getent), and a name in
// /etc/hosts still does.
func TestNetDenyClosesDNS(t *testing.T) {
	t.Parallel()
	if _, err := net.LookupHost("example.com"); err != nil {
		t.Skip("this host cannot resolve example.com outside the wall")
	}
	job, _ := scratch(t)
	self := bin(t)
	read := []string{"--read", filepath.Dir(self), "--write", job}
	code, _, stderr := sandbox(t, job, append(read, "--", self, clientVerb, "resolve", "example.com")...)
	require.Equal(t, 0, code, "the control resolves: "+stderr)
	code, _, _ = sandbox(t, job, append(read, "--net-deny", "--", self, clientVerb, "resolve", "example.com")...)
	assert.Equal(t, 1, code, "DNS is closed under --net-deny")
	if getent, err := exec.LookPath("getent"); err == nil {
		code, out, _ := sandbox(t, job, "--write", job, "--net-deny", "--", getent, "ahosts", "example.com")
		assert.NotEqual(t, 0, code, "getaddrinfo fails under --net-deny: "+out)
		code, out, stderr = sandbox(t, job, "--write", job, "--net-deny", "--", getent, "ahosts", "localhost")
		assert.Equal(t, 0, code, "/etc/hosts still answers: "+stderr)
		assert.Contains(t, out, "127.0.0.1")
	}
}

// TestShowSnapshot compares show to the block it must print on this host: the system
// roots by their presence here, the rest literal. It runs nothing.
func TestShowSnapshot(t *testing.T) {
	t.Parallel()
	_, chk, _ := sandbox(t, os.TempDir(), "check")
	if !strings.Contains(chk, "net=enforceable") {
		t.Skip("--net-deny is not enforceable here: " + chk)
	}
	if !strings.Contains(chk, "backend=landlock") {
		t.Skip("the expected block is Landlock's: " + chk)
	}
	abi := regexp.MustCompile(`abi=(\d+)`).FindStringSubmatch(chk)[1]
	job, out := scratch(t)
	code, got, stderr := sandbox(t, job, "show", "--read", out, "--write", job, "--net-deny")
	require.Equal(t, 0, code, stderr)
	var want strings.Builder
	used := abi
	if n, _ := strconv.Atoi(abi); n > maxABI {
		used = strconv.Itoa(maxABI)
	}
	fmt.Fprintf(&want, "wall backend=landlock abi=%s seccomp=on\n", used)
	n, _ := strconv.Atoi(used)
	fmt.Fprintf(&want, "fs handled=%s\n", fsNames(handledFS(n)))
	roots := slices.Clone(linuxReadRoots)
	if d := resolverDirOf("/etc/resolv.conf"); d != "" && !slices.Contains(roots, d) {
		roots = append(roots, d)
	}
	for _, r := range roots {
		state := ""
		if _, err := os.Stat(r); err != nil {
			state = " state=absent"
		}
		fmt.Fprintf(&want, "path access=read path=%s from=root%s\n", r, state)
	}
	want.WriteString(`path access=write path=/dev/null from=root
path access=write path=/dev/tty from=root
path access=write path=/dev/shm from=root
path access=read path=` + out + ` from=--read
path access=write path=` + job + ` from=--write
net rule=closed tcp=deny by=landlock,seccomp udp=deny raw=deny packet=deny by=seccomp dns=/etc/hosts_only
seccomp action=kill rule="another architecture's syscall (i386, x32 compat)"
seccomp action=kill rule="an x32 syscall number"
seccomp action=eperm rule=ptrace
seccomp action=eperm rule="io_uring_setup, io_uring_enter, io_uring_register"
seccomp action=eperm rule=socket(AF_UNIX)
seccomp action=eperm rule="socket of every family but AF_UNIX and AF_NETLINK (no TCP, UDP, raw or packet socket)"
seccomp action=eperm rule="socket(AF_NETLINK) unless NETLINK_ROUTE (no sock_diag, which lists other processes' connections)"
seccomp action=eperm rule="socketpair of any family but AF_UNIX"
seccomp action=eperm rule="socketpair(AF_UNIX) unless SOCK_STREAM"
seccomp action=allow rule="everything else"
`)
	if n >= 6 {
		want.WriteString("scope signal=wall abstract_unix=wall\n")
	} else {
		want.WriteString("scope none note=below landlock abi 6 a signal can reach a process outside the wall\n")
	}
	want.WriteString("fds close=above_stderr\n")
	assert.Equal(t, want.String(), got)
	assert.Empty(t, stderr)
	entries, err := os.ReadDir(job)
	require.NoError(t, err)
	assert.Empty(t, entries, "show runs nothing and writes nothing")
}

// TestSpecWorkedExample runs a Go build walled as cmd/nova-sandbox/README.md shows, with
// /work as a scratch directory and /home/me/go/pkg/mod as this host's module cache: a Go
// module with a dependency builds walled under --net-deny, HOME and TMPDIR inside the
// --write, the module cache only read.
func TestSpecWorkedExample(t *testing.T) {
	t.Parallel()
	goBin, err := exec.LookPath("go")
	require.NoError(t, err)
	modcache, err := exec.Command(goBin, "env", "GOMODCACHE").Output()
	require.NoError(t, err)
	mod := strings.TrimSpace(string(modcache))
	// The example's toolchain is /usr/local/go, under /usr; a toolchain elsewhere is
	// named with --read, as the spec says for any command outside the roots.
	var toolchain []string
	if goBin, err = filepath.EvalSymlinks(goBin); err == nil && !underAny(goBin, linuxReadRoots) {
		toolchain = []string{"--read", filepath.Dir(filepath.Dir(goBin))}
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	src, scr := filepath.Join(root, "work", "src"), filepath.Join(root, "work", "scratch")
	for _, d := range []string{src, filepath.Join(scr, "tmp"), filepath.Join(scr, "home")} {
		require.NoError(t, os.MkdirAll(d, 0o700))
	}
	// The dependency is this repo's own testify, already in the module cache the
	// tests were built from.
	require.NoError(t, os.WriteFile(filepath.Join(src, "go.mod"), []byte("module example.com/w\n\ngo 1.22\n\nrequire github.com/stretchr/testify v1.12.1\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(src, "main.go"), []byte("package main\n\nimport \"github.com/stretchr/testify/assert\"\n\nvar _ = assert.Equal\n\nfunc main() {}\n"), 0o600))
	tidy := exec.Command(goBin, "mod", "tidy")
	tidy.Dir, tidy.Env = src, append(os.Environ(), "GOFLAGS=-mod=mod", "GOPROXY=off")
	if out, err := tidy.CombinedOutput(); err != nil {
		t.Skip("testify v1.12.1 is not in this host's module cache: " + string(out))
	}
	wall := append([]string{"--write", src, "--write", scr, "--read", mod}, toolchain...)
	cmd := exec.Command(bin(t), append(wall, "--net-deny", "--env", "TMPDIR", "--env", "GOCACHE", "--env", "GOMODCACHE", "--env", "GOPROXY", "--", "go", "build", "./...")...)
	cmd.Dir = src
	cmd.Env = []string{"PATH=" + filepath.Dir(goBin) + ":/usr/bin:/bin", "HOME=" + filepath.Join(scr, "home"), "TMPDIR=" + filepath.Join(scr, "tmp"),
		"GOCACHE=" + filepath.Join(scr, "gocache"), "GOMODCACHE=" + mod, "GOPROXY=off"}
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	assert.FileExists(t, filepath.Join(src, "w"), "the binary is written into the --write")
	home := exec.Command(bin(t), append(wall, "--net-deny", "--", "/bin/ls", os.Getenv("HOME"))...)
	home.Dir, home.Env = src, cmd.Env
	assert.Error(t, home.Run(), "anything else is denied")
}

// TestNetAllowClosesFastOpenAndListen reproduces the two --net-allow holes a reader
// found: a TCP Fast Open send connects inside sendto, out of Landlock's connect hook,
// and listen() without bind() auto-binds a port its bind hook never sees, which an
// outside client could then reach. Both are refused by seccomp under --net-allow.
func TestNetAllowClosesFastOpenAndListen(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed here")
	}
	job, _ := scratch(t)
	allowed, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = allowed.Close() })
	other, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = other.Close() })
	got := make(chan string, 4)
	go func() {
		for {
			c, err := other.Accept()
			if err != nil {
				return
			}
			buf := make([]byte, 16)
			_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
			n, _ := c.Read(buf)
			got <- string(buf[:n])
			c.Close()
		}
	}()
	port := other.Addr().(*net.TCPAddr).Port
	tfo := fmt.Sprintf(`import socket as s
c = s.socket(s.AF_INET, s.SOCK_STREAM)
c.sendto(b"leak", s.MSG_FASTOPEN, ("127.0.0.1", %d))
print("sent")`, port)
	allow := []string{"--write", job, "--net-deny", "--net-allow", allowed.Addr().String()}
	code, out, stderr := sandbox(t, job, "--write", job, "--", "python3", "-c", tfo)
	if code != 0 {
		t.Skip("TCP Fast Open does not work on this host outside the wall: " + stderr)
	}
	assert.Equal(t, "sent\n", out)
	select {
	case d := <-got:
		assert.Equal(t, "leak", d, "the control's data arrives")
	case <-time.After(3 * time.Second):
		t.Fatal("the control's connection never arrived")
	}
	code, out, stderr = sandbox(t, job, append(allow, "--", "python3", "-c", tfo)...)
	assert.NotEqual(t, 0, code, out)
	assert.Contains(t, stderr, "PermissionError", "sendto with MSG_FASTOPEN is refused")
	select {
	case d := <-got:
		t.Fatalf("a Fast Open send reached a port --net-allow did not name: %q", d)
	case <-time.After(500 * time.Millisecond):
	}

	// listen() with no bind: the kernel would pick a port on 0.0.0.0.
	listen := `import socket as s
l = s.socket(s.AF_INET, s.SOCK_STREAM)
l.listen(1)
print("listening", l.getsockname()[1])`
	code, out, stderr = sandbox(t, job, append(allow, "--", "python3", "-c", listen)...)
	assert.NotEqual(t, 0, code, out)
	assert.Contains(t, stderr, "PermissionError")
	assert.NotContains(t, out, "listening")
	code, out, _ = sandbox(t, job, "--write", job, "--net-deny", "--unix", "--", "python3", "-c", `import socket as s, os
p = os.path.join(os.getcwd(), "srv")
l = s.socket(s.AF_UNIX, s.SOCK_STREAM); l.bind(p); l.listen(1); print("unix listening")`)
	assert.Equal(t, 0, code, "--net-deny --unix without --net-allow still serves a unix socket: "+out)
}

// TestFunctionalProbeReportsAStepItCannotSetUpAsUntested runs the probe under a
// TMPDIR so deep that its unix socket path is past the kernel's limit, so the probe's
// own listener cannot be made. The unix steps are reported untested, never dropped.
func TestFunctionalProbeReportsAStepItCannotSetUpAsUntested(t *testing.T) {
	t.Parallel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	deep := filepath.Join(root, strings.Repeat("d", 110))
	require.NoError(t, os.Mkdir(deep, 0o700))
	_, out, _ := sandbox(t, deep, "probe")
	assert.Contains(t, out, "PROBE STEP name=unix_deny expect=deny got=untested note=")
	if runtime.GOOS == "linux" {
		assert.Contains(t, out, "PROBE STEP name=unix_allow expect=allow got=untested note=")
	}
}

// TestFunctionalEverySignalIsForwarded sends the waiter SIGINT, then SIGTERM. The
// command traps both and records each, so a signal the waiter swallows shows as a
// missing line and a command that never exits 3.
func TestFunctionalEverySignalIsForwarded(t *testing.T) {
	t.Parallel()
	job, _ := scratch(t)
	got, ready := filepath.Join(job, "got"), filepath.Join(job, "ready")
	script := "trap 'echo INT >> " + got + "' INT; trap 'echo TERM >> " + got + "; exit 3' TERM; echo > " + ready +
		"; i=0; while [ $i -lt 100 ]; do sleep 0.1; i=$((i+1)); done"
	cmd := exec.Command(bin(t), "--write", job, "--", "/bin/sh", "-c", script)
	cmd.Env, cmd.Dir = []string{"PATH=/usr/bin:/bin", "HOME=" + job}, job
	require.NoError(t, cmd.Start())
	waitFor := func(path, want string) {
		for i := 0; i < 100; i++ {
			if b, err := os.ReadFile(path); err == nil && strings.Contains(string(b), want) {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	waitFor(ready, "")
	require.NoError(t, cmd.Process.Signal(os.Interrupt))
	waitFor(got, "INT")
	require.NoError(t, cmd.Process.Signal(syscall.SIGTERM))
	err := cmd.Wait()
	b, _ := os.ReadFile(got)
	assert.Equal(t, "INT\nTERM\n", string(b))
	var ee *exec.ExitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 3, ee.ExitCode())
}

// TestFunctionalPlantedSandboxExecIsIgnored puts a fake sandbox-exec, which runs the
// command with no profile, first on PATH. The wall must still be the real
// /usr/bin/sandbox-exec, so the write outside the grant is refused.
func TestFunctionalPlantedSandboxExecIsIgnored(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" {
		t.Skip("checks the macOS wall; this is " + runtime.GOOS)
	}
	job, out := scratch(t)
	fake := filepath.Join(filepath.Dir(job), "fake")
	require.NoError(t, os.Mkdir(fake, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(fake, "sandbox-exec"),
		[]byte("#!/bin/sh\nwhile [ $# -gt 0 ] && [ \"$1\" != \"--\" ]; do shift; done\n[ $# -gt 0 ] || exit 0\nshift\nexec \"$@\"\n"), 0o700))
	run := func(args ...string) (string, error) {
		cmd := exec.Command(bin(t), args...)
		cmd.Env, cmd.Dir = []string{"PATH=" + fake + ":/usr/bin:/bin", "HOME=" + job}, job
		b, err := cmd.CombinedOutput()
		return string(b), err
	}
	got, err := run("check")
	require.NoError(t, err, got)
	assert.Contains(t, got, "(/usr/bin/sandbox-exec)")
	got, err = run("--write", job, "--", "/bin/sh", "-c", "echo hi > "+filepath.Join(out, "bad"))
	var ee *exec.ExitError
	require.ErrorAs(t, err, &ee, "the walled write must fail: "+got)
	assert.NotContains(t, got, "SANDBOX REFUSED", "the wall ran and denied the write; it did not refuse to start")
	assert.NotContains(t, got, "run: nova-sandbox -h", "the invocation was not malformed")
	assert.NoFileExists(t, filepath.Join(out, "bad"), "the real profile refused the write: "+got)
}
