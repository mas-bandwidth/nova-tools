package friend

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const dshSetupVersion = "0.2.0-rc.2"
const dshSetupOutputMax = 4 << 20

// PlanDSHSetup describes static profile initialization, never a task or a
// provider turn (SPEC-FRIEND, harness setup). The native dump protocol is the
// headless profile of the supported release; an existing profile is audit-only.
// Libraries considered: SettingsFS preserves the existing filesystem seam;
// encoding/json and yaml.v3 validate bounded JSON without a custom parser.
func PlanDSHSetup(o SetupOptions, fs SettingsFS) (SetupPlan, error) {
	plan := SetupPlan{Harness: "dsh"}
	if fs == nil {
		fs = OSFS{}
	}
	if o.Harness != "dsh" || !o.ProfileOnly || o.Profile != "headless" {
		return plan, errors.New("dsh setup requires the headless profile and profile-only scope; run: nova-friend setup -h")
	}
	if o.Provider != "" || o.Model != "" || o.Package != "" || o.BaseURL != "" || o.KeyEnv != "" || o.Seat != "" || len(o.SecretNames) != 0 || o.ContextLimit != 0 || o.OutputLimit != 0 {
		return plan, errors.New("dsh profile initialization accepts no provider, model, credential or model-limit inputs; run: nova-friend setup -h")
	}
	program := o.Program
	if program == "" {
		program = DSHProgram
	}
	if !filepath.IsAbs(program) || filepath.Clean(program) != program {
		return plan, errors.New("dsh setup requires an absolute native program path; run: nova-friend setup -h")
	}
	for _, dir := range []string{o.Dir, o.Home, o.ConfigDir} {
		if err := dshSetupPath(fs, dir, true, KindDir); err != nil {
			return plan, err
		}
	}
	profile := filepath.Join(o.ConfigDir, "profiles", "headless")
	entry, err := fs.Lstat(profile)
	if err != nil {
		return plan, errors.New("dsh profile cannot be inspected; run: nova-friend setup -h")
	}
	if entry.Kind != KindMissing {
		facts, err := dshSetupProfile(fs, o.ConfigDir)
		if err != nil {
			return plan, err
		}
		plan.Facts = append(facts, SetupFact{Name: "schema", State: "not_checked", Detail: "existing profile is preserved; no native command runs"}, SetupFact{Name: "provider", State: "not_checked", Detail: "profile audit establishes no model, credential or availability proof"})
		return plan, nil
	}
	if err := dshSetupFresh(fs, o); err != nil {
		return plan, err
	}
	tmp := filepath.Join(o.ConfigDir, ".nova-friend-setup", "tmp")
	dirs := slices.Compact([]string{o.Dir, o.Home, o.ConfigDir, tmp})
	plan.Steps = append(plan.Steps, SetupStep{Name: "owned-directories", Kind: "write", Path: o.ConfigDir, Want: "real owned directories", Apply: func() ([]SetupFact, error) {
		if err := dshSetupFresh(fs, o); err != nil {
			return nil, err
		}
		for _, dir := range dirs {
			if err := dshSetupPath(fs, dir, true, KindDir); err != nil {
				return nil, err
			}
			if err := fs.MkdirAll(dir, 0o700); err != nil {
				return nil, errors.New("dsh owned directories could not be created; run: nova-friend setup -h")
			}
		}
		return []SetupFact{{Name: "owned-directories", State: "ready"}}, nil
	}})
	env := []string{"HOME=" + o.Home, "DSH_HOME=" + o.ConfigDir, "TMPDIR=" + tmp, "PATH=/usr/bin:/bin:/usr/sbin:/sbin"}
	command := func(name string, args []string, validate func(SetupOutput) ([]SetupFact, error)) {
		plan.Steps = append(plan.Steps, SetupStep{Name: name, Kind: "native", Command: &SetupCommand{Program: program, Args: args, Env: slices.Clone(env), Dir: o.Dir, Timeout: 90 * time.Second}, Validate: validate})
	}
	command("native-version", []string{"--version"}, func(out SetupOutput) ([]SetupFact, error) {
		if err := dshSetupOutput(out); err != nil {
			return nil, err
		}
		if strings.TrimSpace(out.Stdout) != dshSetupVersion {
			return nil, errors.New("dsh native version is unsupported; select the reviewed native release, then run: nova-friend setup -h")
		}
		return []SetupFact{{Name: "native-version", State: "verified", Detail: dshSetupVersion}}, nil
	})
	command("headless-profile-init", []string{"--profile", "headless", "--dump-default-config"}, func(out SetupOutput) ([]SetupFact, error) {
		if err := dshSetupOutput(out); err != nil {
			return nil, err
		}
		if strings.TrimSpace(out.Stdout) == "" {
			return nil, errors.New("dsh default profile dump is empty; inspect the native installation, then run: nova-friend setup -h")
		}
		if _, err := dshSetupProfile(fs, o.ConfigDir); err != nil {
			return nil, err
		}
		if err := dshSetupGeneratedScope(fs, o); err != nil {
			return nil, err
		}
		return []SetupFact{dshSetupHash("default-config", []byte(out.Stdout))}, nil
	})
	command("headless-profile-schema", []string{"--profile", "headless", "--dump-config-schema"}, func(out SetupOutput) ([]SetupFact, error) {
		if err := dshSetupOutput(out); err != nil {
			return nil, err
		}
		object, err := dshSetupObject([]byte(out.Stdout))
		if err != nil {
			return nil, errors.New("dsh native schema is not an unambiguous JSON object; inspect the native installation, then run: nova-friend setup -h")
		}
		meta, err := dshSetupObject(object["x-cordis"])
		var profileName string
		if err != nil || string(meta["complete"]) != "true" || json.Unmarshal(meta["profile"], &profileName) != nil || profileName != "headless" {
			return nil, errors.New("dsh native schema is incomplete; inspect the native installation, then run: nova-friend setup -h")
		}
		var diagnostics []map[string]json.RawMessage
		if len(meta["diagnostics"]) == 0 || string(meta["diagnostics"]) == "null" || json.Unmarshal(meta["diagnostics"], &diagnostics) != nil {
			return nil, errors.New("dsh native schema diagnostics are malformed; inspect the native installation, then run: nova-friend setup -h")
		}
		for _, diagnostic := range diagnostics {
			var level, message string
			if json.Unmarshal(diagnostic["level"], &level) != nil || level != "warning" || string(diagnostic["message"]) == "null" || json.Unmarshal(diagnostic["message"], &message) != nil {
				return nil, errors.New("dsh native schema has a diagnostic failure; inspect the native installation, then run: nova-friend setup -h")
			}
		}
		facts, err := dshSetupProfile(fs, o.ConfigDir)
		if err != nil {
			return nil, err
		}
		if err := dshSetupGeneratedScope(fs, o); err != nil {
			return nil, err
		}
		return append(facts, dshSetupHash("schema", []byte(out.Stdout))), nil
	})
	plan.Facts = []SetupFact{{Name: "provider", State: "not_checked", Detail: "static profile only; no model, credentials, task or availability proof"}}
	return plan, nil
}

