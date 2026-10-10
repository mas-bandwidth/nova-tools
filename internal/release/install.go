package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
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
		// The name is also written under --bin by install and interpolated into
		// the remote `rm -f` adopt composes, which the far shell parses after ssh
		// reassembles argv: held here to the narrowness pull already enforces
		// (remoteArtifactName), so every caller sees only safe names
		// (security#72 finding 1, artifact-name half).
		if !remoteArtifactName.MatchString(name) {
			return nil, refuse("build the release again with `nova-update release build`",
				"%s line %d names %q, which is not a file name this tool will install or delete", SumsFile, i+1, name)
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
// worse rather than better, because both directories are on
// PATH and which one wins is a fact about the PATH order nobody has read.
//
// WHAT IT WILL REMOVE IS NARROW, and every clause is load-bearing. Only a name
// this very run installed into --bin; only a name beginning `nova-`; only a
// regular file, so a directory or a symlink is left for a person; and only
// through safepath.RemoveUnder, which refuses a path that is not strictly below
// the root, refuses the root itself and refuses a link, so it cannot delete an
// arbitrary directory. --retire naming --bin is
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
	// along with the tools and leave the machine with no way back.
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
	// What the bin directory answered BEFORE this install: the versions its
	// binaries came from, which the prune below never removes, so a bad build
	// can be put back by re-installing the one it replaced.
	var before []string
	// STAGE EVERY NEW FILE BEFORE THE FIRST RENAME (security#72 finding 4).
	// The old loop renamed tool by tool, so a planted .<tool>.new failed the
	// next tool after earlier ones were already replaced. CreateTemp is
	// exclusive and unpredictable; mode 0755; every error path removes the
	// temps. Renames run only once every tool is staged. Each rename is
	// os.Rename of that temp onto the target: installFile would write the
	// bytes again to the fixed name "."+base+".new", and a directory planted
	// there would fail mid-loop after earlier tools were already replaced.
	// installFile itself is unchanged (windows aside, findings 5 and 8).
	// A rename that fails puts back the tools already replaced.
	type stagedTool struct {
		name   string
		target string
		tmp    string
	}
	var staged []stagedTool
	removeStaged := func() {
		for _, s := range staged {
			if s.tmp != "" {
				// ignored: the staged temporary is cleanup on the failure path; the error that stopped the install is the one reported
				_ = os.Remove(s.tmp)
			}
		}
	}
	for _, a := range arts {
		// a.Name is the file name the BUILD chose for the target platform --
		// ToolFile, so `nova-bus.exe` in a windows release -- read back out of
		// that release's own SHA256SUMS. Every name here therefore already
		// carries the right suffix, and nothing below rebuilds one from
		// runtime.GOOS: the file that is verified, the path the probe runs and
		// the rename target are all this one string.
		target := filepath.Join(o.bin, a.Name)
		// THE VERSION ANSWER FILLS THE BEFORE LIST and nothing else.
		// pruneInstalled keeps every release the bin directory answered
		// before this install (SPEC-RELEASE, retention). A file that prints
		// the target version can still hold other bytes, and a skip on that
		// answer would leave them in place with a success receipt
		// (security#72 finding 2).
		line, err := versionOf(ctx, target)
		if err == nil {
			before = append(before, line)
		}
		// SKIP ONLY WHEN THE BYTES ARE ALREADY THESE. An --incremental build
		// ships an unchanged tool as the earlier build's binary; renaming
		// identical bytes over it would only make the loop running it drain
		// and restart on a binary nothing changed (SPEC-RELEASE, install skip).
		if sum, err := fileSum(target); err == nil && sum == a.Sum {
			skipped++
			continue
		}
		tmp, err := stageArtifact(o.bin, a.Name, filepath.Join(dir, a.Name), a.Sum)
		if err != nil {
			removeStaged()
			fmt.Fprintf(errs, "INSTALL FAILED tool=%s bin=%s: %s (fix the permission or the disk and install again; %d of %d were in place)\n",
				field(a.Name), field(o.bin), oneLine("", err), 0, len(arts))
			return 1
		}
		staged = append(staged, stagedTool{name: a.Name, target: target, tmp: tmp})
	}
	type placedTool struct {
		name    string
		target  string
		aside   string
		existed bool
	}
	var placed []placedTool
	// Last replaced first, so a failure walks back out the way it came in.
	restorePlaced := func() ([]string, error) {
		var names []string
		for i := len(placed) - 1; i >= 0; i-- {
			p := placed[i]
			if err := putBack(p.target, p.aside, p.existed); err != nil {
				return names, fmt.Errorf("%s: %w", p.name, err)
			}
			names = append(names, p.name)
		}
		return names, nil
	}
	failReplaced := func(tool string, err error) int {
		names, rerr := restorePlaced()
		removeStaged()
		// names are the tools put back. The rest of placed are still the
		// new bytes. Saying 0 whenever restore failed was a false claim.
		left := len(placed) - len(names)
		if rerr != nil {
			err = fmt.Errorf("%w; restore failed: %v", err, rerr)
		} else if len(names) > 0 {
			err = fmt.Errorf("%w; restored %s", err, strings.Join(names, ","))
		}
		fmt.Fprintf(errs, "INSTALL FAILED tool=%s bin=%s: %s (fix the permission or the disk and install again; %d of %d left replaced)\n",
			field(tool), field(o.bin), oneLine("", err), left, len(arts))
		return 1
	}
	for i, s := range staged {
		aside, existed, err := copyAside(s.target)
		if err != nil {
			return failReplaced(s.name, err)
		}
		progress(errs, "installing %s", s.name)
		// The staged temp is already the exclusive file. Rename it onto
		// the target. Do not call installFile: that writes "."+base+".new".
		if err := os.Rename(s.tmp, s.target); err != nil {
			if backErr := putBack(s.target, aside, existed); backErr != nil {
				err = fmt.Errorf("%w; %s could not be restored: %v", err, s.name, backErr)
			}
			return failReplaced(s.name, err)
		}
		staged[i].tmp = ""
		placed = append(placed, placedTool{name: s.name, target: s.target, aside: aside, existed: existed})
		installed++
	}
	for _, p := range placed {
		if p.aside != "" {
			// ignored: the install has already succeeded; a kept-aside copy the remove leaves is hidden and inert
			_ = os.Remove(p.aside)
		}
	}
	retired := 0
	if o.retire != "" {
		if retired, err = retire(o.retire, o.bin, dir, arts, errs); err != nil {
			return refusal(errs, "INSTALL", err)
		}
	}
	// LAST, and never a reason to fail: the install has succeeded and been
	// verified, and the version directories under --from that prune.go's rule does not
	// keep are removed. The one being installed and every one the bin
	// directory answered before it stay.
	pruned, pruneFailed := pruneInstalled(o.from, o.bin, func(name string) bool {
		if name == o.version {
			return true
		}
		for _, line := range before {
			if hasToken(line, name) {
				return true
			}
		}
		return false
	}, errs)
	fmt.Fprintf(out, "RELEASE INSTALLED version=%s tools=%d skipped=%d retired=%d bin=%s platform=%s pruned=%d prune-failed=%d\n",
		field(o.version), installed, skipped, retired, field(o.bin), field(goos+"-"+goarch), pruned, pruneFailed)
	// THE SEAT'S LINKS, AFTER THE TOOLS ARE WHOLE. install is the release's own
	// step on the seat, so it is where the paths that reach its tools are
	// repointed: the record seat install wrote is read here and every link it
	// names is pointed at the release just placed (docs/SPEC-RELEASE.md, "The
	// seat's links"). A link that cannot be repointed is named and left, and
	// adopt refuses the machine on it; the tools are already installed.
	repointSeatLinks(o.bin, goos, o.version, out, errs)
	return 0
}

