package tlc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// JarEnv is the environment variable that names the TLC jar when --jar is not
// given. The Makefile target and the workflow set it; nothing else is looked at.
const JarEnv = "TLC_JAR"

// Jar is the TLC jar a run uses, and where its path came from.
type Jar struct {
	Path   string // absolute
	Source string // "flag" or "env:TLC_JAR"
	SHA256 string // hex
}

// FindJar resolves the jar: the flag when it is not empty, otherwise the
// environment variable. There is no default location and nothing is downloaded.
// The jar must be a regular file; its digest is read now so every record of a
// run names the same jar even when the file is replaced while the run goes on.
func FindJar(flag string, getenv func(string) string) (Jar, error) {
	path, source := flag, "flag"
	if path == "" {
		path, source = getenv(JarEnv), "env:"+JarEnv
	}
	if path == "" {
		return Jar{}, errors.New("no TLC jar named: neither --jar nor " + JarEnv + " is set")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return Jar{}, fmt.Errorf("TLC jar path %q cannot be resolved: %v", path, err)
	}
	info, err := os.Stat(abs)
	if err != nil || !info.Mode().IsRegular() {
		return Jar{}, fmt.Errorf("TLC jar %s is not a readable file (%s)", abs, source)
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return Jar{}, fmt.Errorf("TLC jar %s cannot be read: %v", abs, err)
	}
	sum := sha256.Sum256(raw)
	return Jar{Path: abs, Source: source, SHA256: hex.EncodeToString(sum[:])}, nil
}

// FindHelper resolves an external program: the override when it is not empty
// (it must exist), otherwise the name on PATH. The path found is returned so a
// caller can echo it. lookPath is exec.LookPath in production.
//
// The path is returned absolute. The program is started with another working
// directory than the caller's (a private copy of the models), so a path that
// is relative to the caller would name nothing there.
func FindHelper(name, override string, lookPath func(string) (string, error)) (string, error) {
	if override != "" {
		abs, err := filepath.Abs(override)
		if err != nil {
			return "", fmt.Errorf("%s override %s cannot be resolved: %v", name, override, err)
		}
		if _, err := os.Stat(abs); err != nil {
			return "", fmt.Errorf("%s override %s does not exist", name, override)
		}
		return abs, nil
	}
	path, err := lookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s is not on PATH", name)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("%s at %s cannot be resolved: %v", name, path, err)
	}
	return abs, nil
}

// LookPath is exec.LookPath, named so callers pass one seam.
var LookPath = exec.LookPath

// javaVersionLine matches the line `java -version` prints for the version: it
// starts with openjdk version or java version, then the quoted version. A line
// that only contains version " (the "Picked up JAVA_TOOL_OPTIONS: ..." lines a
// JVM prints first echo the variable's text) is not one. The version is digits
// first, then letters, digits and . _ + -.
var javaVersionLine = regexp.MustCompile(`^\s*(?:openjdk|java) version "([0-9][0-9A-Za-z._+-]*)"`)

// JavaVersion reads the version out of what `java -version` prints (it goes to
// standard error): the quoted token of the first line that starts with
// `openjdk version "` or `java version "`, for example 21.0.12.1. Lines before
// it are skipped, so a "Picked up JAVA_TOOL_OPTIONS" line never gives the
// version. It is an error when no line is one, so a record never names a java
// of no version.
func JavaVersion(output string) (string, error) {
	trimmed := strings.TrimSpace(output)
	first, _, _ := strings.Cut(trimmed, "\n")
	for _, line := range strings.Split(trimmed, "\n") {
		if m := javaVersionLine.FindStringSubmatch(line); m != nil {
			return m[1], nil
		}
	}
	return "", fmt.Errorf("java -version printed no line of the form openjdk version \"...\" or java version \"...\": %q", strings.TrimSpace(first))
}

// ReadJavaVersion runs `java -version`, bounded by ctx, and returns its
// version. It runs java, so it belongs on a bench.
func ReadJavaVersion(ctx context.Context, java string) (string, error) {
	out, err := subproc.Context(ctx, java, "-version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s -version failed: %v", java, err)
	}
	return JavaVersion(string(out))
}
