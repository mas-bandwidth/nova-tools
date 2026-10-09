package friend

import (
	"bytes"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"time"

	"gopkg.in/yaml.v3"
)

// dshExplicitAccountRoute admits only the explicit native account selection.
// Its authentication is independent of API keys (DSHAccountRoute model).
func dshExplicitAccountRoute(o SetupOptions) bool {
	return o.Harness == "dsh" && !o.ProfileOnly && o.Profile == "headless" && o.Provider == "deepseek-account" && o.Model == "deepseek-account/deepseek-v4-pro"
}

type dshAccountPatch struct {
	ID     string `yaml:"id"`
	Config struct {
		Provider string `yaml:"provider"`
		Model    string `yaml:"model"`
	} `yaml:"config"`
}

// PlanDSHAccountSetup selects public configuration without inspecting account
// tokens, logging in or making a provider request (DSHAccountRoute model).
// Libraries considered: yaml.v3's typed decoder rejects unknown and duplicate
// fields; SettingsFS supplies atomic writes and the native runner owns commands.
func PlanDSHAccountSetup(o SetupOptions, fs SettingsFS) (SetupPlan, error) {
	plan := SetupPlan{Harness: "dsh"}
	if fs == nil {
		fs = OSFS{}
	}
	if !dshExplicitAccountRoute(o) || o.KeyEnv != "" || o.Seat != "" || len(o.SecretNames) != 0 || o.Package != "" || o.BaseURL != "" || o.ContextLimit != 0 || o.OutputLimit != 0 {
		return plan, errors.New("dsh route setup requires explicit deepseek-account/deepseek-v4-pro without API-key, endpoint or credential inputs; preserve the account and run: nova-friend setup -h")
	}
	static := o
	static.ProfileOnly, static.Provider, static.Model = true, "", ""
	plan, err := PlanDSHSetup(static, fs)
	if err != nil {
		return plan, err
	}
	file := filepath.Join(o.ConfigDir, "profiles", "headless", "cordis.patch.yml")
	selected, err := dshAccountRoute(fs, o, true)
	if err != nil {
		return plan, err
	}
	plan.Facts = []SetupFact{{Name: "account-auth", State: "unknown", Detail: "no supported native CLI account-status projection; existing account sign-in through the native UI may be required"}, {Name: "provider-turn", State: "not_checked", Detail: "public route setup never boots an agent or makes a provider request"}}
	audit := SetupStep{Name: "effective-account-route", Kind: "check", Validate: func(SetupOutput) ([]SetupFact, error) {
		if ready, err := dshAccountRoute(fs, o, false); err != nil || !ready {
			return nil, errors.New("dsh effective account route differs; preserve the profile and run: nova-friend setup -h")
		}
		return []SetupFact{{Name: "model-route", State: "selected", Detail: o.Model}}, nil
	}}
	if selected && dshAccountCertified(fs, o) {
		plan.Facts = append(plan.Facts, SetupFact{Name: "model-route", State: "selected", Detail: o.Model})
		plan.Steps = append(plan.Steps, audit)
		return plan, nil
	}
	tmp := filepath.Join(o.ConfigDir, ".nova-friend-setup", "tmp")
	env := []string{"HOME=" + o.Home, "DSH_HOME=" + o.ConfigDir, "TMPDIR=" + tmp, "PATH=/usr/bin:/bin:/usr/sbin:/sbin"}
	plan.Steps = append(plan.Steps, SetupStep{Name: "account-route-version", Kind: "native", Command: &SetupCommand{Program: o.Program, Args: []string{"--version"}, Env: env, Dir: o.Dir, Timeout: 90 * time.Second}, Validate: func(out SetupOutput) ([]SetupFact, error) {
		if err := dshSetupOutput(out); err != nil || string(bytes.TrimSpace([]byte(out.Stdout))) != dshSetupVersion {
			return nil, errors.New("dsh account route requires the reviewed native version; run: nova-friend setup -h")
		}
		return []SetupFact{{Name: "native-version", State: "verified", Detail: dshSetupVersion}}, nil
	}})
	plan.Steps = append(plan.Steps, SetupStep{Name: "account-route", Kind: "file", Path: file, Want: o.Model, Apply: func() ([]SetupFact, error) {
		ready, err := dshAccountRoute(fs, o, false)
		if err != nil {
			return nil, err
		}
		if ready {
			return []SetupFact{{Name: "model-route", State: "selected", Detail: o.Model}}, nil
		}
		patch := dshAccountPatch{ID: "agent-default-model"}
		patch.Config.Provider, patch.Config.Model = o.Provider, "deepseek-v4-pro"
		raw, err := yaml.Marshal([]dshAccountPatch{patch})
		if err != nil {
			return nil, err
		}
		if err := fs.WriteFile(file, raw, 0600); err != nil {
			return nil, errors.New("dsh account route could not be written atomically; preserve the profile and run: nova-friend setup -h")
		}
		if ready, err := dshAccountRoute(fs, o, false); err != nil || !ready {
			return nil, errors.New("dsh account route readback differs; preserve the profile and run: nova-friend setup -h")
		}
		return []SetupFact{{Name: "model-route", State: "selected", Detail: o.Model}}, nil
	}})
	plan.Steps = append(plan.Steps, SetupStep{Name: "account-route-schema", Kind: "native", Command: &SetupCommand{Program: o.Program, Args: []string{"--profile", "headless", "--dump-config-schema"}, Env: env, Dir: o.Dir, Timeout: 90 * time.Second}, Validate: func(out SetupOutput) ([]SetupFact, error) {
		if err := dshSetupValidateSchema(out); err != nil {
			return nil, err
		}
		if ready, err := dshAccountRoute(fs, o, false); err != nil || !ready {
			return nil, errors.New("dsh effective account route drifted during schema inspection; preserve it and run: nova-friend setup -h")
		}
		return []SetupFact{dshSetupHash("account-route-schema", []byte(out.Stdout))}, nil
	}})
	plan.Steps = append(plan.Steps, audit)
	return plan, nil
}

