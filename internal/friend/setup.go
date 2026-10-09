package friend

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// SetupOptions names an explicit local harness configuration. Credential values
// are never setup inputs; KeyEnv names a secret supplied by the selected seat.
type SetupOptions struct {
	Harness, Friend, Dir, ConfigDir, Profile, Home, Program string
	Provider, Model, Package, BaseURL, KeyEnv, Seat         string
	ClaudeAccountID, ClaudeMachine, ClaudeAccountsPath      string
	ClaudeAccountsSHA256                                    string
	ClaudeAccountsSource                                    string `json:"-"` // apply-only public TSV input; PROFILE persists the owned copy instead
	SecretNames, DenySelf                                   []string
	ProfileOnly                                             bool
	ContextLimit, OutputLimit                               int
}

// SetupFact is a public, bounded observation, never native output or credentials.
type SetupFact struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// SetupCommand is a direct native invocation. Env is the complete environment;
// the runner does not add inherited credentials or execute shell expressions.
type SetupCommand struct {
	Program           string        `json:"program"`
	Args              []string      `json:"args,omitempty"`
	Env               []string      `json:"env,omitempty"`
	Dir               string        `json:"dir"`
	Timeout           time.Duration `json:"timeout"`
	AllowedExits      []int         `json:"allowed_exits,omitempty"`
	StatusOnStderr    bool          `json:"status_on_stderr,omitempty"`
	ConsentOnTerminal bool          `json:"consent_on_terminal,omitempty"`
}

// SetupOutput is available only to a step's validator. Full stdout permits
// native JSON/schema validation; the renderer never emits it.
type SetupOutput struct {
	Stdout string
	Exit   int
	Err    error
}

// SetupStep describes one ordered local write, native invocation, or check.
// Plan builders inspect only; Apply and Validate run only on explicit apply.
// Native child ownership belongs to the generic runner, never to Apply.
type SetupStep struct {
	Name     string                                 `json:"name"`
	Kind     string                                 `json:"kind"`
	Path     string                                 `json:"path,omitempty"`
	Want     string                                 `json:"want,omitempty"`
	Command  *SetupCommand                          `json:"command,omitempty"`
	Apply    func() ([]SetupFact, error)            `json:"-"`
	Validate func(SetupOutput) ([]SetupFact, error) `json:"-"`
}

// SetupPlan is the same value used for inspection and explicit local apply.
type SetupPlan struct {
	Harness string      `json:"harness"`
	Roots   []string    `json:"roots,omitempty"`
	Steps   []SetupStep `json:"steps"`
	Facts   []SetupFact `json:"facts,omitempty"`
}

type SetupPlanner func(SetupOptions, SettingsFS) (SetupPlan, error)

