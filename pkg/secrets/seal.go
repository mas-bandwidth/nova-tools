package secrets

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
	"github.com/mas-bandwidth/nova-tools/pkg/gitrun"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/safepath"
	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
)

// errSeatFileAbsent is the typed refusal `seal` makes for a seat file the store does
// not hold (SPEC-SECRETS rule 12; tla/SecretsSeat.tla on sprint/md-secrets-h.w1.g1.e15,
// the Seal action): a new seat is given its first values by seat add, never by seal,
// because the decrypt before the write is the only step that proves the caller's key
// opens the target, and an absent file gives it nothing to prove.
var errSeatFileAbsent = errors.New("a new seat is given its first values by seat add, never by seal (SPEC-SECRETS rule 12)")

// execCommand runs one helper process and returns its stdout. The encrypt step
// takes it as a parameter so a test can supply a pure-Go fake on every platform
// instead of a shell script Windows cannot execute.
type execCommand func(stdin io.Reader, env []string, dir, name string, args ...string) ([]byte, error)

// stderrCarrier is a helper child's error that also carries the stderr the child wrote, so
// a verb can name the class of a failure without the seam's signature growing a return. The
// real runner returns a childError; a test's fake over the same seam returns its own.
type stderrCarrier interface{ Stderr() string }

// childError is a helper child's failure with the stderr it wrote. The transcript is kept
// rather than passed through, so a verb names the class of the failure (a wrong key's, for
// one) and never echoes a value the transcript might hold (STEP 3, "wrong key").
type childError struct {
	err    error
	stderr string
}

func (e *childError) Error() string  { return e.err.Error() }
func (e *childError) Unwrap() error  { return e.err }
func (e *childError) ExitCode() int  { return exitCodeOf(e.err, 1) }
func (e *childError) Stderr() string { return e.stderr }

func realExecCommand(stdin io.Reader, env []string, dir, name string, args ...string) ([]byte, error) {
	// The deadline is the kind of the program named: git, gh, sops and the rest each have
	// their own default (subproc.KindOf).
	cmd, cancel := subproc.CommandFor(context.Background(), subproc.BudgetOf(name, args), name, args...)
	defer cancel()
	cmd.Env = env
	cmd.Dir = dir
	cmd.Stdin = stdin
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if err != nil {
		return out.Bytes(), &childError{err: err, stderr: errBuf.String()}
	}
	return out.Bytes(), nil
}

// say writes one progress line; never a value, only step names and public facts.
func (o SealOptions) say(format string, a ...interface{}) {
	if o.Progress != nil {
		// ignored: the progress line is best effort; the verb's result still carries the outcome
		_, _ = fmt.Fprintf(o.Progress, "seal: "+format+"\n", a...)
	}
}

// SealOptions carries one seal request and the test seams around it.
//
// The value travels from Stdin (or the controlling terminal) to the encrypt child's
// stdin and nowhere else: not argv, not a file in the clear, not an output line.
type SealOptions struct {
	StoreDir string
	AsName   string
	KeyPath  string
	SopsPath string
	Name     string
	GHPath   string
	GitPath  string

	NoPR     bool
	UseStdin bool

	// DryRun prints the plan and writes nothing: no value is read, nothing is encrypted,
	// no file is written, and no git write, push, gh or sops encrypt runs. The plan is
	// read off the same carry the real run walks (sealCarry), so the two cannot differ.
	DryRun bool

	Stdin           io.Reader
	StdinIsTerminal bool

	// Progress receives one short line per step that can take time (nil = silent).
	// A person at a terminal must be able to tell waiting from hung: any interactive
	// verb says what it is doing before each step that can exceed a blink. The lines
	// go to stderr so the one-line result on stdout stays the whole machine answer.
	Progress io.Writer

	Now   func() time.Time
	Sleep func(time.Duration)
	Check func(storeDir, asName, keyPath, sopsPath string) error

	// Exec replaces the os/exec child process. Tests set it to a pure-Go fake
	// so the seal path runs where a POSIX shell-script fake cannot.
	Exec execCommand
}

func (opts SealOptions) exec() execCommand {
	if opts.Exec != nil {
		return opts.Exec
	}
	return realExecCommand
}

var prNumberRegex = regexp.MustCompile(`/pull/(\d+)`)

