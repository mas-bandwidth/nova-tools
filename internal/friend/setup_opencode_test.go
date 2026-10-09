package friend

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAllowDirsPreservesUnknownLargeNumberInRealConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, OpenCodeConfig)
	require.NoError(t, os.WriteFile(path, []byte(`{"large_unknown":9007199254740993,"permission":{"bash":"deny"}}`), 0o600))
	wrote, err := AllowDirs(dir, []string{"/friends/one"})
	require.NoError(t, err)
	assert.True(t, wrote)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"large_unknown": 9007199254740993`)
	assert.Contains(t, string(raw), `"/friends/one/**": "allow"`)
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "a private config stays private after expansion")
}

func TestAllowDirsRefusesAmbiguousRealConfigWithoutWriting(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, config string }{
		{"duplicate root", `{"other":1,"other":2}`},
		{"duplicate nested", `{"other":{"apiKey":"literal secret token","apiKey":"another"}}`},
		{"null", `null`},
		{"array", `[]`},
		{"trailing content", `{} {}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, OpenCodeConfig)
			require.NoError(t, os.WriteFile(path, []byte(tc.config), 0o600))
			wrote, err := AllowDirs(dir, []string{"/friends/one"})
			require.Error(t, err)
			assert.False(t, wrote)
			assert.NotContains(t, err.Error(), "literal secret token")
			raw, readErr := os.ReadFile(path)
			require.NoError(t, readErr)
			assert.Equal(t, tc.config, string(raw))
		})
	}
}

func TestAllowDirsRefusesRestrictiveOrMalformedPermissionsWithoutWriting(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, config string }{
		{"root scalar", `{"permission":"deny","other":"literal secret token"}`},
		{"root null", `{"permission":null,"other":"literal secret token"}`},
		{"external deny", `{"permission":{"external_directory":"deny"},"other":"literal secret token"}`},
		{"external ask", `{"permission":{"external_directory":"ask"},"other":"literal secret token"}`},
		{"external number", `{"permission":{"external_directory":1},"other":"literal secret token"}`},
		{"external array", `{"permission":{"external_directory":[]},"other":"literal secret token"}`},
		{"external null", `{"permission":{"external_directory":null},"other":"literal secret token"}`},
		{"target deny", `{"permission":{"external_directory":{"/friends/one/**":"deny"}},"other":"literal secret token"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, OpenCodeConfig)
			require.NoError(t, os.WriteFile(path, []byte(tc.config), 0o600))
			wrote, err := AllowDirs(dir, []string{"/friends/one"})
			require.Error(t, err)
			assert.False(t, wrote)
			assert.NotContains(t, err.Error(), "literal secret token")
			raw, readErr := os.ReadFile(path)
			require.NoError(t, readErr)
			assert.Equal(t, tc.config, string(raw))
		})
	}
}

func openCodeSetupFixture(t *testing.T) (*MemFS, OpenCodeSetup) {
	t.Helper()
	fs := NewMemFS()
	require.NoError(t, fs.MkdirAll("/friends/one", 0o700))
	return fs, OpenCodeSetup{
		Dir:   "/friends/one",
		Model: "example/model-v2",
		Provider: OpenCodeProvider{
			ID: "example", Name: "Example", NPM: "@ai-sdk/openai-compatible",
			BaseURL: "https://api.example.test/v1", KeyEnv: "EXAMPLE_API_KEY",
			ModelID: "model-v2", ModelName: "Model v2",
		},
		FS: fs,
	}
}

func TestOpenCodeSetupPlansWritesAndPreservesUnrelatedConfig(t *testing.T) {
	t.Parallel()
	fs, setup := openCodeSetupFixture(t)
	path := setup.Dir + "/opencode.json"
	require.NoError(t, fs.WriteFile(path, []byte(`{"model":"example/model-v2","large_unknown":9007199254740993,"permission":{"bash":"deny"},"provider":{"other":{"options":{"apiKey":"unrelated credential"}},"example":{"options":{"apiKey":"{env:EXAMPLE_API_KEY}"},"models":{"model-v2":{"temperature":0.2}}}}}`), 0o600))

	plan, err := setup.Plan()
	require.NoError(t, err)
	assert.True(t, plan.Changed())
	assert.Equal(t, path, plan.File)
	before, err := fs.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(before), "unrelated credential", "dry-run leaves the file untouched")
	assert.NotContains(t, fmt.Sprint(plan), "credential", "the plan names keys, never values")

	wrote, err := setup.Write()
	require.NoError(t, err)
	assert.Equal(t, plan, wrote)
	raw, err := fs.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "unrelated credential", "other providers are preserved")
	assert.Contains(t, string(raw), `"apiKey": "{env:EXAMPLE_API_KEY}"`)
	assert.Contains(t, string(raw), `"large_unknown": 9007199254740993`, "unknown integers retain their exact value")
	cfg := map[string]any{}
	require.NoError(t, json.Unmarshal(raw, &cfg))
	assert.Equal(t, "example/model-v2", cfg["model"])
	assert.Equal(t, map[string]any{"bash": "deny"}, cfg["permission"])
	providers := cfg["provider"].(map[string]any)
	assert.Equal(t, map[string]any{"options": map[string]any{"apiKey": "unrelated credential"}}, providers["other"])
	model := providers["example"].(map[string]any)["models"].(map[string]any)["model-v2"].(map[string]any)
	assert.Equal(t, float64(0.2), model["temperature"])

	again, err := setup.Write()
	require.NoError(t, err)
	assert.Empty(t, again.Changes)
	after, err := fs.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, raw, after)
}

func TestOpenCodeSetupOptionalLimitsAreExplicit(t *testing.T) {
	t.Parallel()
	fs, setup := openCodeSetupFixture(t)
	setup.Provider.ContextLimit, setup.Provider.OutputLimit = 260000, 65536
	_, err := setup.Write()
	require.NoError(t, err)
	raw, err := fs.ReadFile(setup.Dir + "/opencode.json")
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"context": 260000`)
	assert.Contains(t, string(raw), `"output": 65536`)
	assert.NotContains(t, string(raw), "api.example.test/key")
	again, err := setup.Write()
	require.NoError(t, err)
	assert.Empty(t, again.Changes, "parsed numeric limits remain idempotent")
}

