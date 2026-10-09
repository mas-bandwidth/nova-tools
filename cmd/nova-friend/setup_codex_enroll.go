package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

var codexEnrollmentTarget = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@:-]{0,254}$`)

// codexRemoteEnrollmentArgs quotes every remote shell argument independently;
// only a reviewed remote nova-friend performs native enrollment (subscription spec).
func codexRemoteEnrollmentArgs(o friend.SetupOptions, target, program, label string, seconds int, build string) ([]string, error) {
	if seconds < 1 || seconds > 900 {
		return nil, errors.New("enrollment timeout needs 1-900 seconds; inspect setup -h")
	}
	if build == "" || build == buildinfo.Unknown || strings.HasSuffix(build, "-dirty") || len(strings.Fields(build)) != 1 {
		return nil, errors.New("remote enrollment requires a recorded clean build version on both hosts; install the same reviewed nova-friend build and inspect setup -h")
	}
	if !codexEnrollmentTarget.MatchString(target) || !filepath.IsAbs(program) || filepath.Clean(program) != program || strings.ContainsAny(program, "\r\n\x00") {
		return nil, errors.New("remote enrollment wants a plain SSH target and absolute --remote-program path to this same reviewed nova-friend release; inspect setup -h")
	}
	if _, err := friend.PlanCodexEnrollment(o, friend.NewMemFS(), label, time.Duration(seconds)*time.Second); err != nil {
		return nil, err
	}
	argv := []string{program, "setup", "--as", o.Friend, "--harness", "codex", "--dir", o.Dir, "--config-dir", o.ConfigDir, "--home", o.Home, "--profile", o.Profile, "--program", o.Program, "--deny-self", strings.Join(o.DenySelf, ","), "--profile-only", "--codex-device-enroll", "--enrollment-label", label, "--enrollment-timeout", fmt.Sprint(seconds), "--enrollment-build", build, "--apply"}
	for i, item := range argv {
		if strings.ContainsAny(item, "\r\n\x00") {
			return nil, errors.New("remote enrollment arguments must each fit one public line; inspect setup -h")
		}
		argv[i] = "'" + strings.ReplaceAll(item, "'", "'\\''") + "'"
	}
	return []string{"-tt", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ClearAllForwardings=yes", "-o", "ForwardAgent=no", "-o", "ForwardX11=no", "-o", "PermitLocalCommand=no", "-o", "ControlPath=none", "-o", "ControlMaster=no", "--", target, "exec " + strings.Join(argv, " ")}, nil
}

// codexRemoteEnrollment keeps native output on the controlling terminal and
// never stores or returns device codes, native logs or credentials (spec).
func codexRemoteEnrollment(ctx context.Context, terminal *os.File, args []string, seconds int) error {
	return friend.CodexEnrollmentTransport(ctx, terminal, args, time.Duration(seconds+120)*time.Second)
}

func (w world) setupCodexRemote(c *tool.Call, o friend.SetupOptions) *tool.Out {
	if c.Bool("apply") && !c.DryRun() && c.Bool("json") {
		return setupRemedy(tool.Refuse("Codex enrollment refuses JSON mode; run the inspected command in the person's terminal"))
	}
	args, err := codexRemoteEnrollmentArgs(o, c.Str("target"), c.Str("remote-program"), c.Str("enrollment-label"), c.Int("enrollment-timeout"), buildinfo.Version(version))
	if err != nil {
		return setupRemedy(tool.Refuse(err.Error()))
	}
	out := tool.Done().Fact("harness", "codex").Fact("target", c.Str("target")).Fact("profile", o.Profile).Fact("account_label", c.Str("enrollment-label")).Fact("account_identity", "unverified").Fact("applied", false)
	out.Item("remote_native", "program", "/usr/bin/ssh", "args", args)
	if c.DryRun() || !c.Bool("apply") {
		return out
	}
	enrollmentCtx, stopEnrollment := codexEnrollmentSignals(c.Ctx)
	defer stopEnrollment()
	terminal, err := codexEnrollmentTerminal(c)
	if err != nil {
		return setupRemedy(tool.Refuse(err.Error()))
	}
	defer terminal.Close()
	if _, err = fmt.Fprintf(terminal, "Codex enrollment: target=%s profile=%s account label=%s\nSelect the intended account in the native browser consent flow. Native status leaves account identity unverified; run: nova-friend setup -h.\n", c.Str("target"), o.Profile, c.Str("enrollment-label")); err != nil {
		return setupRemedy(tool.Fail("controlling terminal is unavailable; retry interactively"))
	}
	if err = codexRemoteEnrollment(enrollmentCtx, terminal, args, c.Int("enrollment-timeout")); err != nil {
		return setupRemedy(tool.Fail(err.Error()))
	}
	return tool.Done().Fact("target", c.Str("target")).Fact("profile", o.Profile).Fact("enrollment", "remote_setup_completed").Fact("account_identity", "unverified").Fact("provider_turn", "not_checked")
}

func codexEnrollmentTerminal(c *tool.Call) (*os.File, error) {
	if c.Bool("json") {
		return nil, errors.New("Codex enrollment refuses JSON mode; run the inspected command in the person's terminal")
	}
	terminal, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, errors.New("Codex enrollment requires the person's controlling terminal; run the inspected command interactively")
	}
	if _, err = friend.CodexConsentContext(c.Ctx, terminal); err != nil {
		terminal.Close()
		return nil, err
	}
	return terminal, nil
}