// RunSeal reads one value, folds it into the seat file under --name, and carries the
// change through a branch, a commit and, unless --no-pr, a pull request to its merge.
func RunSeal(opts SealOptions) (line string, err error) {
	if err := preflight(opts.StoreDir, need{opts.StoreDir, "--store <dir>", false}, need{opts.AsName, "--as <name>", true},
		need{opts.KeyPath, "--key <path>", false}, need{opts.SopsPath, "--sops <path>", false}, need{opts.Name, "--name <NAME>", false}); err != nil {
		return "", err
	}
	if !IsValidEnvVar(opts.Name) {
		return "", fmt.Errorf("invalid key name %q: must match [A-Z][A-Z0-9_]*", opts.Name)
	}
	if opts.GHPath == "" {
		opts.GHPath = "gh"
	}
	if opts.GitPath == "" {
		opts.GitPath = "git"
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Sleep == nil {
		opts.Sleep = time.Sleep
	}

	run := opts.exec()
	if err := CheckInvariant6(opts.KeyPath); err != nil {
		return "", err
	}
	if _, err := CheckSopsVersion(run, opts.SopsPath); err != nil {
		return "", err
	}

	seatFile := opts.AsName + ".yaml"
	targetFile := filepath.Join(opts.StoreDir, seatFile)
	branch := fmt.Sprintf("seal/%s-%s-%s", opts.AsName, opts.Name, opts.Now().UTC().Format("20060102-150405"))
	commitMsg := fmt.Sprintf("seal %s into %s", opts.Name, seatFile)
	carry := sealCarry{
		run:      run,
		storeDir: opts.StoreDir,
		gitPath:  opts.GitPath,
		ghPath:   opts.GHPath,
		seatFile: seatFile,
		branch:   branch,
		message:  commitMsg,
		title:    commitMsg,
		body:     "Sealed with nova-secrets seal. The value was never written to a file in the clear, to argv, or to output.",
		noPR:     opts.NoPR,
		say:      opts.say,
		check: func() error {
			checkFn := opts.Check
			if checkFn == nil {
				checkFn = checkSeatDecrypts
			}
			return checkFn(opts.StoreDir, opts.AsName, opts.KeyPath, opts.SopsPath)
		},
		now:   opts.Now,
		sleep: opts.Sleep,
	}

	if opts.DryRun {
		return sealDryRun(opts, carry, targetFile)
	}

	// A seat file the store does not hold is never written by seal (SPEC-SECRETS
	// rule 12; tla/SecretsSeat.tla on sprint/md-secrets-h.w1.g1.e15, the Seal action):
	// the decrypt is the only step that proves the caller's key opens the target, and
	// an absent file gives it nothing to prove, so a rule for the name alone would let
	// any store key's holder write the file. The refusal lands before the value is
	// read, so nothing is taken for a write that will not happen.
	if _, err := os.Stat(targetFile); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%s does not exist in the store; %w", seatFile, errSeatFileAbsent)
		}
		return "", err
	}

	value, err := readSealValue(opts)
	if err != nil {
		return "", err
	}

	opts.say("reading %s", seatFile)
	existing, err := sealDecrypt(run, opts.SopsPath, opts.KeyPath, targetFile)
	if err != nil {
		return "", err
	}
	plaintext := sealApply(existing, opts.Name, value)

	opts.say("encrypting to the seat's recipients")
	ciphertext, rules, err := sealEncrypt(run, opts.SopsPath, opts.KeyPath, opts.StoreDir, seatFile, "seal", plaintext)
	if err != nil {
		return "", err
	}
	carry.rules = rules

	prNum, merged, err := carry.carry(ciphertext)
	if err != nil {
		return "", err
	}
	switch {
	// A --no-pr value is not on the store's own branch, so it is not what exec reads: the
	// NOTE says so, with the next command, so nobody takes the OK for a delivered value.
	case opts.NoPR:
		return fmt.Sprintf("SECRETS SEAL OK name=%s seat=%s committed branch=%s\n"+
			"SECRETS SEAL NOTE exec and check read the store's own branch, which does not hold this value yet; next: git -C %s push -u origin %s, then open and merge its pull request",
			oneline.Field(opts.Name), oneline.Field(opts.AsName), oneline.Field(branch), oneline.Field(opts.StoreDir), oneline.Field(branch)), nil
	case !merged:
		return fmt.Sprintf("SECRETS SEAL OK name=%s seat=%s pr=#%s open (gate not yet approved)",
			oneline.Field(opts.Name), oneline.Field(opts.AsName), prNum), nil
	}
	return fmt.Sprintf("SECRETS SEAL OK name=%s seat=%s pr=#%s merged",
		oneline.Field(opts.Name), oneline.Field(opts.AsName), prNum), nil
}

// checkSeatDecrypts is the check a merged seal ends on: the seat's file, at the
// store's new head, through the full `check`.
func checkSeatDecrypts(storeDir, asName, keyPath, sopsPath string) error {
	_, _, _, _, code, err := RunCheck(storeDir, asName, keyPath, sopsPath, 20)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("check failed on %s after seal", asName)
	}
	return nil
}

