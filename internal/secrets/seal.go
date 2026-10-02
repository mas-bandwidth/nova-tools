package secrets

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// execCommand runs one helper process and returns its stdout. The encrypt step
// takes it as a parameter so a test can supply a pure-Go fake on every platform
// instead of a shell script Windows cannot execute.
type execCommand func(stdin io.Reader, env []string, dir, name string, args ...string) ([]byte, error)

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
	return out.Bytes(), err
}

// say writes one progress line; never a value, only step names and public facts.
func (o SealOptions) say(format string, a ...interface{}) {
	if o.Progress != nil {
		fmt.Fprintf(o.Progress, "seal: "+format+"\n", a...)
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

	if err := CheckInvariant6(opts.KeyPath); err != nil {
		return "", err
	}
	if _, err := CheckSopsVersion(opts.SopsPath); err != nil {
		return "", err
	}

	run := opts.exec()
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
	}

	if opts.DryRun {
		return sealDryRun(opts, carry, targetFile)
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
	ciphertext, err := sealEncrypt(run, opts.SopsPath, opts.KeyPath, opts.StoreDir, seatFile, plaintext)
	if err != nil {
		return "", err
	}

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
	seatFile string // the one file the commit touches
	branch   string // seal/<seat>-<NAMES>-<stamp>
	message  string // the commit message
	title    string // the pull request title
	body     string // the pull request body
	noPR     bool
	say      func(format string, a ...interface{})
	check    func() error // runs after the merge and the pull
}

// carry writes the ciphertext into place on a fresh branch and carries it to
// the store. It returns the pull request number ("" under noPR) and whether the
// request merged. On every path out the store is back on the branch it was on
// (#2016); the commit stays on c.branch.
func (c sealCarry) carry(ciphertext []byte) (prNum string, merged bool, err error) {
	say := c.say
	if say == nil {
		say = func(string, ...interface{}) {}
	}
	home, err := c.preflight()
	if err != nil {
		return "", false, err
	}

	say("committing on branch %s", c.branch)
	if err := sealGit(c.run, c.storeDir, c.gitPath, "checkout", "-b", c.branch); err != nil {
		return "", false, err
	}
	// #2016: restore the starting branch on every path; the commit stays on c.branch.
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
	started := time.Now()
	deadline := started.Add(2 * time.Minute)
	lastSaid := started
	say("pull request #%s is open; waiting for the gate's approval (up to 2 min)", prNum)
	for {
		view, err := sealGH(c.run, c.ghPath, c.storeDir, "pr", "view", prNum, "--json", "reviewDecision", "--jq", ".reviewDecision")
		if err != nil {
			return "", false, err
		}
		if strings.TrimSpace(view) == "APPROVED" {
			approved = true
			break
		}
		if time.Now().After(deadline) {
			break
		}
		if time.Since(lastSaid) >= 15*time.Second {
			say("still waiting for approval (%ds)", int(time.Since(started).Seconds()))
			lastSaid = time.Now()
		}
		time.Sleep(5 * time.Second)
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
func sealDryRun(opts SealOptions, carry sealCarry, targetFile string) (string, error) {
	seatFile := carry.seatFile
	recipients, err := sealRecipients(opts.StoreDir, seatFile)
	if err != nil {
		return "", err
	}
	action := "add"
	if _, statErr := os.Stat(targetFile); statErr == nil {
		opts.say("reading %s", seatFile)
		existing, err := sealDecrypt(carry.run, opts.SopsPath, opts.KeyPath, targetFile)
		if err != nil {
			return "", err
		}
		if sealHas(existing, opts.Name) {
			action = "replace"
		}
	}
	home, err := carry.preflight()
	if err != nil {
		return "", err
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
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), name+":") {
			return true
		}
	}
	return false
}

// readSealValue takes the value from stdin when asked or when stdin is not a terminal,
// and otherwise from the controlling terminal with echo off.
func readSealValue(opts SealOptions) (string, error) {
	var raw string
	if opts.UseStdin || !opts.StdinIsTerminal {
		r := opts.Stdin
		if r == nil {
			r = os.Stdin
		}
		b, err := io.ReadAll(r)
		if err != nil {
			return "", fmt.Errorf("unable to read value from stdin: %w", err)
		}
		raw = string(b)
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
		return "", fmt.Errorf("value is multi-line; a file-shaped secret is not an environment variable.\n  generate it where it is used: this store holds no file-shaped secrets.")
	}
	if strings.Contains(value, "\x00") {
		return "", fmt.Errorf("value contains a NUL byte")
	}
	return value, nil
}

// readSealFromTTY opens /dev/tty twice -- one handle to write the prompt, one to read the
// value -- because a single read-write open did not work on the Stella bench.
func readSealFromTTY() (string, error) {
	w, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return "", fmt.Errorf("no controlling terminal to read the value from; run with --stdin")
	}
	defer w.Close()
	r, err := os.OpenFile("/dev/tty", os.O_RDONLY, 0)
	if err != nil {
		return "", fmt.Errorf("no controlling terminal to read the value from; run with --stdin")
	}
	defer r.Close()

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
// clear. An absent seat file starts from nothing, so a new name can be added.
func sealDecrypt(run execCommand, sopsPath, keyPath, filePath string) ([]byte, error) {
	if _, err := os.Stat(filePath); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	tmpDir, err := os.MkdirTemp("", "nova-secrets-seal-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary isolation directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	out, err := run(nil, sealSopsEnv(keyPath, tmpDir), "", sopsPath, "-d", filePath)
	if err != nil {
		exitCode := 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
		return nil, fmt.Errorf("sops failed: exit %d (transcript withheld: run 'sops -d %s' to inspect)", exitCode, filePath)
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
func sealEncrypt(run execCommand, sopsPath, keyPath, storeDir, seatFile string, plaintext []byte) ([]byte, error) {
	tmpDir, err := os.MkdirTemp("", "nova-secrets-seal-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary isolation directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	out, err := run(bytes.NewReader(plaintext), sealSopsEnv(keyPath, tmpDir), storeDir, sopsPath,
		"-e", "--filename-override", seatFile, "--input-type", "yaml", "--output-type", "yaml", "/dev/stdin")
	if err != nil {
		exitCode := 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
		return nil, fmt.Errorf("sops encrypt failed: exit %d (transcript withheld; the value is on stdin only); the usual cause is a recipient in the .sops.yaml rule for %s that is not an age1… public key, such as keygen's <recovery key> placeholder left in; the same call with --dry-run lists the recipients", exitCode, seatFile)
	}
	return out, nil
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
	b.WriteString(name + ": " + value + "\n")
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