// The seat's links (docs/SPEC-RELEASE.md, "The seat's links"): the paths a seat
// reaches its nova binaries through, recorded by seat install and repointed by
// the install adopt runs on it. The coordinator's seat wrapper once kept running
// a nova-sprint three days older than the adopted release -- a pinned copy
// beside the installed one -- so a verb added since was refused as unknown and
// nobody was told; the record is what makes adopt own every path the seat runs.

// SeatLink is one path that reaches a nova binary: the tool's file name and the
// path that must name the installed binary for that tool.
type SeatLink struct {
	Tool string `json:"tool"`
	Path string `json:"path"`
}

// SeatLinks is the seat's record of its links: the directories to look in for a
// nova binary no link names (Paths), the links adopt repoints (Links), and the
// build the sprint's server runs (Server), which version warns on.
type SeatLinks struct {
	Paths  []string   `json:"paths,omitempty"`
	Links  []SeatLink `json:"links,omitempty"`
	Server string     `json:"server_version,omitempty"`
}

// SeatLinksFile is the record's name, beside the seat's login and seat records.
const SeatLinksFile = "seat-links.json"

// SeatLinksPath is the record's file: $XDG_CONFIG_HOME/nova-sprint/seat-links.json,
// else ~/.config/nova-sprint/seat-links.json, beside the store login and the
// seat's own record on every system (cmd/nova-sprint, defaultLoginFile). It is
// a function of the environment, so the seat install that writes it and the
// install that repoints what it records read one file.
func SeatLinksPath() (string, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "nova-sprint", SeatLinksFile), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "nova-sprint", SeatLinksFile), nil
}