var setupSecretName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// PlanSetup validates explicit selections and reserves no resources. The one
// profile-root ownership record prevents two friends sharing initialization.
// It implements Plan and Inspect's owner binding in tla/FriendSetupLifecycle.tla.
func PlanSetup(opts SetupOptions, fs SettingsFS, planner SetupPlanner) (SetupPlan, error) {
	if fs == nil || planner == nil {
		return SetupPlan{}, errors.New("setup wants a supported harness planner and filesystem; select an available harness")
	}
	var problems []error
	for name, value := range map[string]string{"friend": opts.Friend, "harness": opts.Harness, "profile": opts.Profile} {
		if strings.TrimSpace(value) == "" {
			problems = append(problems, fmt.Errorf("setup wants an explicit %s; name the selected %s", name, name))
		}
	}
	for _, path := range []string{opts.Dir, opts.ConfigDir, opts.Home} {
		if err := SetupPath(fs, path, true); err != nil {
			problems = append(problems, err)
		}
	}
	for _, path := range []string{opts.ConfigDir, opts.Home} {
		if err := setupPrivateRoot(fs, path); err != nil {
			problems = append(problems, err)
		}
	}
	if !filepath.IsAbs(opts.Program) {
		problems = append(problems, errors.New("setup wants --program as an explicit absolute native executable path"))
	}
	if opts.Home == opts.Dir || opts.ConfigDir == opts.Dir || opts.Home == opts.ConfigDir {
		problems = append(problems, errors.New("setup home, config root and working directory must be distinct; name dedicated private roots"))
	}
	if strings.HasPrefix(opts.Home, opts.ConfigDir+string(filepath.Separator)) || strings.HasPrefix(opts.ConfigDir, opts.Home+string(filepath.Separator)) {
		problems = append(problems, errors.New("setup home and config roots must not contain each other; name separate owned roots"))
	}
	if len(opts.DenySelf) == 0 {
		problems = append(problems, errors.New("setup wants --deny-self with the coordinator's real roots; name the paths excluded by the lane wall"))
	}
	for _, path := range opts.DenySelf {
		if err := SetupPath(fs, path, true); err != nil {
			problems = append(problems, err)
		}
	}
	if !opts.ProfileOnly && (opts.Model == "" || opts.Provider == "") {
		problems = append(problems, errors.New("setup wants explicit --provider and --model, or --profile-only for initialization without a model route"))
	}
	if opts.ProfileOnly && (opts.Model != "" || opts.Provider != "" || opts.KeyEnv != "" || opts.Package != "" || opts.BaseURL != "" || opts.Seat != "" || len(opts.SecretNames) > 0 || opts.ContextLimit != 0 || opts.OutputLimit != 0) {
		problems = append(problems, errors.New("--profile-only does not select a model, provider, credential or route; remove route inputs or configure an explicit route"))
	}
	if hasClaudeAdmissionSelection(opts) && !ClaudeTokenSetupRoute(opts) {
		problems = append(problems, errors.New("Claude account manifest inputs require the exact sealed claude.ai/token-default route; remove them from other setup selections"))
	}
	if !opts.ProfileOnly && !dshExplicitAccountRoute(opts) {
		required := ProviderKeyEnv(opts.Model)
		if required != "" {
			if opts.KeyEnv != "" && opts.KeyEnv != required {
				problems = append(problems, fmt.Errorf("selected provider requires sealed name %s; use that --key-env and include it in --secrets", required))
			}
			opts.KeyEnv = required
		} else if opts.KeyEnv == "" && !ClaudeAccountSetupRoute(opts) {
			problems = append(problems, errors.New("unknown provider wants explicit --key-env; name its sealed credential without a value"))
		}
	}
	for _, name := range opts.SecretNames {
		if !setupSecretName.MatchString(name) || setupReservedEnv(name) {
			problems = append(problems, errors.New("--secrets accepts credential variable names only; omit native profile/control environment names and never supply values"))
		}
	}
	if opts.KeyEnv != "" && (!setupSecretName.MatchString(opts.KeyEnv) || !slices.Contains(opts.SecretNames, opts.KeyEnv) || opts.Seat == "") {
		problems = append(problems, errors.New("--key-env wants its sealed name in --secrets and an explicit --seat; seal the credential through nova-secrets"))
	}
	if err := errors.Join(problems...); err != nil {
		return SetupPlan{}, err
	}
	root := filepath.Join(opts.ConfigDir, ".nova-friend-setup")
	ownerPath := filepath.Join(root, "OWNER")
	owner := struct{ Friend, Harness, Dir, Profile, Home string }{opts.Friend, opts.Harness, opts.Dir, opts.Profile, opts.Home}
	want, err := json.Marshal(owner)
	if err != nil {
		return SetupPlan{}, err
	}
	checkOwner := func() error {
		if err := SetupPath(fs, ownerPath, false); err != nil {
			return err
		}
		e, err := fs.Lstat(ownerPath)
		if err != nil {
			return err
		}
		if e.Kind == KindMissing {
			return setupUnownedRoot(fs, opts.ConfigDir)
		}
		raw, err := fs.ReadFile(ownerPath)
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(raw)) != string(want) {
			return errors.New("setup config root belongs to another selection; preserve it and name a different owned config root")
		}
		return nil
	}
	if err := checkOwner(); err != nil {
		return SetupPlan{}, err
	}
	homeState := filepath.Join(opts.Home, ".nova-friend-setup")
	homePointer := filepath.Join(homeState, "PROFILE_ROOT")
	checkHome := func() error {
		if err := SetupPath(fs, homePointer, false); err != nil {
			return err
		}
		e, err := fs.Lstat(homePointer)
		if err != nil {
			return err
		}
		if e.Kind == KindMissing {
			return setupUnownedRoot(fs, opts.Home)
		}
		raw, err := fs.ReadFile(homePointer)
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(raw)) != root {
			return errors.New("setup native home belongs to another config root; preserve it and name a fresh private home")
		}
		return nil
	}
	if err := checkHome(); err != nil {
		return SetupPlan{}, err
	}
	plan, err := planner(opts, fs)
	if err != nil {
		return plan, err
	}
	bind := SetupStep{Name: "profile_owner", Kind: "file", Path: ownerPath, Want: "explicit friend/profile ownership", Apply: func() ([]SetupFact, error) {
		for _, path := range []string{opts.ConfigDir, opts.Home} {
			if err := setupPrivateRoot(fs, path); err != nil {
				return nil, err
			}
		}
		if err := checkOwner(); err != nil {
			return nil, err
		}
		if err := fs.MkdirAll(root, 0700); err != nil {
			return nil, err
		}
		if err := fs.WriteFile(ownerPath, append(want, '\n'), 0600); err != nil {
			return nil, err
		}
		return []SetupFact{{Name: "profile_owner", State: "bound", Detail: opts.Friend}}, nil
	}}
	bindHome := SetupStep{Name: "native_home_owner", Kind: "file", Path: homePointer, Want: "reference to the canonical profile owner", Apply: func() ([]SetupFact, error) {
		if err := setupPrivateRoot(fs, opts.Home); err != nil {
			return nil, err
		}
		if err := checkHome(); err != nil {
			return nil, err
		}
		if err := fs.MkdirAll(homeState, 0700); err != nil {
			return nil, err
		}
		if err := fs.WriteFile(homePointer, []byte(root+"\n"), 0600); err != nil {
			return nil, err
		}
		return []SetupFact{{Name: "native_home_owner", State: "bound", Detail: opts.Home}}, nil
	}}
	plan.Roots = []string{root, homeState}
	plan.Steps = append([]SetupStep{bind, bindHome}, plan.Steps...)
	plan.Steps = append(plan.Steps, SetupStep{Name: "profile_binding", Kind: "file", Path: SetupProfilePath(opts.ConfigDir), Want: "completed public setup selection", Apply: func() ([]SetupFact, error) { return writeSetupProfile(fs, opts) }})
	plan.Facts = append(plan.Facts, SetupFact{Name: "activation", State: "not_requested"}, SetupFact{Name: "presence", State: "not_checked"})
	for _, field := range []struct{ name, value string }{{"friend", opts.Friend}, {"profile", opts.Profile}, {"config_root", opts.ConfigDir}, {"native_home", opts.Home}, {"provider_selection", opts.Provider}, {"model_selection", opts.Model}, {"key_env", opts.KeyEnv}, {"secret_seat", opts.Seat}} {
		if field.value != "" {
			plan.Facts = append(plan.Facts, SetupFact{Name: field.name, State: "selected", Detail: field.value})
		}
	}
	if len(opts.SecretNames) > 0 {
		plan.Facts = append(plan.Facts, SetupFact{Name: "secret_names", State: "referenced_unverified", Detail: strings.Join(opts.SecretNames, ",")})
	}
	plan.Facts = append(plan.Facts, SetupFact{Name: "deny_self", State: "selected", Detail: strings.Join(opts.DenySelf, ",")})
	return plan, nil
}
