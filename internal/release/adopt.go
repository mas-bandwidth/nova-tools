package release

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// machineName is what may be handed to ssh as a destination. It is deliberately
// narrower than what ssh accepts: the thing this verb replaces built its remote
// commands by pasting a bench name into a shell line, and a name that cannot
// carry a space, a quote, a semicolon or a `$` cannot be the half of that which
// went wrong.
var machineName = regexp.MustCompile(`^[A-Za-z0-9_.@-]+$`)

// installedLine reads a remote install's receipt back out of its output. The
// receipt is read from what the remote SAID, never from its exit code: a shell
// that could not find the binary exits non-zero for the same reason a disk that
// filled up does, and only one of those is worth the same remedy.
// retired= is optional in the match so that a receipt from an install that
// predates it still parses; every install this verb runs is the one it just
// sent, but a parser that needs a field to exist is a parser that turns one
// added field into a fleet-wide refusal.
var installedLine = regexp.MustCompile(`RELEASE INSTALLED version=(\S+) tools=(\d+) skipped=(\d+)(?: retired=(\d+))?`)

// Machines reads the machine list. MachinesShape is that format said once: one
// machine per line, optionally followed by TAB-separated --bin and --dest
// overrides for that machine, blanks and `#` comments skipped, every name
// checked before ssh is reached. The file is a flag because the fleet is not a
// constant -- it was four benches, then five, and the day the iMac Pro joined
// nothing in a tool should have needed editing.
func Machines(r io.Reader) ([]Machine, error) {
	var machines []Machine
	s := bufio.NewScanner(r)
	for line := 1; s.Scan(); line++ {
		// Only the line ENDING is trimmed here, never the tabs: trimming the
		// whole line first would turn "vision<TAB>" -- a column somebody meant
		// to fill -- into a plain one-field line, and the refusal below would
		// never fire. Each FIELD is trimmed after the split instead.
		text := strings.TrimRight(s.Text(), "\r\n")
		if trimmed := strings.TrimSpace(text); trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// TAB-separated, like every other hand-written table in this estate
		// (SPEC-UPDATE rule 2's manifest), so a path may carry a space.
		fields := strings.Split(text, "\t")
		if len(fields) > 3 {
			return nil, refuse("a line is <name>, optionally TAB <bin>, optionally TAB <dest>",
				"line %d has %d tab-separated fields: %s", line, len(fields), MachinesShape)
		}
		m := Machine{Name: strings.TrimSpace(fields[0])}
		if !machineName.MatchString(m.Name) {
			return nil, refuse("one machine name per line, letters, digits, dot, dash, underscore or @",
				"line %d is not a machine name: %q", line, m.Name)
		}
		// An empty override column is a column somebody meant to fill, and
		// falling back to the flag would be falling back to exactly the thing
		// they were overriding -- silently, onto a path on their machine.
		for i, into := range []*string{nil, &m.Bin, &m.Dest} {
			if i == 0 || len(fields) <= i {
				continue
			}
			if *into = strings.TrimSpace(fields[i]); *into == "" {
				return nil, refuse("fill the column or remove it",
					"line %d leaves %s's column %d empty", line, m.Name, i+1)
			}
		}
		machines = append(machines, m)
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	if len(machines) == 0 {
		return nil, refuse("put one machine name per line in the file",
			"the machine list names no machine")
	}
	return machines, nil
}

// remotePathShape is what may be interpolated into a command the far side's
// shell will see. It is narrow on purpose (Johnny's security read, 2026-09-18):
// `adopt` composes `mkdir -p <dest> && tar -C <dest> -xf -`, which the remote
// shell parses, so a path carrying `;`, `&`, `|`, `$`, a backtick, a quote, a
// redirect, a glob or a newline would not be a path, it would be a command. A
// path is checked BEFORE any remote command is composed, never after.
var remotePathShape = regexp.MustCompile(`^(/|~/)[A-Za-z0-9_.@/+-]*$`)

// sha256Hex is the shape of a digest this host was handed out of band.
var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ValidRemotePath refuses anything that could be more than a path on the far
// side. It also refuses a RELATIVE path, because the binary this verb runs
// there must be named absolutely: a relative path resolves against whatever
// directory the remote shell happens to start in, and a bare name would resolve
// against $PATH -- which is how a machine ends up running a nova-update that is
// not the one just verified and sent.
func ValidRemotePath(what, p string) error {
	remedy := "pass an absolute path, or one rooted at ~/, with no shell metacharacters"
	if strings.TrimSpace(p) == "" {
		return refuse(remedy, "%s is empty", what)
	}
	if !strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "~/") {
		return refuse(remedy, "%s %q is not absolute; the far side would resolve it against a directory or a $PATH nobody here chose", what, p)
	}
	if !remotePathShape.MatchString(p) {
		return refuse(remedy, "%s %q carries a character the remote shell would read as syntax rather than as a path", what, p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return refuse(remedy, "%s %q climbs out of itself with ..", what, p)
		}
	}
	return nil
}