// dshSetupPath rejects aliases and non-directory parents before a native
// initializer can write through them (SPEC-FRIEND, harness setup).
func dshSetupPath(fs SettingsFS, path string, missing bool, kind string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Dir(path) == path {
		return errors.New("dsh setup requires explicit non-root absolute paths; run: nova-friend setup -h")
	}
	for at := path; ; at = filepath.Dir(at) {
		entry, err := fs.Lstat(at)
		if err != nil {
			return errors.New("dsh setup path cannot be inspected; run: nova-friend setup -h")
		}
		want := KindDir
		if at == path {
			want = kind
		}
		if entry.Kind != want && !(missing && entry.Kind == KindMissing) {
			return errors.New("dsh setup requires real paths with no symlink or wrong-kind component; run: nova-friend setup -h")
		}
		if filepath.Dir(at) == at {
			return nil
		}
	}
}

// dshSetupFresh pins absence before explicit initialization (SPEC-FRIEND, harness setup).
func dshSetupFresh(fs SettingsFS, o SetupOptions) error {
	if err := dshSetupNoState(fs, o); err != nil {
		return err
	}
	profile := filepath.Join(o.ConfigDir, "profiles", "headless")
	if err := dshSetupPath(fs, profile, true, KindDir); err != nil {
		return err
	}
	entry, err := fs.Lstat(profile)
	if err != nil || entry.Kind != KindMissing {
		return errors.New("dsh initialization requires an absent headless profile; preserve the existing profile and run: nova-friend setup -h")
	}
	return nil
}

// dshSetupNoState keeps static setup free of live or credential state (SPEC-FRIEND, harness setup).
func dshSetupNoState(fs SettingsFS, o SetupOptions) error {
	for _, path := range []string{filepath.Join(o.ConfigDir, "sessions"), filepath.Join(o.ConfigDir, "storages"), filepath.Join(o.ConfigDir, ".credentials.yaml"), filepath.Join(o.ConfigDir, ".env"), filepath.Join(o.Dir, ".env"), filepath.Join(o.ConfigDir, "cordis.patch.yml"), filepath.Join(o.ConfigDir, "profiles", "headless", "node_modules")} {
		entry, err := fs.Lstat(path)
		if err != nil || entry.Kind != KindMissing {
			return errors.New("dsh static initialization refuses credential, environment, custom patch or live session state; preserve it and use a separate owned profile, then run: nova-friend setup -h")
		}
	}
	return nil
}

