package main

// THE GATE ON A BENCH, IN THE SAME VERB.
//
// Glenn's lock of 2026-09-18 is that landing is by integration batches only, and the gate
// that admits one has to run where the cores are: hulk is 64 of them against the Studio's
// 32, which are also Glenn's, and the fleet toolchain lives there under ~/sdk. But a bench
// holds NO forge credential and no `gh` -- secrets are sealed once in the store and never
// copied between machines -- so on a bench `--plan`, `checks=required` and `--land` all
// refuse, and every landing child that day ran the gate over ssh by hand, read the BATCH OK
// line off a terminal, and did the forge half from the Studio. Eight times. Glenn,
// 2026-09-17: "everything sketched becomes a tool".
//
// `--on <machine>` is that dance as one verb. The split is exactly the one the hand did:
//
//	ON THE MACHINE   the clone, the merges in order, build, vet, the windows cross vet,
//	                 the test suite the way CI runs it, the lisp suite, and the bundle.
//	ON THIS MACHINE  every member's own ci-ok, the plan, the push, the pull request, the
//	                 one door, the queue watch and the members' closes -- everything that
//	                 needs the credential this machine has and that machine must not.
//
// The batch crosses back as a GIT BUNDLE, which is what the landing children carried by
// hand: one file, the batch's own commits, prerequisite the base the gate started from.
// Nothing else comes back; the machine keeps its checkout for the next run to read.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// batchMachines is where the fleet's registry lives, relative to a checkout of the
// bus: the same default `nova-pulse fill` carries, because it is the same file and a
// second spelling of one path is two paths waiting to disagree.
const batchMachines = "queue/control/machines.tsv"

// batchBundle is the file the batch crosses the seam as, under the run's own directory on
// both machines. It is a git bundle: git's own transport as a single file, which is what a
// seam that can only carry bytes needs.
const batchBundle = "batch.bundle"

// bundleSignature is the first bytes of every git bundle, v2 and v3 alike. A file that does
// not begin with it is not a bundle, whatever the remote's cat produced -- an empty file, an
// error message, a shell's own complaint -- and saying so here names the seam rather than
// leaving `git fetch` to say "unrecognized input" ten minutes later.
const bundleSignature = "# v"

// newBatchSite picks the machine the gate runs on: this one, or the bench --on names.
//
// THE REGISTRY IS THE ANSWER TO "WHICH MACHINE", and it is read here for three facts at
// once: whether the name is a machine at all, whether it may take work (fleet.RequireBench
// is the lock -- a runner host is CI-only, and a card beside a shard makes the shard slow,
// the gate red and the queue stop), and its ssh target. The check is on the NAME, before any
// ssh, so a refused machine is never even connected to.
func newBatchSite(in batchRun, deps Deps) (batchSite, error) {
	if in.on == "" {
		return newLocalSite(in, deps)
	}
	reg, err := fleet.ReadRegistry(in.machines)
	if err != nil {
		return nil, fmt.Errorf("--on %s needs the machines registry, which is where a machine's ssh target and its roles are written down: %w", in.on, err)
	}
	if err := reg.RequireBench(in.on); err != nil {
		return nil, err
	}
	m, _ := reg.Lookup(in.on)
	if m.OS == "windows" {
		return nil, fmt.Errorf("%s is a windows machine in %s and every step of this gate is a POSIX shell command; the windows leg is CI's, and the gate's own windows cover is the cross vet it runs everywhere", m.Name, in.machines)
	}
	return newRemoteSite(in, deps, deps.NewRemote(m.Name, m.SSH))
}

// remoteSite is the gate on a bench, reached over the ssh seam.
type remoteSite struct {
	in     batchRun
	deps   Deps
	remote merge.Remote
	// The four paths ON THE MACHINE, all under --root, which is a path on THAT machine and
	// may be written `~/...` for its home rather than this one's.
	work   string
	tmp    string
	clone  string
	bundle string
	// The two paths ON THIS MACHINE, under --local-root, which is where the batch lands
	// when it comes back.
	localWork  string
	localRoot  string
	localClone string
	env        []string
	// branch is the batch's own branch, remembered from Start so that Clone fetches out of
	// the bundle the one ref the gate built rather than recomputing its name.
	branch string
	// The probe's answers, read once: which of the suite's programs this machine has, and
	// which of its files this checkout holds.
	probed   bool
	probeErr error
	haveProg map[string]bool
	haveFile map[string]bool
}