// RemoteFrom splits a --from that names another machine, as `host:dir`.
//
// THIS IS THE ANSWER TO THE ONE THING THE DOGFOOD PASS COULD NOT DO. adopt was
// written assuming it runs on the build host and fans out from there; on this
// fleet it cannot, because no bench has ssh trust to any other bench -- only the
// Studio does, and 3 of 3 machines refused with `Permission denied (publickey)`
// (receipt 20260918T144929Z, rowan-child). The fix that needs NO NEW TRUST is
// to run adopt from the host that already has it and let it read the artifacts
// from the host that built them. A jump host (`ssh -J`) would not have helped:
// -J forwards the connection but still authenticates to the target with the
// CALLING host's key, so fanning out from hulk would still need hulk's key on
// every bench -- new trust between benches, which is the thing we do not want,
// and the Studio is Glenn's and not ours to hand out keys for.
//
// The host part must be a machine name of at least two characters, so a windows
// path (`C:\releases`) reads as a local path rather than as a host called C.
// That is the one ambiguity a colon introduces, resolved in favour of the local
// path because it is the common case and the mistake is loud either way.
func RemoteFrom(value string) (host, dir string, remote bool) {
	before, after, found := strings.Cut(value, ":")
	if !found || len(before) < 2 || after == "" || !machineName.MatchString(before) {
		return "", value, false
	}
	return before, after, true
}

// VersionsUnder lists the releases an artifact root holds for one platform: a
// directory whose <version>/<goos>-<goarch>/SHA256SUMS exists. A directory that
// is not a release is not a candidate, so a stray folder never becomes one.
func VersionsUnder(root, goos, goarch string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var found []string
	for _, e := range entries {
		if !e.IsDir() || ValidVersion(e.Name()) != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(ArtifactDir(root, e.Name(), goos, goarch), SumsFile)); err == nil {
			found = append(found, e.Name())
		}
	}
	sort.Strings(found)
	return found
}

// inferVersion reads the version off the artifact root when there is exactly
// one. A root usually holds one release, and making somebody retype what the
// directory already says is making them repeat themselves -- and a mistyped
// version is how a fleet ends up half adopted. Two releases is the case where a
// guess would be wrong, so it refuses and NAMES BOTH: the remedy is to pick.
func inferVersion(root, goos, goarch string, errs io.Writer) (string, error) {
	found := VersionsUnder(root, goos, goarch)
	switch len(found) {
	case 1:
		progress(errs, "%s holds one release for %s-%s: %s", root, goos, goarch, found[0])
		return found[0], nil
	case 0:
		return "", refuse(fmt.Sprintf("build one first, or pass --version; nothing under %s is a release for %s-%s", root, goos, goarch),
			"no release to adopt: %s holds none for %s-%s", root, goos, goarch)
	default:
		return "", refuse("pass --version to say which",
			"%s holds %d releases for %s-%s (%s); refusing to guess", root, len(found), goos, goarch, strings.Join(found, ", "))
	}
}

