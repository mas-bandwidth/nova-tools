package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// cmdFleetStatus implements `nova-pulse fleet status`.
// It queries per-node and per-slot live execution status, comparing running commit SHA vs disk SHA.
func cmdFleetStatus(args []string, stdout, stderr io.Writer) int {
	f := newFlags("fleet status")
	machinesPath := f.fs.String("machines", "", "")
	root := f.fs.String("root", "", "")
	bench := f.fs.String("bench", "", "")
	repo := f.fs.String("repo", "", "")
	asJSON := f.fs.Bool("json", false, "")

	if !f.parse(args, stderr) {
		return 2
	}

	if strings.TrimSpace(*machinesPath) == "" && strings.TrimSpace(*root) == "" {
		f.add("either --machines <file> or --root <dir> is required to query fleet status")
	}
	if f.refused(stderr) {
		return 2
	}

	var nodeOpts []fleet.QueryNodeOptions

	if strings.TrimSpace(*machinesPath) != "" {
		reg, err := fleet.ReadRegistry(*machinesPath)
		if err != nil {
			fmt.Fprintf(stderr, "nova-pulse fleet status: machines registry: %s\n", oneline.Err(err))
			return 2
		}
		for _, m := range reg.Machines() {
			if *bench != "" && m.Name != *bench {
				continue
			}
			nodeRoot := *root
			if nodeRoot != "" && filepath.Base(nodeRoot) != m.Name {
				candidate := filepath.Join(nodeRoot, m.Name)
				if info, err := os.Stat(candidate); err == nil && info.IsDir() {
					nodeRoot = candidate
				}
			}
			nodeOpts = append(nodeOpts, fleet.QueryNodeOptions{
				NodeName: m.Name,
				NodeRoot: nodeRoot,
				RepoDir:  *repo,
			})
		}
	} else {
		nodeName := *bench
		if nodeName == "" {
			nodeName = "local"
		}
		nodeOpts = append(nodeOpts, fleet.QueryNodeOptions{
			NodeName: nodeName,
			NodeRoot: *root,
			RepoDir:  *repo,
		})
	}

	report, err := fleet.QueryFleetStatus(nodeOpts)
	if err != nil {
		fmt.Fprintf(stderr, "nova-pulse fleet status: %s\n", oneline.Err(err))
		return 2
	}

	if *asJSON {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "nova-pulse fleet status: %s\n", oneline.Err(err))
			return 2
		}
		data = append(data, '\n')
		stdout.Write(data)
	} else {
		stdout.Write([]byte(fleet.FormatFleetStatus(report)))
	}

	return 0
}
