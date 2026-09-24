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
		{"make", "compile-lisp"},
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

func TestPrewarmFailedRerunInvalidatesPriorReceipt(t *testing.T) {
	source, tip := prewarmFixture(t)
	root := filepath.Join(t.TempDir(), "pool")
	input := PrewarmInput{Root: root, Source: source, Repo: "acme/tool", Tip: tip,
		Run: func(PrewarmCommand) error { return nil }}
	if _, err := Prewarm(input); err != nil {
		t.Fatal(err)
	}
	receipt := filepath.Join(root, PrewarmReceiptDir, "acme", "tool@"+tip+".receipt")
	if _, err := os.Stat(receipt); err != nil {
		t.Fatalf("first prewarm receipt: %v", err)
	}
	input.Run = func(c PrewarmCommand) error {
		if c.Name == "build" {
			return errors.New("cold cache build failed")
		}
		return nil
	}
	if _, err := Prewarm(input); err == nil || !strings.Contains(err.Error(), "build") {
		t.Fatalf("rerun error = %v, want build phase failure", err)
	}
	if _, err := os.Lstat(receipt); !os.IsNotExist(err) {
		t.Fatalf("failed rerun left prior success receipt standing: stat err=%v", err)
	}
}

func TestPrewarmGitChildrenDropSecrets(t *testing.T) {
	source, tip := prewarmFixture(t)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "secret-child.log")
	wrapper := "#!/bin/sh\nif [ -n \"$STELLA_REVIEW_TOKEN\" ]; then printf '%s\\n' \"$*\" >> \"$STELLA_REVIEW_LOG\"; fi\nexec \"$STELLA_REAL_GIT\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STELLA_REVIEW_TOKEN", "must-not-reach-child")
	t.Setenv("STELLA_REVIEW_LOG", log)
	t.Setenv("STELLA_REAL_GIT", realGit)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := Prewarm(PrewarmInput{
		Root: filepath.Join(t.TempDir(), "pool"), Source: source, Repo: "acme/tool", Tip: tip,
		Run: func(PrewarmCommand) error { return nil },
	}); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(log); err == nil && len(raw) > 0 {
		t.Fatalf("secret-named variable reached git subprocesses:\n%s", raw)
	} else if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
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

// Each child writes a private overlay beside its checkout. PrepareLispJobCache seeds it
// from the shared exact-tip output before launch.
func TestCacheEnvCarriesAPrivateLispOverlay(t *testing.T) {
	source, _ := prewarmFixture(t)
	root := t.TempDir()
	if err := EnsureCacheDirs(root); err != nil {
		t.Fatal(err)
	}
	env := CacheEnv(root, source)
	if !envHasPrefix(env, "ASDF_OUTPUT_TRANSLATIONS=") {
		t.Fatalf("CacheEnv has no ASDF_OUTPUT_TRANSLATIONS: %v", env)
	}
	if value := cacheEnvValue(t, env, "ASDF_OUTPUT_TRANSLATIONS"); !strings.Contains(value, filepath.ToSlash(JobLispCacheDir(source))) || !strings.Contains(value, filepath.ToSlash(source)) {
		t.Fatalf("ASDF_OUTPUT_TRANSLATIONS=%q, want source %s mapped to private overlay %s", value, source, JobLispCacheDir(source))
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

func TestPrewarmTrackedSymlinkTargetMtimeIsUntouched(t *testing.T) {
	source, _ := prewarmFixture(t)
	target := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(target, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(source, "outside-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	gitT(t, source, "add", "outside-link")
	gitT(t, source, "commit", "-q", "-m", "tracked symlink")
	tip := gitT(t, source, "rev-parse", "HEAD")
	want := time.Unix(123456789, 0)
	if err := os.Chtimes(target, want, want); err != nil {
		t.Fatal(err)
	}
	if _, err := Prewarm(PrewarmInput{
		Root: filepath.Join(t.TempDir(), "pool"), Source: source, Repo: "acme/tool", Tip: tip,
		Run: func(PrewarmCommand) error { return nil },
	}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if fi.ModTime().Unix() != want.Unix() {
		t.Fatalf("prewarm followed tracked symlink and changed external target mtime: got %v want %v", fi.ModTime(), want)
	}
}

func TestPrepareLispJobCacheRefusesSymlinkOverlay(t *testing.T) {
	root := t.TempDir()
	job := filepath.Join(root, "job")
	source := filepath.Join(job, JobRepo)
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	external := t.TempDir()
	if err := os.Symlink(external, filepath.Join(job, ".cache")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := PrepareLispJobCache(root, source); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("PrepareLispJobCache through symlink error = %v, want refusal", err)
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
	load := func(repo, translations string) string {
		t.Helper()
		cmd := exec.Command(sbcl, "--non-interactive",
			"--eval", "(require :asdf)",
			"--eval", fmt.Sprintf("(push #p%q asdf:*central-registry*)", filepath.ToSlash(repo)+"/"),
			"--eval", "(asdf:load-system :warm)",
			"--eval", "(format t \"ANSWER=~A~%\" (warm::answer))")
		cmd.Env = append(os.Environ(), "ASDF_OUTPUT_TRANSLATIONS="+translations)
		out, runErr := cmd.CombinedOutput()
		if runErr != nil {
			t.Fatalf("load fixture from %s: %v\n%s", repo, runErr, out)
		}
		return string(out)
	}
	if first := load(source, ASDFOutputTranslations(cacheRoot, source)); !strings.Contains(first, "compiling file") {
		t.Fatalf("cold load did not compile the fixture:\n%s", first)
	}
	jobA := filepath.Join(root, "job-a", JobRepo)
	jobB := filepath.Join(root, "job-b", JobRepo)
	for _, job := range []string{jobA, jobB} {
		if err := StageJobTree(source, job, nil); err != nil {
			t.Fatal(err)
		}
		if err := PrepareLispJobCache(cacheRoot, job); err != nil {
			t.Fatal(err)
		}
	}
	if second := load(jobA, JobASDFOutputTranslations(jobA)); strings.Contains(second, "compiling file") {
		t.Fatalf("fresh exact-tip clone recompiled instead of reusing shared FASL:\n%s", second)
	}
	mustWrite(t, filepath.Join(jobA, "warm.lisp"), "(defpackage #:warm (:use #:cl))\n(in-package #:warm)\n(defun answer () 7)\n")
	newer := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(filepath.Join(jobA, "warm.lisp"), newer, newer); err != nil {
		t.Fatal(err)
	}
	if edited := load(jobA, JobASDFOutputTranslations(jobA)); !strings.Contains(edited, "ANSWER=7") {
		t.Fatalf("edited card did not load its own code:\n%s", edited)
	}
	if sibling := load(jobB, JobASDFOutputTranslations(jobB)); !strings.Contains(sibling, "ANSWER=42") {
		t.Fatalf("same-tip sibling loaded another card's edited FASL:\n%s", sibling)
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
