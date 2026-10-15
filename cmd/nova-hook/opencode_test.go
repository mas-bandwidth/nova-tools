package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/harness/notice"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingService is a supervisor that records what it was asked to run.
type recordingService struct {
	mu   sync.Mutex
	args map[string][]string
}

func (s *recordingService) Held(as string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.args[as]
	if !ok {
		return "", false
	}
	return hookServiceName("darwin", as), true
}

func (s *recordingService) Install(as string, args []string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.args == nil {
		s.args = map[string][]string{}
	}
	s.args[as] = append([]string(nil), args...)
	return hookServiceName("darwin", as), nil
}

func (s *recordingService) Remove(as string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.args, as)
	return nil
}

// openCodeWorld is the rig's world with a fake opencode CLI: the server holds
// the sessions named, and every request is recorded. The friend runs the
// setup from inside session (OPENCODE_SESSION_ID), or from nowhere when "".
func openCodeWorld(t *testing.T, r *rig, session string, sessions ...string) (world, *[]string) {
	require.NoError(t, r.h.Client.SAdd(context.Background(), "friends", "alex").Err())
	w := r.world()
	state := t.TempDir()
	env := w.getenv
	w.getenv = func(k string) string {
		switch k {
		case "XDG_STATE_HOME":
			return state
		case openCodeSessionEnv:
			return session
		case "PATH":
			return "/Users/u/.local/bin:/usr/bin"
		}
		return env(k)
	}
	w.lookPath = func(name string) (string, error) {
		if name == "opencode" {
			return "/opt/homebrew/bin/opencode", nil
		}
		return "", errors.New("not found")
	}
	var mu sync.Mutex
	calls := &[]string{}
	w.opencodeAPI = func(bin string) notice.OpenCodeAPI {
		return func(_ context.Context, method, path string, _ []byte) ([]byte, error) {
			mu.Lock()
			*calls = append(*calls, bin+" "+method+" "+path)
			mu.Unlock()
			for _, s := range sessions {
				if method == "GET" && path == "/api/session/"+s {
					return json.Marshal(map[string]any{"data": map[string]any{"id": s}})
				}
			}
			return nil, &notice.OpenCodeStatus{Code: 404}
		}
	}
	return w, calls
}

func TestOneLineOpenCodeSetupInstallsTheSupervisedService(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w, calls := openCodeWorld(t, r, "ses_alex1", "ses_alex1")
	svc := &recordingService{}
	w.service = svc
	var out, errOut bytes.Buffer
	code := run([]string{"hook", "--as", "alex", "--role", "comms", "--harness", "opencode"}, strings.NewReader(""), &out, &errOut, w)
	require.Zero(t, code, out.String()+errOut.String())
	assert.Contains(t, out.String(), "HOOK STATE state=opencode-installed session=ses_alex1")
	assert.Contains(t, out.String(), "service=nova-hook.client.alex")
	assert.Equal(t, []string{"/opt/homebrew/bin/opencode GET /api/session/ses_alex1"}, *calls, "the setup only probes the session")
	args := strings.Join(svc.args["alex"], " ")
	assert.Contains(t, args, "--harness opencode")
	assert.Contains(t, args, "--opencode-bin /opt/homebrew/bin/opencode", "the service runs the CLI by its absolute path")
	assert.Contains(t, args, "--opencode-session ses_alex1", "the service has no OPENCODE_SESSION_ID: the session is in its arguments")
}

func TestOpenCodeForegroundRouteArms(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w, _ := openCodeWorld(t, r, "", "ses_alex2")
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		return ctx, cancel
	}
	var out, errOut bytes.Buffer
	code := run([]string{"hook", "--as", "alex", "--role", "comms", "--harness", "opencode", "--opencode-session", "ses_alex2"}, strings.NewReader(""), &out, &errOut, w)
	require.Zero(t, code, out.String()+errOut.String())
	assert.Contains(t, out.String(), "state=opencode-installed session=ses_alex2")
	assert.Contains(t, out.String(), "HOOK ARMED as=alex role=comms")
	m := noticesRe.FindStringSubmatch(out.String())
	require.NotNil(t, m, out.String())
	info, err := os.Stat(m[1])
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm(), "the notice directory is private")
	assert.Equal(t, "alex-opencode", filepath.Base(m[1]))
}

func TestOpenCodeRouteRefusesBeforeArming(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, session string
		args          []string
		want          string
	}{
		{"no session anywhere", "", nil, "run this from inside it (its shell sets OPENCODE_SESSION_ID)"},
		{"a session id that is not one", "", []string{"--opencode-session", "ses_a/../b"}, "ses_<letters and digits>"},
		{"a session the server does not hold", "ses_gone", nil, "is not on the running opencode server"},
		{"once", "ses_alex1", []string{"--once"}, "omit --once"},
		{"no CLI", "ses_alex1", []string{"--opencode-bin", "nope"}, "nope is not on PATH"},
		{"a relative follow directory", "ses_alex1", []string{"--opencode-dir", "work"}, "clean absolute directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t)
			w, _ := openCodeWorld(t, r, tc.session, "ses_alex1")
			w.service = &recordingService{}
			var out bytes.Buffer
			code := run(append([]string{"hook", "--as", "alex", "--role", "comms", "--harness", "opencode"}, tc.args...), strings.NewReader(""), &out, &out, w)
			assert.Equal(t, 2, code, out.String())
			assert.Contains(t, out.String(), tc.want)
			assert.Equal(t, hook.StateNone, r.h.State(t, "alex").State, "nothing is armed")
		})
	}
}

func TestOpenCodeDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w, calls := openCodeWorld(t, r, "ses_alex1", "ses_alex1")
	res := run([]string{"hook", "--as", "alex", "--role", "comms", "--harness", "opencode", "--dry-run"}, strings.NewReader(""), io.Discard, io.Discard, w)
	assert.Zero(t, res)
	assert.Empty(t, *calls, "a dry run never reaches the server")
	assert.Equal(t, hook.StateNone, r.h.State(t, "alex").State)
}

func TestServicePlistCarriesAPathThatReachesTheHarnessCLI(t *testing.T) {
	t.Parallel()
	args := []string{"--as", "alex", "--role", "comms", "--harness", "opencode", "--opencode-bin", "/opt/homebrew/Cellar/opencode/2.0.25/bin/opencode"}
	path := servicePath("/Users/u/.local/bin::relative:/usr/bin", "/opt/nova/bin/nova-hook", args)
	assert.Equal(t, "/opt/homebrew/Cellar/opencode/2.0.25/bin:/opt/nova/bin:/Users/u/.local/bin:/usr/bin:/opt/homebrew/bin:/usr/local/bin:/bin:/usr/sbin:/sbin", path)
	plist := hookServiceText("darwin", "alex", "/opt/nova/bin/nova-hook", args, "/tmp/alex.log", path)
	assert.Contains(t, plist, "<key>PATH</key>\n\t\t<string>"+path+"</string>")
	assert.Contains(t, servicePath("", "nova-hook", []string{"--harness", "muse"}), "/opt/homebrew/bin", "launchd's bare PATH still reaches Homebrew")
}

var noticesRe = regexp.MustCompile(`notices=(\S+)`)