// sealCarry is the road a ciphertext takes from a verb to the store: a branch,
// a commit, and -- unless noPR -- a push, a pull request, the gate's approval,
// a squash merge, a pull and a check. `seal` and `seat inject` walk the same
// road, so the gate sees one shape of pull request from both.
type sealCarry struct {
	run      execCommand
	storeDir string
	gitPath  string
	ghPath   string
	seatFile string // the seat file the commit touches
	rules    []byte // .sops.yaml as it is to be committed beside it, or nil to leave it alone
	branch   string // seal/<seat>-<NAMES>-<stamp>
	message  string // the commit message
	title    string // the pull request title
	body     string // the pull request body
	noPR     bool
	say      func(format string, a ...interface{})
	check    func() error // runs after the merge and the pull
	now      func() time.Time
	sleep    func(time.Duration)
}

// carry writes the ciphertext into place on a fresh branch and carries it to
// the store. It returns the pull request number ("" under noPR) and whether the
// request merged. On every path out the store is back on the branch it was on; the commit stays on c.branch.
func (c sealCarry) carry(ciphertext []byte) (prNum string, merged bool, err error) {
	say := c.say
	if say == nil {
		say = func(string, ...interface{}) {}
	}
	nowFn := c.now
	if nowFn == nil {
		nowFn = time.Now
	}
	sleepFn := c.sleep
	if sleepFn == nil {
		sleepFn = time.Sleep
	}
	home, err := c.preflight()
	if err != nil {
		return "", false, err
	}

	say("committing on branch %s", c.branch)
	if err := sealGit(c.run, c.storeDir, c.gitPath, "checkout", "-b", c.branch); err != nil {
		return "", false, err
	}
	// Restore the starting branch on every path; the commit stays on c.branch.
	restored := false
	restore := func() error {
		if restored {
			return nil
		}
		if rerr := sealGit(c.run, c.storeDir, c.gitPath, "checkout", "-f", home); rerr != nil {
			return rerr
		}
		restored = true
		return nil
	}
	defer func() {
		if rerr := restore(); rerr != nil {
			if err != nil {
				err = fmt.Errorf("%s; also failed to return the store to %s: %s", oneline.Err(err), oneline.Field(home), oneline.Err(rerr))
			} else {
				err = rerr
				prNum, merged = "", false
			}
		}
	}()

	targetFile := filepath.Join(c.storeDir, c.seatFile)
	if err := atomicWriteFile(targetFile, ciphertext, 0600); err != nil {
		return "", false, err
	}
	if err := sealGit(c.run, c.storeDir, c.gitPath, "add", c.seatFile); err != nil {
		return "", false, err
	}
	// The rule that admits the mark, in the same commit, so the gate reviews it with the file.
	if c.rules != nil {
		if err := atomicWriteFile(filepath.Join(c.storeDir, ".sops.yaml"), c.rules, 0644); err != nil {
			return "", false, err
		}
		if err := sealGit(c.run, c.storeDir, c.gitPath, "add", ".sops.yaml"); err != nil {
			return "", false, err
		}
	}
	if err := sealGit(c.run, c.storeDir, c.gitPath, "commit", "-m", c.message); err != nil {
		return "", false, err
	}

	if c.noPR {
		say("returning the store to its branch")
		if err := restore(); err != nil {
			return "", false, err
		}
		return "", false, nil
	}

	say("pushing the branch")
	if err := sealGit(c.run, c.storeDir, c.gitPath, "push", "-u", "origin", c.branch); err != nil {
		return "", false, err
	}
	say("opening the pull request")
	createOut, err := sealGH(c.run, c.ghPath, c.storeDir, "pr", "create", "--head", c.branch, "--title", c.title, "--body", c.body)
	if err != nil {
		return "", false, err
	}
	if m := prNumberRegex.FindStringSubmatch(createOut); len(m) > 1 {
		prNum = m[1]
	}
	if prNum == "" {
		return "", false, fmt.Errorf("gh pr create did not return a pull request number")
	}

	approved := false
	started := nowFn()
	deadline := started.Add(2 * time.Minute)
	lastSaid := started
	say("pull request #%s is open; waiting for the gate's approval (up to 2 min)", prNum)
	for {
		view, err := sealGH(c.run, c.ghPath, c.storeDir, "pr", "view", prNum, "--json", "reviewDecision,state,statusCheckRollup")
		if err != nil {
			errStr := strings.ToLower(err.Error())
			if strings.Contains(errStr, "not found") || strings.Contains(errStr, "could not resolve") || strings.Contains(errStr, "missing") {
				return "", false, fmt.Errorf("pull request #%s not found: %s; run: gh pr list", prNum, oneline.Err(err))
			}
			return "", false, fmt.Errorf("pull request #%s review query failed: %s; run: gh pr view %s", prNum, oneline.Err(err), prNum)
		}
		termErr, isApproved := classifySealReview(view, prNum)
		if termErr != nil {
			return "", false, termErr
		}
		if isApproved {
			approved = true
			break
		}
		if !nowFn().Before(deadline) {
			break
		}
		if nowFn().Sub(lastSaid) >= 15*time.Second {
			say("still waiting for approval (%ds)", int(nowFn().Sub(started).Seconds()))
			lastSaid = nowFn()
		}
		sleepFn(5 * time.Second)
	}
	if !approved {
		return prNum, false, nil
	}

	say("approved; merging #%s", prNum)
	if _, err := sealGH(c.run, c.ghPath, c.storeDir, "pr", "merge", prNum, "--squash"); err != nil {
		return "", false, err
	}
	// Back to the branch the store was on: the squash leaves the seal branch stale, and a
	// store parked on it would serve the next exec from a branch nobody merges again.
	say("returning the store to its branch and pulling")
	if err := restore(); err != nil {
		return "", false, err
	}
	if err := sealGit(c.run, c.storeDir, c.gitPath, "pull"); err != nil {
		return "", false, err
	}

	say("checking the seat decrypts")
	if c.check != nil {
		if err := c.check(); err != nil {
			return "", false, err
		}
	}
	return prNum, true, nil
}

