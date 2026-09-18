package release

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Artifact is one shipped binary: the name it installs under and the checksum
// the build recorded for it.
type Artifact struct {
	Name string
	Sum  string
}

// ReadSums reads an artifact directory's checksum file. It is the whole of what
// `install` trusts about a directory it did not build: the names come from the
// file, so a binary somebody dropped into the directory afterwards is not part
// of the release and is not installed.
func ReadSums(dir string) ([]Artifact, error) {
	body, err := os.ReadFile(filepath.Join(dir, SumsFile))
	if err != nil {
		return nil, err
	}
	var arts []Artifact
	for i, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		sum, name, ok := strings.Cut(line, "  ")
		if !ok || sum == "" || name == "" {
			return nil, refuse("build the release again with `nova-update release build`",
				"%s line %d is not a sha256sum line: %q", SumsFile, i+1, line)
		}
		// A name with a separator in it would install outside --bin. The
		// build writes bare names; anything else is not this file's.
		if strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
			return nil, refuse("build the release again with `nova-update release build`",
				"%s line %d names a path rather than a file: %q", SumsFile, i+1, name)
		}
		arts = append(arts, Artifact{Name: name, Sum: sum})
	}
	if len(arts) == 0 {
		return nil, refuse("build the release again with `nova-update release build`",
			"%s lists no artifact", SumsFile)
	}
	sort.Slice(arts, func(i, j int) bool { return arts[i].Name < arts[j].Name })
	return arts, nil
}

func install(ctx context.Context, o options, deps Deps, out, errs io.Writer) int {
	goos, goarch, err := Platform(o.platform)
	if err != nil {
		return refusal(errs, "INSTALL", err)
	}
	dir := ArtifactDir(o.from, o.version, goos, goarch)
	arts, err := ReadSums(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return refusal(errs, "INSTALL", refuse(
				fmt.Sprintf("build it first: nova-update release build --version %s --out %s --source <checkout>", o.version, o.from),
				"there is no %s for %s at %s", o.version, goos+"-"+goarch, dir))
		}
		return refusal(errs, "INSTALL", err)
	}
	// VERIFIED WHOLE BEFORE THE FIRST RENAME. A set checked file by file as it
	// installs puts good binaries beside a bad one and leaves the box in a
	// state no version answers for.
	progress(errs, "verifying %d artifacts against %s", len(arts), SumsFile)
	for _, a := range arts {
		path := filepath.Join(dir, a.Name)
		got, err := fileSum(path)
		if err != nil {
			return refusal(errs, "INSTALL", fmt.Errorf("cannot read %s: %w (build the release again)", path, err))
		}
		if got != a.Sum {
			return refusal(errs, "INSTALL", refuse("build the release again; do not install an artifact whose bytes changed after it was built",
				"%s does not match %s: recorded %s, on disk %s", a.Name, SumsFile, a.Sum, got))
		}
	}
	if err := os.MkdirAll(o.bin, 0o755); err != nil {
		return refusal(errs, "INSTALL", fmt.Errorf("cannot create %s: %w (name a writable --bin)", o.bin, err))
	}
	versionOf := deps.VersionOf
	if versionOf == nil {
		versionOf = ExecVersion
	}
	installed, skipped := 0, 0
	for _, a := range arts {
		target := filepath.Join(o.bin, a.Name)
		// SKIP WHEN THE BOX ALREADY ANSWERS. The question is asked of the
		// BINARY, not of a marker file: a marker says what somebody meant to
		// install, and the whole point of the version verbs is to say what is
		// actually there.
		if line, err := versionOf(ctx, target); err == nil && hasToken(line, o.version) {
			skipped++
			continue
		}
		progress(errs, "installing %s", a.Name)
		if err := atomicInstall(filepath.Join(dir, a.Name), target); err != nil {
			fmt.Fprintf(errs, "INSTALL FAIL tool=%s bin=%s: %s (fix the permission or the disk and install again; %d of %d were in place)\n",
				field(a.Name), field(o.bin), oneLine("", err), installed, len(arts))
			return 1
		}
		installed++
	}
	fmt.Fprintf(out, "RELEASE INSTALLED version=%s tools=%d skipped=%d bin=%s platform=%s\n",
		field(o.version), installed, skipped, field(o.bin), field(goos+"-"+goarch))
	return 0
}

// hasToken is the same whole-token match .github/scripts/assert-version-stamp.sh
// makes, and for the same reason: v0.1 must not pass for v0.11, and `=` is a
// separator because a tool printing `version=<tag>` is printing the tag.
func hasToken(line, version string) bool {
	for _, f := range strings.Fields(strings.ReplaceAll(line, "=", " ")) {
		if f == version {
			return true
		}
	}
	return false
}

// atomicInstall writes beside the target and renames over it. The rename is what
// makes this safe to run on a bench with work in flight: a process already
// running keeps its own inode, and no reader ever sees a half-copied binary.
func atomicInstall(src, dst string) error {
	body, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst)+".new")
	if err := os.WriteFile(tmp, body, 0o755); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// ExecVersion is the production answer to what the binary at path reports: its
// own `version` verb, which every tool in this repository answers (#121), under
// a short deadline because the binary being replaced may be the broken one.
func ExecVersion(ctx context.Context, path string) (string, error) {
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := runCommand(ctx, path, "version")
	if err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	return line, nil
}
