//go:build functional

package main

import (
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tailscale_ssh_off_functional_test.go runs, through ansible-core on this machine with the local connection, the
// tailscale role's steps that turn Tailscale SSH off: the guard in main.yml (sshd on the node's tailnet address, sshd
// -T for this login from the control node, the control node's key authorized) and ssh-off.yml (the detached switch
// with its dead-man revert, the reconnect, the key login, the keep file). tailscale, sshd and ssh are fakes in the
// run's own directory; every file written is the test's.

// writeTailscaleFakes puts tailscale (status prints control.json; set --ssh=X writes runssh; debug prefs reads it),
// sshd (prints sshd.conf, or fails without one) and ssh (sleeps the seconds in ssh.sleep, fails while ssh.fail exists)
// in r.bin; each logs its argv.
func writeTailscaleFakes(t *testing.T, r *playRun) {
	t.Helper()
	d := "D='" + r.dir + "'\n"
	fakes := map[string]string{
		"tailscale": "#!/bin/sh\n" + d + `echo "$*" >> "$D/tailscale.log"
case "$1" in
status) cat "$D/control.json" ;;
set) for a in "$@"; do case "$a" in --ssh=false) echo false > "$D/runssh" ;; --ssh=true) echo true > "$D/runssh" ;; esac; done ;;
debug) printf '{"RunSSH": %s}\n' "$(cat "$D/runssh" 2>/dev/null || echo true)" ;;
esac
`,
		"sshd": "#!/bin/sh\n" + d + `echo "$*" >> "$D/sshd.log"
if [ -f "$D/sshd.conf" ]; then cat "$D/sshd.conf"; exit 0; fi
echo "sshd: no hostkeys available -- exiting." >&2
exit 1
`,
		"ssh": "#!/bin/sh\n" + d + `echo "$*" >> "$D/ssh.log"
if [ -f "$D/ssh.sleep" ]; then sleep "$(cat "$D/ssh.sleep")"; fi
if [ -f "$D/ssh.fail" ]; then echo "nova@node: Permission denied (publickey)." >&2; exit 255; fi
`,
	}
	for name, body := range fakes {
		require.NoError(t, os.WriteFile(filepath.Join(r.bin, name), []byte(body), 0o755))
	}
	control := `{"Self": {"DNSName": "control.tail1.ts.net.", "TailscaleIPs": ["100.64.0.1", "fd7a:115c:a1e0::1"]}}`
	require.NoError(t, os.WriteFile(filepath.Join(r.dir, "control.json"), []byte(control), 0o600))
}

// The guard before Tailscale SSH goes off, as main.yml has it: sshd answers on the node's tailnet address (never
// loopback), sshd -T for this login from the control node's name and tailnet address allows a key login, and only then
// is the control node's key authorized, once. Each refusal is a TAILNET STOP with nothing written. On a node without
// Tailscale SSH, or with nova_tailscale_ssh true, none of it runs.
func TestFunctionalTailscaleSSHOffKeepsTheControlNodesWayIn(t *testing.T) {
	t.Parallel()
	file := "roles/tailscale/tasks/main.yml"
	var tasks []map[string]any
	for _, name := range []string{"whether this run turns Tailscale SSH off", "the control node's public key", "the control node on the tailnet",
		"sshd on this machine's tailnet address", "sshd's settings for this login", "sshd takes this login by key", "the control node's key authorized for"} {
		tasks = append(tasks, roleTask(t, file, name))
	}
	// a listener that greets every connection with banner, as an ssh server does
	serve := func(banner string) int {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		t.Cleanup(func() { _ = l.Close() }) // ignored: closing a listener the test opened; after the parallel subtests
		go func() {
			for {
				c, err := l.Accept()
				if err != nil {
					return
				}
				_, _ = c.Write([]byte(banner + "\r\n")) // ignored: a probe that hung up early sees no banner, as it should
				_ = c.Close()                           // ignored: the probe has its answer
			}
		}()
		return l.Addr().(*net.TCPAddr).Port
	}
	open := serve("SSH-2.0-OpenSSH_9.6p1 Ubuntu-3ubuntu13")
	tailscaleSSH := serve("SSH-2.0-Tailscale")

	closed, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	closedPort := closed.Addr().(*net.TCPAddr).Port
	require.NoError(t, closed.Close())
	me, err := user.Current()
	require.NoError(t, err)

	// the tasks with the probe's port moved off 22, sshd -T run without sudo, and the key written in the run's directory
	at := func(r *playRun, port int) []map[string]any {
		var out []map[string]any
		for _, task := range tasks {
			c := map[string]any{}
			for k, v := range task {
				c[k] = v
			}
			if probe, ok := task["ansible.builtin.wait_for"].(map[string]any); ok {
				require.Equal(t, "{{ ts_tail_ip }}", probe["host"], "the probe is the node's tailnet address")
				require.Equal(t, 22, probe["port"])
				w := map[string]any{"port": port}
				for k, v := range probe {
					if k != "port" {
						w[k] = v
					}
				}
				c["ansible.builtin.wait_for"] = w
			}
			if cmd, ok := task["ansible.builtin.command"].(map[string]any); ok {
				argv := cmd["argv"].([]any)
				if argv[0] == "{{ nova_sshd }}" {
					c["become"] = false
				}
			}
			if ak, ok := task["ansible.posix.authorized_key"].(map[string]any); ok {
				w := map[string]any{"path": filepath.Join(r.dir, ".ssh", "authorized_keys")}
				for k, v := range ak {
					w[k] = v
				}
				c["ansible.posix.authorized_key"] = w
			}
			out = append(out, c)
		}
		return out
	}
	vars := func(r *playRun, runSSH, want bool, ips ...string) map[string]any {
		return map[string]any{"ts_join": false, "ts_root_daemon": true, "nova_tailscale_ssh": want, "nova_control_ssh_pubkey": testControlKey,
			"nova_sshd": filepath.Join(r.bin, "sshd"), "ts_self": map[string]any{"TailscaleIPs": ips},
			"ts_prefs": map[string]any{"stdout": `{"RunSSH": ` + strconv.FormatBool(runSSH) + `}`}}
	}
	setup := func(t *testing.T, conf string) *playRun {
		r := newPlayRun(t)
		writeTailscaleFakes(t, r)
		if conf != "" {
			require.NoError(t, os.WriteFile(filepath.Join(r.dir, "sshd.conf"), []byte(conf), 0o600))
		}
		return r
	}
	const takesKeys = "port 22\npubkeyauthentication yes\npasswordauthentication no\n"
	stop := "TAILNET STOP localhost: Tailscale SSH is on, and turning it off would leave the control node no way in: "

	t.Run("sshd takes the login by key", func(t *testing.T) {
		t.Parallel()
		r := setup(t, takesKeys+"allowusers root "+me.Username+"@100.64.0.0/10\n")
		for run := 1; run <= 2; run++ {
			out, err := r.play(t, vars(r, true, false, "fd7a:115c:a1e0::2", "127.0.0.1"), true, at(r, open))
			require.NoError(t, err, out)
			assert.Equal(t, testControlKey+"\n", r.read(t, ".ssh/authorized_keys"), "run %d", run)
			if run == 2 {
				assert.Regexp(t, `localhost +: ok=\d+ +changed=0 `, out, "a second run changed something")
			}
		}
		assert.Contains(t, r.read(t, "sshd.log"), "-T -C user="+me.Username+",host=control.tail1.ts.net,addr=100.64.0.1\n")
	})
	for name, tc := range map[string]struct {
		conf, why string
		port      int
		ips       []string
	}{
		"nothing answers":       {takesKeys, "no OpenSSH sshd answers on 127.0.0.1:22", closedPort, []string{"127.0.0.1"}},
		"Tailscale SSH answers": {takesKeys, "no OpenSSH sshd answers on 127.0.0.1:22", tailscaleSSH, []string{"127.0.0.1"}},
		"no tailnet address":    {takesKeys, "the node has no tailnet IPv4 address", open, []string{"fd7a:115c:a1e0::2"}},
		"sshd -T fails":         {"", "sshd -T failed: sshd: no hostkeys available -- exiting.", open, []string{"127.0.0.1"}},
		"no key logins":         {"pubkeyauthentication no\n", "sshd has PubkeyAuthentication off for " + me.Username, open, []string{"127.0.0.1"}},
		"AllowUsers omits it":   {takesKeys + "allowusers root admin\n", "sshd AllowUsers (root admin) leaves out " + me.Username, open, []string{"127.0.0.1"}},
		"DenyUsers names it":    {takesKeys + "denyusers " + me.Username + "\n", "sshd DenyUsers names " + me.Username, open, []string{"127.0.0.1"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := setup(t, tc.conf)
			out, err := r.play(t, vars(r, true, false, tc.ips...), true, at(r, tc.port))
			require.Error(t, err, out)
			assert.Contains(t, out, stop)
			assert.Contains(t, out, tc.why)
			assert.Contains(t, out, "nothing was changed")
			assert.Empty(t, r.read(t, ".ssh/authorized_keys"))
		})
	}
	for name, ssh := range map[string][2]bool{"Tailscale SSH already off": {false, false}, "Tailscale SSH wanted on": {true, true}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := setup(t, "")
			out, err := r.play(t, vars(r, ssh[0], ssh[1], "127.0.0.1"), true, at(r, closedPort))
			require.NoError(t, err, out)
			assert.Empty(t, r.read(t, ".ssh/authorized_keys"))
			assert.Empty(t, r.read(t, "ssh-add.log"))
			assert.Empty(t, r.read(t, "sshd.log"))
		})
	}
}

