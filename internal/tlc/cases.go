package tlc

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LoadCases reads tla/CASES.tsv under root and checks it against the tree: it
// must name every MC*.cfg exactly once and every module must exist.
func LoadCases(root string) ([]Case, error) {
	dir := filepath.Join(root, "tla")
	f, err := os.Open(filepath.Join(dir, CasesFile))
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %v", filepath.Join(dir, CasesFile), err)
	}
	defer f.Close()
	cases, err := ParseCases(f)
	if err != nil {
		return nil, err
	}
	cfgs, err := filepath.Glob(filepath.Join(dir, "MC*.cfg"))
	if err != nil {
		return nil, err
	}
	var actual, named []string
	for _, p := range cfgs {
		actual = append(actual, filepath.Base(p))
	}
	seen := map[string]bool{}
	for _, c := range cases {
		named = append(named, c.Config)
		if seen[c.Config] {
			return nil, fmt.Errorf("%s must name every MC*.cfg exactly once: %s is named twice", CasesFile, c.Config)
		}
		seen[c.Config] = true
	}
	sort.Strings(actual)
	sort.Strings(named)
	if strings.Join(actual, "\n") != strings.Join(named, "\n") {
		return nil, fmt.Errorf("%s must name every MC*.cfg exactly once", CasesFile)
	}
	for _, c := range cases {
		if info, err := os.Stat(filepath.Join(dir, c.Module)); err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("missing instance module: %s", c.Module)
		}
	}
	return cases, nil
}

// RequiredGroups returns the groups that hold a required case, sorted: the
// matrix a CI run derives.
func RequiredGroups(cases []Case) []string {
	set := map[string]bool{}
	for _, c := range cases {
		if c.Gate == "required" {
			set[c.Group] = true
		}
	}
	out := make([]string, 0, len(set))
	for g := range set {
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}

// Select returns the cases one run covers: a group, or shard number shard of
// shards (every shards'th case from shard), never both.
func Select(cases []Case, group string, shards, shard int) ([]Case, error) {
	if shards < 1 || shard < 0 || shard >= shards {
		return nil, errors.New("use a positive shard count and a zero-based shard below it")
	}
	if shards > len(cases) {
		return nil, errors.New("shard count exceeds the number of declared cases")
	}
	if group != "" {
		if shards != 1 || shard != 0 {
			return nil, errors.New("--group and shard selection cannot be combined")
		}
		var chosen []Case
		for _, c := range cases {
			if c.Group == group {
				chosen = append(chosen, c)
			}
		}
		if len(chosen) == 0 {
			return nil, fmt.Errorf("unknown group: %s", group)
		}
		return chosen, nil
	}
	var chosen []Case
	for i := shard; i < len(cases); i += shards {
		chosen = append(chosen, cases[i])
	}
	return chosen, nil
}