// dshAccountCertified reuses the canonical public PROFILE binding. A static
// epoch or failed later native attempt cannot certify the route epoch.
func dshAccountCertified(fs SettingsFS, o SetupOptions) bool {
	profile, err := ReadSetupProfile(fs, SetupProfilePath(o.ConfigDir))
	return err == nil && profile.RunID != "" && reflect.DeepEqual(profile.Selection, o)
}

// dshAccountRoute audits every user layer that could override the selected
// native route. Empty layers may be filled, conflicting layers stay untouched.
// It implements the model's EffectiveRoute check before publication and use.
func dshAccountRoute(fs SettingsFS, o SetupOptions, missing bool) (bool, error) {
	profile := filepath.Join(o.ConfigDir, "profiles", "headless")
	if err := dshSetupPath(fs, profile, missing, KindDir); err != nil {
		return false, err
	}
	entry, err := fs.Lstat(profile)
	if err != nil || entry.Kind == KindMissing {
		return false, err
	}
	if _, err := dshSetupProfile(fs, o.ConfigDir); err != nil {
		return false, err
	}
	raw, err := fs.ReadFile(filepath.Join(profile, "package.json"))
	manifest, parseErr := dshSetupObject(raw)
	deps, depErr := dshSetupObject(manifest["dependencies"])
	if err != nil || parseErr != nil || depErr != nil || len(deps) != 0 {
		return false, errors.New("dsh account setup refuses local plugin dependencies; preserve the profile and run: nova-friend setup -h")
	}
	for _, file := range []string{filepath.Join(o.ConfigDir, ".env"), filepath.Join(o.Dir, ".env"), filepath.Join(profile, "node_modules")} {
		e, err := fs.Lstat(file)
		if err != nil || e.Kind != KindMissing {
			return false, errors.New("dsh account setup refuses environment or local plugin overrides; preserve them and use a fresh owned profile, then run: nova-friend setup -h")
		}
	}
	credential, err := fs.Lstat(filepath.Join(o.ConfigDir, ".credentials.yaml"))
	if err != nil || (credential.Kind != KindMissing && (credential.Kind != KindFile || credential.Mode.Perm()&0077 != 0)) {
		return false, errors.New("dsh account state must stay in a real owner-private native file; preserve it and use a fresh owned profile, then run: nova-friend setup -h")
	}
	for _, file := range []string{filepath.Join(profile, "cordis.yml"), filepath.Join(o.ConfigDir, "cordis.patch.yml")} {
		if err := dshSetupPath(fs, file, true, KindFile); err != nil {
			return false, err
		}
		e, err := fs.Lstat(file)
		if err != nil {
			return false, err
		}
		if e.Kind != KindMissing {
			raw, err := fs.ReadFile(file)
			var entries []dshAccountPatch
			if err != nil || dshAccountYAML(raw, &entries) != nil || len(entries) != 0 {
				return false, errors.New("dsh account setup refuses a competing user layer; preserve it and use a fresh owned profile, then run: nova-friend setup -h")
			}
		}
	}
	file := filepath.Join(profile, "cordis.patch.yml")
	raw, err = fs.ReadFile(file)
	var entries []dshAccountPatch
	if err != nil || dshAccountYAML(raw, &entries) != nil {
		return false, errors.New("dsh account route patch is malformed; preserve it and run: nova-friend setup -h")
	}
	if len(entries) == 0 {
		return false, nil
	}
	if len(entries) != 1 || entries[0].ID != "agent-default-model" || entries[0].Config.Provider != o.Provider || entries[0].Config.Model != "deepseek-v4-pro" {
		return false, errors.New("dsh account route conflicts with the existing patch; preserve it and use a fresh owned profile, then run: nova-friend setup -h")
	}
	return true, nil
}

// dshAccountYAML accepts one bounded plain YAML list; aliases, expressions,
// duplicate keys and extra fields cannot hide a different effective route.
func dshAccountYAML(raw []byte, into any) error {
	if len(raw) > dshSetupOutputMax {
		return errors.New("oversized YAML")
	}
	var node yaml.Node
	if yaml.Unmarshal(raw, &node) != nil || len(node.Content) != 1 || node.Content[0].Kind != yaml.SequenceNode || !dshAccountPlain(&node) {
		return errors.New("not plain YAML list")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(into); err != nil {
		return err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return errors.New("extra YAML document")
	}
	return nil
}

// dshAccountPlain restricts the route audit to scalar strings and ordinary
// maps/lists; the schema dump never receives executable user expressions.
func dshAccountPlain(n *yaml.Node) bool {
	if n.Kind == yaml.AliasNode || n.Anchor != "" || (n.Kind == yaml.ScalarNode && n.Tag != "!!str") || (n.Kind == yaml.MappingNode && n.Tag != "!!map") || (n.Kind == yaml.SequenceNode && n.Tag != "!!seq") {
		return false
	}
	for _, child := range n.Content {
		if !dshAccountPlain(child) {
			return false
		}
	}
	return true
}
