package friend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/tty"
)

type codexConsentTerminalKey struct{}

var codexEnrollmentProfile = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// CodexConsentContext binds native consent to the person's terminal, never to
// tool output, recordings or JSON (SPEC-CODEX-SUBSCRIPTION-SETUP, enrollment).
func CodexConsentContext(ctx context.Context, terminal *os.File) (context.Context, error) {
	if terminal == nil || !tty.IsTerminal(terminal) {
		return nil, errors.New("Codex enrollment needs the person's controlling terminal; run the inspected setup command interactively")
	}
	return context.WithValue(ctx, codexConsentTerminalKey{}, terminal), nil
}

// PlanCodexEnrollment adds native device consent before status certification.
// Locks and birth-bound receipts reuse FriendSetupLifecycle; no credentials or
// browser account identity are recorded (SPEC-CODEX-SUBSCRIPTION-SETUP).
func PlanCodexEnrollment(o SetupOptions, fs SettingsFS, label string, timeout time.Duration) (SetupPlan, error) {
	if fs == nil {
		fs = OSFS{}
	}
	if strings.TrimSpace(label) == "" || len(label) > 128 || strings.ContainsAny(label, "\r\n\x00") {
		return SetupPlan{}, errors.New("Codex enrollment wants a public account label of 1-128 bytes on one line; name the browser account selection explicitly")
	}
	if !codexEnrollmentProfile.MatchString(o.Profile) || timeout < time.Second || timeout > 15*time.Minute {
		return SetupPlan{}, errors.New("Codex enrollment wants a stable safe profile ID and --enrollment-timeout of 1-900 seconds; inspect setup -h")
	}
	p, err := PlanCodexSetup(o, fs)
	if err != nil {
		return p, err
	}
	status := p.Steps[len(p.Steps)-1]
	originalStatus := status.Validate
	status.Validate = func(out SetupOutput) ([]SetupFact, error) {
		facts, err := originalStatus(out)
		if err != nil {
			return facts, err
		}
		if len(facts) == 0 || facts[0].State != "local_chatgpt_auth" {
			return facts, errors.New("Codex device consent lacks subsequent ChatGPT auth status; retry after verified exit and no profile is certified")
		}
		return facts, nil
	}
	command := *status.Command
	logDir := filepath.Join(o.ConfigDir, "log")
	if err := SetupPath(fs, logDir, true); err != nil {
		return SetupPlan{}, err
	}
	command.Args = []string{"-c", "log_dir=" + strconv.Quote(logDir), "login", "--device-auth"}
	command.Timeout = timeout
	command.AllowedExits = []int{0}
	command.StatusOnStderr = false
	command.ConsentOnTerminal = true
	consent := SetupStep{Name: "codex_device_consent", Kind: "native_consent", Path: o.ConfigDir, Want: "person selects " + label + " in the native browser flow", Command: &command, Validate: func(out SetupOutput) ([]SetupFact, error) {
		if out.Err != nil || out.Exit != 0 || out.Stdout != "" {
			return nil, errors.New("Codex device consent did not finish safely; retry interactively after verified native exit")
		}
		return []SetupFact{{Name: "device_consent", State: "native_completed", Detail: "selected browser account identity remains unverified"}}, nil
	}}
	logs := SetupStep{Name: "codex_private_logs", Kind: "local_write", Path: logDir, Want: "private native diagnostic directory", Apply: func() ([]SetupFact, error) {
		if err := SetupPath(fs, logDir, true); err != nil {
			return nil, err
		}
		if err := fs.MkdirAll(logDir, 0700); err != nil {
			return nil, err
		}
		return []SetupFact{{Name: "native_log_directory", State: "private", Detail: logDir}}, nil
	}}
	p.Steps = append(p.Steps[:len(p.Steps)-1], logs, consent, status)
	p.Facts = append(p.Facts, SetupFact{Name: "enrollment_label", State: "user_selected", Detail: label})
	return p, nil
}
