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

	"github.com/mas-bandwidth/nova-tools/internal/safepath"
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

// VerifyArtifacts checks every artifact in dir against the checksums the build
// recorded. It is one function because two callers need exactly this: `install`
// before its first rename, and `adopt` after fetching a release from another
// machine -- a truncated fetch caught once on the adopting host is one refusal
// rather than one per machine.
// It returns HOW MANY it checked, and `build` prints that number rather than
// the length of the list it was given. The two are equal when the check ran and
// there is no way to print the number without running it -- which is the point:
// `verified=21` computed from a slice length would be a claim about work that
// may not have happened, and a claim like that is worse than no claim.
func VerifyArtifacts(dir string, arts []Artifact) (int, error) {
	checked := 0
	for _, a := range arts {
		path := filepath.Join(dir, a.Name)
		got, err := fileSum(path)
		if err != nil {
			return checked, fmt.Errorf("cannot read %s: %w (build the release again)", path, err)
		}
		if got != a.Sum {
			return checked, refuse("build the release again; do not install an artifact whose bytes changed after it was built",
				"%s does not match %s: recorded %s, on disk %s", a.Name, SumsFile, a.Sum, got)
		}
		checked++
	}
	return checked, nil
}

// retire removes this release's tools from a SECOND directory that is no longer
// the one anybody should be running from.
//
// It exists because the shell script this verb replaces kept ~/go/bin in step
// with ~/.local/bin, and the verb did not: after the first real adoption every
// bench held 18 stale ~/go/bin/nova-* from a `go install` months ago, which is
// worse than the old state rather than better, because both directories are on
// PATH and which one wins is a fact about the PATH order nobody has read.
//
// WHAT IT WILL REMOVE IS NARROW, and every clause is load-bearing. Only a name
// this very run installed into --bin; only a name beginning `nova-`; only a
// regular file, so a directory or a symlink is left for a person; and only
// through safepath.RemoveUnder, which refuses a path that is not strictly below
// the root, refuses the root itself and refuses a link (Glenn 2026-09-17: "it
// shouldn't be able to delete arbitrary directories"). --retire naming --bin is
// refused outright: that is the one argument that would delete the release this
// verb has just installed.
func retire(dir, bin, stamp string, arts []Artifact, errs io.Writer) (int, error) {
	binAbs, err := filepath.Abs(bin)
	if err != nil {
		return 0, fmt.Errorf("cannot resolve --bin %s: %w", bin, err)
	}
	retireAbs, err := filepath.Abs(dir)
	if err != nil {
		return 0, fmt.Errorf("cannot resolve --retire %s: %w", dir, err)
	}
	if retireAbs == binAbs {
		return 0, refuse("name a DIFFERENT directory, the stale one this release is not installed into",
			"--retire %s is --bin: retiring there would delete the release just installed", dir)
	}
	// AND NEVER THE STAMP. --from's artifact directory is the last-good copy
	// of this release: it is what a re-install reads, what a rollback reads,
	// and what `adopt` just verified. Retiring there would delete the evidence
	// along with the tools and leave the machine with no way back (Johnny,
	// 2026-09-18).
	stampAbs, err := filepath.Abs(stamp)
	if err != nil {
		return 0, fmt.Errorf("cannot resolve --from %s: %w", stamp, err)
	}
	// SYMMETRIC: the stamp itself, anything inside it, and anything that
	// CONTAINS it. The artifact root is the third case -- `--retire <the
	// --from root>` is somebody pointing the cleaner at the shelf the release
	// is sitting on, and whether today's layout happens to put a nova-* file
	// directly there is not a property worth depending on.
	if within(retireAbs, stampAbs) || within(stampAbs, retireAbs) {
		return 0, refuse("name a DIFFERENT directory; the stamp is the last-good copy of this release",
			"--retire %s and the release stamp %s are the same tree: retiring there would delete the copy a re-install or a rollback reads", dir, stamp)
	}
	if _, err := os.Stat(retireAbs); os.IsNotExist(err) {
		// Nothing there is nothing to retire. A bench without a ~/go/bin is
		// not a bench with a problem.
		return 0, nil
	}
	retired := 0
	for _, a := range arts {
		if !strings.HasPrefix(a.Name, "nova-") {
			continue
		}
		stale := filepath.Join(retireAbs, a.Name)
		info, err := os.Lstat(stale)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return retired, fmt.Errorf("cannot read %s: %w (check the permissions on --retire)", stale, err)
		}
		if !info.Mode().IsRegular() {
			progress(errs, "leaving %s alone: it is not a regular file (%s)", stale, info.Mode())
			continue
		}
		progress(errs, "retiring %s", stale)
		if err := safepath.RemoveUnder(retireAbs, stale); err != nil {
			return retired, fmt.Errorf("cannot retire %s: %w", stale, err)
		}
		retired++
	}
	return retired, nil
}

// within reports whether a is b or sits below it.
func within(a, b string) bool {
	return a == b || strings.HasPrefix(a+string(filepath.Separator), b+string(filepath.Separator))
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
	if _, err := VerifyArtifacts(dir, arts); err != nil {
		return refusal(errs, "INSTALL", err)
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
		// a.Name is the file name the BUILD chose for the target platform --
		// ToolFile, so `nova-bus.exe` in a windows release -- read back out of
		// that release's own SHA256SUMS. Every name here therefore already
		// carries the right suffix, and nothing below rebuilds one from
		// runtime.GOOS: the file that is verified, the path the probe runs and
		// the rename target are all this one string.
		target := filepath.Join(o.bin, a.Name)
		// SKIP WHEN THE BOX ALREADY ANSWERS. The question is asked of the
		// BINARY -- by its real name, the one it was installed under -- and
		// not of a marker file: a marker says what somebody meant to install,
		// and the whole point of the version verbs is to say what is actually
		// there.
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
	retired := 0
	if o.retire != "" {
		if retired, err = retire(o.retire, o.bin, dir, arts, errs); err != nil {
			return refusal(errs, "INSTALL", err)
		}
	}
	fmt.Fprintf(out, "RELEASE INSTALLED version=%s tools=%d skipped=%d retired=%d bin=%s platform=%s\n",
		field(o.version), installed, skipped, retired, field(o.bin), field(goos+"-"+goarch))
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
