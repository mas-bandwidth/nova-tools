//go:build darwin

package sandbox

import (
	"bytes"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestNetAllowGrantsExactlyTheNamedLoopbackPort pins issue #591's --net-allow on the ONE
// backend it exists for: real sandbox-exec, not the profile TEXT. PR #599 first pinned
// `(allow network-outbound (local ip (host "<h>") (port "<p>")))`, asserted only by a
// profile-text test with no macOS to run it on; measured here, that form aborts the WHOLE
// profile with `unbound variable: host` at sandbox-exec exit 65 -- a silent grant of
// NOTHING, on a machine that had no way to notice. This test runs the real binary and
// would have caught it: a run that cannot even compile its profile fails this test's
// positive control before ever reaching the negative one.
func TestNetAllowGrantsExactlyTheNamedLoopbackPort(t *testing.T) {
	if _, ok := available(); !ok {
		t.Skip("no sandbox-exec on this machine")
	}
	nc, err := exec.LookPath("nc")
	if err != nil {
		t.Skip("no nc on PATH to probe a TCP connect with")
	}

	allowedLn := loopbackListener(t)
	defer allowedLn.Close()
	deniedLn := loopbackListener(t)
	defer deniedLn.Close()
	allowedPort := allowedLn.Addr().(*net.TCPAddr).Port
	deniedPort := deniedLn.Addr().(*net.TCPAddr).Port

	write := t.TempDir()
	home := filepath.Join(write, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	env := ChildEnv(append(os.Environ(), "HOME="+home), filepath.Join(write, tmpDirName))

	connect := func(port int) (int, string) {
		iv := in(t, write, t.TempDir(), home, nc, "-z", "-w", "2", "127.0.0.1", strconv.Itoa(port))
		iv.NetDeny = true // the exception is --net-allow's alone; nothing else opens it
		iv.NetAllow = []string{"127.0.0.1:" + strconv.Itoa(allowedPort)}
		p, bad := Build(iv)
		if len(bad) > 0 {
			t.Fatalf("refused at build: %v", bad)
		}
		var out, errb bytes.Buffer
		code, err := Run(p, env, strings.NewReader(""), &out, &errb, nil)
		if err != nil {
			t.Fatalf("run port %d: %v (%s)", port, err, errb.String())
		}
		return code, errb.String()
	}

	// THE POSITIVE CONTROL: the port --net-allow names, reachable. If the SBPL form
	// DarwinProfile emits does not compile, sandbox-exec exits 65 here and this fails --
	// which is exactly the failure #599's own profile-text-only tests could not produce.
	if code, errb := connect(allowedPort); code != 0 {
		t.Fatalf("connect to the ALLOWED loopback port %d: exit %d, stderr %q", allowedPort, code, errb)
	}

	// THE NEGATIVE CONTROL: a different loopback port, under the same --net-deny, must
	// still be refused -- --net-allow opens the one port named, never the loopback at
	// large. `nc -z` exits nonzero on a refused connect.
	if code, _ := connect(deniedPort); code == 0 {
		t.Fatalf("connect to the DENIED loopback port %d succeeded; --net-allow %d opened more than its own port", deniedPort, allowedPort)
	}
}

func loopbackListener(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("loopback listen: %v", err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	return ln
}
