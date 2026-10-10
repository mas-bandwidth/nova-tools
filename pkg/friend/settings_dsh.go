package friend

import (
	"bytes"
	"fmt"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DSHPreset is the agent preset a friend's DeepSeek Harness opens new
// sessions under. A session under "minimal" makes every headless turn exit 1
// before any write (measured 2026-10-04 and 2026-10-05, docs/SPEC-FRIEND.md,
// the dsh row); the owner set the desktop profile's selected default to
// "standard" by hand on 2026-10-04, and install now writes it. A session
// keeps the preset it was opened under: one opened under minimal stays so.
const DSHPreset = "standard"

// The patch entry that holds it: the desktop profile's patch layer,
// DSH_HOME/profiles/desktop/cordis.patch.yml, a YAML list of loader patch
// entries; the preset registry's config keys default and selectedDefault.
const (
	DSHPresetEntry = "agent-preset-registry"
	dshPresetName  = "@deepseek-ai/dsh-agent-preset-registry"
)

// dshSettings: the desktop profile exists (the app made it; install does
// not invent a profile), the patch file is not a symlink, and the preset
// registry's default and selectedDefault are DSHPreset. The file is edited
// as a YAML node tree, so its comments and every other entry stay.
func dshSettings(h HarnessSettings) ([]setting, error) {
	profile := filepath.Join(h.homeDir(h.DSHHome, ".dsh"), "profiles", "desktop")
	file := filepath.Join(profile, "cordis.patch.yml")
	out := []setting{h.realDir("desktop-profile", profile, false), h.realFile(file)}
	for _, key := range []string{"default", "selectedDefault"} {
		out = append(out, setting{
			Setting: Setting{Harness: h.Harness, File: file, Name: DSHPresetEntry + ".config." + key, Want: DSHPreset},
			read: func() (string, error) {
				doc, err := h.dshPatch(file)
				if err != nil {
					return "", err
				}
				if v := yamlLookup(dshEntry(doc, false), "config", key); v != nil {
					return v.Value, nil
				}
				return KindMissing, nil
			},
			write: func() error {
				doc, err := h.dshPatch(file)
				if err != nil {
					return err
				}
				config := yamlMap(dshEntry(doc, true), "config")
				if v := yamlLookup(config, key); v != nil {
					v.Kind, v.Tag, v.Value, v.Style = yaml.ScalarNode, "!!str", DSHPreset, 0
				} else {
					config.Content = append(config.Content, yamlScalar(key), yamlScalar(DSHPreset))
				}
				var b bytes.Buffer
				enc := yaml.NewEncoder(&b)
				enc.SetIndent(2)
				if err := enc.Encode(doc); err != nil {
					return err
				}
				return h.writeConfig(file, b.String(), 0o600)
			},
		})
	}
	return out, nil
}

// dshPatch is the patch file as a YAML document whose root is a sequence;
// a missing or empty file is an empty sequence.
func (h HarnessSettings) dshPatch(file string) (*yaml.Node, error) {
	text, err := h.readOrEmpty(file)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return nil, fmt.Errorf("%s is not YAML: %v", file, err)
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, HeadComment: doc.HeadComment, Content: []*yaml.Node{{Kind: yaml.SequenceNode, Tag: "!!seq"}}}
	}
	if doc.Content[0].Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("%s is not a YAML list of patch entries", file)
	}
	return &doc, nil
}

// dshEntry is the preset registry's patch entry, appended when add is set
// and there is none; nil when there is none and add is not set.
func dshEntry(doc *yaml.Node, add bool) *yaml.Node {
	list := doc.Content[0]
	for _, e := range list.Content {
		if id := yamlLookup(e, "id"); id != nil && id.Value == DSHPresetEntry {
			return e
		}
	}
	if !add {
		return nil
	}
	e := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		yamlScalar("id"), yamlScalar(DSHPresetEntry), yamlScalar("name"), yamlScalar(dshPresetName)}}
	list.Content = append(list.Content, e)
	return e
}

// yamlLookup follows keys through mappings; nil when one is missing.
func yamlLookup(n *yaml.Node, keys ...string) *yaml.Node {
	for _, k := range keys {
		if n == nil || n.Kind != yaml.MappingNode {
			return nil
		}
		var next *yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == k {
				next = n.Content[i+1]
			}
		}
		n = next
	}
	return n
}

// yamlMap is the mapping at key in n, made when missing or not a mapping.
func yamlMap(n *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			if v := n.Content[i+1]; v.Kind == yaml.MappingNode {
				return v
			}
			n.Content[i+1] = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			return n.Content[i+1]
		}
	}
	m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	n.Content = append(n.Content, yamlScalar(key), m)
	return m
}

func yamlScalar(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}