func TestOpenCodeSetupRefusesUnsafeInputsWithoutWriting(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		edit func(*OpenCodeSetup)
	}{
		{"blank model", func(s *OpenCodeSetup) { s.Model = "" }},
		{"wrong provider", func(s *OpenCodeSetup) { s.Model = "other/model-v2" }},
		{"credential instead of key name", func(s *OpenCodeSetup) { s.Provider.KeyEnv = "sk-secret-value" }},
		{"plaintext URL credential", func(s *OpenCodeSetup) { s.Provider.BaseURL = "https://secret@api.example.test/v1" }},
		{"partial limits", func(s *OpenCodeSetup) { s.Provider.ContextLimit = 260000 }},
		{"relative directory", func(s *OpenCodeSetup) { s.Dir = "friends/one" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fs, setup := openCodeSetupFixture(t)
			tc.edit(&setup)
			_, err := setup.Plan()
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "secret-value")
			e, err := fs.Lstat("/friends/one/opencode.json")
			require.NoError(t, err)
			assert.Equal(t, KindMissing, e.Kind)
		})
	}
}

func TestOpenCodeSetupRefusesExistingInvalidOrLinkedConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, existing string
		link           bool
	}{
		{"malformed JSON", `{broken`, false},
		{"null JSON", `null`, false},
		{"provider wrong type", `{"provider":"bad"}`, false},
		{"provider entry wrong type", `{"provider":{"example":"bad"}}`, false},
		{"symlink", "/other/account/opencode.json", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fs, setup := openCodeSetupFixture(t)
			path := setup.Dir + "/opencode.json"
			if tc.link {
				fs.Symlink(tc.existing, path)
			} else {
				require.NoError(t, fs.WriteFile(path, []byte(tc.existing), 0o600))
			}
			_, err := setup.Write()
			require.Error(t, err)
			assert.False(t, strings.Contains(err.Error(), "previous literal credential"))
			if !tc.link {
				raw, readErr := fs.ReadFile(path)
				require.NoError(t, readErr)
				assert.Equal(t, tc.existing, string(raw))
			}
		})
	}
}

