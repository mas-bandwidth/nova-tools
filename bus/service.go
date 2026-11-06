package main

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// A hook started inside an AI's own session blocks that session; --service
// runs it beside the session instead, as a per-user unit the system keeps
// running: a launchd agent on macOS, a systemd --user unit elsewhere. The
// unit runs exactly the hook command, with NOVA_BUS_REDIS, NOVA_BUS_USER and
// NOVA_BUS_PASSWORD in its environment and never on its command line, and
// appends its stdout and stderr to ~/.local/state/nova-bus/<name>.log. The
// harness is checked before the unit is written (newWaker).
const serviceWait = 45 * time.Second

type unit struct {
	label, path, log, mgr string
}

func unitFor(s sys, name string) unit {
	u := unit{log: filepath.Join(s.home, ".local", "state", "nova-bus", name+".log")}
	if s.goos == "darwin" {
		u.label, u.mgr = "nova-bus.hook."+name, "launchctl"
		u.path = filepath.Join(s.home, "Library", "LaunchAgents", u.label+".plist")
	} else {
		u.label, u.mgr = "nova-bus-hook-"+name+".service", "systemctl"
		u.path = filepath.Join(s.home, ".config", "systemd", "user", u.label)
	}
	return u
}

func xmlText(v string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(v)) // a bytes.Buffer never fails a write
	return b.String()
}

// sdQuote quotes one word for a systemd unit file: specifiers (%) and
// variables ($) are doubled, so the value is taken as it is.
func sdQuote(v string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$", "\n", `\n`)
	return `"` + r.Replace(v) + `"`
}

