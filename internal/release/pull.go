package release

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// PulledPrefix is how a changelog section says the release was withdrawn. It is
// a prefix rather than a whole sentence because the note carries a date and a
// reason, and it is one constant because `pull` writes it and `pull` reads it
// back: a second run must not stack a second note.
const PulledPrefix = "**PULLED "

// PullNote is what a person needs before their first pull, in the help, because
// every sentence of it is a decision somebody would otherwise have to guess at.
const PullNote = "pull withdraws a release that should not have shipped (a leak, a key, a file that was never meant to travel). " +
	"THE TAG STAYS: a tag that vanishes is a history that cannot be read, so the CHANGELOG section is marked " + PulledPrefix + "<date>** instead, carrying --reason. " +
	"What is deleted is the ARTIFACTS: the release's own files under --out here, and the same files under --dest on every machine in --machines, by name, never recursively. " +
	"The names come from that release's own " + SumsFile + " under --out, so --out must still hold the release being pulled. " +
	"It does not touch an INSTALLED binary: a machine keeps running what it is running until the next release is adopted over it."

// remoteArtifactName is what may be named in a remote `rm`. The names come from
// the release's own checksum file, which ReadSums has already refused a
// separator in, and this is the second gate: the value is interpolated into a
// command the far side's shell parses, so it is held to the same narrowness as
// a remote path (Johnny's read of adopt, 2026-09-18, applied to the verb that
// deletes rather than the one that installs).
var remoteArtifactName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.+-]*$`)

// PulledNote is the line a pulled section carries, composed in one place so the
// mark `pull` writes and the mark it recognises on a second run are the same
// string.
func PulledNote(when time.Time, reason string) string {
	if strings.TrimSpace(reason) == "" {
		reason = "no reason was given"
	}
	return fmt.Sprintf("%s%s** this release was withdrawn: %s. The tag stays, so the history still reads; the artifacts were deleted here and on every machine that held them.",
		PulledPrefix, when.UTC().Format("2006-01-02"), strings.TrimRight(strings.TrimSpace(reason), "."))
}

// MarkPulled writes note under version's section and returns the new text. It
// is pure, so what a withdrawal does to the record is asserted by a test rather
// than by reading a file somebody edited afterwards.
//
// A section that already carries a note is returned UNCHANGED: a pull run twice
// -- which is what happens when the first run refused on one machine -- must not
// stack two notes. A version the changelog does not describe is a refusal: that
// is somebody pointing --changelog at the wrong file, and writing the note
// anywhere else would be worse than not writing it.
func MarkPulled(text, version, note string) (string, error) {
	lines := strings.Split(text, "\n")
	heading := -1
	for i, line := range lines {
		// `## v0.16.0 — 2026-09-18`, and the space after the version is what
		// keeps v0.16.0 off v0.16.0-rc1's section.
		if strings.HasPrefix(line, "## "+version+" ") || strings.TrimRight(line, " \t") == "## "+version {
			heading = i
			break
		}
	}
	if heading < 0 {
		return "", refuse("name the changelog that carries this release's section, or add the section first",
			"the changelog has no `## %s` section to mark as pulled", version)
	}
	for _, line := range lines[heading+1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, PulledPrefix) {
			return text, nil // already pulled; nothing to say twice
		}
		break
	}
	after := heading + 1
	if after < len(lines) && strings.TrimSpace(lines[after]) == "" {
		after++
	}
	out := append([]string{}, lines[:after]...)
	out = append(out, note, "")
	out = append(out, lines[after:]...)
	return strings.Join(out, "\n"), nil
}

// pullNames is what a pull deletes: the release's own files, its checksum file
// and that file's digest, and nothing else in the directory. The names come
// from SHA256SUMS for the same reason `--retire` takes its names from the set
// it installed -- a directory listing would delete whatever somebody had left
// there.
//
// DigestFile is named here for a reason worth keeping: it is the one file
// `build` writes that SHA256SUMS does not list, so a pull that took only the
// listed names would leave it behind, and the `rmdir` that follows -- which
// refuses a directory that is not empty, deliberately -- would fail on every
// machine for a file this tool wrote itself.
func pullNames(arts []Artifact) ([]string, error) {
	names := make([]string, 0, len(arts)+2)
	for _, a := range arts {
		if !remoteArtifactName.MatchString(a.Name) {
			return nil, refuse("build the release again with `nova-update release build`",
				"%s names %q, which is not a file name this verb will delete", SumsFile, a.Name)
		}
		names = append(names, a.Name)
	}
	return append(names, SumsFile, DigestFile), nil
}

