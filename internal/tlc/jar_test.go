package tlc

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parseDuration(s string) (time.Duration, error) { return time.ParseDuration(s) }

func TestFindJarPrefersTheFlagThenTheEnvironment(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	flagJar := filepath.Join(dir, "flag.jar")
	envJar := filepath.Join(dir, "env.jar")
	for _, p := range []string{flagJar, envJar} {
		require.NoError(t, os.WriteFile(p, []byte(filepath.Base(p)), 0o644))
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
	require.Equal(t, hex.EncodeToString(sum[:]), got.SHA256, "digest %s is not the jar's", got.SHA256)
	got, err = FindJar("", env)
	if err != nil || got.Path != envJar || got.Source != "env:TLC_JAR" {
		t.Fatalf("env: %+v, %v", got, err)
	}
}

func TestFindJarRefusesWhatIsNotAFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	none := func(string) string { return "" }
	_, err := FindJar("", none)
	assert.ErrorContains(t, err, "neither --jar nor TLC_JAR", "no jar named")
	for name, path := range map[string]string{"a directory": dir, "a missing file": filepath.Join(dir, "gone.jar")} {
		_, err := FindJar(path, none)
		assert.Error(t, err, "%s was accepted", name)
	}
}

func TestFindHelperUsesTheOverrideThenPATH(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	override := filepath.Join(dir, "java")
	require.NoError(t, os.WriteFile(override, nil, 0o755))
	onPath := func(name string) (string, error) { return "/usr/bin/" + name, nil }
	offPath := func(string) (string, error) { return "", errors.New("not found") }
	if got, err := FindHelper("java", override, offPath); err != nil || got != override {
		t.Errorf("override: %q, %v", got, err)
	}
	if got, err := FindHelper("java", "", onPath); err != nil || got != "/usr/bin/java" {
		t.Errorf("PATH: %q, %v", got, err)
	}
	_, err := FindHelper("java", "", offPath)
	assert.ErrorContains(t, err, "java is not on PATH", "absent")
	_, err = FindHelper("java", filepath.Join(dir, "gone"), onPath)
	assert.Error(t, err, "a missing override fell back to PATH")
}

func TestJavaVersionReadsTheQuotedTokenOfTheVersionLine(t *testing.T) {
	t.Parallel()
	ok := map[string]string{
		"openjdk version \"21.0.12.1\" 2026-08-18 LTS\nOpenJDK Runtime Environment Temurin-21.0.12.1+1 (build 21.0.12.1+1-LTS)\n": "21.0.12.1",
		"java version \"1.8.0_402\"\nJava(TM) SE Runtime Environment\n":                                                           "1.8.0_402",
		"Picked up JAVA_TOOL_OPTIONS: -Xmx2g\nopenjdk version \"21.0.12.1\" 2026-08-18\n":                                         "21.0.12.1",
		"Picked up JAVA_TOOL_OPTIONS: -Dsome=\"quoted\"\nPicked up _JAVA_OPTIONS: -Xmx1g\njava version \"1.8.0_402\"\n":           "1.8.0_402",
		"Picked up JAVA_TOOL_OPTIONS: -Dfoo=version \"evil\"\nopenjdk version \"21.0.12.1\"\n":                                    "21.0.12.1",
		"NOTE: Picked up JDK_JAVA_OPTIONS: --version \"x\"\nopenjdk version \"21.0.12.1\" 2026-08-18\n":                           "21.0.12.1",
		"  openjdk version \"17\" 2021-09-14\n":                                                                                   "17",
	}
	for out, want := range ok {
		if got, err := JavaVersion(out); err != nil || got != want {
			t.Errorf("%q: %q, %v; want %q", out, got, err, want)
		}
	}
	for _, out := range []string{"", "no version here\n", "openjdk version \"\"\n", "openjdk version \"21 x\"\n", "Picked up JAVA_TOOL_OPTIONS: -Dfoo=version \"evil\"\n", "NOTE: Picked up JDK_JAVA_OPTIONS: --version \"x\"\n", "openjdk version \"-\"\n", "openjdk version \"21,0\"\n", "java version \"x21\"\n", "openjdk version 21\nsecond \"22\"\n", "Picked up JAVA_TOOL_OPTIONS: -Xmx2g\nno version line\n", "Picked up JAVA_TOOL_OPTIONS: -Xmx2g\nopenjdk version \"\"\n"} {
		got, err := JavaVersion(out)
		assert.Error(t, err, "%q gave version %q", out, got)
	}
}