// olderThan says whether the version this binary is stamped with is BEHIND the
// release it has been asked to fan out. It answers false for anything it cannot
// read -- an unstamped dev binary, a string that is not a version -- because a
// gate that refuses on a value it does not understand is a gate that stops the
// work it exists to protect.
func olderThan(self, release string) bool {
	if ValidVersion(self) != nil || ValidVersion(release) != nil {
		return false
	}
	return lessVersion(versionParts(self), versionParts(release))
}

func adopt(ctx context.Context, o options, deps Deps, out, errs io.Writer) int {
	goos, goarch, err := Platform(o.platform)
	if err != nil {
		return refusal(errs, "ADOPT", err)
	}
	// A DIGEST FILE ON THE FAR SIDE IS NOT A DIGEST. --expect-sums-from names
	// the DigestFile that THIS host's `release build` wrote; a path with a
	// machine in front of it would be the machine holding the bits vouching
	// for them, which is the exact circle Johnny's decision 2 exists to break.
	// Checked before anything is opened, fetched or composed.
	if o.expectSumsFrom != "" {
		if host, _, remote := RemoteFrom(o.expectSumsFrom); remote {
			return refusal(errs, "ADOPT", refuse(
				"name the "+DigestFile+" that THIS host's `release build` wrote, or pass --expect-sums <sha256>",
				"--expect-sums-from %q names the machine %s, and a digest computed where the bits live is that machine vouching for itself", o.expectSumsFrom, host))
		}
	}
	f, err := os.Open(o.machines)
	if err != nil {
		return refusal(errs, "ADOPT", fmt.Errorf("cannot open %s: %w (name a readable --machines file, one machine per line)", o.machines, err))
	}
	machines, err := Machines(f)
	f.Close()
	if err != nil {
		return refusal(errs, "ADOPT", err)
	}
	// The version may come from the artifact root. Only a LOCAL --from can be
	// read this way: a host:dir root lives on another machine, and asking it
	// what it holds would be one more remote command run before anything has
	// been verified.
	if o.version == "" {
		if _, _, remote := RemoteFrom(o.from); remote {
			return refusal(errs, "ADOPT", refuse("pass --version; a release on another machine is not scanned from here",
				"--from names a machine, so the version cannot be read off the directory"))
		}
		v, err := inferVersion(o.from, goos, goarch, errs)
		if err != nil {
			return refusal(errs, "ADOPT", err)
		}
		o.version = v
	}
	// INSTALL ON THE COORDINATOR FIRST, THEN ADOPT. `adopt` is not a courier:
	// it is this host's nova-update reading a release, verifying it, and
	// running THIS RELEASE'S install on every machine. A coordinator behind
	// the release it is fanning out is a coordinator whose own verb may not
	// understand what it is holding -- and the fourth dogfood met exactly that
	// as a Studio that could not adopt the fleet at all, because the `--from`
	// it needed ships inside the release it had not installed.
	if deps.Self != nil {
		if self := deps.Self(); olderThan(self, o.version) {
			return refusal(errs, "ADOPT", refuse(
				fmt.Sprintf("install it here first: nova-update release install --from %s --version %s --bin <dir>, then adopt with the new binary", o.from, o.version),
				"this nova-update is %s and the release being adopted is %s: the install every machine runs is the one this host is holding", self, o.version))
		}
	}
	// EVERY HOST AND PATH IS VALIDATED BEFORE ANY REMOTE COMMAND IS COMPOSED,
	// not while it is being composed (Johnny, 2026-09-18). The names came from
	// a file and the paths from flags; both are interpolated into a line the
	// far side's shell parses, and a check that happens after the string is
	// built is a check that has already lost.
	for _, p := range []struct{ what, v string }{{"--bin", o.bin}, {"--dest", o.dest}, {"--retire", o.retire}} {
		if p.v == "" {
			continue
		}
		if err := ValidRemotePath(p.what, p.v); err != nil {
			return refusal(errs, "ADOPT", err)
		}
	}
	for _, m := range machines {
		for _, p := range []struct{ what, v string }{{"the bin column", m.Bin}, {"the dest column", m.Dest}} {
			if p.v == "" {
				continue
			}
			if err := ValidRemotePath(p.what+" for "+m.Name, p.v); err != nil {
				return refusal(errs, "ADOPT", err)
			}
		}
	}
	ssh := deps.SSH
	if ssh == nil {
		ssh = ExecSSH{Path: o.ssh}
	}
	// --from may name another machine. The artifacts are pulled ONCE into
	// --stage and pushed from there, rather than streamed host-to-machine per
	// machine: one fetch, one verification of what was fetched, and then every
	// machine gets bytes this host has already checked.
	fromHost, fromDir, remoteFrom := RemoteFrom(o.from)
	localRoot := o.from
	if remoteFrom {
		if o.stage == "" {
			return refusal(errs, "ADOPT", refuse("pass --stage <dir> to say where the fetched release lands",
				"--from names the machine %s, so the release has to be fetched somewhere first", fromHost))
		}
		// THE DIGEST IS NOT ALLOWED TO TRAVEL WITH THE BITS. SHA256SUMS
		// arriving from the build host proves only that the bits agree with a
		// file that came from the same place; anybody who could change one
		// could change the other. So a remote --from is checked against a
		// digest this host got some OTHER way -- the tag object the cut
		// annotated, or the CHANGELOG entry it wrote, both of which reached
		// here through git rather than through the machine being read
		// (Johnny, 2026-09-18).
		//
		// --repo is the way that needs no transcription (decision 2, #1337): a
		// tag object is a git object, and its `sums=` line is the digest the
		// release was cut with. --expect-sums stays for a release cut before
		// the tags were annotated, and WINS when both are given -- a digest a
		// person typed deliberately is a decision, not a default.
		expectSums, sumsFrom := o.expectSums, "--expect-sums"
		// A DEV BUILD HAS NO TAG, so it has no annotation to read and no
		// CHANGELOG entry to copy from: the digest of the only release that
		// exists is the one the build wrote beside the artifacts, HERE. That
		// file is read locally and never hashed remotely.
		if expectSums == "" && o.expectSumsFrom != "" {
			digest, err := ReadDigestFile(o.expectSumsFrom)
			if err != nil {
				return refusal(errs, "ADOPT", err)
			}
			expectSums, sumsFrom = digest, o.expectSumsFrom
			progress(errs, "%s says this release's %s hashes to %s", o.expectSumsFrom, SumsFile, expectSums)
		}
		if expectSums == "" && o.repo != "" {
			forge := deps.Forge
			if forge == nil {
				forge = NewGH(o.timeout)
			}
			progress(errs, "reading the %s tag object on %s for the digest it was cut with", o.version, o.repo)
			message, err := forge.TagMessage(ctx, o.repo, o.version)
			if err != nil {
				return refusal(errs, "ADOPT", fmt.Errorf("cannot read the %s tag of %s: %w", o.version, o.repo, err))
			}
			if expectSums = SumsInAnnotation(message); expectSums == "" {
				return refusal(errs, "ADOPT", refuse(
					"pass --expect-sums <sha256 of SHA256SUMS> from that release's CHANGELOG entry",
					"the %s tag of %s carries no %s<digest> line, so that release was cut without one", o.version, o.repo, AnnotationSumsPrefix))
			}
			sumsFrom = "the " + o.version + " tag object of " + o.repo
			progress(errs, "the %s tag says this release was cut with %s", o.version, expectSums)
		}
		if expectSums == "" {
			return refusal(errs, "ADOPT", refuse(
				"pass --repo <owner/name> to read the digest off the tag the cut annotated, --expect-sums-from <"+DigestFile+"> to read it out of this host's own build, or --expect-sums <sha256 of SHA256SUMS> to name it outright",
				"--from names the machine %s, and a release fetched from a machine cannot be verified by the checksum file that came with it", fromHost))
		}
		if err := ValidRemotePath("--from's directory", fromDir); err != nil {
			return refusal(errs, "ADOPT", err)
		}
		if !sha256Hex.MatchString(expectSums) {
			return refusal(errs, "ADOPT", refuse("pass the 64 hex characters of `sha256sum SHA256SUMS`",
				"the digest from %s, %q, is not a sha256", sumsFrom, expectSums))
		}
		localRoot = o.stage
		into := ArtifactDir(o.stage, o.version, goos, goarch)
		if err := os.MkdirAll(into, 0o755); err != nil {
			return refusal(errs, "ADOPT", fmt.Errorf("cannot create %s: %w (name a writable --stage)", into, err))
		}
		remoteArtifacts := path.Join(fromDir, o.version, goos+"-"+goarch)
		progress(errs, "fetching %s from %s:%s", o.version, fromHost, remoteArtifacts)
		if output, err := ssh.Fetch(ctx, fromHost, remoteArtifacts, into); err != nil {
			return refusal(errs, "ADOPT", fmt.Errorf("cannot fetch %s from %s: %s (check `ssh %s` reaches it and that %s holds this release)", remoteArtifacts, fromHost, oneLine(output, err), fromHost, fromDir))
		}
		// Checked BEFORE the checksum file is so much as read, so nothing
		// this host does downstream is steered by a file it has not vouched
		// for. Both digests are named: which one is wrong is the whole
		// question, and a refusal that shows one of them cannot answer it.
		got, err := fileSum(filepath.Join(into, SumsFile))
		if err != nil {
			return refusal(errs, "ADOPT", fmt.Errorf("cannot read the fetched %s: %w (the fetch did not bring a checksum file)", SumsFile, err))
		}
		if got != expectSums {
			return refusal(errs, "ADOPT", refuse(
				"do not adopt this release; the bits on that machine are not the bits that were cut",
				"the %s fetched from %s has digest %s, but %s says the release %s was cut with digest %s", SumsFile, fromHost, got, sumsFrom, o.version, expectSums))
		}
		progress(errs, "the fetched %s matches the digest %s was cut with", SumsFile, o.version)
	}
	local := ArtifactDir(localRoot, o.version, goos, goarch)
	arts, err := ReadSums(local)
	if err != nil {
		if os.IsNotExist(err) {
			return refusal(errs, "ADOPT", refuse(
				fmt.Sprintf("build it first: nova-update release build --version %s --out %s --source <checkout> --platform %s-%s", o.version, o.from, goos, goarch),
				"there is nothing to adopt: no %s for %s at %s", o.version, goos+"-"+goarch, local))
		}
		return refusal(errs, "ADOPT", err)
	}
	// VERIFIED HERE TOO, not only on each machine. A truncated fetch caught
	// once on this host is one refusal; caught on each machine it is four, and
	// the fleet is left in four different states while somebody reads them.
	progress(errs, "verifying %d artifacts against %s", len(arts), SumsFile)
	if _, err := VerifyArtifacts(local, arts); err != nil {
		return refusal(errs, "ADOPT", err)
	}
	// THE RELEASE INSTALLS ITSELF. The nova-update that runs the remote
	// install is the one this verb just copied there, so a machine with no
	// nova-tools at all -- a bench provisioned this morning -- adopts with the
	// same command as one that is a version behind.
	// The file is named for the TARGET platform, never this host: adopting a
	// windows bench from the Studio must look for, send and run
	// `nova-update.exe`. A bare `nova-update` there is a path that exists
	// nowhere in the release, and the machine would refuse with `command not
	// found` for a mistake made on this side.
	updateFile := ToolFile("nova-update", goos)
	var carriesUpdate bool
	for _, a := range arts {
		if a.Name == updateFile {
			carriesUpdate = true
		}
	}
	if !carriesUpdate {
		return refusal(errs, "ADOPT", refuse("build from a checkout that has cmd/nova-update",
			"this release carries no %s, so no machine could run the install", updateFile))
	}
	localSums, err := os.ReadFile(filepath.Join(local, SumsFile))
	if err != nil {
		return refusal(errs, "ADOPT", fmt.Errorf("cannot read %s: %w (build the release again)", filepath.Join(local, SumsFile), err))
	}
	adopted, refused := 0, 0
	for _, entry := range machines {
		machine := entry.Name
		// The file's columns win over the flags, for this machine only: the
		// fleet has three home directories and one of them is the odd one out.
		bin, dest := o.bin, o.dest
		if entry.Bin != "" {
			bin = entry.Bin
		}
		if entry.Dest != "" {
			dest = entry.Dest
		}
		// Remote paths are slash paths whatever this host is: a release
		// adopted from the Studio lands on Linux benches, and filepath.Join
		// on darwin would be right by accident and on windows wrong on
		// purpose. A leading ~ is left alone for the remote shell to expand,
		// which is how one --bin names three different home directories.
		remoteDir := path.Join(dest, o.version, goos+"-"+goarch)
		remoteTool := path.Join(remoteDir, updateFile)
		// --dry-run ANSWERS FROM THE MACHINES, not from what this host
		// assumes. It asks each one three things -- can I reach you, is the
		// destination there, what are you running now -- and streams nothing
		// and installs nothing. A plan composed without asking is a plan about
		// a fleet somebody remembers rather than the one that exists.
		if o.dryRun {
			installedNow, err := ssh.Run(ctx, machine, []string{path.Join(bin, updateFile), "version"})
			if err != nil {
				refused++
				fmt.Fprintf(errs, "RELEASE REFUSED machine=%s version=%s: %s (check `ssh %s` reaches it; nothing was sent)\n",
					field(machine), field(o.version), oneLine(installedNow, err), machine)
				continue
			}
			destOK := "yes"
			if _, err := ssh.Run(ctx, machine, []string{"test", "-d", dest}); err != nil {
				destOK = "no"
			}
			action := "install"
			current := firstToken(installedNow)
			if hasToken(installedNow, o.version) {
				action = "skip"
			}
			adopted++
			fmt.Fprintf(out, "RELEASE WOULD ADOPT machine=%s version=%s installed=%s dest=%s action=%s bin=%s\n",
				field(machine), field(o.version), field(current), field(destOK), field(action), field(bin))
			continue
		}
		// THE MACHINE IS ASKED WHAT IT ALREADY HOLDS before anything is
		// streamed. A bench that took this release an hour ago does not need
		// twenty-one binaries pushed to it again, and on a fleet this is most
		// of the benches most of the time.
		sent := "yes"
		if remote, err := ssh.Run(ctx, machine, []string{"cat", path.Join(remoteDir, SumsFile)}); err == nil && sameSums(remote, localSums) {
			sent = "no"
			progress(errs, "%s already holds %s; streaming nothing", machine, o.version)
		} else {
			progress(errs, "sending %s to %s:%s", o.version, machine, remoteDir)
			if output, err := ssh.Send(ctx, machine, local, path.Dir(remoteDir)); err != nil {
				refused++
				fmt.Fprintf(errs, "RELEASE REFUSED machine=%s version=%s: %s (check `ssh %s` reaches it and that %s is writable there; adopt runs from the host that has ssh to every machine)\n",
					field(machine), field(o.version), oneLine(output, err), machine, dest)
				continue
			}
		}
		argv := []string{
			remoteTool, "release", "install",
			"--from", dest, "--version", o.version, "--bin", bin,
			"--platform", goos + "-" + goarch,
		}
		if o.retire != "" {
			argv = append(argv, "--retire", o.retire)
		}
		progress(errs, "installing on %s", machine)
		output, err := ssh.Run(ctx, machine, argv)
		if err != nil {
			refused++
			fmt.Fprintf(errs, "RELEASE REFUSED machine=%s version=%s: %s (run `ssh %s %s` by hand to see the whole message)\n",
				field(machine), field(o.version), oneLine(output, err), machine, argv[0])
			continue
		}
		m := installedLine.FindStringSubmatch(output)
		if m == nil {
			refused++
			fmt.Fprintf(errs, "RELEASE REFUSED machine=%s version=%s: the install printed no RELEASE INSTALLED line: %s (run `ssh %s %s` by hand)\n",
				field(machine), field(o.version), oneLine(output, errNoReceipt), machine, argv[0])
			continue
		}
		if m[1] != o.version {
			refused++
			fmt.Fprintf(errs, "RELEASE REFUSED machine=%s version=%s: it installed %s instead (check --dest and --version name the same release)\n",
				field(machine), field(o.version), field(m[1]))
			continue
		}
		adopted++
		retired := m[4]
		if retired == "" {
			retired = "0"
		}
		// sent= is the STREAM, skipped= is the TOOLS: a machine can be
		// sent=no tools=0 skipped=21 (it already had everything) or sent=yes
		// tools=21 (it had nothing). Two different facts, two fields.
		fmt.Fprintf(out, "RELEASE ADOPTED machine=%s version=%s tools=%s skipped=%s retired=%s sent=%s bin=%s\n",
			field(machine), field(o.version), field(m[2]), field(m[3]), field(retired), field(sent), field(bin))
	}
	w, result, code := out, "OK", 0
	if refused > 0 {
		w, result, code = errs, "FAIL", 1
	}
	fmt.Fprintf(w, "RELEASE ADOPT %s machines=%d adopted=%d refused=%d version=%s dry-run=%s\n",
		result, len(machines), adopted, refused, field(o.version), map[bool]string{true: "yes", false: "no"}[o.dryRun])
	return code
}