// ReadSeatLinks reads the record at path. A file that is not there is a seat
// with no recorded links, not an error; a file that is not a record is one.
func ReadSeatLinks(path string) (SeatLinks, error) {
	var r SeatLinks
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return SeatLinks{}, refuse("run: nova-sprint seat install", "the seat links %s are not a seat links record: %v", path, err)
	}
	return r, nil
}

// WriteSeatLinks writes the record at path whole, for its user alone.
func WriteSeatLinks(path string, r SeatLinks) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

// repointSeatLinks is the step install takes after the tools are installed:
// repoint every link the seat's record names at this release, name every nova
// binary in the seat's paths that no link names, and record the build the
// server now runs. A seat with no record, or one that cannot be read, is a seat
// with nothing recorded and nothing repointed, said on the progress stream.
func repointSeatLinks(bin, goos, version string, out, errs io.Writer) {
	path, err := SeatLinksPath()
	if err != nil {
		progress(errs, "the seat links record has no place: %v (no link was repointed)", err)
		return
	}
	r, err := ReadSeatLinks(path)
	if err != nil {
		progress(errs, "the seat links record %s cannot be read: %v (no link was repointed)", path, err)
		return
	}
	if len(r.Links) == 0 && len(r.Paths) == 0 {
		return
	}
	repointed, refused := RepointSeatLinks(r, bin, goos)
	stale := StaleSeatBinaries(r, bin, goos)
	r.Server = version
	if err := WriteSeatLinks(path, r); err != nil {
		progress(errs, "the seat links record %s was not written: %v (the links were repointed, the record is as it was)", path, err)
	}
	fmt.Fprintf(out, "RELEASE SEAT LINKS bin=%s repointed=%d refused=%d stale=%d\n",
		field(bin), len(repointed), len(refused), len(stale))
	for _, why := range refused {
		fmt.Fprintf(out, "RELEASE SEAT REFUSED link=%s\n", oneLine("", errors.New(why)))
	}
	for _, p := range stale {
		fmt.Fprintf(out, "RELEASE SEAT STALE path=%s\n", field(p))
	}
}

// RepointSeatLinks repoints every recorded link at bin's binary for its tool and
// returns the paths it repointed and one line for each it cannot, naming the
// link and why. A link whose tool the release does not carry is refused rather
// than guessed at; a path already naming the installed binary is left alone.
func RepointSeatLinks(r SeatLinks, bin, goos string) (repointed, refused []string) {
	for _, l := range r.Links {
		target := filepath.Join(bin, ToolFile(l.Tool, goos))
		if _, err := os.Lstat(target); err != nil {
			refused = append(refused, l.Path+": the release carries no "+l.Tool)
			continue
		}
		if filepath.Clean(l.Path) == filepath.Clean(target) {
			continue
		}
		info, err := os.Lstat(l.Path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			// nothing is there yet: the link is written below
		case err != nil:
			refused = append(refused, l.Path+": "+err.Error())
			continue
		case info.IsDir():
			refused = append(refused, l.Path+": it is a directory, not a link")
			continue
		}
		if err := relinkSeatPath(l.Path, target); err != nil {
			refused = append(refused, l.Path+": "+err.Error())
			continue
		}
		repointed = append(repointed, l.Path)
	}
	return repointed, refused
}

// relinkSeatPath points path at target whole: the link is made beside the path
// under a name the filesystem chose and renamed onto it, so a failure leaves
// the old link or file exactly as it was.
func relinkSeatPath(path, target string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".link.*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	// ignored: the temp is closed only to free its name for the link; a close on an empty temp acts on nothing
	_ = tmp.Close()
	// ignored: the temp is removed only to free its name; the symlink below reports a remove that failed
	_ = os.Remove(name)
	if err := os.Symlink(target, name); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		// ignored: the temp link is cleanup on the error path; the rename's error is the one returned
		_ = os.Remove(name)
		return err
	}
	return nil
}

// StaleSeatBinaries names every nova-* file under the record's paths that no
// recorded link names: the pinned copy beside the installed binary, the one the
// wrapper kept running. The installed directory bin is skipped, because install
// owns every name there and replaces each one it carries.
func StaleSeatBinaries(r SeatLinks, bin, goos string) []string {
	known := map[string]bool{}
	for _, l := range r.Links {
		known[filepath.Clean(l.Path)] = true
	}
	var stale []string
	for _, dir := range r.Paths {
		if filepath.Clean(dir) == filepath.Clean(bin) {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), "nova-") {
				continue
			}
			p := filepath.Join(dir, e.Name())
			if known[filepath.Clean(p)] {
				continue
			}
			stale = append(stale, p)
		}
	}
	sort.Strings(stale)
	return stale
}