// newRemoteSite checks both roots before anything is removed on either machine.
func newRemoteSite(in batchRun, deps Deps, remote merge.Remote) (*remoteSite, error) {
	if err := merge.ValidRemotePath(in.root); err != nil {
		return nil, fmt.Errorf("--root is the directory this batch clones and builds under ON %s: %w", remote.Name(), err)
	}
	// --reference is a mirror on the machine that CLONES, which with --on is that machine
	// and not this one. It is held to the same shape as --root for the same reason: it
	// reaches that machine's shell.
	if ref := strings.TrimSpace(in.reference); ref != "" {
		if err := merge.ValidRemotePath(ref); err != nil {
			return nil, fmt.Errorf("--reference is a git mirror ON %s, which is the machine doing the cloning: %w", remote.Name(), err)
		}
	}
	work := merge.RemoteJoin(in.root, in.name)
	localRoot, err := filepath.Abs(in.localRoot)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(localRoot, 0o755); err != nil {
		return nil, err
	}
	localWork := filepath.Join(localRoot, in.name)
	// THE LOCAL SIDE IS REBUILT EVERY RUN too, for the local gate's own reason: a bundle
	// or a clone an earlier run left is a tree this run did not build. It is a path this
	// tool COMPUTED, so its removal is safepath's and nobody else's.
	if err := safepath.RemoveUnder(localRoot, localWork); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(localWork, 0o755); err != nil {
		return nil, err
	}
	tmp := merge.RemoteJoin(work, "tmp")
	return &remoteSite{
		in: in, deps: deps, remote: remote,
		work:   work,
		tmp:    tmp,
		clone:  merge.RemoteJoin(work, "repo"),
		bundle: merge.RemoteJoin(work, batchBundle),

		localRoot:  localRoot,
		localWork:  localWork,
		localClone: filepath.Join(localWork, "repo"),
		env:        remoteTestEnv(tmp, in.gomaxprocs),
		haveProg:   map[string]bool{},
		haveFile:   map[string]bool{},
	}, nil
}

// remoteTestEnv is ciTestEnv across the seam: the same private temp directory inside the
// batch's own working directory, and the same fair share of the cores.
//
// It carries no copy of this process's environment, which is the difference from the local
// one and is right: the machine's own environment is the machine's, and everything the go
// command must not inherit is unset by the prelude (merge.RemotePrelude).
func remoteTestEnv(tmp string, gomaxprocs int) []string {
	env := make([]string, 0, len(batchTempVars)+1)
	for _, name := range batchTempVars {
		env = append(env, name+"="+tmp)
	}
	if gomaxprocs > 0 {
		env = append(env, "GOMAXPROCS="+strconv.Itoa(gomaxprocs))
	}
	return env
}

func (s *remoteSite) On() string { return s.remote.Name() }

// exec is one command in one directory on the machine, under the run's --timeout, RAW: the
// output and the error exactly as the machine gave them. The suite's steps take this one,
// because a red step's OUTPUT is the news and stepFailure reads it for the failing packages
// and tests.
func (s *remoteSite) exec(dir, command string) (string, error) {
	return s.remote.Exec(merge.RemoteScript(dir, s.env, command), s.in.timeout)
}

// run is exec for every CONTROL command -- the clone, the fetches, the merges, the bundle,
// the probe -- and it folds THE COMMAND AND WHAT THE FAR SIDE SAID into the error.
//
// 2026-09-18, measured against vision: the first step of a remote gate failed and the whole
// refusal a caller got was
//
//	BATCH REFUSED: vision could not make the batch's working directory ~/...: exit status 1
//
// The machine had said `bash: line 1: cd: null directory` and nobody could see it, so the
// reader was left with an exit code and a path that was perfectly fine -- `mkdir -p` on it
// by hand worked. An error from another machine that does not carry that machine's own words
// is a refusal somebody has to reproduce by hand before they can read it, which is the whole
// cost this verb exists to remove. The command goes in beside them, so the reader can run
// the thing themselves without rebuilding it from the source.
func (s *remoteSite) run(dir, command string) (string, error) {
	out, err := s.exec(dir, command)
	if err == nil {
		return out, nil
	}
	return out, fmt.Errorf("%w; the command was %q; %s said: %s",
		err, command, s.On(), oneline.Cap(remoteSaid(out), oneline.TailBytes))
}