// preflight is the two git reads the road opens with: the branch the store stands on and
// that no tracked change would be discarded by the return to it. `seal`, `seat inject`
// and their --dry-run all ask it, so a dry run refuses exactly where the real run would.
func (c sealCarry) preflight() (home string, err error) {
	home, err = sealGitOutput(c.run, c.storeDir, c.gitPath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		// In a working copy (the store's shape is checked first) this is a HEAD with no
		// commit under it: a store made with git init and nothing committed yet.
		return "", fmt.Errorf("%w; a store with no commit yet has no branch to return to; run: git -C %s add .sops.yaml recovery.pub && git -C %s commit -m 'a new store'", err, c.storeDir, c.storeDir)
	}
	if home == "" || home == "HEAD" {
		return "", fmt.Errorf("store %s is not on a branch; seal needs a named branch to return to", c.storeDir)
	}
	// checkout -f of home would discard these. Refuse before checkout -b.
	status, err := sealGitOutput(c.run, c.storeDir, c.gitPath, "status", "--porcelain", "-uno")
	if err != nil {
		return "", err
	}
	if status != "" {
		return "", fmt.Errorf("store %s is not clean; commit, stash, or restore tracked changes before seal (git status). seal will not discard them", c.storeDir)
	}
	return home, nil
}

// planLines are the carry's own steps as a --dry-run prints them: the branch and commit
// the real run makes, and the push and pull request it opens unless noPR. They are read
// off the fields carry() acts on, so the plan is the road, not a description of it.
func (c sealCarry) planLines(token, home string) []string {
	lines := []string{fmt.Sprintf("SECRETS %s PLAN git store=%s from=%s branch=%s commit=%s",
		token, oneline.Field(c.storeDir), oneline.Field(home), oneline.Field(c.branch), oneline.Quote(c.message))}
	if _, changed, err := seatMarkRuleIn(c.storeDir, c.seatFile); err == nil && changed {
		lines = append(lines, fmt.Sprintf("SECRETS %s PLAN rule .sops.yaml rule for %s gains unencrypted_regex %s in the same commit: the mark is read in the clear",
			token, oneline.Field(c.seatFile), seatMarkRegex))
	}
	if c.noPR {
		return append(lines, fmt.Sprintf("SECRETS %s PLAN push none (--no-pr): no push, no gh call; the store returns to %s",
			token, oneline.Field(home)))
	}
	return append(lines, fmt.Sprintf("SECRETS %s PLAN push remote=origin branch=%s pr title=%s then waits up to 2m for the gate's approval, merges --squash, pulls and checks the seat",
		token, oneline.Field(c.branch), oneline.Quote(c.title)))
}

// sealRecipients are the public keys sops will encrypt seatFile to: the recipients of the
// rule in .sops.yaml that matches it, which is the rule `sops -e --filename-override`
// picks (SPEC-SECRETS, seal "How it seals").
func sealRecipients(storeDir, seatFile string) ([]string, error) {
	cfg, err := ParseSopsConfig(storeDir)
	if err != nil {
		return nil, fmt.Errorf("store %s: %w", storeDir, err)
	}
	rule, err := FindMatchingRule(cfg, seatFile)
	if err != nil {
		return nil, fmt.Errorf(".sops.yaml carries no rule matching %s, so sops has no recipients to encrypt to; the rule is added in a pull request the store's gate reviews", seatFile)
	}
	return rule.Recipients, nil
}

