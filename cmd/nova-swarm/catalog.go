package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// One catalog serves every launch, so each job runs the harness in
// a fresh data home, so opencode fetched its model catalog (models.dev, 5.3 MB) at every
// start, and a fetch that was slow or lost the race fell back to the snapshot built into
// the binary, which predates the fleet's newer models: the same route then ran with the
// catalog's entry on one launch and failed `ProviderModelNotFoundError`, or ran on bare
// defaults, on the next. Now a member refreshes ONE catalog file when it starts, and every
// launch reads that file with the harness's own fetch turned off
// (OPENCODE_DISABLE_MODELS_FETCH, OPENCODE_MODELS_PATH): the same catalog for every launch
// on the machine, whether the network answers or not.

// catalogFile is the machine's one catalog under a pool root.
func catalogFile(root string) string { return filepath.Join(root, "catalog", "models.json") }

// catalogRefreshBudget bounds the member's one refresh of the catalog.
const catalogRefreshBudget = 30 * time.Second

// refreshCatalog is a member's one refresh at its start: the harness fetches its catalog
// into a home of its own (`<harness> models`) and the file is put in place whole (written
// beside it, then renamed). When that fails, the catalog already in place is kept; with
// none, the login's own cached catalog is taken; with none of those, there is no catalog
// and every launch says so. It returns what it did, one word and the file.
func refreshCatalog(harness, root string) string {
	dst := catalogFile(root)
	home := filepath.Join(root, "catalog", "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		return "none: " + err.Error()
	}
	cmd, cancel := subproc.CommandFor(context.Background(), catalogRefreshBudget, harness, "models")
	defer cancel()
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "XDG_DATA_HOME=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "XDG_CACHE_HOME=" + filepath.Join(home, ".cache")}
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	runErr := cmd.Run()
	fetched := filepath.Join(home, ".cache", "opencode", "models.json")
	if catalogValid(fetched) {
		if err := installCatalog(fetched, dst); err == nil {
			return "refreshed " + dst
		} else {
			runErr = err
		}
	}
	if catalogValid(dst) {
		return "kept " + oneline.Field(dst) + " (the refresh failed: " + oneline.Err(runErr) + ")"
	}
	if login, err := os.UserHomeDir(); err == nil {
		if src := filepath.Join(login, ".cache", "opencode", "models.json"); catalogValid(src) {
			if err := installCatalog(src, dst); err == nil {
				return "login " + dst + " (from " + src + "; the refresh failed)"
			}
		}
	}
	return "none: no catalog at " + oneline.Field(dst) + " and the refresh failed (" + oneline.Err(runErr) + "); each launch fetches its own"
}

// catalogValid is whether path holds a catalog: a non-empty JSON object.
func catalogValid(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return false
	}
	var m map[string]json.RawMessage
	return json.Unmarshal(raw, &m) == nil && len(m) > 0
}

// installCatalog copies src over dst whole: a copy beside dst, then a rename, so a launch
// reading dst never sees half a file.
func installCatalog(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".new"
	if err := copyRegularFile(src, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// seedCatalog gives one launch the machine's catalog in its own data home (a copy, never
// a link: a harness that wrote its cache in place must not touch the machine's file; the
// data home is inside the wall's write roots) and returns the environment that has the
// harness read it and fetch nothing. note is why there is none, "" when seeded.
func seedCatalog(root, dataHome string) (env []string, note string) {
	src := catalogFile(root)
	if !catalogValid(src) {
		return nil, "no catalog at " + src + " (a member refreshes it at its start); the harness fetches its own at this start"
	}
	dst := filepath.Join(dataHome, ".cache", "opencode", "models.json")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return nil, "the catalog could not be placed: " + err.Error()
	}
	if err := copyRegularFile(src, dst); err != nil {
		return nil, "the catalog could not be placed: " + err.Error()
	}
	return []string{"OPENCODE_MODELS_PATH=" + dst, "OPENCODE_DISABLE_MODELS_FETCH=1"}, ""
}