// remoteSaid is what a machine printed, as ONE line: every line that says anything, joined,
// and a plain sentence when it said nothing at all. An empty reason field reads as a tool
// that forgot to fill it in, which is how this defect hid.
func remoteSaid(out string) string {
	var said []string
	for _, line := range strings.Split(out, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			said = append(said, s)
		}
	}
	if len(said) == 0 {
		return "(nothing at all, which is a command that failed silently)"
	}
	return strings.Join(said, " / ")
}

// Start rebuilds the working directory on the machine and clones into it.
//
// It is ONE script rather than five round trips because every one of them is an ssh
// handshake, and because the five are a single all-or-nothing step: a clone with no checkout
// after it is a directory nobody wants either.
func (s *remoteSite) Start(base, branch string) (string, error) {
	s.branch = branch
	prepare := "mkdir -p " + merge.RemoteQuote(s.in.root) +
		" && rm -rf " + merge.RemoteQuote(s.work) +
		" && mkdir -p " + merge.RemoteQuote(s.tmp)
	// No directory to run in: this step is the one that MAKES it. run's empty dir emits no
	// cd, which is the vision defect above, and its error carries what the machine said.
	if _, err := s.run("", prepare); err != nil {
		return "", fmt.Errorf("%s could not make the batch's working directory %s: %w", s.On(), s.work, err)
	}
	clone := "git clone --quiet"
	if ref := strings.TrimSpace(s.in.reference); ref != "" {
		// --reference names a mirror ON THE MACHINE, which is the only machine doing any
		// cloning here. It was checked as a path on that machine at the flag site.
		clone += " --reference " + merge.RemoteQuote(ref)
	}
	clone += " -- " + merge.RemoteQuote(s.deps.RepoURL(s.in.repo)) + " " + merge.RemoteQuote(s.clone)
	if _, err := s.run(s.work, clone); err != nil {
		return "", fmt.Errorf("%s could not clone %s: %w", s.On(), s.in.repo, err)
	}
	start := "git fetch --quiet origin " + merge.RemoteQuote(base) +
		" && git checkout --quiet -B " + merge.RemoteQuote(branch) + " FETCH_HEAD" +
		" && git rev-parse HEAD"
	out, err := s.run(s.clone, start)
	if err != nil {
		return "", fmt.Errorf("%s could not start %s at origin/%s: %w", s.On(), branch, base, err)
	}
	return remoteSHA(out)
}

func (s *remoteSite) Merge(n int, message string) (bool, error) {
	fetch := "git fetch --quiet origin " + merge.RemoteQuote("pull/"+strconv.Itoa(n)+"/head")
	if _, err := s.run(s.clone, fetch); err != nil {
		return false, fmt.Errorf("could not fetch pull/%d/head on %s: %w", n, s.On(), err)
	}
	// The identity is nova-merge's own, exactly as the local site passes it: a machine with
	// no git identity anywhere dies on `git merge --no-ff` with "Committer identity unknown".
	do := "git -c user.name=nova-merge -c user.email=nova-merge@localhost merge --no-ff --no-edit -m " +
		merge.RemoteQuote(message) + " FETCH_HEAD"
	_, mergeErr := s.run(s.clone, do)
	if mergeErr == nil {
		return true, nil
	}
	// A CONFLICT LEAVES UNMERGED PATHS IN THE INDEX, and anything else is a failure of the
	// run rather than a member to drop -- the same reading hasConflicts does locally.
	//
	// THE INDEX IS READ TO CLASSIFY THE FAILURE, NEVER TO REPLACE IT (Stella's read of
	// #1443, P3). The merge's own error is what says why the merge failed; the read after it
	// only says which KIND of failure it was, and a merge that died of a lock file or a
	// missing object reported nothing about that at all once its error had been dropped.
	unmerged, indexErr := s.run(s.clone, "git diff --name-only --diff-filter=U")
	if indexErr != nil {
		return false, fmt.Errorf("the merge of pull/%d on %s failed (%w) and its index could not be read afterwards, so this gate cannot tell a conflict from a merge that could not run: %w", n, s.On(), mergeErr, indexErr)
	}
	if strings.TrimSpace(unmerged) == "" {
		return false, fmt.Errorf("the merge of pull/%d on %s failed and left no conflicting file, so it is a merge that could not run rather than a member to drop: %w", n, s.On(), mergeErr)
	}
	if _, err := s.run(s.clone, "git merge --abort"); err != nil {
		return false, fmt.Errorf("the conflicting merge of pull/%d on %s could not be aborted, so the checkout there is mid-merge and the members after this one would be judged on it: %w", n, s.On(), err)
	}
	return false, nil
}