// sealDryRun is `seal --dry-run`: the refusals the real run has, the plan its road
// takes, and nothing written. The value is never read (a dry run takes no stdin and
// opens no terminal prompt); the one sops call is the decrypt the real run also
// makes, to say whether NAME is added or replaced, and no value reaches a line.
// An absent seat file is the same typed refusal the real run makes (SPEC-SECRETS
// rule 12; tla/SecretsSeat.tla, Seal): a dry run never plans the write the real
// run will not take.
func sealDryRun(opts SealOptions, carry sealCarry, targetFile string) (string, error) {
	seatFile := carry.seatFile
	recipients, err := sealRecipients(opts.StoreDir, seatFile)
	if err != nil {
		return "", err
	}
	action := "add"
	home, err := carry.preflight()
	if err != nil {
		return "", err
	}
	if _, statErr := os.Stat(targetFile); statErr == nil {
		opts.say("reading %s", seatFile)
		existing, err := sealDecrypt(carry.run, opts.SopsPath, opts.KeyPath, targetFile)
		if err != nil {
			return "", err
		}
		if sealHas(existing, opts.Name) {
			action = "replace"
		}
	} else if os.IsNotExist(statErr) {
		return "", fmt.Errorf("%s does not exist in the store; %w", seatFile, errSeatFileAbsent)
	} else {
		return "", statErr
	}
	lines := []string{fmt.Sprintf("SECRETS SEAL PLAN write=%s action=%s name=%s seat=%s recipients=%s value=not read (dry run)",
		oneline.Field(targetFile), action, oneline.Field(opts.Name), oneline.Field(opts.AsName), oneline.Field(strings.Join(recipients, ",")))}
	lines = append(lines, carry.planLines("SEAL", home)...)
	lines = append(lines, fmt.Sprintf("SECRETS SEAL DRY-RUN OK name=%s seat=%s nothing written, no value read, no push, no gh call",
		oneline.Field(opts.Name), oneline.Field(opts.AsName)))
	return strings.Join(lines, "\n"), nil
}

// sealHas reports whether the decrypted seat file already holds name: the same line
// sealApply drops before it appends the new one.
func sealHas(existing []byte, name string) bool {
	return slices.ContainsFunc(strings.Split(string(existing), "\n"), func(line string) bool {
		return strings.HasPrefix(strings.TrimSpace(line), name+":")
	})
}

// readSealValue takes the value from stdin when asked or when stdin is not a terminal,
// and otherwise from the controlling terminal with echo off. A stdin value must end
// with Enter. On a pipe, bytes after that line are the multi-line refusal. On a
// terminal, Enter is the end: nothing further is read, so Ctrl-D is not required.
// EOF without a newline is Ctrl-D, and that is a refusal, not a value.
// The no-flag terminal read still accepts that EOF.
func readSealValue(opts SealOptions) (string, error) {
	var raw string
	if opts.UseStdin || !opts.StdinIsTerminal {
		r := opts.Stdin
		if r == nil {
			r = os.Stdin
		}
		br := bufio.NewReader(r)
		line, err := br.ReadString('\n')
		if errors.Is(err, io.EOF) && line != "" {
			return "", fmt.Errorf("the value must end with Enter, and Ctrl-D is not a value")
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("unable to read value from stdin: %w", err)
		}
		if err == nil && !opts.StdinIsTerminal {
			rest, rerr := io.ReadAll(br)
			if rerr != nil {
				return "", fmt.Errorf("unable to read value from stdin: %w", rerr)
			}
			if len(rest) > 0 {
				return "", fmt.Errorf("value is multi-line; a file-shaped secret is not an environment variable; generate it where it is used: this store holds no file-shaped secrets")
			}
		}
		raw = line
	} else {
		s, err := readSealFromTTY()
		if err != nil {
			return "", err
		}
		raw = s
	}

	value := strings.TrimRight(raw, "\r\n")
	if value == "" {
		return "", fmt.Errorf("empty value: refusing to seal nothing; paste a value on stdin or at the terminal")
	}
	if strings.ContainsAny(value, "\r\n") {
		return "", fmt.Errorf("value is multi-line; a file-shaped secret is not an environment variable; generate it where it is used: this store holds no file-shaped secrets")
	}
	if strings.Contains(value, "\x00") {
		return "", fmt.Errorf("value contains a NUL byte")
	}
	return value, nil
}