// pullHere deletes the release's own files from one local artifact directory,
// through safepath.RemoveUnder -- which refuses a path that is not strictly
// below the root, refuses the root itself and refuses a link. Only regular
// files, like `retire`: a directory or a symlink under that name is somebody's
// and is left for a person.
func pullHere(dir string, names []string, errs io.Writer) (int, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return 0, fmt.Errorf("cannot resolve %s: %w", dir, err)
	}
	deleted := 0
	for _, name := range names {
		target := filepath.Join(root, name)
		info, err := os.Lstat(target)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return deleted, fmt.Errorf("cannot read %s: %w (check the permissions on --out)", target, err)
		}
		if !info.Mode().IsRegular() {
			progress(errs, "leaving %s alone: it is not a regular file (%s)", target, info.Mode())
			continue
		}
		progress(errs, "deleting %s", target)
		if err := safepath.RemoveUnder(root, target); err != nil {
			return deleted, fmt.Errorf("cannot delete %s: %w", target, err)
		}
		deleted++
	}
	// The now-empty directory goes too, so nothing reads this root as holding
	// the release any more. It fails when something else is still in there,
	// which is not this verb's business and not a failure.
	if err := os.Remove(root); err != nil && !os.IsNotExist(err) {
		progress(errs, "leaving %s in place: %s", root, oneErr(err))
	}
	return deleted, nil
}

func oneErr(err error) string { return strings.Join(strings.Fields(err.Error()), " ") }

