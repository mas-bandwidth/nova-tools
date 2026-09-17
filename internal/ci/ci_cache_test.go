package ci

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestNoCacheStepRunsOnASelfHostedRunner holds a class shut. On 2026-09-17 the merge
// gate's darwin leg had moved to the Studio's persistent runners and kept an
// actions/cache step written for a fresh GitHub-hosted machine. Its save phase tarred
// the whole multi-GB Go build cache on every job, outlived the job's timeout, and was
// orphaned still compressing: 82 tar and 79 zstd processes put the Studio at load 147
// and made every test on the machine crawl. A persistent runner already has its cache
// on disk; an actions/cache or a setup-go cache there is pure cost.
//
// Rule: every actions/cache step in ci.yml carries the github-hosted condition, and
// every setup-go step there says cache: false (the workflow manages caching itself).
func TestNoCacheStepRunsOnASelfHostedRunner(t *testing.T) {
	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	steps := strings.Split(src, "\n      - ")
	caches, setups := 0, 0
	for _, step := range steps {
		if strings.Contains(step, "uses: actions/cache@") {
			caches++
			if !strings.Contains(step, "if: runner.environment == 'github-hosted'") {
				t.Errorf("an actions/cache step can run on a self-hosted runner; add `if: runner.environment == 'github-hosted'`:\n%s", firstLines(step, 4))
			}
		}
		if strings.Contains(step, "uses: actions/setup-go@") {
			setups++
			if !strings.Contains(step, "cache: false") {
				t.Errorf("a setup-go step leaves its built-in cache on; say `cache: false`:\n%s", firstLines(step, 4))
			}
		}
	}
	if caches == 0 || setups == 0 {
		t.Fatalf("found %d cache steps and %d setup-go steps; the splitter no longer matches ci.yml's step indentation", caches, setups)
	}
}

func firstLines(s string, n int) string {
	l := strings.Split(s, "\n")
	if len(l) > n {
		l = l[:n]
	}
	return strings.Join(l, "\n")
}