// readSealFromTTY opens /dev/tty twice -- one handle to write the prompt, one to read the
// value -- because a single read-write open of /dev/tty does not work on the bench.
func readSealFromTTY() (string, error) {
	w, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return "", fmt.Errorf("no controlling terminal to read the value from; run with --stdin")
	}
	// ignored: the prompt is written and its error judged below; the close is best effort
	defer func() { _ = w.Close() }()
	r, err := os.OpenFile("/dev/tty", os.O_RDONLY, 0)
	if err != nil {
		return "", fmt.Errorf("no controlling terminal to read the value from; run with --stdin")
	}
	// ignored: the read handle was opened only for reading
	defer func() { _ = r.Close() }()

	if _, err := fmt.Fprint(w, "value: "); err != nil {
		return "", err
	}
	restore := disableEcho(r)
	defer restore()

	line, err := bufio.NewReader(r).ReadString('\n')
	// ignored: the newline the disabled echo swallowed, to the terminal; the read error is judged on the next line
	_, _ = fmt.Fprintln(w)
	if err != nil && err != io.EOF {
		return "", err
	}
	return line, nil
}

// disableEcho turns terminal echo off for the read and returns the restore.
func disableEcho(tty *os.File) func() {
	if runtime.GOOS == "windows" {
		return func() {}
	}
	if err := runStty(tty, "-echo"); err != nil {
		return func() {}
	}
	// ignored: echo is restored best effort; a terminal that refuses stty shows its own state to the person at it
	return func() { _ = runStty(tty, "echo") }
}

func runStty(tty *os.File, arg string) error {
	cmd, cancel := subproc.Command(context.Background(), subproc.Tool, "stty", arg)
	defer cancel()
	cmd.Stdin = tty
	return cmd.Run()
}

// sealDecrypt reads the seat file's plaintext through a sops pipe, never a file in the
// clear. An absent seat file is the typed not-exist refusal, never an empty document:
// the decrypt is the only step that proves the caller's key opens the file, and a new
// seat's first values come from seat add (SPEC-SECRETS rule 12; tla/SecretsSeat.tla,
// Seal). Without it, a rule for the name alone would let any store key's holder write
// the file.
func sealDecrypt(run execCommand, sopsPath, keyPath, filePath string) ([]byte, error) {
	if _, err := os.Stat(filePath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%s does not exist in the store; %w", filepath.Base(filePath), errSeatFileAbsent)
		}
		return nil, err
	}
	tmpDir, err := os.MkdirTemp("", "nova-secrets-seal-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary isolation directory: %w", err)
	}
	defer func() { _ = safepath.RemoveUnder(os.TempDir(), tmpDir) }() // ignored: the temporary directory may already be gone

	out, err := run(nil, sealSopsEnv(keyPath, tmpDir), "", sopsPath, "-d", filePath)
	if err != nil {
		// The class sops's stderr names, when the seam carried it; the transcript is
		// never echoed (STEP 3, "wrong key").
		if cause := sopsDecryptFailure(keyPath, filePath, childStderr(err)); cause != "" {
			return nil, fmt.Errorf("sops failed: %s", cause)
		}
		return nil, fmt.Errorf("sops failed: exit %d (transcript withheld: run 'sops -d %s' to inspect)", exitCodeOf(err, 1), filePath)
	}
	return out, nil
}

// sealEncrypt hands the plaintext to sops on stdin, with the seat file named only through
// --filename-override so the config's own rule picks the recipients.
//
// Two things real sops insists on, which a lenient fake once hid: it needs a file
// argument even when the bytes come from stdin (/dev/stdin; without it sops exits 100,
// "no file specified"), and it finds .sops.yaml from its working directory, whose
// path_regex rules are relative to the store. So the child runs inside the store and
// the verb works from any directory the caller happens to be in.
//
// Every seat file a verb writes goes through here, and here the verb's mark is put into the
// plaintext (sealMarked), so no verb can write a seat file the gate cannot tell from a hand
// seal (SPEC-SECRETS "gate", the mark; tla/SecretsSeat.tla on sprint/md-secrets-h.w1.g1.e15,
// the MCSecretsSeatReachHandSeal config).
//
// The mark is read in the clear, so the rule must admit it. A rule that does not (one written
// before the mark existed) is given `^NOVA_SECRETS_WRITTEN_BY$` (seatMarkRule): sops is run
// against that config, from a copy in the isolation directory, and the updated .sops.yaml is
// answered as rules for the caller to write and commit beside the seat file, so the store's
// gate reviews the rule change in the same pull request. rules is nil when the rule already
// admits the mark.
func sealEncrypt(run execCommand, sopsPath, keyPath, storeDir, seatFile, verb string, plaintext []byte) (out, rules []byte, err error) {
	plaintext = sealMarked(plaintext, verb)
	updated, changed, err := seatMarkRuleIn(storeDir, seatFile)
	if err != nil {
		return nil, nil, err
	}
	tmpDir, err := os.MkdirTemp("", "nova-secrets-seal-*")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create temporary isolation directory: %w", err)
	}
	defer func() { _ = safepath.RemoveUnder(os.TempDir(), tmpDir) }() // ignored: the temporary directory may already be gone

	dir := storeDir
	if changed {
		// sops finds .sops.yaml from its working directory and matches path_regex against
		// the --filename-override name, so a copy beside nothing else picks the same rule.
		if err := os.WriteFile(filepath.Join(tmpDir, ".sops.yaml"), updated, 0o600); err != nil {
			return nil, nil, fmt.Errorf("failed to write the rule that admits the mark: %w", err)
		}
		dir, rules = tmpDir, updated
	}
	out, err = run(bytes.NewReader(plaintext), sealSopsEnv(keyPath, tmpDir), dir, sopsPath,
		"-e", "--filename-override", seatFile, "--input-type", "yaml", "--output-type", "yaml", "/dev/stdin")
	if err != nil {
		exitCode := exitCodeOf(err, 1)
		return nil, nil, fmt.Errorf("sops encrypt failed: exit %d (transcript withheld; the value is on stdin only); the usual cause is a recipient in the .sops.yaml rule for %s that is not an age1… public key, such as keygen's <recovery key> placeholder left in; the same call with --dry-run lists the recipients", exitCode, seatFile)
	}
	return out, rules, nil
}

