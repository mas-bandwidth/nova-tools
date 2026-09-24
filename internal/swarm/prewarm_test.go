package swarm

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func prewarmFixture(t *testing.T) (string, string) {
	t.Helper()
	source := filepath.Join(t.TempDir(), "source")
	mustWrite(t, filepath.Join(source, "go.mod"), "module example.com/prewarm\n\ngo 1.22\n")
	mustWrite(t, filepath.Join(source, "main.go"), "package prewarm\n")
	gitT(t, "", "init", "-q", "-b", "dev", source)
	gitT(t, source, "add", "-A")
	gitT(t, source, "commit", "-q", "-m", "fixture")
	return source, gitT(t, source, "rev-parse", "HEAD")
}

// S3's useful unit is a pinned tip, not a branch name. The reference checkout is hidden
// until every warm phase succeeds, and every phase receives the exact caches native jobs
// later receive.
func TestPrewarmPublishesExactTipAfterEveryCachePhase(t *testing.T) {
	source, tip := prewarmFixture(t)
	root := filepath.Join(t.TempDir(), "pool")
	var calls []PrewarmCommand
	got, err := Prewarm(PrewarmInput{
		Root: root, Source: source, Repo: "acme/tool", Tip: tip,
		Run: func(c PrewarmCommand) error {
			calls = append(calls, c)
			wantCheckout := filepath.Join(root, "ref", "acme", "tool@"+tip)
			if c.Dir == wantCheckout {
				t.Fatalf("phase %s ran in the published path before the whole prewarm succeeded", c.Name)
			}
			for _, want := range []string{
				"GOMODCACHE=" + GoModCacheDir(root),
				"GOCACHE=" + GoBuildCacheDir(root),
				"ASDF_OUTPUT_TRANSLATIONS=",
			} {
				if !envHasPrefix(c.Env, want) {
					t.Errorf("phase %s has no %q in env: %v", c.Name, want, c.Env)
				}
			}
			asdf := envValue(c.Env, "ASDF_OUTPUT_TRANSLATIONS")
			if !strings.Contains(asdf, filepath.ToSlash(c.Dir)) || !strings.Contains(asdf, filepath.ToSlash(LispCacheDir(root))) {
				t.Errorf("phase %s ASDF mapping does not join its checkout to the stable cache: %s", c.Name, asdf)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Prewarm: %v", err)
	}
	wantNames := []string{"modules", "build", "test-binaries", "lisp"}
	wantArgv := [][]string{
		{"go", "mod", "download"},
		{"make", "build"},
		{"make", "test-full", "RUN=^$$", "PKGS=./cmd/... ./internal/..."},
		{"make", "test-lisp"},
	}
	var names []string
	for i, c := range calls {
		names = append(names, c.Name)
		if !reflect.DeepEqual(c.Argv, wantArgv[i]) {
			t.Errorf("phase %s argv=%q, want %q", c.Name, c.Argv, wantArgv[i])
		}
	}
	if !reflect.DeepEqual(names, wantNames) {
		t.Fatalf("phases = %v, want %v", names, wantNames)
	}
	wantCheckout := filepath.Join(root, "ref", "acme", "tool@"+tip)
	if got.Checkout != wantCheckout || got.Tip != tip || got.Phases != len(wantNames) {
		t.Fatalf("result = %+v, want checkout=%s tip=%s phases=%d", got, wantCheckout, tip, len(wantNames))
	}
	if head := gitT(t, got.Checkout, "rev-parse", "HEAD"); head != tip {
		t.Fatalf("published checkout HEAD = %s, want exact tip %s", head, tip)
	}
	if status := gitT(t, got.Checkout, "status", "--porcelain"); status != "" {
		t.Fatalf("published checkout is dirty: %s", status)
	}
	commitUnix, _ := strconv.ParseInt(gitT(t, got.Checkout, "show", "-s", "--format=%ct", "HEAD"), 10, 64)
	if fi, statErr := os.Stat(filepath.Join(got.Checkout, "main.go")); statErr != nil || fi.ModTime().Unix() != commitUnix {
		t.Fatalf("published source mtime = %v err=%v, want commit time %d for FASL reuse", fi, statErr, commitUnix)
	}
	receipt, err := os.ReadFile(filepath.Join(root, PrewarmReceiptDir, "acme", "tool@"+tip+".receipt"))
	if err != nil {
		t.Fatalf("no durable prewarm receipt: %v", err)
	}
	for _, want := range []string{"repo=acme/tool", "tip=" + tip, "phases=modules,build,test-binaries,lisp"} {
		if !strings.Contains(string(receipt), want) {
			t.Errorf("receipt does not contain %q: %s", want, receipt)
		}
	}
}

// A failed compiler must not leave a checkout that staging mistakes for a warm exact tip.
func TestPrewarmFailurePublishesNeitherCheckoutNorReceipt(t *testing.T) {
	source, tip := prewarmFixture(t)
	root := filepath.Join(t.TempDir(), "pool")
	_, err := Prewarm(PrewarmInput{
		Root: root, Source: source, Repo: "acme/tool", Tip: tip,
		Run: func(c PrewarmCommand) error {
			if c.Name == "test-binaries" {
				return errors.New("compiler failed")
			}
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "test-binaries") {
		t.Fatalf("Prewarm error = %v, want named failed phase", err)
	}
	for _, absent := range []string{
		filepath.Join(root, "ref", "acme", "tool@"+tip),
		filepath.Join(root, PrewarmReceiptDir, "acme", "tool@"+tip+".receipt"),
	} {
		if _, statErr := os.Stat(absent); !os.IsNotExist(statErr) {
			t.Errorf("failed prewarm published %s (stat err=%v)", absent, statErr)
		}
	}
}

func envHasPrefix(env []string, prefix string) bool {
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return true
		}
	}
	return false
}

func envValue(env []string, name string) string {
	for _, entry := range env {
		if key, value, ok := strings.Cut(entry, "="); ok && key == name {
			return value
		}
	}
	return ""
}

// The shared Lisp cache has to reach ordinary swarm children as well as the prewarm
// process; otherwise the prewarm compiles FASLs that every fresh card ignores.
func TestCacheEnvCarriesTheSharedLispCache(t *testing.T) {
	source, tip := prewarmFixture(t)
	root := t.TempDir()
	if err := EnsureCacheDirs(root); err != nil {
		t.Fatal(err)
	}
	env := CacheEnv(root, source)
	if !envHasPrefix(env, "ASDF_OUTPUT_TRANSLATIONS=") {
		t.Fatalf("CacheEnv has no ASDF_OUTPUT_TRANSLATIONS: %v", env)
	}
	if value := cacheEnvValue(t, env, "ASDF_OUTPUT_TRANSLATIONS"); !strings.Contains(value, filepath.ToSlash(filepath.Join(LispCacheDir(root), tip))) || !strings.Contains(value, filepath.ToSlash(source)) {
		t.Fatalf("ASDF_OUTPUT_TRANSLATIONS=%q, want source %s mapped to shared tip cache %s", value, source, filepath.Join(LispCacheDir(root), tip))
	}
}

// ASDF invalidates a FASL when a fresh clone's source mtime is newer than the compiled
// output. Staging pins tracked-file mtimes to the tip's commit time so two paths mapping to
// the same shared output can actually reuse it.
func TestStageJobTreePinsTrackedMtimeForCompiledCacheReuse(t *testing.T) {
	source, _ := prewarmFixture(t)
	dest := filepath.Join(t.TempDir(), "job", JobRepo)
	if err := StageJobTree(source, dest, nil); err != nil {
		t.Fatal(err)
	}
	commitUnix, _ := strconv.ParseInt(gitT(t, source, "show", "-s", "--format=%ct", "HEAD"), 10, 64)
	fi, err := os.Stat(filepath.Join(dest, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.ModTime().Unix() != commitUnix {
		t.Fatalf("fresh clone source mtime=%s, want tip commit time %s", fi.ModTime(), time.Unix(commitUnix, 0))
	}
}

func TestASDFMappingReusesCompiledOutputAcrossFreshJobClone(t *testing.T) {
	sbcl, err := exec.LookPath("sbcl")
	if err != nil {
		t.Skip("sbcl is not installed on this test host")
	}
	root := t.TempDir()
	source := filepath.Join(root, "source")
	mustWrite(t, filepath.Join(source, "warm.asd"), "(asdf:defsystem #:warm :serial t :components ((:file \"warm\")))\n")
	mustWrite(t, filepath.Join(source, "warm.lisp"), "(defpackage #:warm (:use #:cl))\n(in-package #:warm)\n(defun answer () 42)\n")
	gitT(t, "", "init", "-q", "-b", "dev", source)
	gitT(t, source, "add", "-A")
	gitT(t, source, "commit", "-q", "-m", "lisp fixture")
	if err := normalizeTrackedTimes(source); err != nil {
		t.Fatal(err)
	}
	cacheRoot := filepath.Join(root, "pool")
	if err := os.MkdirAll(LispCacheDir(cacheRoot), 0o755); err != nil {
		t.Fatal(err)
	}
	load := func(repo string) string {
		t.Helper()
		cmd := exec.Command(sbcl, "--non-interactive",
			"--eval", "(require :asdf)",
			"--eval", fmt.Sprintf("(push #p%q asdf:*central-registry*)", filepath.ToSlash(repo)+"/"),
			"--eval", "(asdf:load-system :warm)")
		cmd.Env = append(os.Environ(), "ASDF_OUTPUT_TRANSLATIONS="+ASDFOutputTranslations(cacheRoot, repo))
		out, runErr := cmd.CombinedOutput()
		if runErr != nil {
			t.Fatalf("load fixture from %s: %v\n%s", repo, runErr, out)
		}
		return string(out)
	}
	if first := load(source); !strings.Contains(first, "compiling file") {
		t.Fatalf("cold load did not compile the fixture:\n%s", first)
	}
	job := filepath.Join(root, "job", JobRepo)
	if err := StageJobTree(source, job, nil); err != nil {
		t.Fatal(err)
	}
	if second := load(job); strings.Contains(second, "compiling file") {
		t.Fatalf("fresh exact-tip clone recompiled instead of reusing shared FASL:\n%s", second)
	}
}

func TestSpecNamesExactTipPrewarmAndItsFleetMeasurement(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-SWARM.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := strings.Join(strings.Fields(string(raw)), " ")
	for _, want := range []string{
		"## Exact-tip bench prewarm (#2498 S3)",
		"modules, ordinary builds, compiled Go test binaries and ASDF FASLs",
		"hidden until all four phases succeed",
		"`make test` in a fresh job on each adopted bench finishes in under 60 seconds",
		"A PREWARM receipt proves preparation, not fleet adoption",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("SPEC-SWARM.md does not contain %q", want)
		}
	}
}
