package friend

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// PlanOpenCodeSetup prepares the OpenCode-specific part of a friend's local
// setup. The generic runner owns directory creation, direct native execution,
// seat checks and daemon installation; this plan never starts a provider turn.
func PlanOpenCodeSetup(opts SetupOptions, fsys SettingsFS) (SetupPlan, error) {
	plan := SetupPlan{Harness: "opencode"}
	if opts.Harness != "opencode" {
		return plan, errors.New("OpenCode setup requires harness=opencode")
	}
	if opts.ProfileOnly && (opts.Provider != "" || opts.Model != "" || opts.Package != "" || opts.BaseURL != "" || opts.KeyEnv != "" || opts.Seat != "" || len(opts.SecretNames) > 0 || opts.ContextLimit != 0 || opts.OutputLimit != 0) {
		return plan, errors.New("OpenCode --profile-only initializes isolation without a route; remove provider, model, package, endpoint, key, seat and limit inputs")
	}
	for name, value := range map[string]string{"dir": opts.Dir, "home": opts.Home, "config-dir": opts.ConfigDir, "program": opts.Program} {
		if !filepath.IsAbs(value) || filepath.Clean(value) != value {
			return plan, fmt.Errorf("OpenCode %s must be an explicit absolute clean path", name)
		}
	}
	if fsys == nil {
		fsys = OSFS{}
	}
	for _, value := range []string{opts.Dir, opts.Home, opts.ConfigDir} {
		if err := SetupPath(fsys, value, true); err != nil {
			return plan, err
		}
	}
	work, err := fsys.Lstat(opts.Dir)
	if err != nil {
		return plan, err
	}
	if work.Kind != KindDir {
		return plan, fmt.Errorf("OpenCode working directory %s must be a real directory", opts.Dir)
	}
	plan.Facts = []SetupFact{
		{Name: "home", State: "selected", Detail: opts.Home},
		{Name: "config-dir", State: "selected", Detail: opts.ConfigDir},
		{Name: "xdg-config-home", State: "selected", Detail: filepath.Join(opts.Home, ".config")},
		{Name: "xdg-data-home", State: "selected", Detail: filepath.Join(opts.Home, ".local", "share")},
	}
	for _, dir := range []struct{ name, path string }{
		{"xdg-config-home", filepath.Join(opts.Home, ".config")},
		{"xdg-data-home", filepath.Join(opts.Home, ".local", "share")},
	} {
		if err := SetupPath(fsys, dir.path, true); err != nil {
			return plan, err
		}
		e, err := fsys.Lstat(dir.path)
		if err != nil {
			return plan, err
		}
		if e.Kind != KindMissing && e.Kind != KindDir {
			return plan, fmt.Errorf("OpenCode %s %s must be a real directory", dir.name, dir.path)
		}
		if e.Kind == KindMissing {
			name, path := dir.name, dir.path
			plan.Steps = append(plan.Steps, SetupStep{
				Name: name, Kind: "directory", Path: path, Want: "private directory",
				Apply: func() ([]SetupFact, error) {
					if err := SetupPath(fsys, path, true); err != nil {
						return nil, err
					}
					if err := fsys.MkdirAll(path, 0o700); err != nil {
						return nil, err
					}
					return []SetupFact{{Name: name, State: "ready", Detail: path}}, nil
				},
			})
		}
	}
	if opts.ProfileOnly {
		plan.Facts = append(plan.Facts,
			SetupFact{Name: "route", State: "unselected", Detail: "profile only"},
			SetupFact{Name: "provider-turn", State: "not_checked"})
	} else {
		provider, modelID, ok := strings.Cut(opts.Model, "/")
		if !ok || provider == "" || modelID == "" || provider != opts.Provider {
			return plan, errors.New("OpenCode setup requires an explicit provider/model matching --provider")
		}
		settings := HarnessSettings{Harness: "opencode", Dir: opts.Dir, Model: opts.Model, FS: fsys}
		permissionChanges, err := settings.Plan()
		if err != nil {
			return plan, err
		}
		setup := OpenCodeSetup{
			Dir: opts.Dir, Model: opts.Model, FS: fsys,
			Provider: OpenCodeProvider{ID: opts.Provider, Name: opts.Provider, NPM: opts.Package,
				BaseURL: opts.BaseURL, KeyEnv: opts.KeyEnv, ModelID: modelID, ModelName: modelID,
				ContextLimit: opts.ContextLimit, OutputLimit: opts.OutputLimit},
		}
		change, err := setup.Plan()
		if err != nil {
			return plan, err
		}
		plan.Facts = append(plan.Facts,
			SetupFact{Name: "route", State: "configured_unverified", Detail: opts.Model},
			SetupFact{Name: "key-env", State: "selected", Detail: opts.KeyEnv},
			SetupFact{Name: "provider-turn", State: "not_checked"})
		if change.Changed() {
			plan.Steps = append(plan.Steps, SetupStep{
				Name: "opencode-config", Kind: "file", Path: change.File,
				Want: "explicit provider/model and environment key reference",
				Apply: func() ([]SetupFact, error) {
					wrote, err := setup.Write()
					if err != nil {
						return nil, err
					}
					state := "unchanged"
					if wrote.Changed() {
						state = "written"
					}
					return []SetupFact{{Name: "opencode-config", State: state, Detail: change.File}}, nil
				},
			})
		}
		if len(permissionChanges) > 0 {
			plan.Steps = append(plan.Steps, SetupStep{
				Name: "opencode-permission", Kind: "file", Path: change.File,
				Want: "allow the selected project directory for headless tool calls",
				Apply: func() ([]SetupFact, error) {
					written, err := settings.Write()
					if err != nil {
						return nil, err
					}
					state := "unchanged"
					if len(written) > 0 {
						state = "written"
					}
					return []SetupFact{{Name: "opencode-permission", State: state, Detail: change.File}}, nil
				},
			})
		}
	}
	plan.Steps = append(plan.Steps, SetupStep{
		Name: "opencode-version", Kind: "native",
		Command: &SetupCommand{Program: opts.Program, Args: []string{"--version"}, Env: openCodeSetupEnv(opts), Dir: opts.Dir, Timeout: 15 * time.Second},
		Validate: func(out SetupOutput) ([]SetupFact, error) {
			if out.Err != nil || out.Exit != 0 || strings.TrimSpace(out.Stdout) == "" {
				return nil, errors.New("OpenCode native --version failed; check the selected program and private profile")
			}
			return []SetupFact{{Name: "opencode-version", State: "observed", Detail: opts.Program}}, nil
		},
	})
	return plan, nil
}