// SeatMarkKey is the root key every verb-written seat file carries in the clear. A rule that
// lists it in `unencrypted_regex` keeps sops from sealing it, and the gate reads it.
const SeatMarkKey = "NOVA_SECRETS_WRITTEN_BY"

// seatMarkValue is the only value the mark may hold in the clear: the verb that wrote the
// file and the tool's version. Any other cleartext under the mark key is a plain value,
// refused by the gate, check and seat inject whatever the rule's unencrypted_regex says
// (SPEC-SECRETS "gate", the mark).
var seatMarkValue = regexp.MustCompile(`^(seal|seat add|seat inject) ` + markVersionPattern + `$`)

// markVersionPattern is a release tag or "dev", and nothing a value could ride in: a
// shape check alone let 256 bits of hex pass as a version, an unbounded suffix did the
// same after a tag (the second and third reads of 2026-10-06), and an unbounded numeric
// component let eighty digits ride in the clear (the read of 2026-10-07). Each component
// is capped at four digits, the most a real release tag holds.
const markVersionPattern = `(dev|v?\d{1,4}\.\d{1,4}\.\d{1,4}(-rc\d{1,3})?)`

// markVersion is MarkVersion when it is a version the gate reads as one, and "dev"
// otherwise: a verb never writes a mark its own gate refuses.
func markVersion() string {
	if regexp.MustCompile(`^` + markVersionPattern + `$`).MatchString(MarkVersion) {
		return MarkVersion
	}
	return "dev"
}

// isSeatMark reports whether val, as it stands after the key's colon, is a verb's mark. A
// value in matching quotes is read inside them: sops may quote what it writes.
func isSeatMark(val string) bool {
	val = strings.TrimSpace(val)
	if n := len(val); n >= 2 && (val[0] == '"' || val[0] == '\'') && val[n-1] == val[0] {
		val = val[1 : n-1]
	}
	return seatMarkValue.MatchString(val)
}

// MarkVersion is the tool version the mark names; the command sets it from its build stamp.
var MarkVersion = "dev"

// sealMarked returns plaintext with its one mark line for verb ("seal", "seat add" or
// "seat inject"): an older mark is dropped, so a re-seal states the verb that wrote it now.
// A mark is not a signature; the spec says what it catches and what it does not
// (SPEC-SECRETS "gate").
func sealMarked(plaintext []byte, verb string) []byte {
	var b strings.Builder
	for _, line := range strings.SplitAfter(string(plaintext), "\n") {
		if strings.HasPrefix(line, SeatMarkKey+":") {
			continue
		}
		b.WriteString(line)
	}
	out := b.String()
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return []byte(out + SeatMarkKey + ": " + verb + " " + markVersion() + "\n")
}

// sealApply drops any existing --name line and appends the new one.
func sealApply(existing []byte, name, value string) []byte {
	var b strings.Builder
	if len(existing) > 0 {
		for _, line := range strings.Split(string(existing), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), name+":") {
				continue
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	b.WriteString(name + ": " + yamlSingleQuote(value) + "\n")
	return []byte(b.String())
}

func sealSopsEnv(keyPath, tmpDir string) []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"SOPS_AGE_KEY_FILE=" + keyPath,
		"HOME=" + tmpDir,
		"XDG_CONFIG_HOME=" + tmpDir,
	}
	if tmp := os.Getenv("TMPDIR"); tmp != "" {
		env = append(env, "TMPDIR="+tmp)
	}
	if sysroot := os.Getenv("SYSTEMROOT"); sysroot != "" {
		env = append(env, "SYSTEMROOT="+sysroot)
	}
	return env
}

