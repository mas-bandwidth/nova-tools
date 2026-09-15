package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// THE NATIVE OPENCODE EXECUTION PATH (issue #296, slice 2). A frozen run
// configuration is a set of fields the caller hands over complete; nothing in it
// is derived on this side, and the run starts exactly one child bound to exactly
// those fields. This is the path a native `opencode` binary executes on, not the
// legacy runner, which passes a prompt FILE to a harness selected by a worker
// description. Here the model, the card text, the auth copy, the slot and the
// deadline are all in the configuration, and the child is
// `<binary> run --model <provider/model> --title <label> -- <card text>`.

// nativeRunConfig is the frozen configuration of one native run.
type nativeRunConfig struct {
	binary   string        // the harness binary path, resolved once
	model    string        // provider/model, one slash, both sides nonempty
	label    string        // the --title this run is labelled with
	card     []byte        // the card text, passed as the message, byte-for-byte
	slotDir  string        // the slot directory; HOME is a data dir beneath it
	root     string        // the configured root the slot directory must sit under
	authFile string        // optional: an auth file to copy one entry out of
	deadline time.Duration // the wall bound that kills the child
	repos    []string      // repositories a card may clone (owner/name): network to
	// github.com only, expressed as a wall host rule
	recipients []string // bus lanes a card may address; default none, and a bus
	// send is denied inside the wall regardless
	sandbox string // the nova-sandbox binary naming the wall; "" = no wall
}

// nativeRunResult is what one run records when the child has gone.
type nativeRunResult struct {
	rc           int     // the child's exit code; -1 when the deadline killed it
	wallSeconds  float64 // the wall the run took
	cardSHA256   string  // sha256 of the card text, lowercase hex
	binarySHA256 string  // sha256 of the harness binary, lowercase hex
}