func TestOpenCodeSetupRefusesConflictingRouteWithoutLeakingOrWriting(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, config string }{
		{"literal key", `{"model":"example/model-v2","provider":{"example":{"options":{"apiKey":"literal secret token"}}}}`},
		{"other key reference", `{"model":"example/model-v2","provider":{"example":{"options":{"apiKey":"{env:OTHER_API_KEY}"}}}}`},
		{"provider package", `{"model":"example/model-v2","provider":{"example":{"npm":"other-package"}}}`},
		{"provider endpoint", `{"model":"example/model-v2","provider":{"example":{"options":{"baseURL":"https://other.example.test/v1"}}}}`},
		{"selected model", `{"model":"other/old"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fs, setup := openCodeSetupFixture(t)
			path := setup.Dir + "/opencode.json"
			require.NoError(t, fs.WriteFile(path, []byte(tc.config), 0o600))
			_, err := setup.Plan()
			require.ErrorContains(t, err, "conflicts with the selected route")
			assert.NotContains(t, err.Error(), "literal secret token")
			assert.NotContains(t, err.Error(), "OTHER_API_KEY")
			_, err = setup.Write()
			require.Error(t, err)
			raw, err := fs.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, tc.config, string(raw))
		})
	}
}

func TestOpenCodeSetupRefusesDuplicateJSONKeysWithoutWriting(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, config string }{
		{"selected model", `{"model":"example/model-v2","model":"other/old"}`},
		{"unknown root member", `{"extra":1,"extra":2}`},
		{"nested provider key", `{"provider":{"example":{"options":{"apiKey":"literal secret token","apiKey":"{env:EXAMPLE_API_KEY}"}}}}`},
		{"nested array member", `{"other":[{"id":1,"id":2}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fs, setup := openCodeSetupFixture(t)
			path := setup.Dir + "/opencode.json"
			require.NoError(t, fs.WriteFile(path, []byte(tc.config), 0o600))
			_, err := setup.Plan()
			require.ErrorContains(t, err, "duplicate keys")
			assert.NotContains(t, err.Error(), "literal secret token")
			_, err = setup.Write()
			require.Error(t, err)
			h := HarnessSettings{Harness: "opencode", Dir: setup.Dir, FS: fs}
			_, err = h.Plan()
			require.ErrorContains(t, err, "duplicate keys")
			raw, err := fs.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, tc.config, string(raw))
		})
	}
}