func sealGit(run execCommand, dir, gitPath string, args ...string) error {
	_, err := sealGitOutput(run, dir, gitPath, args...)
	return err
}

func sealGitOutput(run execCommand, dir, gitPath string, args ...string) (string, error) {
	out, err := run(nil, append(gitrun.WithoutRepoVars(os.Environ()), "GIT_TERMINAL_PROMPT=0"), dir, gitPath, args...)
	if err != nil {
		return "", fmt.Errorf("git %s failed: %s (transcript withheld)", args[0], oneline.Err(err))
	}
	return strings.TrimSpace(string(out)), nil
}

func sealGH(run execCommand, ghPath, dir string, args ...string) (string, error) {
	out, err := run(nil, append(os.Environ(), "GH_PROMPT_DISABLED=1"), dir, ghPath, args...)
	if err != nil {
		verb := strings.Join(args[:min(2, len(args))], " ")
		return "", fmt.Errorf("gh %s failed: %s (transcript withheld)", verb, oneline.Err(err))
	}
	return string(out), nil
}

// atomicWriteFile writes the ciphertext beside the target and renames it into place.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	if err := atomicfile.Write(filepath.Clean(path), data, perm, atomicfile.ExactMode()); err != nil {
		return fmt.Errorf("unable to write %s: %w", path, err)
	}
	return nil
}

type sealPRView struct {
	State             string          `json:"state"`
	ReviewDecision    string          `json:"reviewDecision"`
	StatusCheckRollup []sealCheckItem `json:"statusCheckRollup"`
}

type sealCheckItem struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

// classifySealReview classifies the review state of a seal pull request
// according to SPEC-SECRETS seal. Terminal states (changes-requested, closed,
// merged elsewhere, terminal check failure) stop immediately with a redacted
// one-line error carrying a remedy. Approved continues to the merge path, and
// pending reviews poll to the bounded deadline.
func classifySealReview(raw, prNum string) (error, bool) {
	trimmed := strings.TrimSpace(raw)
	var view sealPRView
	if strings.HasPrefix(trimmed, "{") {
		if err := json.Unmarshal([]byte(trimmed), &view); err != nil {
			return fmt.Errorf("pull request #%s review query response invalid: %s; run: gh pr view %s", prNum, oneline.Err(err), prNum), false
		}
	} else {
		switch strings.ToUpper(trimmed) {
		case "APPROVED":
			view.State = "OPEN"
			view.ReviewDecision = "APPROVED"
		case "CHANGES_REQUESTED":
			view.State = "OPEN"
			view.ReviewDecision = "CHANGES_REQUESTED"
		case "CLOSED":
			view.State = "CLOSED"
		case "MERGED":
			view.State = "MERGED"
		case "FAILURE", "FAILED", "CHECK_FAILURE":
			view.State = "OPEN"
			view.StatusCheckRollup = []sealCheckItem{{Name: "gate", Conclusion: "FAILURE"}}
		case "REVIEW_REQUIRED", "PENDING", "":
			view.State = "OPEN"
			view.ReviewDecision = "REVIEW_REQUIRED"
		default:
			view.State = "OPEN"
			view.ReviewDecision = trimmed
		}
	}

	if strings.EqualFold(view.State, "CLOSED") {
		return fmt.Errorf("pull request #%s was closed without merging; run: gh pr reopen %s", prNum, prNum), false
	}
	if strings.EqualFold(view.State, "MERGED") {
		return fmt.Errorf("pull request #%s was merged elsewhere; run: git pull", prNum), false
	}

	for _, check := range view.StatusCheckRollup {
		conc := strings.ToUpper(check.Conclusion)
		st := strings.ToUpper(check.State)
		if conc == "FAILURE" || conc == "TIMED_OUT" || conc == "CANCELLED" || conc == "STARTUP_FAILURE" || conc == "ACTION_REQUIRED" ||
			st == "FAILURE" || st == "ERROR" {
			name := check.Name
			if name == "" {
				name = "gate"
			}
			return fmt.Errorf("pull request #%s check %s failed; run: gh pr checks %s", prNum, name, prNum), false
		}
	}

	if strings.EqualFold(view.ReviewDecision, "CHANGES_REQUESTED") {
		return fmt.Errorf("pull request #%s changes requested; run: gh pr view %s", prNum, prNum), false
	}

	if strings.EqualFold(view.ReviewDecision, "APPROVED") {
		return nil, true
	}

	return nil, false
}