// pruneInstalled also protects the installed binaries' directory, including a
// bin symlink's target: a version stamp alone does not protect an empty bin
// installed into an older release directory (SPEC-RELEASE, retention).
func pruneInstalled(root, bin string, keep func(string) bool, errs io.Writer) (int, int) {
	binAbs, err := filepath.Abs(bin)
	if err != nil {
		progress(errs, "cannot resolve installed bin %s for pruning: %v (nothing removed)", bin, err)
		return 0, 1
	}
	binReal, err := filepath.EvalSymlinks(binAbs)
	if err != nil {
		progress(errs, "cannot resolve installed bin %s for pruning: %v (nothing removed)", bin, err)
		return 0, 1
	}
	// Preserve both the route to a bin symlink and its actual target. Identity
	// handles case and normalization aliases without guessing volume policy.
	var ancestors []os.FileInfo
	for _, path := range []string{binAbs, binReal} {
		for {
			info, err := os.Stat(path)
			if err != nil {
				progress(errs, "cannot identify installed bin ancestor %s: %v (nothing removed)", path, err)
				return 0, 1
			}
			ancestors = append(ancestors, info)
			parent := filepath.Dir(path)
			if parent == path {
				break
			}
			path = parent
		}
	}
	return pruneDefault(root, func(name string) bool {
		if keep(name) {
			return true
		}
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil {
			progress(errs, "leaving release %s alone: %v", name, err)
			return true
		}
		for _, ancestor := range ancestors {
			if os.SameFile(info, ancestor) {
				return true
			}
		}
		return false
	}, errs)
}

// hasToken is the same whole-token match tools/ghrelease's stamp verb
// makes, and for the same reason: v0.1 must not pass for v0.11, and `=` is a
// separator so that no tag is matched out of the value half of a version line's
// `key=value` extra -- the stamp is field two, alone, in every binary.
func hasToken(line, version string) bool {
	return slices.Contains(strings.Fields(strings.ReplaceAll(line, "=", " ")), version)
}

// stageArtifact copies src into an exclusive temp in bin. Publish renames
// this file onto the target. The name is not the predictable .<tool>.new
// installFile still uses: a planted directory there must not be opened.
//
// THE BYTES STAGED ARE THE BYTES VERIFIED (security#72 finding 8).
// VerifyArtifacts hashed the file earlier; a writer to the artifact directory
// between that pass and this read could substitute other bytes. The source is
// Lstat-ed and a non-regular file (a symlink) refused, and the buffer actually
// read is hashed against sum, the recorded one, before any temp is written.
func stageArtifact(bin, name, src, sum string) (string, error) {
	info, err := os.Lstat(src)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", src)
	}
	body, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	got := sha256.Sum256(body)
	if hex.EncodeToString(got[:]) != sum {
		return "", refuse("build the release again; do not install an artifact whose bytes changed after it was verified",
			"%s does not match %s at install: recorded %s, read %s", name, SumsFile, sum, hex.EncodeToString(got[:]))
	}
	return writeTemp(bin, "."+name+".new.*", body, 0o755)
}

// copyAside keeps target's bytes so a later rename failure can put them back.
// A missing target is nothing to keep. A non-regular file is refused before
// any rename or aside (security#72 finding 5).
func copyAside(target string) (string, bool, error) {
	info, err := os.Lstat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	if !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("%s is not a regular file", target)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		return "", false, err
	}
	path, err := writeTemp(filepath.Dir(target), "."+filepath.Base(target)+".aside.*", body, info.Mode().Perm())
	if err != nil {
		return "", false, err
	}
	return path, true, nil
}

// writeTemp creates an exclusive file, writes body, and sets mode. Any error
// removes the file.
func writeTemp(dir, pattern string, body []byte, mode os.FileMode) (string, error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	path := f.Name()
	_, werr := f.Write(body)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		// ignored: the temporary is removed on the error path; the write's or the close's error is the one returned
		_ = os.Remove(path)
		if werr != nil {
			return "", werr
		}
		return "", cerr
	}
	if err := os.Chmod(path, mode); err != nil {
		// ignored: the temporary is removed on the error path; the chmod error is the one returned
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

// putBack undoes one replacement. existed false means the name was absent and
// must be absent again; otherwise the aside's bytes go back under target.
func putBack(target, aside string, existed bool) error {
	if !existed {
		err := os.Remove(target)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return os.Rename(aside, target)
}

// ExecVersion is the production answer to what the binary at path reports: its
// own `version` verb, which every tool in this repository answers, under
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