func TestPlanOpenCodeSetupIsReadOnlyAndUsesPrivateNativeEnvironment(t *testing.T) {
	t.Parallel()
	fs, _ := openCodeSetupFixture(t)
	opts := SetupOptions{
		Harness: "opencode", Dir: "/friends/one", Home: "/profiles/one", ConfigDir: "/profiles/one/opencode-extra",
		Program: "/opt/opencode/bin/opencode", Provider: "example", Model: "example/model-v2",
		Package: "@ai-sdk/openai-compatible", BaseURL: "https://api.example.test/v1", KeyEnv: "EXAMPLE_API_KEY",
	}
	plan, err := PlanOpenCodeSetup(opts, fs)
	require.NoError(t, err)
	assert.Equal(t, "opencode", plan.Harness)
	require.Len(t, plan.Steps, 5)
	assert.Equal(t, "xdg-config-home", plan.Steps[0].Name)
	assert.Equal(t, "xdg-data-home", plan.Steps[1].Name)
	assert.Equal(t, "opencode-config", plan.Steps[2].Name)
	assert.Equal(t, "opencode-permission", plan.Steps[3].Name)
	assert.Equal(t, "opencode-version", plan.Steps[4].Name)
	assert.Contains(t, plan.Facts, SetupFact{Name: "route", State: "configured_unverified", Detail: opts.Model})
	assert.Contains(t, plan.Facts, SetupFact{Name: "provider-turn", State: "not_checked"})
	require.NoError(t, validateSetupCommand(*plan.Steps[4].Command))
	assert.Equal(t, []string{
		"HOME=/profiles/one", "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "XDG_CONFIG_HOME=/profiles/one/.config", "XDG_DATA_HOME=/profiles/one/.local/share",
		"OPENCODE_CONFIG=/friends/one/opencode.json", "OPENCODE_CONFIG_DIR=/profiles/one/opencode-extra",
	}, plan.Steps[4].Command.Env)
	e, err := fs.Lstat("/friends/one/opencode.json")
	require.NoError(t, err)
	assert.Equal(t, KindMissing, e.Kind, "planning is read-only")
	assert.NotContains(t, fmt.Sprint(plan), "api.example.test/key")

	for _, step := range plan.Steps[:2] {
		_, err = step.Apply()
		require.NoError(t, err)
	}
	facts, err := plan.Steps[2].Apply()
	require.NoError(t, err)
	assert.Equal(t, []SetupFact{{Name: "opencode-config", State: "written", Detail: "/friends/one/opencode.json"}}, facts)
	facts, err = plan.Steps[3].Apply()
	require.NoError(t, err)
	assert.Equal(t, []SetupFact{{Name: "opencode-permission", State: "written", Detail: "/friends/one/opencode.json"}}, facts)
	raw, err := fs.ReadFile("/friends/one/opencode.json")
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"/friends/one/**": "allow"`)
	facts, err = plan.Steps[4].Validate(SetupOutput{Stdout: "1.2.3", Exit: 0})
	require.NoError(t, err)
	assert.Equal(t, []SetupFact{{Name: "opencode-version", State: "observed", Detail: opts.Program}}, facts)
	_, err = plan.Steps[4].Validate(SetupOutput{Stdout: "", Exit: 125})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "1.2.3")

	again, err := PlanOpenCodeSetup(opts, fs)
	require.NoError(t, err)
	require.Len(t, again.Steps, 1, "idempotent directory and route writes")
	assert.Equal(t, "opencode-version", again.Steps[0].Name)
}

func TestPlanOpenCodeProfileOnlyDoesNotClaimOrChangeARoute(t *testing.T) {
	t.Parallel()
	fs, _ := openCodeSetupFixture(t)
	path := "/friends/one/opencode.json"
	require.NoError(t, fs.WriteFile(path, []byte(`{"model":"existing/route","provider":{"existing":{"options":{"apiKey":"literal credential"}}}}`), 0o600))
	plan, err := PlanOpenCodeSetup(SetupOptions{
		Harness: "opencode", Dir: "/friends/one", Home: "/profiles/one", ConfigDir: "/profiles/one/opencode-extra",
		Program: "/opt/opencode/bin/opencode", ProfileOnly: true,
	}, fs)
	require.NoError(t, err)
	require.Len(t, plan.Steps, 3)
	assert.Equal(t, "opencode-version", plan.Steps[2].Name)
	assert.Contains(t, plan.Facts, SetupFact{Name: "route", State: "unselected", Detail: "profile only"})
	assert.Contains(t, plan.Facts, SetupFact{Name: "provider-turn", State: "not_checked"})
	raw, err := fs.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "literal credential")
	assert.NotContains(t, fmt.Sprint(plan), "literal credential")
}

func TestPlanOpenCodeProfileOnlyRefusesIgnoredRouteInputs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		set  func(*SetupOptions)
	}{
		{"provider", func(o *SetupOptions) { o.Provider = "example" }},
		{"model", func(o *SetupOptions) { o.Model = "example/model-v2" }},
		{"package", func(o *SetupOptions) { o.Package = "@ai-sdk/openai-compatible" }},
		{"endpoint", func(o *SetupOptions) { o.BaseURL = "https://api.example.test/v1" }},
		{"key name", func(o *SetupOptions) { o.KeyEnv = "EXAMPLE_API_KEY" }},
		{"seat", func(o *SetupOptions) { o.Seat = "seat" }},
		{"secret names", func(o *SetupOptions) { o.SecretNames = []string{"EXAMPLE_API_KEY"} }},
		{"context limit", func(o *SetupOptions) { o.ContextLimit = 1000 }},
		{"output limit", func(o *SetupOptions) { o.OutputLimit = 1000 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fs, _ := openCodeSetupFixture(t)
			opts := SetupOptions{Harness: "opencode", Dir: "/friends/one", Home: "/profiles/one",
				ConfigDir: "/profiles/one/opencode-extra", Program: "/opt/opencode/bin/opencode", ProfileOnly: true}
			tc.set(&opts)
			_, err := PlanOpenCodeSetup(opts, fs)
			require.ErrorContains(t, err, "--profile-only")
			e, err := fs.Lstat("/friends/one/opencode.json")
			require.NoError(t, err)
			assert.Equal(t, KindMissing, e.Kind)
		})
	}
}

func TestPlanOpenCodeSetupRefusesSubstitutedProfileParent(t *testing.T) {
	t.Parallel()
	fs, _ := openCodeSetupFixture(t)
	opts := SetupOptions{Harness: "opencode", Dir: "/friends/one", Home: "/profiles/one",
		ConfigDir: "/profiles/one/opencode-extra", Program: "/opt/opencode/bin/opencode", ProfileOnly: true}
	plan, err := PlanOpenCodeSetup(opts, fs)
	require.NoError(t, err)
	require.NotEmpty(t, plan.Steps)
	fs.Symlink("/other/profile", "/profiles")
	_, err = plan.Steps[0].Apply()
	require.ErrorContains(t, err, "symlink")
	e, err := fs.Lstat("/profiles/one/.config")
	require.NoError(t, err)
	assert.Equal(t, KindMissing, e.Kind)
}

func TestPlanOpenCodeSetupRefusesDeniedDirectoryBeforeAnyWrite(t *testing.T) {
	t.Parallel()
	fs, _ := openCodeSetupFixture(t)
	path := "/friends/one/opencode.json"
	before := `{"permission":{"external_directory":"deny"},"other":"keep"}`
	require.NoError(t, fs.WriteFile(path, []byte(before), 0o600))
	_, err := PlanOpenCodeSetup(SetupOptions{
		Harness: "opencode", Dir: "/friends/one", Home: "/profiles/one", ConfigDir: "/profiles/one/opencode-extra",
		Program: "/opt/opencode/bin/opencode", Provider: "example", Model: "example/model-v2",
		Package: "@ai-sdk/openai-compatible", BaseURL: "https://api.example.test/v1", KeyEnv: "EXAMPLE_API_KEY",
	}, fs)
	require.ErrorContains(t, err, "will not replace a scalar permission")
	raw, err := fs.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, string(raw))
}

func TestOpenCodeSetupKeepsUnknownLargeNumberThroughPermissionApply(t *testing.T) {
	t.Parallel()
	fs, _ := openCodeSetupFixture(t)
	path := "/friends/one/opencode.json"
	require.NoError(t, fs.WriteFile(path, []byte(`{"large_unknown":9007199254740993,"permission":{"bash":"deny"}}`), 0o600))
	plan, err := PlanOpenCodeSetup(SetupOptions{
		Harness: "opencode", Dir: "/friends/one", Home: "/profiles/one", ConfigDir: "/profiles/one/opencode-extra",
		Program: "/opt/opencode/bin/opencode", Provider: "example", Model: "example/model-v2",
		Package: "@ai-sdk/openai-compatible", BaseURL: "https://api.example.test/v1", KeyEnv: "EXAMPLE_API_KEY",
	}, fs)
	require.NoError(t, err)
	for _, step := range plan.Steps {
		if step.Name == "opencode-config" || step.Name == "opencode-permission" {
			_, err := step.Apply()
			require.NoError(t, err)
		}
	}
	raw, err := fs.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"large_unknown": 9007199254740993`)
	assert.Contains(t, string(raw), `"/friends/one/**": "allow"`)
}