func (u unit) text(cmd []string, env [][2]string) string {
	if u.mgr == "launchctl" {
		var b strings.Builder
		b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>` + xmlText(u.label) + `</string>
	<key>ProgramArguments</key>
	<array>
`)
		for _, a := range cmd {
			b.WriteString("\t\t<string>" + xmlText(a) + "</string>\n")
		}
		b.WriteString("\t</array>\n\t<key>EnvironmentVariables</key>\n\t<dict>\n")
		for _, kv := range env {
			b.WriteString("\t\t<key>" + kv[0] + "</key><string>" + xmlText(kv[1]) + "</string>\n")
		}
		b.WriteString("\t</dict>\n\t<key>KeepAlive</key><true/>\n\t<key>RunAtLoad</key><true/>\n" +
			"\t<key>ThrottleInterval</key><integer>10</integer>\n" +
			"\t<key>StandardOutPath</key><string>" + xmlText(u.log) + "</string>\n" +
			"\t<key>StandardErrorPath</key><string>" + xmlText(u.log) + "</string>\n</dict>\n</plist>\n")
		return b.String()
	}
	words := make([]string, len(cmd))
	for i, a := range cmd {
		words[i] = sdQuote(a)
	}
	desc := strings.NewReplacer("%", "%%", "\n", " ", "\r", " ").Replace(strings.Join(cmd[1:], " "))
	t := "[Unit]\nDescription=nova-bus " + desc +
		"\n\n[Service]\nExecStart=" + strings.Join(words, " ") + "\n"
	for _, kv := range env {
		t += "Environment=" + sdQuote(kv[0]+"="+kv[1]) + "\n"
	}
	return t + "Restart=always\nRestartSec=10\nStandardOutput=append:" + u.log + "\nStandardError=append:" + u.log +
		"\n\n[Install]\nWantedBy=default.target\n"
}

func (u unit) run(ctx context.Context, s sys, args ...string) error {
	if u.mgr == "systemctl" {
		args = append([]string{"--user"}, args...)
	}
	out, err := s.exec(ctx, nil, u.mgr, args...)
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s", u.mgr, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (u unit) domain(s sys) string { return "gui/" + strconv.Itoa(s.uid) }

func (u unit) running(ctx context.Context, s sys) bool {
	if u.mgr == "launchctl" {
		return u.run(ctx, s, "print", u.domain(s)+"/"+u.label) == nil
	}
	return u.run(ctx, s, "is-active", "--quiet", u.label) == nil
}

func (u unit) start(ctx context.Context, s sys) error {
	if u.mgr == "launchctl" {
		_ = u.run(ctx, s, "bootout", u.domain(s)+"/"+u.label) // ignored: absent when it was not loaded
		return u.run(ctx, s, "bootstrap", u.domain(s), u.path)
	}
	for _, a := range [][]string{{"daemon-reload"}, {"enable", u.label}, {"restart", u.label}} {
		if err := u.run(ctx, s, a...); err != nil {
			return err
		}
	}
	return nil
}

// needManager refuses a system without the service manager, exit 2.
func needManager(s sys, u unit, stderr io.Writer) int {
	if _, err := s.look(u.mgr); err != nil {
		return misuse(stderr, "hook", "--service wants %s on PATH (launchctl on macOS, systemctl elsewhere); without one, run the hook in the foreground under a supervisor of your own", u.mgr)
	}
	return 0
}

// installService writes the unit for cmd (unless it already says exactly
// that), starts it, and waits for its HOOK OK in the log.
func installService(ctx context.Context, s sys, clk Clock, name string, cmd []string, env func(string) string, stdout, stderr io.Writer) int {
	u := unitFor(s, name) // run has checked its manager (needManager)
	var vars [][2]string
	for _, k := range []string{"NOVA_BUS_REDIS", "NOVA_BUS_USER", "NOVA_BUS_PASSWORD"} {
		if v := env(k); v != "" {
			vars = append(vars, [2]string{k, v})
		}
	}
	text := u.text(append([]string{s.exe}, cmd...), vars)
	old, _ := os.ReadFile(u.path) // absent: a first install
	changed := string(old) != text
	for _, d := range []string{filepath.Dir(u.path), filepath.Dir(u.log)} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fail(stderr, "hook", "HOOK SERVICE FAILED name=%s reason=%v", name, err)
		}
	}
	var from int64
	if fi, err := os.Stat(u.log); err == nil {
		from = fi.Size()
	}
	if !changed && u.running(ctx, s) {
		fmt.Fprintf(stdout, "HOOK SERVICE OK name=%s unit=%s log=%s changed=false\n", name, u.label, u.log)
		return 0
	}
	// 0600: the unit holds the Redis password in its environment.
	err := os.WriteFile(u.path, []byte(text), 0o600)
	if err == nil {
		err = os.Chmod(u.path, 0o600)
	}
	if err == nil {
		err = u.start(ctx, s)
	}
	if err != nil {
		return fail(stderr, "hook", "HOOK SERVICE FAILED name=%s unit=%s reason=%s", name, u.label, escaper.Replace(err.Error()))
	}
	want := "HOOK OK name=" + name + " "
	for end := clk.Now().Add(serviceWait); ; {
		b, _ := os.ReadFile(u.log) // absent until the hook has written
		if int64(len(b)) >= from && strings.Contains(string(b[from:]), want) {
			fmt.Fprintf(stdout, "HOOK SERVICE OK name=%s unit=%s log=%s changed=%t\n", name, u.label, u.log, changed)
			return 0
		}
		if !clk.Now().Before(end) {
			return fail(stderr, "hook", "HOOK SERVICE FAILED name=%s unit=%s reason=no HOOK OK in %s; run: tail -n 20 %s", name, u.label, serviceWait, shellQuote(u.log))
		}
		select {
		case <-ctx.Done():
			return fail(stderr, "hook", "HOOK SERVICE FAILED name=%s unit=%s reason=interrupted while it started", name, u.label)
		case <-clk.After(time.Second):
		}
	}
}

// stopService stops and removes the unit; one that is not there is a valid
// empty result.
func stopService(ctx context.Context, s sys, name string, stdout, stderr io.Writer) int {
	u := unitFor(s, name)
	if code := needManager(s, u, stderr); code != 0 {
		return code
	}
	if _, err := os.Stat(u.path); errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(stdout, "HOOK SERVICE OK name=%s unit=%s state=absent\n", name, u.label)
		return 0
	}
	if u.mgr == "launchctl" {
		_ = u.run(ctx, s, "bootout", u.domain(s)+"/"+u.label) // ignored: it may not be loaded
	} else {
		_ = u.run(ctx, s, "disable", "--now", u.label) // ignored: it may not be loaded
	}
	err := os.Remove(u.path)
	if err == nil && u.mgr == "systemctl" {
		err = u.run(ctx, s, "daemon-reload")
	}
	if err != nil {
		return fail(stderr, "hook", "HOOK SERVICE FAILED name=%s unit=%s reason=%s", name, u.label, escaper.Replace(err.Error()))
	}
	fmt.Fprintf(stdout, "HOOK SERVICE OK name=%s unit=%s state=stopped\n", name, u.label)
	return 0
}
