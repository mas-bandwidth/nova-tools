package dshdesktop

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// MountID is the loader entry id of the include in the profile's patch layer.
const MountID = "nova-hook-dsh-include"

// PatchFile is the desktop profile's user patch layer, the file the harness's
// own profile header says to edit (lib/main.js PROFILE_PATCH_FILENAME).
func PatchFile(profileDir string) string { return filepath.Join(profileDir, "cordis.patch.yml") }

// Mount puts the include into the profile's patch layer as a documented loader
// insert of cordis:include, the same entry an operator would write by hand:
//
//   - insert:
//   - id: nova-hook-dsh-include
//     name: "cordis:include"
//     config:
//     path: "<hooks file>"
//
// An include already mounted at hooksFile is left alone, byte for byte. An
// entry with MountID at another path is repointed. Any other content is kept;
// a new entry is appended as text so the user's comments and layout stay. It
// reports whether the patch layer changed: the harness reads it only at start.
func Mount(profileDir, hooksFile string) (changed bool, err error) {
	if !filepath.IsAbs(hooksFile) || filepath.Clean(hooksFile) != hooksFile {
		return false, errors.New("dsh mount wants a clean absolute include path")
	}
	if info, err := os.Stat(profileDir); err != nil || !info.IsDir() {
		return false, fmt.Errorf("no DeepSeek Harness desktop profile at %s; open the harness once, then hook again", profileDir)
	}
	path := PatchFile(profileDir)
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	var doc yaml.Node
	if len(bytes.TrimSpace(old)) > 0 {
		if err := yaml.Unmarshal(old, &doc); err != nil {
			return false, fmt.Errorf("profile patch layer %s is not YAML: %w", path, err)
		}
	}
	var seq *yaml.Node
	if doc.Kind == yaml.DocumentNode && len(doc.Content) == 1 {
		seq = doc.Content[0]
	}
	if seq != nil && seq.Kind != yaml.SequenceNode {
		return false, fmt.Errorf("profile patch layer %s is not a top-level list; mount %s by hand", path, hooksFile)
	}
	if seq != nil {
		if entry := findMount(seq); entry != nil {
			cfg := mapValue(entry, "config")
			p := mapValue(cfg, "path")
			if p != nil && p.Value == hooksFile {
				return false, nil
			}
			if cfg == nil || p == nil {
				return false, fmt.Errorf("profile entry %s in %s has no config.path; fix it by hand", MountID, path)
			}
			p.Value = hooksFile
			out, err := yaml.Marshal(&doc)
			if err != nil {
				return false, err
			}
			return true, writeMode(path, out, old)
		}
	}
	block := fmt.Sprintf("- insert:\n    - id: %s\n      name: \"cordis:include\"\n      config:\n        path: %q\n", MountID, hooksFile)
	var out []byte
	if seq == nil || len(seq.Content) == 0 {
		// Empty layer or a flow-style []: keep the header comments, start a block list.
		out = append(leadingComments(old), []byte(block)...)
	} else {
		if seq.Style&yaml.FlowStyle != 0 {
			return false, fmt.Errorf("profile patch layer %s is a flow-style list; mount %s by hand", path, hooksFile)
		}
		out = append([]byte{}, old...)
		if len(out) > 0 && out[len(out)-1] != '\n' {
			out = append(out, '\n')
		}
		out = append(out, []byte(block)...)
	}
	var check yaml.Node
	if err := yaml.Unmarshal(out, &check); err != nil || len(check.Content) != 1 || findMount(check.Content[0]) == nil {
		return false, fmt.Errorf("mounting into %s would not parse back; mount %s by hand", path, hooksFile)
	}
	return true, writeMode(path, out, old)
}

// Mounted reports whether the profile's patch layer includes hooksFile.
func Mounted(profileDir, hooksFile string) bool {
	data, err := os.ReadFile(PatchFile(profileDir))
	if err != nil {
		return false
	}
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil || len(doc.Content) != 1 {
		return false
	}
	return includes(doc.Content[0], hooksFile)
}

func includes(seq *yaml.Node, hooksFile string) bool {
	for _, item := range seq.Content {
		for _, e := range append([]*yaml.Node{item}, entries(mapValue(item, "insert"))...) {
			if n := mapValue(e, "name"); n != nil && n.Value == "cordis:include" {
				if p := mapValue(mapValue(e, "config"), "path"); p != nil && p.Value == hooksFile {
					return true
				}
			}
		}
	}
	return false
}

func findMount(seq *yaml.Node) *yaml.Node {
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return nil
	}
	for _, item := range seq.Content {
		for _, e := range append([]*yaml.Node{item}, entries(mapValue(item, "insert"))...) {
			if id := mapValue(e, "id"); id != nil && id.Value == MountID {
				return e
			}
		}
	}
	return nil
}

func entries(n *yaml.Node) []*yaml.Node {
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil
	}
	return n.Content
}

func mapValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func leadingComments(data []byte) []byte {
	var out []byte
	for _, line := range bytes.SplitAfter(data, []byte("\n")) {
		t := bytes.TrimSpace(line)
		if len(t) == 0 || t[0] == '#' {
			out = append(out, line...)
			continue
		}
		break
	}
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	return out
}

// writeMode keeps an existing file's mode; a new patch layer is private.
func writeMode(path string, data, old []byte) error {
	mode := os.FileMode(0600)
	if old != nil {
		if info, err := os.Stat(path); err == nil {
			mode = info.Mode().Perm()
		}
	}
	return atomicWrite(path, data, mode)
}
