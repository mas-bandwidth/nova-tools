package ci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/build"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
	"github.com/mas-bandwidth/nova-tools/internal/goenv"
)

// deadcodeAllowlistPath is the shrink-only list of unreachable functions found
// in cmd/... by go tool deadcode.
const deadcodeAllowlistPath = "testdata/deadcode_allowlist.txt"

type deadcodeJSONPackage struct {
	Name  string `json:"Name"`
	Path  string `json:"Path"`
	Funcs []struct {
		Name     string `json:"Name"`
		Position struct {
			File string `json:"File"`
			Line int    `json:"Line"`
			Col  int    `json:"Col"`
		} `json:"Position"`
		Generated bool `json:"Generated"`
		Marker    bool `json:"Marker"`
	} `json:"Funcs"`
}

type deadcodeFinding struct {
	File string
	Line int
	Func string
}

// deadcodePlatforms are the GOOS/GOARCH configurations deadcode analysis covers.
// A function is truly dead only when it is unreachable across every platform where
// its file is compiled; a function called only on Linux or only on Darwin or Windows
// is live on that platform and must not be flagged or removed.
var deadcodePlatforms = []struct{ goos, goarch string }{
	{"linux", "amd64"},
	{"darwin", "arm64"},
	{"windows", "amd64"},
}

// TestNoDeadCode runs deadcode analysis over cmd/... (via go tool deadcode)
// across supported target platforms and asserts that no unreachable function exists
// outside the shrink-only allowlist.
func TestNoDeadCode(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	allow := loadAllowlist(t, deadcodeAllowlistPath, shrinkOnly)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	bin := filepath.Join(t.TempDir(), "deadcode")
	buildCmd := exec.CommandContext(ctx, "go", "build", "-o", bin, "golang.org/x/tools/cmd/deadcode")
	buildCmd.Dir = root
	buildCmd.Env = goenv.Clean(os.Environ())
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build deadcode failed: %v\n%s", err, out)
	}

	unreachablePerPlatform := make([]map[string]bool, len(deadcodePlatforms))
	findings := map[string]deadcodeFinding{}

	for i, p := range deadcodePlatforms {
		unreachablePerPlatform[i] = make(map[string]bool)
		cmd := exec.CommandContext(ctx, bin, "-json", "./cmd/...")
		cmd.Dir = root
		cmd.Env = append(goenv.Clean(os.Environ()), "GOOS="+p.goos, "GOARCH="+p.goarch)
		out, err := cmd.Output()
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				t.Fatalf("deadcode failed on %s/%s: %v\n%s", p.goos, p.goarch, err, exitErr.Stderr)
			}
			t.Fatalf("deadcode failed on %s/%s: %v", p.goos, p.goarch, err)
		}

		var pkgs []deadcodeJSONPackage
		if err := json.Unmarshal(out, &pkgs); err != nil {
			t.Fatalf("unmarshaling deadcode json for %s/%s: %v", p.goos, p.goarch, err)
		}

		for _, pkg := range pkgs {
			for _, fn := range pkg.Funcs {
				rel := filepath.ToSlash(fn.Position.File)
				key := rel + ":" + fn.Name
				unreachablePerPlatform[i][key] = true
				if _, ok := findings[key]; !ok {
					findings[key] = deadcodeFinding{
						File: rel,
						Line: fn.Position.Line,
						Func: fn.Name,
					}
				}
			}
		}
	}

	allKeys := make(map[string]bool)
	for _, m := range unreachablePerPlatform {
		for k := range m {
			allKeys[k] = true
		}
	}

	seen := map[string]bool{}
	for k := range allKeys {
		lastColon := strings.LastIndex(k, ":")
		if lastColon < 0 {
			continue
		}
		file := k[:lastColon]
		dir := filepath.Dir(file)
		base := filepath.Base(file)

		compiledCount := 0
		unreachableCount := 0
		for i, p := range deadcodePlatforms {
			bctx := build.Context{
				GOOS:   p.goos,
				GOARCH: p.goarch,
			}
			match, err := bctx.MatchFile(filepath.Join(root, dir), base)
			if err != nil || !match {
				continue
			}
			compiledCount++
			if unreachablePerPlatform[i][k] {
				unreachableCount++
			}
		}

		// A function is dead iff it is unreachable across all platforms where its file is compiled.
		if compiledCount > 0 && compiledCount == unreachableCount {
			seen[k] = true
		}
	}

	res := allowlist.Check(t, allow, seen)
	var violations []string
	for _, k := range res.Unlisted {
		f := findings[k]
		violations = append(violations, fmt.Sprintf("%s:%d: unreachable func: %s; delete the dead function or wire it into a command (the allowlist only shrinks)", f.File, f.Line, f.Func))
	}
	for _, row := range res.Stale {
		violations = append(violations, fmt.Sprintf("%s lists %s, but deadcode reports it as reachable or removed; delete the stale entry (the list only shrinks; NOVA_CI_UPDATE=1 drops it)", deadcodeAllowlistPath, row.Key))
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Error(v)
	}
}