// nativeRun executes one frozen configuration and returns the recorded result and
// the command's exit code: 0 the child ran, 2 a refusal (one REFUSED line on
// errOut). A refusal is a defect in the configuration the run can see before it
// spends anything, and it names one reason.
func nativeRun(cfg nativeRunConfig, errOut io.Writer) (nativeRunResult, int) {
	// (1) THE BINARY. Resolved once, on PATH when the name has no separator, then
	// checked for existence and the execute bit. A missing binary and an
	// unexecutable one are the same refusal class, one line each.
	bin := cfg.binary
	if !strings.ContainsRune(bin, filepath.Separator) {
		found, err := exec.LookPath(cfg.binary)
		if err != nil {
			refuseNative(errOut, fmt.Sprintf("the harness binary %s is missing", oneline.Field(cfg.binary)))
			return nativeRunResult{}, 2
		}
		bin = found
	}
	st, err := os.Stat(bin)
	if err != nil {
		refuseNative(errOut, fmt.Sprintf("the harness binary %s is missing", oneline.Field(bin)))
		return nativeRunResult{}, 2
	}
	if st.IsDir() || st.Mode().Perm()&0o111 == 0 {
		refuseNative(errOut, fmt.Sprintf("the harness binary %s is not executable", oneline.Field(bin)))
		return nativeRunResult{}, 2
	}

	// (2) THE MODEL. Native routes are named provider/model, and a model id with no
	// provider prefix cannot be given to any harness. providerOf splits on the one
	// slash and refuses a missing side or a slash inside the provider.
	provider, ok := providerOf(cfg.model)
	if !ok {
		refuseNative(errOut, fmt.Sprintf("the model %q has no provider prefix (a native model is provider/model, one slash, both sides nonempty)", cfg.model))
		return nativeRunResult{}, 2
	}

	// (3) THE SLOT IS UNDER THE ROOT. A slot outside the configured root is a write
	// this run has no business making, and it refuses before any directory is made.
	if !within(cfg.root, cfg.slotDir) {
		refuseNative(errOut, fmt.Sprintf("the slot directory %s is outside the configured root %s",
			oneline.Field(cfg.slotDir), oneline.Field(cfg.root)))
		return nativeRunResult{}, 2
	}

	// HOME is a data directory under the slot directory; the child is pointed at it
	// and nothing above it.
	dataHome := filepath.Join(cfg.slotDir, "data")
	if err := os.MkdirAll(dataHome, 0o755); err != nil {
		refuseNative(errOut, fmt.Sprintf("the data directory %s could not be made: %s", oneline.Field(dataHome), oneline.Escape(err.Error())))
		return nativeRunResult{}, 2
	}

	// (4) THE AUTH COPY. One entry, the model's provider's, moved to the data home so
	// the child's account resolves, and left mode 0600. A source that is looser than
	// 0600 is refused: its copy would spread a secret further than its owner.
	if cfg.authFile != "" {
		if reason := copyAuth(cfg.authFile, provider, dataHome); reason != "" {
			refuseNative(errOut, reason)
			return nativeRunResult{}, 2
		}
	}

	// The two hashes are recorded from the same bytes the run is about to use, so a
	// caller can prove later that neither the card nor the binary changed under it.
	binaryHash, _ := fileSHA256(bin)
	cardHash := sha256.Sum256(cfg.card)

	// (5) THE WALL (slice 11). When a wall is named the child runs inside nova-sandbox,
	// and the two lists a run may carry -- repos a card may clone, recipients a card may
	// address -- are the wall's allow rules. A repo is network to github.com only, which is
	// a HOST rule; a wall that cannot express it is a refusal (`wall cannot express repo
	// rule`), never an unwalled run. A recipient is never expressed: a bus send is denied
	// inside the wall by construction (no nova-bus on PATH, no bus checkout in the write
	// set), so the default of none is what the wall enforces and no allow rule is built.
	runPath := bin
	runArgv := []string{"run", "--model", cfg.model, "--title", cfg.label, "--", string(cfg.card)}
	if cfg.sandbox != "" {
		if len(cfg.repos) > 0 && !sandboxHostRules(cfg.sandbox) {
			refuseNative(errOut, fmt.Sprintf("%s wall cannot express repo rule", oneline.Field(cfg.label)))
			return nativeRunResult{}, 2
		}
		runPath = cfg.sandbox
		runArgv = nativeSandboxArgv(bin, cfg, dataHome)
	} else if len(cfg.repos) > 0 {
		refuseNative(errOut, fmt.Sprintf("%s wall cannot express repo rule", oneline.Field(cfg.label)))
		return nativeRunResult{}, 2
	}

	// THE CHILD. The deadline is a context, so the process (and any it started in its
	// own group) is killed when the wall runs out, not merely handed a suggestion.
	ctx, cancel := context.WithTimeout(context.Background(), cfg.deadline)
	defer cancel()
	cmd := exec.CommandContext(ctx, runPath, runArgv...)
	cmd.Env = append(os.Environ(),
		"HOME="+dataHome,
		"XDG_DATA_HOME="+dataHome,
		"NOVA_SWARM_JOB="+cfg.slotDir,
	)
	cmd.Dir = cfg.slotDir
	cmd.Stdin = strings.NewReader("")
	log, err := os.OpenFile(filepath.Join(cfg.slotDir, "native.log"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		refuseNative(errOut, fmt.Sprintf("the run log %s could not be opened: %s", oneline.Field(filepath.Join(cfg.slotDir, "native.log")), oneline.Escape(err.Error())))
		return nativeRunResult{}, 2
	}
	cmd.Stdout, cmd.Stderr = log, log

	res := nativeRunResult{
		rc:           -1,
		cardSHA256:   hex.EncodeToString(cardHash[:]),
		binarySHA256: binaryHash,
	}
	start := time.Now()
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			res.rc = ee.ExitCode()
		}
	} else {
		res.rc = 0
	}
	res.wallSeconds = time.Since(start).Seconds()
	log.Close()
	return res, 0
}

// refuseNative writes the one REFUSED line the run owes its caller.
func refuseNative(w io.Writer, reason string) {
	fmt.Fprintf(w, "NATIVE REFUSED: %s\n", oneline.Escape(reason))
}

