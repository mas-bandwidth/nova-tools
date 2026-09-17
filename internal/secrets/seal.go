package secrets

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// execCommand runs one helper process and returns its stdout. The encrypt step
// takes it as a parameter so a test can supply a pure-Go fake on every platform
// instead of a shell script Windows cannot execute.
type execCommand func(stdin io.Reader, env []string, dir, name string, args ...string) ([]byte, error)

func realExecCommand(stdin io.Reader, env []string, dir, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = env
	cmd.Dir = dir
	cmd.Stdin = stdin
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err := cmd.Run()
	return out.Bytes(), err
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

	Stdin           io.Reader
	StdinIsTerminal bool

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
func RunSeal(opts SealOptions) (string, error) {
	if opts.StoreDir == "" {
		return "", fmt.Errorf("missing --store <dir>")
	}
	if opts.AsName == "" {
		return "", fmt.Errorf("missing --as <name>")
	}
	if !IsValidAsName(opts.AsName) {
		return "", fmt.Errorf("invalid seat name %q: must match [A-Za-z0-9_-]+", opts.AsName)
	}
	if opts.KeyPath == "" {
		return "", fmt.Errorf("missing --key <path>")
	}
	if opts.SopsPath == "" {
		return "", fmt.Errorf("missing --sops <path>")
	}
	if opts.Name == "" {
		return "", fmt.Errorf("missing --name <NAME>")
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

	sFi, err := os.Stat(opts.StoreDir)
	if err != nil || !sFi.IsDir() {
		return "", fmt.Errorf("store %s is not a directory", opts.StoreDir)
	}
	gitDir := filepath.Join(opts.StoreDir, ".git")
	gFi, err := os.Stat(gitDir)
	if err != nil || !gFi.IsDir() {
		return "", fmt.Errorf("store %s has no .git directory; clone it: git clone <url> %s", opts.StoreDir, opts.StoreDir)
	}
	if _, err := os.Stat(filepath.Join(opts.StoreDir, ".sops.yaml")); err != nil {
		return "", fmt.Errorf("store %s carries no .sops.yaml", opts.StoreDir)
	}
	if err := CheckInvariant6(opts.KeyPath); err != nil {
		return "", err
	}
	if _, err := CheckSopsVersion(opts.SopsPath); err != nil {
		return "", err
	}

	value, err := readSealValue(opts)
	if err != nil {
		return "", err
	}

	run := opts.exec()
	seatFile := opts.AsName + ".yaml"
	targetFile := filepath.Join(opts.StoreDir, seatFile)

	existing, err := sealDecrypt(run, opts.SopsPath, opts.KeyPath, targetFile)
	if err != nil {
		return "", err
	}
	plaintext := sealApply(existing, opts.Name, value)

	ciphertext, err := sealEncrypt(run, opts.SopsPath, opts.KeyPath, seatFile, plaintext)
	if err != nil {
		return "", err
	}
	if err := atomicWriteFile(targetFile, ciphertext, 0600); err != nil {
		return "", err
	}

	branch := fmt.Sprintf("seal/%s-%s-%s", opts.AsName, opts.Name, opts.Now().UTC().Format("20060102-150405"))
	if err := sealGit(run, opts.StoreDir, opts.GitPath, "checkout", "-b", branch); err != nil {
		return "", err
	}
	if err := sealGit(run, opts.StoreDir, opts.GitPath, "add", seatFile); err != nil {
		return "", err
	}
	commitMsg := fmt.Sprintf("seal %s into %s", opts.Name, seatFile)
	if err := sealGit(run, opts.StoreDir, opts.GitPath, "commit", "-m", commitMsg); err != nil {
		return "", err
	}

	if opts.NoPR {
		return fmt.Sprintf("SECRETS SEAL OK name=%s seat=%s committed",
			oneline.Field(opts.Name), oneline.Field(opts.AsName)), nil
	}

	if err := sealGit(run, opts.StoreDir, opts.GitPath, "push", "-u", "origin", branch); err != nil {
		return "", err
	}
	title := fmt.Sprintf("seal %s into %s", opts.Name, seatFile)
	body := "Sealed with nova-secrets seal. The value was never written to a file in the clear, to argv, or to output."
	createOut, err := sealGH(run, opts.GHPath, opts.StoreDir, "pr", "create", "--head", branch, "--title", title, "--body", body)
	if err != nil {
		return "", err
	}
	prNum := ""
	if m := prNumberRegex.FindStringSubmatch(createOut); len(m) > 1 {
		prNum = m[1]
	}
	if prNum == "" {
		return "", fmt.Errorf("gh pr create did not return a pull request number")
	}

	approved := false
	deadline := time.Now().Add(2 * time.Minute)
	for {
		view, err := sealGH(run, opts.GHPath, opts.StoreDir, "pr", "view", prNum, "--json", "reviewDecision", "--jq", ".reviewDecision")
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(view) == "APPROVED" {
			approved = true
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Second)
	}
	if !approved {
		return fmt.Sprintf("SECRETS SEAL OK name=%s seat=%s pr=#%s open (gate not yet approved)",
			oneline.Field(opts.Name), oneline.Field(opts.AsName), prNum), nil
	}

	if _, err := sealGH(run, opts.GHPath, opts.StoreDir, "pr", "merge", prNum, "--squash"); err != nil {
		return "", err
	}
	if err := sealGit(run, opts.StoreDir, opts.GitPath, "pull"); err != nil {
		return "", err
	}

	checkFn := opts.Check
	if checkFn == nil {
		checkFn = func(storeDir, asName, keyPath, sopsPath string) error {
			_, _, _, _, code, err := RunCheck(storeDir, asName, keyPath, sopsPath, 20)
			if err != nil {
				return err
			}
			if code != 0 {
				return fmt.Errorf("check failed on %s after seal", asName)
			}
			return nil
		}
	}
	if err := checkFn(opts.StoreDir, opts.AsName, opts.KeyPath, opts.SopsPath); err != nil {
		return "", err
	}

	return fmt.Sprintf("SECRETS SEAL OK name=%s seat=%s pr=#%s merged",
		oneline.Field(opts.Name), oneline.Field(opts.AsName), prNum), nil
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
	return func() { _ = runStty(tty, "echo") }
}

func runStty(tty *os.File, arg string) error {
	cmd := exec.Command("stty", arg)
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
func sealEncrypt(run execCommand, sopsPath, keyPath, seatFile string, plaintext []byte) ([]byte, error) {
	tmpDir, err := os.MkdirTemp("", "nova-secrets-seal-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary isolation directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	out, err := run(bytes.NewReader(plaintext), sealSopsEnv(keyPath, tmpDir), "", sopsPath,
		"-e", "--filename-override", seatFile, "--input-type", "yaml", "--output-type", "yaml")
	if err != nil {
		exitCode := 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
		return nil, fmt.Errorf("sops encrypt failed: exit %d (transcript withheld; the value is on stdin only)", exitCode)
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
	_, err := run(nil, append(os.Environ(), "GIT_TERMINAL_PROMPT=0"), dir, gitPath, args...)
	if err != nil {
		return fmt.Errorf("git %s failed: %s (transcript withheld)", args[0], oneline.Err(err))
	}
	return nil
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
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".nova-seal-*.tmp")
	if err != nil {
		return fmt.Errorf("unable to create temporary file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("unable to write %s: %w", path, err)
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return fmt.Errorf("unable to chmod %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("unable to sync %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("unable to close %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("unable to rename into %s: %w", path, err)
	}
	cleanup = false
	return nil
}
