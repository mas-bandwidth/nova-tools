package friend

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"slices"

	"gopkg.in/yaml.v3"
)

// dshSettings checks the already initialized headless profile, never changing
// its manifest, patches, model or credentials (SPEC-FRIEND.md, Harness settings).
// The official rc.2 headless template composes base and headless bundles; it
// does not compose the desktop preset registry. Only the owner's DSH may
// initialize that profile. The settings phase never launches DSH.
func dshSettings(h HarnessSettings) ([]setting, error) {
	home := h.homeDir(h.DSHHome, ".dsh")
	if !filepath.IsAbs(home) {
		return nil, fmt.Errorf("DSH_HOME %q wants an absolute path", home)
	}
	profiles := filepath.Join(home, "profiles")
	profile := filepath.Join(profiles, "headless")
	manifest := filepath.Join(profile, "package.json")
	var out []setting
	for parent := filepath.Dir(home); ; parent = filepath.Dir(parent) {
		out = append(out, h.realDir("dsh-parent", parent, false))
		if parent == filepath.Dir(parent) {
			break
		}
	}
	out = append(out, []setting{h.realDir("dsh-home", home, false), h.realDir("profiles", profiles, false),
		h.realDir("headless-profile", profile, false),
		{Setting: Setting{Harness: h.Harness, File: manifest, Name: "file", Want: KindFile}, guard: true,
			read: func() (string, error) { return kindOf(h.fs(), manifest) }},
		{Setting: Setting{Harness: h.Harness, File: manifest, Name: "headless-bundles", Want: "base and headless bundles"},
			read: func() (string, error) { return h.dshBundles(manifest) }},
	}...)
	for _, file := range []string{filepath.Join(profile, "cordis.patch.yml"), filepath.Join(home, "cordis.patch.yml")} {
		out = append(out, h.realFile(file), setting{
			Setting: Setting{Harness: h.Harness, File: file, Name: "patch", Want: "YAML patch list or missing"},
			read:    func() (string, error) { return h.dshPatchList(file) },
		})
	}
	return out, nil
}

// dshBundles checks composition prerequisites, not runtime package resolution
// or provider readiness. Unknown unrelated manifest fields stay untouched.
func (h HarnessSettings) dshBundles(file string) (string, error) {
	raw, err := h.fs().ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return KindMissing, nil
	}
	if err != nil {
		return "", err
	}
	// DSH reads JavaScript properties by exact name; tagged Go structs would
	// also accept case aliases that the native profile loader ignores.
	var profile map[string]json.RawMessage
	for _, key := range []string{"dsh", "profile"} {
		if err := json.Unmarshal(raw, &profile); err != nil || profile == nil {
			return "", fmt.Errorf("%s wants a JSON profile manifest", file)
		}
		raw = profile[key]
		profile = nil
	}
	if err := json.Unmarshal(raw, &profile); err != nil || profile == nil {
		return "", fmt.Errorf("%s wants a JSON profile manifest", file)
	}
	var bundles []string
	if err := json.Unmarshal(profile["bundles"], &bundles); err != nil {
		return "", fmt.Errorf("%s wants a JSON profile manifest", file)
	}
	if !slices.Contains(bundles, "@deepseek-ai/dsh-base") || !slices.Contains(bundles, "@deepseek-ai/dsh-headless") {
		return "", fmt.Errorf("%s wants base and headless bundles in an initialized headless profile", file)
	}
	return "base and headless bundles", nil
}

// dshPatchList validates optional user patches without evaluating expressions
// or serializing the YAML again. Parse errors never quote configuration bytes.
func (h HarnessSettings) dshPatchList(file string) (string, error) {
	text, err := h.readOrEmpty(file)
	if err != nil {
		return "", err
	}
	decode := yaml.NewDecoder(bytes.NewBufferString(text))
	var doc yaml.Node
	if err := decode.Decode(&doc); err != nil && err != io.EOF {
		return "", fmt.Errorf("%s wants a YAML list of patch entries", file)
	}
	if len(doc.Content) > 0 && doc.Content[0].Kind != yaml.SequenceNode {
		return "", fmt.Errorf("%s wants a YAML list of patch entries", file)
	}
	if len(doc.Content) > 0 {
		for _, entry := range doc.Content[0].Content {
			if entry.Kind == yaml.AliasNode {
				entry = entry.Alias
			}
			if entry == nil || entry.Kind != yaml.MappingNode {
				return "", fmt.Errorf("%s wants a YAML list of patch entries", file)
			}
		}
	}
	var extra yaml.Node
	if err := decode.Decode(&extra); err != io.EOF {
		return "", fmt.Errorf("%s wants one YAML list of patch entries", file)
	}
	return "YAML patch list or missing", nil
}
