package tlc

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindJarPrefersTheFlagThenTheEnvironment(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	flagJar, flagSum := r.jar("flag.jar")
	envJar, _ := r.jar("env.jar")
	env := func(k string) string {
		if k == JarEnv {
			return envJar
		}
		return ""
	}
	got, err := FindJar(flagJar, env)
	require.NoError(t, err, "flag: %+v, %v", got, err)
	require.Equal(t, flagJar, got.Path, "flag: %+v, %v", got, err)
	require.Equal(t, "flag", got.Source, "flag: %+v, %v", got, err)
	require.Equal(t, flagSum, got.SHA256, "digest %s is not the jar's", got.SHA256)
	got, err = FindJar("", env)
	require.NoError(t, err, "env: %+v, %v", got, err)
	require.Equal(t, envJar, got.Path, "env: %+v, %v", got, err)
	require.Equal(t, "env:TLC_JAR", got.Source, "env: %+v, %v", got, err)
}

func TestFindJarRefusesWhatIsNotAFile(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	none := func(string) string { return "" }
	_, err := FindJar("", none)
	assert.ErrorContains(t, err, "neither --jar nor TLC_JAR", "no jar named")
	for name, path := range map[string]string{"a directory": r.root, "a missing file": filepath.Join(r.root, "gone.jar")} {
		_, err := FindJar(path, none)
		assert.Error(t, err, "%s was accepted", name)
	}
}

func TestFindHelperUsesTheOverrideThenPATH(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	override := filepath.Join(r.root, "java")
	require.NoError(t, os.WriteFile(override, nil, 0o755))
	onPath := func(name string) (string, error) { return "/usr/bin/" + name, nil }
	offPath := func(string) (string, error) { return "", errors.New("not found") }
	got, err := FindHelper("java", override, offPath)
	assert.NoError(t, err, "override: %q, %v", got, err)
	assert.Equal(t, override, got, "override: %q, %v", got, err)
	got, err = FindHelper("java", "", onPath)
	assert.NoError(t, err, "PATH: %q, %v", got, err)
	assert.Equal(t, "/usr/bin/java", got, "PATH: %q, %v", got, err)
	_, err = FindHelper("java", "", offPath)
	assert.ErrorContains(t, err, "java is not on PATH", "absent")
	_, err = FindHelper("java", filepath.Join(r.root, "gone"), onPath)
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
		got, err := JavaVersion(out)
		if assert.NoError(t, err, "%q: %q, %v; want %q", out, got, err, want) {
			assert.Equal(t, want, got, "%q: %q, %v; want %q", out, got, err, want)
		}
	}
	for _, out := range []string{"", "no version here\n", "openjdk version \"\"\n", "openjdk version \"21 x\"\n", "Picked up JAVA_TOOL_OPTIONS: -Dfoo=version \"evil\"\n", "NOTE: Picked up JDK_JAVA_OPTIONS: --version \"x\"\n", "openjdk version \"-\"\n", "openjdk version \"21,0\"\n", "java version \"x21\"\n", "openjdk version 21\nsecond \"22\"\n", "Picked up JAVA_TOOL_OPTIONS: -Xmx2g\nno version line\n", "Picked up JAVA_TOOL_OPTIONS: -Xmx2g\nopenjdk version \"\"\n"} {
		got, err := JavaVersion(out)
		assert.Error(t, err, "%q gave version %q", out, got)
	}
}