func pull(ctx context.Context, o options, deps Deps, out, errs io.Writer) int {
	goos, goarch, err := Platform(o.platform)
	if err != nil {
		return refusal(errs, "PULL", err)
	}
	// THE NAMES COME FIRST, from the release's own checksum file, and nothing
	// is deleted anywhere until they are known. A pull that worked out what to
	// delete from a directory listing would delete whatever was sitting there;
	// a pull that ran `rm -rf` on a path composed from a flag would be one typo
	// away from the fleet.
	local := ArtifactDir(o.out, o.version, goos, goarch)
	arts, err := ReadSums(local)
	if err != nil {
		if os.IsNotExist(err) {
			return refusal(errs, "PULL", refuse(
				fmt.Sprintf("name the --out root that holds %s; a pull deletes the release's own files by name", o.version),
				"cannot read %s: there is no %s for %s at %s, so this verb cannot know what the release's files are called",
				filepath.Join(local, SumsFile), o.version, goos+"-"+goarch, local))
		}
		return refusal(errs, "PULL", err)
	}
	names, err := pullNames(arts)
	if err != nil {
		return refusal(errs, "PULL", err)
	}
	// EVERY PATH IS VALIDATED BEFORE ANY REMOTE COMMAND IS COMPOSED, the same
	// rule adopt has: the names came from a file and the paths from flags, and
	// a check made while the string is being built is a check that has lost.
	var machines []Machine
	if o.machines != "" {
		if o.ssh == "" || o.dest == "" {
			return refusal(errs, "PULL", refuse("pass --ssh <path> and --dest <dir>, or drop --machines to pull only from --out",
				"--machines names a fleet, so this verb needs the ssh binary and the artifact root on each machine"))
		}
		f, err := os.Open(o.machines)
		if err != nil {
			return refusal(errs, "PULL", fmt.Errorf("cannot open %s: %w (name a readable --machines file, one machine per line)", o.machines, err))
		}
		machines, err = Machines(f)
		f.Close()
		if err != nil {
			return refusal(errs, "PULL", err)
		}
		if err := ValidRemotePath("--dest", o.dest); err != nil {
			return refusal(errs, "PULL", err)
		}
		for _, m := range machines {
			if m.Dest == "" {
				continue
			}
			if err := ValidRemotePath("the dest column for "+m.Name, m.Dest); err != nil {
				return refusal(errs, "PULL", err)
			}
		}
	}
	ssh := deps.SSH
	if ssh == nil {
		ssh = ExecSSH{Path: o.ssh}
	}
	pulled, refused := 0, 0
	for _, entry := range machines {
		machine := entry.Name
		dest := o.dest
		if entry.Dest != "" {
			dest = entry.Dest
		}
		remoteDir := path.Join(dest, o.version, goos+"-"+goarch)
		// THE MACHINE IS ASKED WHETHER IT HOLDS IT before anything is deleted,
		// so a machine that never took this release is a receipt rather than a
		// refusal -- and so that `rm` is not run at all where there is nothing
		// to remove.
		held := "yes"
		if _, err := ssh.Run(ctx, machine, []string{"cat", path.Join(remoteDir, SumsFile)}); err != nil {
			held = "no"
		}
		if o.dryRun {
			pulled++
			fmt.Fprintf(out, "RELEASE WOULD PULL machine=%s version=%s held=%s files=%d dest=%s\n",
				field(machine), field(o.version), field(held), len(names), field(remoteDir))
			continue
		}
		if held == "no" {
			pulled++
			progress(errs, "%s does not hold %s; deleting nothing there", machine, o.version)
			fmt.Fprintf(out, "RELEASE PULLED machine=%s version=%s held=no files=0 dest=%s\n",
				field(machine), field(o.version), field(remoteDir))
			continue
		}
		// BY NAME, NEVER RECURSIVELY. Each argument is one file this release
		// shipped, under a directory whose every segment was validated above.
		argv := []string{"rm", "-f"}
		for _, name := range names {
			argv = append(argv, path.Join(remoteDir, name))
		}
		progress(errs, "deleting %s from %s:%s", o.version, machine, remoteDir)
		if output, err := ssh.Run(ctx, machine, argv); err != nil {
			refused++
			fmt.Fprintf(errs, "RELEASE REFUSED machine=%s version=%s: %s (check `ssh %s` reaches it and that %s is writable there; nothing else was touched)\n",
				field(machine), field(o.version), oneLine(output, err), machine, remoteDir)
			continue
		}
		// The directory itself, once it is empty. `rmdir` refuses a directory
		// that is not, which is exactly the check wanted: anything else in
		// there is somebody's.
		if _, err := ssh.Run(ctx, machine, []string{"rmdir", remoteDir}); err != nil {
			progress(errs, "leaving %s:%s in place: something else is in it", machine, remoteDir)
		}
		pulled++
		fmt.Fprintf(out, "RELEASE PULLED machine=%s version=%s held=yes files=%d dest=%s\n",
			field(machine), field(o.version), len(names), field(remoteDir))
	}
	if o.dryRun {
		fmt.Fprintf(out, "RELEASE WOULD PULL LOCAL version=%s files=%d out=%s changelog=%s\n",
			field(o.version), len(names), field(local), field(o.changelog))
		fmt.Fprintf(out, "RELEASE PULL OK version=%s machines=%d pulled=%d refused=%d local=0 dry-run=yes\n",
			field(o.version), len(machines), pulled, refused)
		return 0
	}
	deleted, err := pullHere(local, names, errs)
	if err != nil {
		return refusal(errs, "PULL", err)
	}
	fmt.Fprintf(out, "RELEASE PULLED LOCAL version=%s files=%d out=%s\n", field(o.version), deleted, field(local))
	// THE CHANGELOG LAST, and the tag is never touched. The artifacts are the
	// urgent half of a withdrawal; the record is the lasting half, and if the
	// record cannot be written the receipt says so rather than leaving somebody
	// to wonder which half happened.
	body, err := os.ReadFile(o.changelog)
	if err != nil {
		fmt.Fprintf(errs, "PULL FAIL version=%s changelog=%s: cannot read it: %s (the artifacts are deleted; mark the section by hand)\n",
			field(o.version), field(o.changelog), oneErr(err))
		return 1
	}
	marked, err := MarkPulled(string(body), o.version, PulledNote(deps.Now(), o.reason))
	if err != nil {
		fmt.Fprintf(errs, "PULL FAIL version=%s changelog=%s: %s (the artifacts are deleted; mark the section by hand)\n",
			field(o.version), field(o.changelog), oneErr(err))
		return 1
	}
	if err := os.WriteFile(o.changelog, []byte(marked), 0o644); err != nil {
		fmt.Fprintf(errs, "PULL FAIL version=%s changelog=%s: cannot write it: %s (the artifacts are deleted; mark the section by hand)\n",
			field(o.version), field(o.changelog), oneErr(err))
		return 1
	}
	w, result, code := out, "OK", 0
	if refused > 0 {
		w, result, code = errs, "FAIL", 1
	}
	fmt.Fprintf(w, "RELEASE PULL %s version=%s machines=%d pulled=%d refused=%d local=%d dry-run=no\n",
		result, field(o.version), len(machines), pulled, refused, deleted)
	return code
}