func TestOpenCodeSettingsRejectNonObjectWithoutPanic(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"null", "[]", "{} {}"} {
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			fs, _ := openCodeSetupFixture(t)
			path := "/friends/one/opencode.json"
			require.NoError(t, fs.WriteFile(path, []byte(input), 0o600))
			h := HarnessSettings{Harness: "opencode", Dir: "/friends/one", FS: fs}
			_, err := h.Plan()
			require.ErrorContains(t, err, "must contain one JSON object")
			_, err = h.Write()
			require.ErrorContains(t, err, "must contain one JSON object")
			raw, err := fs.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, input, string(raw))
		})
	}
}

func TestOpenCodeSettingsRefuseScalarPermissionWithoutMutation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, permission string }{
		{"scalar deny", `"deny"`},
		{"scalar ask", `"ask"`},
		{"scalar untrusted text", `"literal credential"`},
		{"path deny", `{"/friends/one/**":"deny"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fs, _ := openCodeSetupFixture(t)
			path := "/friends/one/opencode.json"
			before := `{"permission":{"external_directory":` + tc.permission + `},"other":"keep"}`
			require.NoError(t, fs.WriteFile(path, []byte(before), 0o600))
			h := HarnessSettings{Harness: "opencode", Dir: "/friends/one", FS: fs}
			_, err := h.Plan()
			require.ErrorContains(t, err, "will not replace")
			assert.NotContains(t, err.Error(), "literal credential")
			_, err = h.Write()
			require.Error(t, err)
			raw, err := fs.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, before, string(raw))
		})
	}
}