// ssh-off.yml as the role has it: the detached job turns Tailscale SSH off; the play reconnects, reads RunSSH false,
// logs in by key with ControlPath=none and claims the decision file, and the job leaves it off. When the key login
// fails, or lands after the deadline, the job claims the file first and turns Tailscale SSH back on, and the play
// fails: it never reports kept. The job's pending file exists while it runs and is gone when it ends.
func TestFunctionalTailscaleSSHOffIsADeadMansSwitch(t *testing.T) {
	t.Parallel()
	tasks := roleFileTasks(t, "roles/tailscale/tasks/ssh-off.yml")
	for _, task := range tasks {
		if _, ok := task["become"]; ok {
			task["become"] = false
		}
	}
	// deadline is the job's; sleep is the key login's own time; the play waits 5 s before it reconnects
	run := func(t *testing.T, deadline int, fail bool, sleep int) (*playRun, string, error, bool) {
		r := newPlayRun(t)
		writeTailscaleFakes(t, r)
		if fail {
			require.NoError(t, os.WriteFile(filepath.Join(r.dir, "ssh.fail"), nil, 0o600))
		}
		if sleep > 0 {
			require.NoError(t, os.WriteFile(filepath.Join(r.dir, "ssh.sleep"), []byte(strconv.Itoa(sleep)), 0o600))
		}
		pending, hold := filepath.Join(r.dir, "pending"), filepath.Join(r.dir, "hold.d", "tailscale-ssh-off")
		if !fail { // a failing login runs where netwatch is not installed: no hold directory, and the job goes on
			require.NoError(t, os.Mkdir(filepath.Dir(hold), 0o700))
		}
		vars := map[string]any{"ts_bin": filepath.Join(r.bin, "tailscale"), "nova_tailscale_ssh_keep": filepath.Join(r.dir, "keep"),
			"nova_tailscale_ssh_deadline": deadline, "nova_tailscale_ssh_pending": pending, "nova_tailscale_ssh_hold": hold}
		out, err := r.play(t, vars, true, tasks)
		_, statErr := os.Stat(pending)
		pendingAtEnd := statErr == nil && (fail || fileExists(hold))
		// the job is done when async_wrapper has written the command's result (its delta) into the job file
		assert.Eventually(t, func() bool {
			jobs, err := filepath.Glob(filepath.Join(r.dir, "async", "*"))
			if err != nil || len(jobs) != 1 {
				return false
			}
			b, err := os.ReadFile(jobs[0])
			return err == nil && strings.Contains(string(b), `"delta"`)
		}, 60*time.Second, 200*time.Millisecond, "the detached job never finished")
		assert.NoFileExists(t, pending, "the job's pending file outlived it")
		assert.NoFileExists(t, hold, "the job's netwatch hold outlived it")
		return r, out, err, pendingAtEnd
	}

	t.Run("kept off", func(t *testing.T) {
		t.Parallel()
		r, out, err, pending := run(t, 15, false, 0) // 15 s: past the play's 5 s wait and the key login
		require.NoError(t, err, out)
		assert.True(t, pending, "the job is still deciding when the play ends, and holds its pending file and netwatch's hold")
		assert.Contains(t, out, "TAILNET SSH-OFF localhost runssh=false key-login=ok kept")
		assert.FileExists(t, filepath.Join(r.dir, "keep"))
		assert.Equal(t, "false\n", r.read(t, "runssh"))
		assert.NotContains(t, r.read(t, "tailscale.log"), "--ssh=true")
		assert.Contains(t, r.read(t, "ssh.log"), "-n -o BatchMode=yes -o ControlPath=none -o ConnectTimeout=10 -o StrictHostKeyChecking=yes -l "+mustUser(t)+" localhost true")
	})
	t.Run("no key login: back on at the deadline", func(t *testing.T) {
		t.Parallel()
		r, out, err, _ := run(t, 15, true, 0)
		require.Error(t, err, out)
		assert.Contains(t, out, "TAILNET STOP localhost: Tailscale SSH is off and the control node cannot log in by key (nova@node: Permission denied (publickey).)")
		assert.NotContains(t, out, "kept")
		assert.Equal(t, "true\n", r.read(t, "runssh"))
		assert.Contains(t, r.read(t, "tailscale.log"), "set --ssh=false\ndebug prefs\n")
		assert.True(t, strings.HasSuffix(r.read(t, "tailscale.log"), "set --ssh=true\n"), r.read(t, "tailscale.log"))
	})
	t.Run("a key login after the deadline is not kept", func(t *testing.T) {
		t.Parallel()
		// the job switches at 2 s and decides at 2+8 s; the play reconnects at 5 s and its login takes 10 s more
		r, out, err, _ := run(t, 8, false, 10)
		require.Error(t, err, out)
		assert.Contains(t, out, "TAILNET STOP localhost: the key login came after the 8 s deadline (the detached job decided first and turned Tailscale SSH back on); nothing is kept.")
		assert.NotContains(t, out, "TAILNET SSH-OFF localhost runssh=false")
		assert.Equal(t, "true\n", r.read(t, "runssh"))
		assert.True(t, strings.HasSuffix(r.read(t, "tailscale.log"), "set --ssh=true\ndebug prefs\n"), r.read(t, "tailscale.log"))
	})
}