func (s *remoteSite) Head() (string, error) {
	out, err := s.run(s.clone, "git rev-parse HEAD")
	if err != nil {
		return "", fmt.Errorf("%s could not say what the batch's head is: %w", s.On(), err)
	}
	return remoteSHA(out)
}

func (s *remoteSite) Read(rel string) (string, bool) {
	out, err := s.exec(s.clone, "cat -- "+merge.RemoteQuote(rel))
	if err != nil {
		return "", false
	}
	return out, true
}

func (s *remoteSite) Probe(command string) (string, bool) {
	out, err := s.exec(s.clone, command)
	return out, err == nil
}

// Unavailable is edge 2 across the seam: a step whose program or file is not on the MACHINE
// is skipped out loud, and the skip is on the verdict line.
//
// The probe is one script and one round trip for the whole suite, run the first time it is
// asked. It runs UNDER THE PRELUDE, so `command -v go` sees the same PATH the steps will --
// a probe that looked at a bare non-interactive PATH would report a bench with go1.26.5
// under ~/sdk as a bench with no go at all.
func (s *remoteSite) Unavailable(step batchStep) (string, error) {
	// AN ASK THAT FAILED IS NOT AN ANSWER OF "no" (Stella's read of #1443, P1). This used to
	// mark the probe done and return silently on a transport failure, which left every
	// answer false: `go` and `sbcl` were reported missing, every step was skipped out loud,
	// and the run printed BATCH OK over a suite that had run NOTHING. A green verdict about
	// a tree nobody checked is the worst line this tool can print.
	if err := s.probe(); err != nil {
		return "", err
	}
	if step.file != "" && !s.haveFile[step.file] {
		return "this checkout holds no " + step.file, nil
	}
	if step.needs != "" && !s.haveProg[step.needs] {
		return step.needs + " is not on " + s.On() + " and is not under its ~/" + sdkDir, nil
	}
	return "", nil
}

// probe asks the machine, once, which of the suite's programs and files are there. The ask
// is one script and one round trip for the whole suite, and its failure is REMEMBERED rather
// than swallowed: every later caller gets the same answer, which is that nobody knows.
func (s *remoteSite) probe() error {
	if s.probed {
		return s.probeErr
	}
	s.probed = true
	var lines []string
	seenProg, seenFile := map[string]bool{}, map[string]bool{}
	for _, step := range batchGate {
		if step.needs != "" && !seenProg[step.needs] {
			seenProg[step.needs] = true
			lines = append(lines, "command -v "+merge.RemoteQuote(step.needs)+" >/dev/null 2>&1 && echo prog "+merge.RemoteQuote(step.needs))
		}
		if step.file != "" && !seenFile[step.file] {
			seenFile[step.file] = true
			lines = append(lines, "test -f "+merge.RemoteQuote(step.file)+" && echo file "+merge.RemoteQuote(step.file))
		}
	}
	// `true` last, so a probe whose every answer is "no" still exits zero: a machine with
	// no sbcl is a fact to report and not a seam that failed.
	out, err := s.run(s.clone, strings.Join(lines, "; ")+"; true")
	if err != nil {
		// The script ends in `true`, so a machine that HAS none of these still exits zero.
		// An error here is therefore the ask itself failing -- the connection, the shell,
		// the checkout -- and never the answer "no".
		s.probeErr = fmt.Errorf("%s could not be asked which of the suite's programs and files it has, so this gate cannot tell a machine without sbcl from a machine it could not reach: %w", s.On(), err)
		return s.probeErr
	}
	for _, line := range strings.Split(out, "\n") {
		kind, name, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		switch kind {
		case "prog":
			s.haveProg[name] = true
		case "file":
			s.haveFile[name] = true
		}
	}
	return nil
}

// Step runs one step of the suite on the machine. A step's own variables (the windows cross
// vet's GOOS and GOARCH) go in front of its command, which is where a shell takes them for
// that one command and no other.
func (s *remoteSite) Step(step batchStep) (string, error) {
	command := step.command
	if len(step.env) > 0 {
		command = strings.Join(step.env, " ") + " " + command
	}
	return s.exec(s.clone, command)
}

