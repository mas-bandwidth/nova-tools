package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// SumsFile is the name of the checksum file in every artifact directory, in the
// format `sha256sum -c` reads, because the person verifying a copy by hand
// should not need this tool to do it.
const SumsFile = "SHA256SUMS"

// Platform is the goos-goarch an artifact directory is named for. A release
// built here for this host is the fleet's common case -- hulk builds for hulk,
// the Studio builds for the Studio -- and --platform is the flag for the other
// one, cross-compiling to a bench from wherever the release was cut.
func Platform(flagValue string) (string, string, error) {
	if flagValue == "" {
		return runtime.GOOS, runtime.GOARCH, nil
	}
	goos, goarch, ok := strings.Cut(flagValue, "-")
	if !ok || goos == "" || goarch == "" {
		return "", "", refuse("pass --platform <goos>-<goarch>, for example linux-amd64",
			"%q is not a goos-goarch", flagValue)
	}
	return goos, goarch, nil
}

// ArtifactDir is where one platform's binaries for one version live, under the
// root --out or --from names. The version and the platform are both in the path
// so that one root can hold several releases and several platforms at once,
// which is what a build bench serving four benches actually holds.
func ArtifactDir(root, version, goos, goarch string) string {
	return filepath.Join(root, version, goos+"-"+goarch)
}

// Tools lists the shipped set: every cmd/nova-* directory in the source tree,
// discovered by walking it rather than from a list. release.yml makes the same
// argument at greater length -- a tool added tomorrow ships on the day it
// appears rather than on the day somebody remembers a list.
func Tools(source string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(source, "cmd"))
	if err != nil {
		return nil, err
	}
	var tools []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "nova-") {
			tools = append(tools, e.Name())
		}
	}
	sort.Strings(tools)
	return tools, nil
}

func build(ctx context.Context, o options, deps Deps, out, errs io.Writer) int {
	goos, goarch, err := Platform(o.platform)
	if err != nil {
		return refusal(errs, "BUILD", err)
	}
	tools, err := Tools(o.source)
	// A --source with no cmd/ at all is the same mistake as one with no
	// cmd/nova-*: somebody named a directory that is not a nova-tools
	// checkout. One refusal says so, rather than two that read differently
	// for the same error.
	if err != nil && !os.IsNotExist(err) {
		return refusal(errs, "BUILD", fmt.Errorf("cannot read %s: %w (name a nova-tools checkout with --source)", filepath.Join(o.source, "cmd"), err))
	}
	if len(tools) == 0 {
		return refusal(errs, "BUILD", refuse("name a nova-tools checkout with --source",
			"%s holds no cmd/nova-* directory, so this build would ship an empty set", o.source))
	}
	tc := deps.Toolchain
	if tc == nil {
		tc = GoBuild{}
	}
	dir := ArtifactDir(o.out, o.version, goos, goarch)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return refusal(errs, "BUILD", fmt.Errorf("cannot create %s: %w (name a writable --out)", dir, err))
	}
	// THE STAMP IS COMPOSED ONCE, before the first target, the way
	// .github/scripts/release-ldflags.sh composes it once for the release
	// workflow: `-X main.version=` with an empty value is a legal linker flag
	// that stamps nothing, and nothing downstream notices (#118).
	args := []string{"-trimpath", "-ldflags", Ldflags(o.version)}
	for i, tool := range tools {
		progress(errs, "building %s for %s/%s (%d/%d)", tool, goos, goarch, i+1, len(tools))
		name := tool
		if goos == "windows" {
			name += ".exe"
		}
		output, err := tc.Build(ctx, o.source, "./cmd/"+tool, filepath.Join(dir, name), goos, goarch, args)
		if err != nil {
			fmt.Fprintf(errs, "BUILD FAIL tool=%s platform=%s version=%s: %s (fix the compile error and build again; no %s was written)\n",
				field(tool), field(goos+"-"+goarch), field(o.version), oneLine(output, err), SumsFile)
			return 1
		}
	}
	// SHA256SUMS LAST, over the whole set, in the same step that finished it.
	// A checksum file written beside a half-built directory is a file that
	// agrees with itself and with nothing anybody released.
	sums, err := writeSums(dir)
	if err != nil {
		return refusal(errs, "BUILD", fmt.Errorf("cannot write %s: %w (name a writable --out)", filepath.Join(dir, SumsFile), err))
	}
	fmt.Fprintf(out, "RELEASE BUILT version=%s platform=%s tools=%d out=%s sums=%s\n",
		field(o.version), field(goos+"-"+goarch), len(tools), field(dir), field(sums))
	return 0
}

// writeSums hashes every file in dir except the checksum file itself and writes
// the result in `sha256sum` format. The file cannot list itself: a checksum over
// a file that is being written is a number nobody can reproduce.
func writeSums(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var lines []string
	for _, e := range entries {
		if e.IsDir() || e.Name() == SumsFile {
			continue
		}
		sum, err := fileSum(filepath.Join(dir, e.Name()))
		if err != nil {
			return "", err
		}
		lines = append(lines, sum+"  "+e.Name())
	}
	sort.Strings(lines)
	path := filepath.Join(dir, SumsFile)
	return path, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

func fileSum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// oneLine folds a child's output and its error into one readable token, bounded,
// so a compiler that printed 400 lines does not print 400 lines here.
func oneLine(output string, err error) string {
	text := strings.TrimSpace(output)
	if text == "" {
		text = err.Error()
	} else {
		text = err.Error() + ": " + text
	}
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > 300 {
		text = text[:300] + "..."
	}
	return text
}