// dshSetupGeneratedScope admits only the empty shipped user layers before
// schema imports native modules (SPEC-FRIEND, harness setup).
// Libraries considered: yaml.v3 validates the official empty YAML arrays;
// exact JSON maps keep out-of-tree dependencies out of a new static profile.
func dshSetupGeneratedScope(fs SettingsFS, o SetupOptions) error {
	if err := dshSetupNoState(fs, o); err != nil {
		return err
	}
	profile := filepath.Join(o.ConfigDir, "profiles", "headless")
	for _, name := range []string{"cordis.patch.yml", "cordis.yml"} {
		raw, err := fs.ReadFile(filepath.Join(profile, name))
		var layer any
		if err != nil || len(raw) > dshSetupOutputMax || yaml.Unmarshal(raw, &layer) != nil {
			return errors.New("dsh generated static layer is malformed; preserve the profile and run: nova-friend setup -h")
		}
		entries, ok := layer.([]any)
		if !ok || len(entries) != 0 {
			return errors.New("dsh generated static profile contains a user layer; preserve the profile and run: nova-friend setup -h")
		}
	}
	raw, err := fs.ReadFile(filepath.Join(profile, "package.json"))
	if err != nil {
		return errors.New("dsh generated manifest cannot be read; run: nova-friend setup -h")
	}
	manifest, err := dshSetupObject(raw)
	if err != nil {
		return errors.New("dsh generated manifest is malformed; run: nova-friend setup -h")
	}
	dependencies, err := dshSetupObject(manifest["dependencies"])
	if err != nil || len(dependencies) != 0 {
		return errors.New("dsh generated static profile contains local dependencies; preserve the profile and run: nova-friend setup -h")
	}
	return nil
}

// dshSetupProfile audits native-generated bytes without rewriting a profile
// or selecting a model (SPEC-FRIEND, harness setup).
// Libraries considered: encoding/json exact-key maps and yaml.v3 duplicate
// detection preserve native manifest semantics; no alternate manifest is made.
func dshSetupProfile(fs SettingsFS, home string) ([]SetupFact, error) {
	profile := filepath.Join(home, "profiles", "headless")
	if err := dshSetupPath(fs, profile, false, KindDir); err != nil {
		return nil, err
	}
	facts := []SetupFact{{Name: "headless-profile", State: "present"}}
	for _, name := range []string{"package.json", "cordis.patch.yml", "cordis.yml", "pnpm-workspace.yaml"} {
		file := filepath.Join(profile, name)
		if err := dshSetupPath(fs, file, false, KindFile); err != nil {
			return nil, err
		}
		raw, err := fs.ReadFile(file)
		if err != nil || len(raw) > dshSetupOutputMax {
			return nil, errors.New("dsh profile file is unreadable or oversized; preserve the profile and run: nova-friend setup -h")
		}
		if name == "package.json" {
			top, err := dshSetupObject(raw)
			if err != nil {
				return nil, errors.New("dsh profile manifest is malformed; preserve the profile and run: nova-friend setup -h")
			}
			dsh, err := dshSetupObject(top["dsh"])
			if err != nil {
				return nil, errors.New("dsh profile manifest has no exact dsh object; preserve the profile and run: nova-friend setup -h")
			}
			settings, err := dshSetupObject(dsh["profile"])
			var bundles []string
			if err != nil || json.Unmarshal(settings["bundles"], &bundles) != nil || !slices.Equal(bundles, []string{"@deepseek-ai/dsh-base", "@deepseek-ai/dsh-headless"}) {
				return nil, errors.New("dsh profile requires the base and headless bundles in order; preserve the profile and run: nova-friend setup -h")
			}
		}
		facts = append(facts, dshSetupHash(name, raw))
	}
	return facts, nil
}

// dshSetupObject uses the adopted YAML decoder's duplicate-key check before
// the JSON decoder; encoding/json alone silently accepts repeated members
// (SPEC-FRIEND, harness setup).
func dshSetupObject(raw []byte) (map[string]json.RawMessage, error) {
	if len(raw) > dshSetupOutputMax || !json.Valid(raw) {
		return nil, errors.New("invalid JSON")
	}
	var checked any
	if err := yaml.Unmarshal(raw, &checked); err != nil {
		return nil, errors.New("ambiguous JSON")
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return nil, errors.New("JSON is not an object")
	}
	return object, nil
}

// dshSetupOutput returns only public failures (SPEC-FRIEND, harness setup).
func dshSetupOutput(out SetupOutput) error {
	if out.Err != nil || out.Exit != 0 || len(out.Stdout) > dshSetupOutputMax {
		return errors.New("dsh native setup command failed or exceeded its output bound; inspect the native installation, then run: nova-friend setup -h")
	}
	return nil
}

// dshSetupHash reports bounded public evidence, never file contents (SPEC-FRIEND, harness setup).
func dshSetupHash(name string, raw []byte) SetupFact {
	sum := sha256.Sum256(raw)
	return SetupFact{Name: name, State: "verified", Detail: fmt.Sprintf("bytes=%d sha256=%s", len(raw), hex.EncodeToString(sum[:]))}
}