// ReadDigestFile reads a DigestFile: the sha256 of a release's SHA256SUMS, as
// `release build` wrote it. The first whitespace-separated token is taken, so a
// file produced by `sha256sum SHA256SUMS` -- which is `<digest>  SHA256SUMS` --
// reads too, and a person who made one by hand that way is not punished for it.
// The path is LOCAL: this function opens a file, and nothing in this package
// ever asks a machine to hash anything.
func ReadDigestFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("cannot read --expect-sums-from %s: %w (name the %s that `release build` wrote beside the artifacts)", path, err, DigestFile)
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 || !sha256Hex.MatchString(fields[0]) {
		return "", refuse(fmt.Sprintf("name the %s that `release build` wrote, whose first token is the 64 hex characters of `sha256sum %s`", DigestFile, SumsFile),
			"%s does not start with a sha256", path)
	}
	return fields[0], nil
}

// errNoReceipt gives oneLine something to fold when the remote's failure is that
// it said nothing this tool recognises.
var errNoReceipt = fmt.Errorf("no receipt")

// sameSums compares two checksum files by content, ignoring only the trailing
// newline a shell redirect may or may not have left. It is a comparison of the
// WHOLE file rather than of a digest, because both sides are already here.
func sameSums(remote string, local []byte) bool {
	return strings.TrimSpace(remote) != "" && strings.TrimSpace(remote) == strings.TrimSpace(string(local))
}

// firstToken is the version a tool printed, for a probe line: its `version`
// verb answers `<name> <version> <goos>/<goarch> <go>`, and the probe reports
// the second token when there is one.
func firstToken(line string) string {
	fields := strings.Fields(strings.TrimSpace(firstLineOf(line)))
	if len(fields) >= 2 {
		return fields[1]
	}
	if len(fields) == 1 {
		return fields[0]
	}
	return ""
}

func firstLineOf(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