func openCodeSetupEnv(opts SetupOptions) []string {
	return []string{
		"HOME=" + opts.Home,
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin",
		"XDG_CONFIG_HOME=" + filepath.Join(opts.Home, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(opts.Home, ".local", "share"),
		"OPENCODE_CONFIG=" + filepath.Join(opts.Dir, OpenCodeConfig),
		"OPENCODE_CONFIG_DIR=" + opts.ConfigDir,
	}
}

// OpenCodeProvider is the explicit metadata for one provider and model. The
// key is named, never supplied: OpenCode resolves its value from the daemon's
// environment after nova-secrets exec opens the named seat secret.
type OpenCodeProvider struct {
	ID, Name, NPM, BaseURL, KeyEnv string
	ModelID, ModelName             string
	ContextLimit, OutputLimit      int
}

// OpenCodeSetup prepares one project's provider route without consulting a
// global OpenCode config or copying an account. The launcher must separately
// give OpenCode private XDG paths and the named secret in its environment.
type OpenCodeSetup struct {
	Dir, Model string
	Provider   OpenCodeProvider
	FS         SettingsFS
}

// OpenCodeSetupPlan reports which keys would change. It never contains an
// existing config value, so even a pre-existing literal credential is not
// copied into command output.
type OpenCodeSetupPlan struct {
	File    string
	Changes []string
}

func (p OpenCodeSetupPlan) Changed() bool { return len(p.Changes) > 0 }

var openCodeEnvName = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// Plan validates an explicit OpenCode route and compares it with the project
// config. It reads no global config and writes nothing.
func (s OpenCodeSetup) Plan() (OpenCodeSetupPlan, error) {
	p, _, err := s.prepare()
	return p, err
}

// Write applies the plan to the project config atomically. Unknown keys and
// other providers survive; a second identical call writes nothing.
func (s OpenCodeSetup) Write() (OpenCodeSetupPlan, error) {
	p, cfg, err := s.prepare()
	if err != nil || !p.Changed() {
		return p, err
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return p, fmt.Errorf("%s: render OpenCode config: %w", p.File, err)
	}
	if err := s.fsys().WriteFile(p.File, append(raw, '\n'), 0o600); err != nil {
		return p, fmt.Errorf("%s: write OpenCode config: %w", p.File, err)
	}
	return p, nil
}

func (s OpenCodeSetup) fsys() SettingsFS {
	if s.FS != nil {
		return s.FS
	}
	return OSFS{}
}

// prepare has one source of truth for dry-run and write. The project config
// is the highest ordinary config layer and pins both route and env reference.
func (s OpenCodeSetup) prepare() (OpenCodeSetupPlan, map[string]any, error) {
	p := OpenCodeSetupPlan{File: filepath.Join(s.Dir, OpenCodeConfig)}
	if err := s.validate(); err != nil {
		return p, nil, err
	}
	fsys := s.fsys()
	dir, err := fsys.Lstat(s.Dir)
	if err != nil {
		return p, nil, err
	}
	if dir.Kind != KindDir {
		return p, nil, fmt.Errorf("%s must be a real directory for OpenCode setup; create it and retry", s.Dir)
	}
	file, err := fsys.Lstat(p.File)
	if err != nil {
		return p, nil, err
	}
	if file.Kind != KindMissing && file.Kind != KindFile {
		return p, nil, fmt.Errorf("%s must be a regular file, not %s; name the real project directory", p.File, file.Kind)
	}
	original := map[string]any{}
	if file.Kind == KindFile {
		raw, err := fsys.ReadFile(p.File)
		if err != nil {
			return p, nil, err
		}
		original, err = parseOpenCodeConfig(string(raw), p.File)
		if err != nil {
			return p, nil, err
		}
	}
	cfg := cloneOpenCodeMap(original)
	put := func(object map[string]any, key string, value any, label string) {
		if !reflect.DeepEqual(object[key], value) {
			object[key] = value
			p.Changes = append(p.Changes, label)
		}
	}
	if file.Kind == KindMissing {
		put(cfg, "$schema", "https://opencode.ai/config.json", "$schema")
	}
	if err := openCodeSameIfPresent(cfg, "model", s.Model, "model"); err != nil {
		return p, nil, err
	}
	put(cfg, "model", s.Model, "model")
	providers, err := openCodeObject(cfg, "provider", p.File)
	if err != nil {
		return p, nil, err
	}
	provider, err := openCodeObject(providers, s.Provider.ID, p.File)
	if err != nil {
		return p, nil, err
	}
	if err := openCodeSameIfPresent(provider, "npm", s.Provider.NPM, "provider package"); err != nil {
		return p, nil, err
	}
	put(provider, "npm", s.Provider.NPM, "provider."+s.Provider.ID+".npm")
	if _, exists := provider["name"]; !exists {
		put(provider, "name", s.Provider.Name, "provider."+s.Provider.ID+".name")
	}
	options, err := openCodeObject(provider, "options", p.File)
	if err != nil {
		return p, nil, err
	}
	if err := openCodeSameIfPresent(options, "baseURL", s.Provider.BaseURL, "provider base URL"); err != nil {
		return p, nil, err
	}
	put(options, "baseURL", s.Provider.BaseURL, "provider."+s.Provider.ID+".options.baseURL")
	keyReference := "{env:" + s.Provider.KeyEnv + "}"
	if err := openCodeSameIfPresent(options, "apiKey", keyReference, "provider key reference"); err != nil {
		return p, nil, err
	}
	put(options, "apiKey", keyReference, "provider."+s.Provider.ID+".options.apiKey")
	models, err := openCodeObject(provider, "models", p.File)
	if err != nil {
		return p, nil, err
	}
	model, err := openCodeObject(models, s.Provider.ModelID, p.File)
	if err != nil {
		return p, nil, err
	}
	if _, exists := model["name"]; !exists {
		put(model, "name", s.Provider.ModelName, "provider."+s.Provider.ID+".models."+s.Provider.ModelID+".name")
	}
	if s.Provider.ContextLimit > 0 {
		limit, err := openCodeObject(model, "limit", p.File)
		if err != nil {
			return p, nil, err
		}
		contextLimit, outputLimit := json.Number(strconv.Itoa(s.Provider.ContextLimit)), json.Number(strconv.Itoa(s.Provider.OutputLimit))
		if err := openCodeSameIfPresent(limit, "context", contextLimit, "model context limit"); err != nil {
			return p, nil, err
		}
		if err := openCodeSameIfPresent(limit, "output", outputLimit, "model output limit"); err != nil {
			return p, nil, err
		}
		put(limit, "context", contextLimit, "provider."+s.Provider.ID+".models."+s.Provider.ModelID+".limit.context")
		put(limit, "output", outputLimit, "provider."+s.Provider.ID+".models."+s.Provider.ModelID+".limit.output")
	}
	return p, cfg, nil
}

func openCodeSameIfPresent(object map[string]any, key string, want any, field string) error {
	if have, exists := object[key]; exists && !reflect.DeepEqual(have, want) {
		return fmt.Errorf("OpenCode %s conflicts with the selected route; preserve this profile and use an explicit migration", field)
	}
	return nil
}

func (s OpenCodeSetup) validate() error {
	p := s.Provider
	if !filepath.IsAbs(s.Dir) || filepath.Clean(s.Dir) != s.Dir {
		return fmt.Errorf("OpenCode --dir must be an absolute clean path")
	}
	if p.ID == "" || p.ModelID == "" || strings.ContainsAny(p.ID+p.ModelID, "/\\ \t\n") || s.Model != p.ID+"/"+p.ModelID {
		return fmt.Errorf("OpenCode model must explicitly match provider ID and model ID as provider/model")
	}
	if p.Name == "" || p.ModelName == "" || p.NPM == "" {
		return errors.New("OpenCode provider name, package and model name are required explicitly")
	}
	u, err := url.Parse(p.BaseURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("OpenCode provider base URL must be an explicit HTTPS URL without credentials or query")
	}
	if !openCodeEnvName.MatchString(p.KeyEnv) {
		return errors.New("OpenCode provider key must be an environment variable name, never a value")
	}
	if p.ContextLimit < 0 || p.OutputLimit < 0 || (p.ContextLimit == 0) != (p.OutputLimit == 0) {
		return errors.New("OpenCode model limits must both be positive or both omitted")
	}
	return nil
}

func openCodeObject(parent map[string]any, key, file string) (map[string]any, error) {
	v, ok := parent[key]
	if !ok {
		m := map[string]any{}
		parent[key] = m
		return m, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: %s must be a JSON object; fix it before setup", file, key)
	}
	return m, nil
}

func cloneOpenCodeMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if nested, ok := v.(map[string]any); ok {
			v = cloneOpenCodeMap(nested)
		}
		out[k] = v
	}
	return out
}