// The role waits for an earlier run's switch to end before it reads Tailscale SSH's state: with the pending file
// there it does not go on; once it is gone it does.
func TestFunctionalTailscaleRerunWaitsForAPendingSwitch(t *testing.T) {
	t.Parallel()
	wait := roleTask(t, "roles/tailscale/tasks/main.yml", "no earlier switch still pending")
	r := newPlayRun(t)
	pending := filepath.Join(r.dir, "pending")
	require.NoError(t, os.WriteFile(pending, nil, 0o600))
	vars := map[string]any{"ts_join": false, "ts_root_daemon": true, "nova_tailscale_ssh_pending": pending, "nova_tailscale_ssh_deadline": -58} // waits 2 s
	tasks := []map[string]any{wait, {"ansible.builtin.debug": map[string]any{"msg": "READ THE STATE"}}}
	out, err := r.play(t, vars, false, tasks)
	require.Error(t, err, out)
	assert.NotContains(t, out, "READ THE STATE")

	go func() {
		time.Sleep(2 * time.Second)
		_ = os.Remove(pending) // ignored: the play below fails on its timeout if the file stays
	}()
	vars["nova_tailscale_ssh_deadline"] = -50 // waits up to 10 s
	start := time.Now()
	out, err = r.play(t, vars, false, tasks)
	require.NoError(t, err, out)
	assert.Contains(t, out, "READ THE STATE")
	assert.GreaterOrEqual(t, time.Since(start), 2*time.Second, "it went on before the earlier switch ended")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func mustUser(t *testing.T) string {
	t.Helper()
	me, err := user.Current()
	require.NoError(t, err)
	return me.Username
}
