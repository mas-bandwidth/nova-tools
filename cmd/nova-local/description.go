package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Description is a worker description in nova-swarm's schema (pkg/swarm/worker.go,
// Worker, decoded with DisallowUnknownFields): exactly its twelve keys, board omitted
// when not given, and no temperature, seed or num_ctx, which serve baked into the served
// tag (rule 6); no prompt conditions either, which nova-swarm owns (rule 11).
type Description struct {
	Name        string   `json:"name"`
	Provider    string   `json:"provider"`
	Model       string   `json:"model"`
	BaseURL     string   `json:"base_url"`
	EnvVar      string   `json:"env_var"`
	KeyFile     string   `json:"key_file"`
	Usage       string   `json:"usage"`
	Harness     string   `json:"harness"`
	HarnessArgs []string `json:"harness_args"`
	WorkerDir   string   `json:"worker_dir"`
	Deadline    string   `json:"deadline"`
	Board       string   `json:"board,omitempty"`
}

// The usage sources nova-swarm reads (pkg/swarm, UsageOpenCode and UsageNone).
var usages = []string{"opencode", "none"}

// SplitArgs is --harness-args split on commas, with no escape.
func SplitArgs(s string) []string { return strings.Split(s, ",") }

// Problems is every independent reason the description cannot be written, each naming
// the field and the command that satisfies it (rule 14, test 15): {model} placed in the
// harness's argv, an absolute existing worker directory, a key file that exists and is
// not empty (stat'ed, never opened), a usage nova-swarm reads, a Go duration.
func (d Description) Problems(stat func(string) (os.FileInfo, error)) []string {
	var out []string
	joined := strings.Join(d.HarnessArgs, "\x00")
	if !strings.Contains(joined, "{model}") {
		out = append(out, "--harness-args wants {model} placed in the harness's argv (such as run,--model,ollama/{model},--,{prompt}); without it the harness is told no model")
	}
	switch fi, err := stat(d.WorkerDir); {
	case !filepath.IsAbs(d.WorkerDir):
		out = append(out, fmt.Sprintf("--worker-dir wants an absolute directory (got %q): mkdir -p $PWD/home and pass $PWD/home", d.WorkerDir))
	case err != nil || !fi.IsDir():
		out = append(out, fmt.Sprintf("--worker-dir %s is not a directory: mkdir -p %s", d.WorkerDir, d.WorkerDir))
	}
	switch fi, err := stat(d.KeyFile); {
	case err != nil:
		out = append(out, fmt.Sprintf("--key-file %s does not exist; the local engine wants no key, and nova-swarm a non-empty file: printf 'local\\n' > %s && chmod 600 %s", d.KeyFile, d.KeyFile, d.KeyFile))
	case fi.Size() == 0:
		out = append(out, fmt.Sprintf("--key-file %s is empty; nova-swarm refuses an empty key file: printf 'local\\n' > %s && chmod 600 %s", d.KeyFile, d.KeyFile, d.KeyFile))
	}
	if !slices.Contains(usages, d.Usage) {
		out = append(out, fmt.Sprintf("--usage wants opencode (OpenCode's own accounting) or none (got %q)", d.Usage))
	}
	if _, err := time.ParseDuration(d.Deadline); err != nil {
		out = append(out, fmt.Sprintf("--deadline wants a Go duration such as 20m (got %q)", d.Deadline))
	}
	return out
}

// Write writes the description to path, the one file this tool writes (rule 11).
func (d Description) Write(path string) error {
	raw, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}
