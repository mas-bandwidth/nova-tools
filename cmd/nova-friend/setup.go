package main

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

func (w world) setupVerb() tool.Verb {
	return tool.Verb{Name: "setup", Usage: "setup --as <me> --harness <h> --dir <d> --config-dir <d> --home <d> --profile <p> --program <exe> --deny-self <d,...> [--profile-only | --provider <p> --model <m> --key-env NAME --secrets NAME --seat <seat>] [--apply] [--dry-run]",
		Example: "setup --as worker --harness opencode --dir /tmp/worker --config-dir /tmp/worker-config --home /tmp/worker-home --profile isolated --program /usr/local/bin/opencode --deny-self /tmp/coordinator --profile-only --dry-run",
		Effect:  tool.LocalWrite + ": explicit --apply initializes local harness configuration; otherwise inspects and prints the plan",
		Detail:  "Plans read existing paths and configuration only. --apply runs bounded native initialization or authentication inspection with a complete environment and durable process receipts. It preserves existing profiles and selects no implicit provider/model. Only --codex-device-enroll --apply starts native device consent in the person's terminal, locally or on --target; it never installs or starts the daemon, sends a provider turn, or proves presence. --dry-run always reads only, even with --apply. Credential inputs are sealed names and seat references, never values. An active or unverified native setup receipt holds retries until its group exits.", DryRun: true,
		Flags: func(f *tool.Flags) {
			for _, flag := range []struct{ name, want string }{{"as", "the friend's name"}, {"harness", "the harness to initialize"}, {"dir", "the absolute working directory"}, {"config-dir", "the absolute private harness config root"}, {"home", "the absolute private native home"}, {"profile", "the explicitly selected profile"}, {"program", "the absolute installed native executable"}, {"deny-self", "the absolute coordinator roots excluded from the lane wall, comma-separated"}} {
				f.Required(flag.name, flag.want)
			}
			f.Bool("codex-device-enroll", false, "run native device consent only on explicit apply in the person's terminal; no credentials copied")
			f.String("enrollment-label", "", "public browser account label; native status does not verify account identity")
			f.Int("enrollment-timeout", 600, "native device consent deadline in seconds, 1-900; expiry or cancellation never certifies setup")
			f.String("target", "", "SSH user@host for Codex device enrollment on that execution host")
			f.String("enrollment-build", "", "expected coordinator build version; remote setup refuses a different or unknown build")
			f.String("remote-program", "", "absolute remote nova-friend executable from this same reviewed release")
			f.Bool("apply", false, "apply the inspected local setup plan; no daemon or provider work starts")
			f.Bool("profile-only", false, "initialize the explicit profile without choosing a model or provider")
			for _, flag := range []struct{ name, want string }{{"provider", "the explicit provider ID"}, {"model", "the explicit model ID"}, {"package", "the explicit provider adapter package"}, {"base-url", "the explicit provider endpoint"}, {"key-env", "the sealed credential's environment name, never its value"}, {"secrets", "sealed variable names, comma-separated"}, {"seat", "the nova-secrets seat holding those names"}} {
				f.String(flag.name, "", flag.want)
			}
			f.Int("context-limit", 0, "explicit provider model context limit in tokens; zero leaves it unspecified")
			f.Int("output-limit", 0, "explicit provider model output limit in tokens; zero leaves it unspecified")
		}, Run: w.setup,
	}
}