// Bring carries the green batch back, as a git bundle.
//
// THE BUNDLE IS THIN ON PURPOSE: its prerequisite is the base the gate started from, so what
// crosses the seam is the members' own commits and the merges over them rather than the
// repository's whole history. The local clone that reads it has the base already -- it is
// the branch the batch lands on -- and a bundle whose prerequisite is missing says so in one
// line rather than producing a tree with holes in it.
func (s *remoteSite) Bring(branch, baseSHA, headSHA string) (string, int64, error) {
	create := "git bundle create " + merge.RemoteQuote(s.bundle) + " " +
		merge.RemoteQuote(baseSHA) + ".." + merge.RemoteQuote(branch)
	if _, err := s.run(s.clone, create); err != nil {
		return "", 0, fmt.Errorf("%s could not bundle %s: %w", s.On(), branch, err)
	}
	local := filepath.Join(s.localWork, batchBundle)
	if err := s.remote.Get(s.bundle, local, s.in.timeout); err != nil {
		return "", 0, err
	}
	info, err := os.Stat(local)
	if err != nil {
		return "", 0, err
	}
	// WHAT CAME BACK IS CHECKED HERE, at the seam, rather than by whatever reads it next.
	// A `cat` over ssh answers with whatever the far side wrote on its stdout -- an empty
	// file, a shell's complaint, half a transfer -- and every one of those is a bundle that
	// fails much later with a message about the wrong thing.
	head, err := os.Open(local)
	if err != nil {
		return "", 0, err
	}
	defer head.Close()
	sig := make([]byte, len(bundleSignature)+4)
	n, _ := head.Read(sig)
	if !strings.HasPrefix(string(sig[:n]), bundleSignature) {
		return "", 0, fmt.Errorf("what came back from %s as %s is not a git bundle: it begins %q, and a bundle begins %q. The gate's tree is still on that machine, under %s",
			s.On(), local, string(sig[:n]), bundleSignature, s.clone)
	}
	return local, info.Size(), nil
}

// Clone is the landing's local checkout: a clone of the repository on THIS machine with the
// batch's own commits fetched into it out of the bundle. From here on the landing is the
// landing -- push, pull request, the one door, the queue -- and nothing it does can tell
// that the tree was built somewhere else.
func (s *remoteSite) Clone() (string, error) {
	g := merge.NewGit(s.localWork, s.in.timeout, s.deps.Runner)
	if _, err := g.Run("clone", "--quiet", "--", s.deps.RepoURL(s.in.repo), s.localClone); err != nil {
		return "", fmt.Errorf("the batch was gated on %s and this machine is the one that pushes it, so it needs a clone of its own: %w", s.On(), err)
	}
	local := merge.NewGit(s.localClone, s.in.timeout, s.deps.Runner)
	if _, err := local.Run("fetch", "--quiet", "origin", s.in.base); err != nil {
		return "", fmt.Errorf("could not fetch origin/%s into the local clone: %w", s.in.base, err)
	}
	bundle := filepath.Join(s.localWork, batchBundle)
	ref := "refs/heads/" + s.branch
	if _, err := local.Run("fetch", "--quiet", bundle, ref+":"+ref); err != nil {
		return "", fmt.Errorf("the bundle %s carried back from %s could not be read into the local clone; the gate's tree is still on that machine, under %s: %w", bundle, s.On(), s.clone, err)
	}
	return s.localClone, nil
}

// remoteSHA is the sha a remote git printed: the LAST thing it said, because a shell that
// ran three commands with && printed whatever the first two had to say in front of it.
func remoteSHA(out string) (string, error) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		s := strings.TrimSpace(lines[i])
		if s == "" {
			continue
		}
		if !merge.IsSHA(s) {
			return "", fmt.Errorf("the machine answered %q where a 40-character sha was asked for", firstWordsOf(s))
		}
		return s, nil
	}
	return "", fmt.Errorf("the machine printed nothing where a 40-character sha was asked for")
}

// firstWordsOf is how much of an unreadable answer a refusal quotes back: enough to
// recognise, never the whole of something that arrived from another machine.
func firstWordsOf(s string) string {
	if len(s) > 120 {
		return s[:120]
	}
	return s
}