// sandboxHostRules asks the wall, once, whether it can express a repo allow rule: network
// to github.com for the named repositories is a HOST rule, and the wall's `check` verb says
// so with `hosts=enforceable`. Anything else -- a check that will not run, or a line without
// that token -- is a wall that cannot express the rule, and the run refuses rather than run
// the card unwalled (SPEC-SANDBOX rule 1 and rule 11).
func sandboxHostRules(sandbox string) bool {
	out, err := exec.Command(sandbox, "check").CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "hosts=enforceable")
}

// nativeSandboxArgv is the wrap for a native run: the wall's flags, then --, then the
// harness verbatim (SPEC-SANDBOX rule 12). The job directory is the first --write and the
// --cwd (rule 13); the data home is the second --write and the child's HOME (rule 9); the
// slot directory is the read set. Each repo the card named is a --repo allow rule, and a
// recipient never appears: a bus send is denied by the wall itself, not granted by the
// caller, so no allow rule is ever built for one.
func nativeSandboxArgv(bin string, cfg nativeRunConfig, dataHome string) []string {
	argv := []string{
		"--read", cfg.slotDir,
		"--write", cfg.slotDir,
		"--write", dataHome,
		"--cwd", cfg.slotDir,
	}
	for _, r := range cfg.repos {
		argv = append(argv, "--repo", r)
	}
	argv = append(argv, "--")
	argv = append(argv, bin, "run", "--model", cfg.model, "--title", cfg.label, "--", string(cfg.card))
	return argv
}

// providerOf splits a native model id on its single slash and reports whether it
// has a believable provider prefix: both sides nonempty, no slash inside the
// provider.
func providerOf(model string) (string, bool) {
	i := strings.Index(model, "/")
	if i <= 0 || i >= len(model)-1 {
		return "", false
	}
	provider := model[:i]
	if strings.Contains(provider, "/") {
		return "", false
	}
	return provider, true
}

// within reports whether path sits at or under root, lexically, without touching the
// filesystem, so a missing slot can still be judged.
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// copyAuth moves exactly the provider's entry from the auth file into
// dataHome/auth.json, mode 0600, and returns the refusal reason when the source is
// looser than 0600 or the copy cannot end 0600.
func copyAuth(src, provider, dataHome string) string {
	st, err := os.Stat(src)
	if err != nil {
		return fmt.Sprintf("the auth file %s could not be read: %s", oneline.Field(src), oneline.Escape(err.Error()))
	}
	if st.Mode().Perm()&0o077 != 0 {
		return fmt.Sprintf("the auth copy would not be 0600: the auth file %s is mode %04o, so copying it spreads a secret beyond its owner; chmod 600 it first",
			oneline.Field(src), st.Mode().Perm())
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		return fmt.Sprintf("the auth file %s could not be read: %s", oneline.Field(src), oneline.Escape(err.Error()))
	}
	var entries map[string]any
	if err := json.Unmarshal(raw, &entries); err != nil {
		return fmt.Sprintf("the auth file %s is not one JSON object of provider entries: %s", oneline.Field(src), oneline.Escape(err.Error()))
	}
	one := map[string]any{}
	if v, ok := entries[provider]; ok {
		one[provider] = v
	}
	body, _ := json.Marshal(one)
	dst := filepath.Join(dataHome, "auth.json")
	if err := os.WriteFile(dst, body, 0o600); err != nil {
		return fmt.Sprintf("the auth copy %s could not be written: %s", oneline.Field(dst), oneline.Escape(err.Error()))
	}
	if dstSt, err := os.Stat(dst); err == nil && dstSt.Mode().Perm() != 0o600 {
		return fmt.Sprintf("the auth copy would not be 0600: %s ended mode %04o", oneline.Field(dst), dstSt.Mode().Perm())
	}
	ocDir := filepath.Join(dataHome, "opencode")
	if err := os.MkdirAll(ocDir, 0o755); err == nil {
		_ = os.WriteFile(filepath.Join(ocDir, "auth.json"), body, 0o600)
	}
	return ""
}

// fileSHA256 returns the lowercase hex sha256 of a file's bytes.
func fileSHA256(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