func (w world) setup(c *tool.Call) *tool.Out {
	dry := c.DryRun()
	opts := friend.SetupOptions{Harness: c.Str("harness"), Friend: c.Str("as"), Dir: c.Str("dir"), ConfigDir: c.Str("config-dir"), Home: c.Str("home"), Profile: c.Str("profile"), Program: c.Str("program"), Provider: c.Str("provider"), Model: c.Str("model"), Package: c.Str("package"), BaseURL: c.Str("base-url"), KeyEnv: c.Str("key-env"), Seat: c.Str("seat"), SecretNames: secretNames(c.Str("secrets")), DenySelf: commaList(c.Str("deny-self")), ProfileOnly: c.Bool("profile-only"), ContextLimit: c.Int("context-limit"), OutputLimit: c.Int("output-limit")}
	enroll := c.Bool("codex-device-enroll")
	if enroll && (c.Int("enrollment-timeout") < 1 || c.Int("enrollment-timeout") > 900) {
		return setupRemedy(tool.Refuse("enrollment timeout needs 1-900 seconds; inspect setup -h"))
	}
	if expected := c.Str("enrollment-build"); expected != "" && (!enroll || expected == buildinfo.Unknown || expected != buildinfo.Version(version)) {
		return setupRemedy(tool.Refuse("enrollment build differs from the coordinator; install the same reviewed nova-friend release and retry"))
	}
	if !enroll && (c.Str("target") != "" || c.Str("remote-program") != "" || c.Str("enrollment-label") != "") {
		return setupRemedy(tool.Refuse("enrollment options need --codex-device-enroll; inspect setup -h"))
	}
	if enroll && c.Str("target") != "" {
		return w.setupCodexRemote(c, opts)
	}
	if enroll && c.Str("remote-program") != "" {
		return setupRemedy(tool.Refuse("--remote-program needs an explicit --target; inspect setup -h"))
	}
	planner := w.setupPlanner
	if planner == nil {
		planner = setupPlanner
	}
	fs := w.settings
	if fs == nil {
		fs = friend.OSFS{}
	}
	if enroll {
		planner = func(o friend.SetupOptions, fs friend.SettingsFS) (friend.SetupPlan, error) {
			return friend.PlanCodexEnrollment(o, fs, c.Str("enrollment-label"), time.Duration(c.Int("enrollment-timeout"))*time.Second)
		}
	}
	plan, err := friend.PlanSetup(opts, fs, planner)
	if err != nil {
		return setupRemedy(tool.Refuse(err.Error()))
	}
	apply := c.Bool("apply") && !dry
	applyCtx := c.Ctx
	if apply {
		if enroll {
			enrollmentCtx, stopEnrollment := codexEnrollmentSignals(c.Ctx)
			defer stopEnrollment()
			applyCtx = enrollmentCtx
			terminal, terminalErr := codexEnrollmentTerminal(c)
			if terminalErr != nil {
				return setupRemedy(tool.Refuse(terminalErr.Error()))
			}
			defer terminal.Close()
			if _, terminalErr = fmt.Fprintf(terminal, "Codex enrollment: local target profile=%s account label=%s\nSelect the intended account in the native browser consent flow.\n", opts.Profile, c.Str("enrollment-label")); terminalErr != nil {
				return setupRemedy(tool.Fail("controlling terminal is unavailable; retry interactively"))
			}
			applyCtx, terminalErr = friend.CodexConsentContext(applyCtx, terminal)
			if terminalErr != nil {
				return setupRemedy(tool.Refuse(terminalErr.Error()))
			}
		}
		runner := w.setupRunner
		if runner == nil {
			native := friend.NativeSetupRunner()
			runner = &native
		}
		plan, err = runner.Apply(applyCtx, plan, filepath.Join(opts.ConfigDir, ".nova-friend-setup"))
		if err != nil {
			return setupOut(plan, apply, setupRemedy(tool.Fail(err.Error())))
		}
	}
	return setupOut(plan, apply, tool.Done())
}

func setupOut(plan friend.SetupPlan, apply bool, out *tool.Out) *tool.Out {
	out.Fact("harness", plan.Harness).Fact("apply_requested", apply).Fact("applied", apply && out.Status == tool.OK)
	for _, step := range plan.Steps {
		out.Item("step", "name", step.Name, "kind", step.Kind, "path", step.Path, "want", step.Want)
		if step.Command != nil {
			out.Item("native", "name", step.Name, "program", step.Command.Program, "args", step.Command.Args, "env", step.Command.Env, "dir", step.Command.Dir, "timeout", step.Command.Timeout.String())
		}
	}
	for _, fact := range plan.Facts {
		out.Item("fact", "name", fact.Name, "state", fact.State, "detail", fact.Detail)
	}
	return out
}

func setupPlanner(opts friend.SetupOptions, fs friend.SettingsFS) (friend.SetupPlan, error) {
	switch opts.Harness {
	case "opencode":
		return friend.PlanOpenCodeSetup(opts, fs)
	case "codex":
		return friend.PlanCodexSetup(opts, fs)
	case "claude":
		return friend.PlanClaudeSetup(opts, fs)
	case "dsh":
		return friend.PlanDSHSetup(opts, fs)
	}
	return friend.SetupPlan{}, setupUnsupported(opts.Harness)
}

func setupUnsupported(harness string) error {
	return fmt.Errorf("setup harness %q is unsupported; select opencode, codex, claude or dsh after their initializer is available", harness)
}

func setupRemedy(out *tool.Out) *tool.Out { out.Remedy = "nova-friend setup -h"; return out }
