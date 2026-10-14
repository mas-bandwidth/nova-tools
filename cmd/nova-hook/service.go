package main

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
	"github.com/mas-bandwidth/nova-tools/pkg/units"
	"github.com/redis/go-redis/v9"
)

const (
	serviceRunEnv = "NOVA_HOOK_SERVICE_RUN"
	serviceField  = "service"
)

// serviceManager manages the one supervised nova-hook client service per friend
// (launchd on macOS, systemd --user on Linux).
type serviceManager interface {
	Held(as string) (string, bool)
	Install(as string, args []string) (string, error)
	Remove(as string) error
}

func hookServiceName(goos, as string) string {
	if goos == "darwin" {
		return "nova-hook.client." + as
	}
	return "nova-hook-" + as + ".service"
}

func hookServiceFile(goos, as string) string {
	if goos == "darwin" {
		return hookServiceName(goos, as) + ".plist"
	}
	return hookServiceName(goos, as)
}

func hookServiceText(goos, as, exe string, args []string, logPath string) string {
	cmdArgs := append([]string{exe, "hook"}, args...)
	env := [][2]string{{serviceRunEnv, "1"}}
	if goos == "darwin" {
		return units.LaunchdPlist(hookServiceName(goos, as), cmdArgs, env, logPath, 1)
	}
	return units.SystemdUnit("nova-hook supervised client for "+as, cmdArgs, env, 1)
}

type osServiceManager struct {
	goos   string
	home   func() (string, error)
	exe    func() (string, error)
	getenv func(string) string
	load   func(goos, op, path string) error
}

func newOSServiceManager(goos string, home func() (string, error), exe func() (string, error), getenv func(string) string, load func(goos, op, path string) error) serviceManager {
	return &osServiceManager{
		goos:   goos,
		home:   home,
		exe:    exe,
		getenv: getenv,
		load:   load,
	}
}

func (m *osServiceManager) unitPath(as string) (string, error) {
	h, err := m.home()
	if err != nil {
		return "", err
	}
	dir := units.Dir(m.goos, h, m.getenv)
	return filepath.Join(dir, hookServiceFile(m.goos, as)), nil
}

func (m *osServiceManager) Held(as string) (string, bool) {
	path, err := m.unitPath(as)
	if err != nil {
		return "", false
	}
	if _, err := os.Stat(path); err == nil {
		return hookServiceName(m.goos, as), true
	}
	return "", false
}

func (m *osServiceManager) Install(as string, args []string) (string, error) {
	path, err := m.unitPath(as)
	if err != nil {
		return "", err
	}
	exe, err := m.exe()
	if err != nil {
		return "", err
	}
	h, err := m.home()
	if err != nil {
		return "", err
	}
	logPath := ""
	if m.goos == "darwin" {
		logDir := filepath.Join(h, ".local", "state", "nova-hook")
		if err := os.MkdirAll(logDir, 0o700); err != nil {
			return "", err
		}
		logPath = filepath.Join(logDir, as+".log")
	}
	text := hookServiceText(m.goos, as, exe, args, logPath)
	inst := units.Installer{
		Dir:    filepath.Dir(path),
		Load:   func(p string) error { return m.load(m.goos, "load", p) },
		Unload: func(p string) error { return m.load(m.goos, "unload", p) },
	}
	if _, err := inst.Write(path, text); err != nil {
		return "", err
	}
	return hookServiceName(m.goos, as), nil
}

func (m *osServiceManager) Remove(as string) error {
	path, err := m.unitPath(as)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	inst := units.Installer{
		Dir:    filepath.Dir(path),
		Load:   func(p string) error { return m.load(m.goos, "load", p) },
		Unload: func(p string) error { return m.load(m.goos, "unload", p) },
	}
	_, err = inst.Remove(path)
	return err
}

func (w world) isServiceRun() bool {
	return w.getenv != nil && strings.TrimSpace(w.getenv(serviceRunEnv)) == "1"
}

func (w world) heldService(ctx context.Context, s *session) (string, bool) {
	if w.service != nil {
		if svc, ok := w.service.Held(s.as); ok && svc != "" {
			return svc, true
		}
	}
	if s != nil && s.hooks.C != nil {
		st, _, err := s.hooks.Read(ctx, s.as)
		if err == nil && len(st) > 0 && st[0].State != hook.StateNone && st[0].State != hook.StateUnhooked {
			svc, err := s.hooks.C.HGet(ctx, hook.RecordKey(s.as), serviceField).Result()
			if err == nil && strings.TrimSpace(svc) != "" {
				return strings.TrimSpace(svc), true
			}
		}
	}
	return "", false
}

func (w world) recordService(ctx context.Context, s *session, svc string) error {
	if s == nil || s.hooks.C == nil || svc == "" {
		return nil
	}
	return s.hooks.C.HSet(ctx, hook.RecordKey(s.as), serviceField, svc).Err()
}

func (w world) clearService(ctx context.Context, s *session) error {
	if s == nil || s.hooks.C == nil {
		return nil
	}
	err := s.hooks.C.HDel(ctx, hook.RecordKey(s.as), serviceField).Err()
	if errors.Is(err, redis.Nil) {
		return nil
	}
	return err
}

func hookServiceArgs(c *tool.Call, as string, role hook.Role, addr string) []string {
	args := []string{"--as", as, "--role", string(role)}
	if addr != "" {
		args = append(args, "--redis", addr)
	}
	for _, flag := range []string{
		"harness",
		"grok-home",
		"muse-bundle",
		"muse-bin",
		"codex-socket",
		"codex-thread",
		"codex-events-dir",
		"codex-since",
		"dsh-events-dir",
		"dsh-hooks-file",
		"dsh-session",
		"dsh-since",
		"antigravity-dir",
		"antigravity-session",
		"session",
		"antigravity-server",
		"antigravity-events-dir",
		"antigravity-since",
	} {
		if v := strings.TrimSpace(c.Str(flag)); v != "" {
			args = append(args, "--"+flag, v)
		}
	}
	return args
}

func (w world) retryBackoff(attempt int) time.Duration {
	base := w.tick
	if base <= 0 {
		base = Tick
	}
	shift := attempt
	if shift > 5 {
		shift = 5
	}
	d := base * time.Duration(1<<shift)
	if d > hook.RenewEvery {
		d = hook.RenewEvery
	}
	return d
}

func (w world) sleep(ctx context.Context, d time.Duration) bool {
	if w.wait != nil {
		w.wait(ctx, d)
		return ctx.Err() == nil
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return ctx.Err() == nil
	}
}

func (w world) openHookSession(c *tool.Call) (*session, *tool.Out) {
	if !w.isServiceRun() || c.DryRun() || c.Bool("once") {
		return w.openSession(c, true)
	}
	ctx, stop := w.signals(context.Background())
	defer stop()
	lastFault := ""
	for attempt := 0; ; attempt++ {
		s, refused := w.openSession(c, true)
		if refused == nil {
			return s, nil
		}
		why := strings.Join(refused.Why, "; ")
		if why != lastFault {
			lastFault = why
			line(c, tool.Done().As("STATE").Fact("state", "store-retry").Note("the bus store did not answer: "+why+"; retrying with backoff"))
		}
		if !w.sleep(ctx, w.retryBackoff(attempt)) {
			return nil, tool.Exit(0)
		}
	}
}

func defaultServiceName(as string) string {
	return hookServiceName(runtime.GOOS, as)
}
