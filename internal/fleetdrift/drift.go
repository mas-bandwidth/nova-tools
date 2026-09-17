// Package fleetdrift is the read-each-plan witness for the bench module of
// SPEC-FLEET-KUBE, Part 1. The Terraform module declares the bytes of every
// managed file; a null_resource's triggers hash the declaration and cannot see
// a hand edit, so the trigger is never the witness. The witness is a data
// "external" that reads the host on every plan: `ssh <bench> sha256sum <path>`.
// This package is that read on the Go side, so the same comparison can be run
// and tested without an apply and without a network.
//
// The rule the package holds: a managed file with a witness drifts the moment
// the observed hash differs from the declared hash, a hand edit nobody declared
// included; a file with only a null_resource trigger never drifts. Every drift
// names the resource, its path, and the two hashes, so a plan is the report.
package fleetdrift

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// Record is one managed file in the module's declared-files JSON: the path on
// the bench, the declared sha256 hex, and whether a read-each-plan witness is
// attached. Witness false is the null_resource trigger only. Content is the
// declared bytes the module writes; the declared sha256 is sha256(Content), so
// the declaration and the hash cannot drift apart.
type Record struct {
	Path     string `json:"path"`
	Declared string `json:"sha256"`
	Witness  bool   `json:"witness"`
	Content  string `json:"content"`
}

// Manifest is the bench module's declared files, keyed by the Terraform
// resource label. It is the shared input: the module's files.json drives both
// the HCL for_each and this package, so the tool and the declaration cannot
// disagree about what is managed.
type Manifest map[string]Record

// LoadManifest reads the module's declared-files JSON. A missing or malformed
// file is an error: a plan against a declaration nobody can read is not clean.
func LoadManifest(path string) (Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s is not the module's declared-files JSON: %w", path, err)
	}
	return m, nil
}

// Drift is one managed file whose observed hash differs from its declaration.
type Drift struct {
	Resource string
	Path     string
	Declared string
	Observed string
}

// Observer reads one remote path and returns its sha256 hex. The production
// observer is SSHObserver; a test hands in a map.
type Observer func(ctx context.Context, path string) (string, error)

// Plan compares every witnessed file's declared hash with the hash the observer
// reads. A record with Witness false is a null_resource trigger only and is
// never drift. Drifts come back in resource-label order so a plan is stable.
func Plan(ctx context.Context, m Manifest, observe Observer) ([]Drift, error) {
	drifts := []Drift{}
	for _, name := range sortedKeys(m) {
		r := m[name]
		if !r.Witness {
			continue
		}
		observed, err := observe(ctx, r.Path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", r.Path, err)
		}
		if normalize(observed) != normalize(r.Declared) {
			drifts = append(drifts, Drift{
				Resource: name,
				Path:     r.Path,
				Declared: r.Declared,
				Observed: observed,
			})
		}
	}
	return drifts, nil
}

// SSHObserver is the real remote check: it runs the same
// `ssh <target> sha256sum <path>` the module's data "external" runs, through
// the ssh program from the caller. A test puts a fake ssh on PATH and no test
// opens a socket. The returned observer takes the first field of the one output
// line as the observed hash.
func SSHObserver(program, target string, timeout time.Duration) Observer {
	if program == "" {
		program = "ssh"
	}
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return func(ctx context.Context, path string) (string, error) {
		cctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		cmd := exec.CommandContext(cctx, program, "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", target, "sha256sum", path)
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("%s %s: %w", program, target, err)
		}
		fields := strings.Fields(string(out))
		if len(fields) == 0 {
			return "", fmt.Errorf("%s %s returned no hash for %s", program, target, path)
		}
		return fields[0], nil
	}
}

// sortedKeys is the deterministic order a plan prints in.
func sortedKeys(m Manifest) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// normalize compares hashes case-insensitively and without surrounding space,
// so the declaration and sha256sum's output are the same bytes whichever case
// the tool printed.
func normalize(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
