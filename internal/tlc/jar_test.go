package tlc

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func parseDuration(s string) (time.Duration, error) { return time.ParseDuration(s) }

func TestFindJarPrefersTheFlagThenTheEnvironment(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	flagJar := filepath.Join(dir, "flag.jar")
	envJar := filepath.Join(dir, "env.jar")
	for _, p := range []string{flagJar, envJar} {
		if err := os.WriteFile(p, []byte(filepath.Base(p)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	env := func(k string) string {
		if k == JarEnv {
			return envJar
		}
		return ""
	}
	got, err := FindJar(flagJar, env)
	if err != nil || got.Path != flagJar || got.Source != "flag" {
		t.Fatalf("flag: %+v, %v", got, err)
	}
	sum := sha256.Sum256([]byte("flag.jar"))
	if got.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("digest %s is not the jar's", got.SHA256)
	}
	got, err = FindJar("", env)
	if err != nil || got.Path != envJar || got.Source != "env:TLC_JAR" {
		t.Fatalf("env: %+v, %v", got, err)
	}
}

func TestFindJarRefusesWhatIsNotAFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	none := func(string) string { return "" }
	if _, err := FindJar("", none); err == nil || !strings.Contains(err.Error(), "neither --jar nor TLC_JAR") {
		t.Errorf("no jar named: %v", err)
	}
	for name, path := range map[string]string{"a directory": dir, "a missing file": filepath.Join(dir, "gone.jar")} {
		if _, err := FindJar(path, none); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestFindHelperUsesTheOverrideThenPATH(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	override := filepath.Join(dir, "java")
	if err := os.WriteFile(override, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	onPath := func(name string) (string, error) { return "/usr/bin/" + name, nil }
	offPath := func(string) (string, error) { return "", errors.New("not found") }
	if got, err := FindHelper("java", override, offPath); err != nil || got != override {
		t.Errorf("override: %q, %v", got, err)
	}
	if got, err := FindHelper("java", "", onPath); err != nil || got != "/usr/bin/java" {
		t.Errorf("PATH: %q, %v", got, err)
	}
	if _, err := FindHelper("java", "", offPath); err == nil || !strings.Contains(err.Error(), "java is not on PATH") {
		t.Errorf("absent: %v", err)
	}
	if _, err := FindHelper("java", filepath.Join(dir, "gone"), onPath); err == nil {
		t.Error("a missing override fell back to PATH")
	}
}

func TestJavaVersionReadsTheQuotedTokenOfTheFirstLine(t *testing.T) {
	t.Parallel()
	ok := map[string]string{
		"openjdk version \"21.0.12.1\" 2026-08-18 LTS\nOpenJDK Runtime Environment Temurin-21.0.12.1+1 (build 21.0.12.1+1-LTS)\n": "21.0.12.1",
		"java version \"1.8.0_402\"\nJava(TM) SE Runtime Environment\n":                                                           "1.8.0_402",
		"  openjdk version \"17\" 2021-09-14\n":                                                                                   "17",
	}
	for out, want := range ok {
		if got, err := JavaVersion(out); err != nil || got != want {
			t.Errorf("%q: %q, %v; want %q", out, got, err, want)
		}
	}
	for _, out := range []string{"", "no version here\n", "openjdk version \"\"\n", "openjdk version \"21 x\"\n", "openjdk version 21\nsecond \"22\"\n"} {
		if got, err := JavaVersion(out); err == nil {
			t.Errorf("%q gave version %q", out, got)
		}
	}
}
