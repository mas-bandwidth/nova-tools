package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
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
	sandbox    string // the nova-sandbox binary naming the wall; "" = resolve on PATH
	noWall     bool   // the caller typed --no-wall: run with no containment, named by its OK line
	configFile string // optional: an opencode.json provider config copied beside the auth copy
}

// nativeRunResult is what one run records when the child has gone.
type nativeRunResult struct {
	rc           int     // the child's exit code; -1 when the deadline killed it
	wallSeconds  float64 // the wall the run took
	wall         string  // the wall's own name from its SANDBOX OK line, or "none"
	cardSHA256   string  // sha256 of the card text, lowercase hex
	binarySHA256 string  // sha256 of the harness binary, lowercase hex
	job          string  // the job directory <slot>/jobs/<label> the child ran in
	usageState   string  // the store path the NATIVE OK line names when no store answered, "" otherwise
	usageReason  string  // no-rows | no-store | no-sqlite3, "" when the store answered
	configSHA    string  // sha8 of the carried provider config, "" when --config named none
	tmp          string  // the TMPDIR the child was handed, <slot>/tmp/<label>, never a repo
}

// nativeRun executes one frozen configuration and returns the recorded result and
// the command's exit code: 0 the child ran, 2 a refusal (one REFUSED line on
// errOut). A refusal is a defect in the configuration the run can see before it
// spends anything, and it names one reason.
func nativeRun(cfg nativeRunConfig, errOut io.Writer) (nativeRunResult, int) {
	// (0) ABSOLUTE PATHS. The slot and the root are turned absolute AND symlink-resolved at
	// admission so a relative spelling cannot reach the wall (which refuses `--read ./x` and
	// `--write x/...`), and so the run's own paths cannot disagree with each other: on darwin
	// `/var` is a symlink to `/private/var`, so an absolute spelling and a relative one of one
	// directory came out as two different names (issue #578).
	abslot, err := swarm.AbsResolved(cfg.slotDir)
	if err != nil {
		refuseNative(errOut, fmt.Sprintf("the slot directory %s could not be made absolute: %s", oneline.Field(cfg.slotDir), oneline.Escape(err.Error())))
		return nativeRunResult{}, 2
	}
	cfg.slotDir = abslot
	absroot, err := swarm.AbsResolved(cfg.root)
	if err != nil {
		refuseNative(errOut, fmt.Sprintf("the configured root %s could not be made absolute: %s", oneline.Field(cfg.root), oneline.Escape(err.Error())))
		return nativeRunResult{}, 2
	}
	cfg.root = absroot

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

	// The job directory is where the child runs and writes: <slot>/jobs/<label>, made here
	// before the child starts, so the card's cwd exists and the card is told its place by
	// that cwd (SPEC-SWARM rule 13). HOME is a data directory under the slot directory; the
	// child is pointed at it and nothing above it.
	jobDir := filepath.Join(cfg.slotDir, "jobs", cfg.label)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		refuseNative(errOut, fmt.Sprintf("the job directory %s could not be made: %s", oneline.Field(jobDir), oneline.Escape(err.Error())))
		return nativeRunResult{}, 2
	}
	dataHome := filepath.Join(cfg.slotDir, "data")
	if err := os.MkdirAll(dataHome, 0o755); err != nil {
		refuseNative(errOut, fmt.Sprintf("the data directory %s could not be made: %s", oneline.Field(dataHome), oneline.Escape(err.Error())))
		return nativeRunResult{}, 2
	}
	// TMPDIR is the slot's own tmp/<label>, never the job directory (which admission git-inits
	// into a repo): a card's temp dir inside a repo is exactly what makes nova-wake's
	// TestAwakeRefusesNonBus fail for a reason the card did not cause (#460). The slot
	// directory is never a repo, so a temp file made here sits outside every repository the
	// card's work could touch. It is made here so the child's TMPDIR exists before it starts.
	tmpDir := filepath.Join(cfg.slotDir, "tmp", cfg.label)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		refuseNative(errOut, fmt.Sprintf("the temp directory %s could not be made: %s", oneline.Field(tmpDir), oneline.Escape(err.Error())))
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

	// (4b) THE PROVIDER CONFIG (issue #465). The clean env carries the provider's auth entry
	// into the job's own XDG data home but no opencode.json, so every configured provider --
	// ollama, inception, zen -- is unknown to the harness and the run dies rc=1 in under a
	// second. --config copies an opencode.json beside the carried auth file, mode 0600, so
	// the harness resolves the provider exactly as it does when a person adds it to
	// ~/.config/opencode. Only THE MODEL'S OWN provider is checked: a config whose entry for
	// it has no key in --auth is refused before anything runs, naming the provider and never
	// the key; a provider whose options carry a baseURL and no apiKey field has no key to be
	// absent (ollama on localhost) and is admitted without one. Every other provider in the
	// file is carried verbatim and not checked -- this run never calls them, and checking
	// them refused local-model cards for an absent inception key on every adoption pass
	// (#523 follow-up).
	configSHA := ""
	if cfg.configFile != "" {
		sha8, reason := copyProviderConfig(cfg.configFile, cfg.authFile, provider, dataHome)
		if reason != "" {
			refuseNative(errOut, reason)
			return nativeRunResult{}, 2
		}
		configSHA = sha8
	}

	// The two hashes are recorded from the same bytes the run is about to use, so a
	// caller can prove later that neither the card nor the binary changed under it.
	binaryHash, _ := fileSHA256(bin)
	cardHash := sha256.Sum256(cfg.card)

	// (5) THE WALL (slice 11). Every native run is walled unless the caller typed --no-wall:
	// the wall is never implied away (SPEC-SANDBOX rule 1). A --sandbox name is used as typed;
	// otherwise the tool's own name is resolved on PATH. A wall that cannot express a repo
	// allow rule is still a wall -- a card naming no repos runs inside it without the rule,
	// and one naming repos is refused, never unwalled. A machine with no wall binary at all
	// refuses unless --no-wall owns every read and write the child makes.
	runPath := bin
	runArgv := []string{"run", "--model", cfg.model, "--title", cfg.label, "--", string(cfg.card)}
	wall := cfg.sandbox
	if wall == "" && !cfg.noWall {
		found, err := exec.LookPath(swarm.SandboxBinary)
		if err != nil {
			refuseNative(errOut, fmt.Sprintf("%s no wall: %s is on no PATH entry and --sandbox names no file; name the wall with --sandbox <path> or run with --no-wall and own every read and write the child makes",
				oneline.Field(cfg.label), oneline.Field(swarm.SandboxBinary)))
			return nativeRunResult{}, 2
		}
		wall = found
	}
	if wall != "" {
		if len(cfg.repos) > 0 && !sandboxHostRules(wall) {
			refuseNative(errOut, fmt.Sprintf("%s wall cannot express repo rule", oneline.Field(cfg.label)))
			return nativeRunResult{}, 2
		}
		runPath = wall
		runArgv = nativeSandboxArgv(bin, cfg, dataHome, jobDir, tmpDir)
	} else if len(cfg.repos) > 0 {
		refuseNative(errOut, fmt.Sprintf("%s wall cannot express repo rule", oneline.Field(cfg.label)))
		return nativeRunResult{}, 2
	}

	// THE CHILD. The deadline is a context, so the process (and any it started in its
	// own group) is killed when the wall runs out, not merely handed a suggestion.
	ctx, cancel := context.WithTimeout(context.Background(), cfg.deadline)
	defer cancel()
	childEnv := nativeChildEnv(dataHome, jobDir, tmpDir)
	writeNativeArgvLog(cfg.slotDir, runPath, runArgv, childEnv)
	cmd := exec.CommandContext(ctx, runPath, runArgv...)
	cmd.Env = childEnv
	cmd.Dir = jobDir
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		refuseNative(errOut, fmt.Sprintf("the child's stdin %s could not be opened: %s", oneline.Field(os.DevNull), oneline.Escape(err.Error())))
		return nativeRunResult{}, 2
	}
	defer devNull.Close()
	cmd.Stdin = devNull
	log, err := os.OpenFile(filepath.Join(cfg.slotDir, "native.log"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		refuseNative(errOut, fmt.Sprintf("the run log %s could not be opened: %s", oneline.Field(filepath.Join(cfg.slotDir, "native.log")), oneline.Escape(err.Error())))
		return nativeRunResult{}, 2
	}
	// ONE CAPTURE PATH, WALLED OR NOT (issue #608). The child's output also lands in the
	// job's own `harness.log`, the name every other part of this tool reads a run's evidence
	// by: `finish` counts refusals there, the input-limit reader looks for the provider's
	// words there, batch's idle watch measures it, and `harness=silent` (#604) asks whether
	// it holds bytes. Before this the native path wrote only <slot>/native.log, so an
	// UNWALLED card -- every Space card -- that produced no RESULT left no evidence at all
	// and its failure could not be diagnosed; a silent harness and a lost log read the same.
	// The file is opened O_APPEND and never truncated: a batch opens this same path and
	// pins its runner's stdout to it before this process starts, and truncating it here
	// would cut the runner's own lines out from under it.
	harnessLog, err := os.OpenFile(filepath.Join(jobDir, "harness.log"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		log.Close()
		refuseNative(errOut, fmt.Sprintf("the harness log %s could not be opened: %s", oneline.Field(filepath.Join(jobDir, "harness.log")), oneline.Escape(err.Error())))
		return nativeRunResult{}, 2
	}
	// The wall's own stderr is split out of the log: the SANDBOX OK line the wall prints
	// is where the tool learns the wall's name and the cwd it actually applied, and neither
	// is guessed. The logs still carry every byte; the buffer holds stderr for the parse.
	var wallOut bytes.Buffer
	capture := io.MultiWriter(log, harnessLog)
	cmd.Stdout = capture
	cmd.Stderr = io.MultiWriter(capture, &wallOut)

	res := nativeRunResult{
		rc:           -1,
		cardSHA256:   hex.EncodeToString(cardHash[:]),
		binarySHA256: binaryHash,
		job:          jobDir,
		wall:         "none",
		configSHA:    configSHA,
		tmp:          tmpDir,
	}
	if cfg.noWall {
		res.wall = "none-by-flag"
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
	harnessLog.Close()

	if wall != "" {
		backend, cwd, ok := wallNamed(wallOut.String())
		if !ok {
			refuseNative(errOut, fmt.Sprintf("%s wall ran without a SANDBOX OK line naming its backend; the run is refused rather than silently unwalled", oneline.Field(cfg.label)))
			return nativeRunResult{}, 2
		}
		res.wall = backend
		if !sameDir(cwd, jobDir) {
			refuseNative(errOut, fmt.Sprintf("%s wall ran the child in %s, not the job directory %s; --cwd was not applied", oneline.Field(cfg.label), oneline.Field(cwd), oneline.Field(jobDir)))
			return nativeRunResult{}, 2
		}
	}

	// Slice 10: one usage.tsv beside the run, read from the harness's own store, so a batch
	// can fold the card's tokens and dollars without re-reading the harness.
	res.usageReason, res.usageState = writeNativeUsage(cfg, dataHome, provider, cfg.model[len(provider)+1:], start, time.Now(), res.rc, errOut)
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
// temp directory is a --write so the child's TMPDIR is usable inside the wall; the
// slot directory is the read set. Each repo the card named is a --repo allow rule, and a
// recipient never appears: a bus send is denied by the wall itself, not granted by the
// caller, so no allow rule is ever built for one.
func nativeSandboxArgv(bin string, cfg nativeRunConfig, dataHome, jobDir, tmpDir string) []string {
	argv := []string{
		"--read", cfg.slotDir,
		"--write", jobDir,
		"--write", dataHome,
		"--write", tmpDir,
		"--cwd", jobDir,
	}
	// The shell launcher read the harness's own directory and /opt/homebrew so git and the
	// harness's libraries resolve inside the wall; the native path does the same (run 7).
	// Without the harness directory the wall denies even the resolver's own files, and
	// without /opt/homebrew the common toolchain roots are invisible.
	argv = append(argv, "--read", filepath.Dir(bin))
	if fi, err := os.Stat("/opt/homebrew"); err == nil && fi.IsDir() {
		argv = append(argv, "--read", "/opt/homebrew")
	}
	for _, r := range cfg.repos {
		argv = append(argv, "--repo", r)
	}
	argv = append(argv, "--")
	argv = append(argv, bin, "run", "--model", cfg.model, "--title", cfg.label, "--", string(cfg.card))
	return argv
}

// nativeChildEnv is the child's whole environment, built rather than inherited: a short
// allowlist survives the caller's own environment (PATH, LANG, TERM, the XDG_ and
// NOVA_SWARM_ families, and any provider credential whose name carries KEY, TOKEN or
// SECRET), and the names this run owns are then set exactly once. HOME and XDG_DATA_HOME
// point at the data home, NOVA_SWARM_JOB names the job directory, and TMPDIR is the slot's
// own tmp/<label> (never the job directory, which admission git-inits into a repo) instead
// of the caller's own, which the wall denies.
// XDG_CONFIG_HOME and XDG_CACHE_HOME are dropped, never inherited, so the harness defaults
// them under HOME and never follows them outside the wall.
func nativeChildEnv(dataHome, jobDir, tmpDir string) []string {
	var kept []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if keepNativeEnv(name) {
			kept = append(kept, kv)
		}
	}
	for _, name := range []string{"HOME", "XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "NOVA_SWARM_JOB", "TMPDIR"} {
		kept = environWithoutName(kept, name)
	}
	return append(kept,
		"HOME="+dataHome,
		"XDG_DATA_HOME="+dataHome,
		"NOVA_SWARM_JOB="+jobDir,
		"TMPDIR="+tmpDir,
	)
}

// keepNativeEnv says whether one inherited name survives into the native child: the names a
// program needs (PATH, LANG, TERM), the XDG_ and NOVA_SWARM_ families, and any provider
// credential whose name carries KEY, TOKEN or SECRET. Everything else is the caller's own
// noise and is dropped, so no path the caller happened to export reaches the child.
func keepNativeEnv(name string) bool {
	switch name {
	case "PATH", "LANG", "TERM":
		return true
	}
	if strings.HasPrefix(name, "XDG_") || strings.HasPrefix(name, "NOVA_SWARM_") {
		return true
	}
	up := strings.ToUpper(name)
	return strings.Contains(up, "KEY") || strings.Contains(up, "TOKEN") || strings.Contains(up, "SECRET")
}

// environWithoutName is the environment minus one name, so a name this run sets itself is
// certain to appear exactly once.
func environWithoutName(env []string, name string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if n, _, _ := strings.Cut(kv, "="); n == name {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// writeNativeArgvLog records the exact argv and environment the child is about to be handed
// into <slot>/native-argv.log, so a later reader can prove what the wall was asked to run.
// A value whose name carries KEY, TOKEN or SECRET is written as <redacted>, never the secret
// itself.
func writeNativeArgvLog(slotDir, runPath string, runArgv, env []string) {
	f, err := os.OpenFile(filepath.Join(slotDir, "native-argv.log"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "argv: %s\n", oneline.Escape(strings.Join(append([]string{runPath}, runArgv...), " ")))
	for _, kv := range env {
		name, val, _ := strings.Cut(kv, "=")
		if keepNativeSecretName(name) {
			val = "<redacted>"
		}
		fmt.Fprintf(f, "env: %s=%s\n", oneline.Escape(name), oneline.Escape(val))
	}
}

// keepNativeSecretName says whether a name carries a secret, which the argv log redacts.
func keepNativeSecretName(name string) bool {
	up := strings.ToUpper(name)
	return strings.Contains(up, "KEY") || strings.Contains(up, "TOKEN") || strings.Contains(up, "SECRET")
}

// wallNamed reads the SANDBOX OK line out of the wall's captured stderr and returns the
// backend it named and the cwd it applied. A wall that printed no SANDBOX OK line -- one
// that refused, or a stand-in that says nothing -- is a run this tool cannot trust to name
// its own containment, and the second return is false (SPEC-SANDBOX rules 1 and 11: never
// silently degraded, and a wall that cannot say what it is is no wall).
func wallNamed(out string) (backend, cwd string, ok bool) {
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "SANDBOX OK ") {
			continue
		}
		for _, tok := range strings.Fields(line) {
			switch {
			case strings.HasPrefix(tok, "backend="):
				backend = strings.TrimPrefix(tok, "backend=")
			case strings.HasPrefix(tok, "cwd="):
				cwd = strings.TrimPrefix(tok, "cwd=")
			}
		}
		if backend != "" {
			return backend, cwd, true
		}
	}
	return "", "", false
}

// sameDir asks whether two paths name the same directory once symlinks are resolved, so a
// wall that reports the job directory spelled through a symlinked parent still matches the
// path the caller built it from. When either path will not resolve, the raw strings are
// compared.
func sameDir(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA == nil && errB == nil {
		return ra == rb
	}
	return a == b
}

// writeNativeUsage records one card's usage row next to its RESULT.md, once the child is
// gone and its store is complete. The token numbers come from the store; the data home is
// passed here explicitly (the run chose it), and the reader looks in its standard locations.
// When sqlite3 is missing the columns are dashes and the note is carried to the caller, and
// the run still finishes rather than failing on a number nobody can see. When no store exists
// the row keeps its dashes and the returned reason and path name what the NATIVE OK line says.
func writeNativeUsage(cfg nativeRunConfig, dataHome, provider, model string, start, end time.Time, rc int, errOut io.Writer) (reason, path string) {
	usage, note, storePath, rr := swarm.ReadCardUsage(dataHome, start, end)
	rcCol := "-"
	if rc >= 0 {
		rcCol = strconv.Itoa(rc)
	}
	row := swarm.UsageRow{
		"job": cfg.label, "attempt": "1",
		"started": start.UTC().Format(time.RFC3339),
		"ended":   end.UTC().Format(time.RFC3339),
		"rc":      rcCol, "provider": provider, "model": model,
	}
	for _, c := range swarm.TokenColumns {
		row[c] = dash(usage.Values[c])
	}
	row["usd"] = dash(usage.Values["usd"])
	jobDir := filepath.Join(cfg.slotDir, "jobs", cfg.label)
	if err := swarm.WriteCardUsage(filepath.Join(jobDir, "usage.tsv"), row); err != nil {
		fmt.Fprintf(errOut, "NATIVE NOTE: the usage.tsv could not be written: %s\n", oneline.Escape(err.Error()))
	}
	_ = swarm.WriteCardUsage(filepath.Join(cfg.slotDir, "usage.tsv"), row)
	if note != "" {
		fmt.Fprintf(errOut, "NATIVE NOTE: %s\n", oneline.Escape(note))
	}
	if rr == "" {
		return "", ""
	}
	if storePath == "" {
		storePath = filepath.Join(dataHome, filepath.FromSlash(swarm.OpenCodeDB))
	}
	return rr, storePath
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

// copyProviderConfig copies an opencode.json provider config beside the carried auth copy
// in the job's own data home, mode 0600, and returns the sha8 the NATIVE OK line names. The
// config's entry for THE MODEL'S provider is refused when its key is absent from the auth
// file: that provider is exactly the one the harness is about to call, and the refusal names
// the provider, never the key. The bytes are copied verbatim even when they are not a JSON
// object this side can parse -- the refusal check is best-effort, the copy is not.
func copyProviderConfig(configPath, authPath, provider, dataHome string) (sha8, reason string) {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return "", fmt.Sprintf("the config file %s could not be read: %s", oneline.Field(configPath), oneline.Escape(err.Error()))
	}
	if modelProviderMissingAuth(raw, authPath, provider) {
		return "", fmt.Sprintf("the config file %s names provider %s, whose key is absent from the auth file %s; add it to --auth or drop the provider from --config",
			oneline.Field(configPath), oneline.Field(provider), oneline.Field(dash(authPath)))
	}
	sum := sha256.Sum256(raw)
	dst := filepath.Join(dataHome, ".config", "opencode", "opencode.json")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", fmt.Sprintf("the config directory %s could not be made: %s", oneline.Field(filepath.Dir(dst)), oneline.Escape(err.Error()))
	}
	if err := os.WriteFile(dst, raw, 0o600); err != nil {
		return "", fmt.Sprintf("the config copy %s could not be written: %s", oneline.Field(dst), oneline.Escape(err.Error()))
	}
	return hex.EncodeToString(sum[:])[:8], ""
}

// modelProviderMissingAuth reports whether the one provider this run will call -- the
// --model's -- is named by the config and has no entry in the auth file. Only that provider
// is asked about. A config is the whole of a person's ~/.config/opencode and names every
// provider they keep; the ones this model does not use are never reached by the child, so
// their keys are not this run's business, and refusing on them refused good cards (#523).
//
// It answers false when the config does not parse into a "provider" object (the copy is
// still performed verbatim), when the config does not name this provider at all, and when
// the entry's options carry a baseURL and no apiKey field -- ollama on localhost has no key
// to be absent.
func modelProviderMissingAuth(raw []byte, authPath, provider string) bool {
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return false
	}
	providers, ok := cfg["provider"].(map[string]any)
	if !ok {
		return false
	}
	entry, named := providers[provider]
	if !named || keylessProvider(entry) {
		return false
	}
	if authPath == "" {
		return true
	}
	authRaw, err := os.ReadFile(authPath)
	if err != nil {
		return true
	}
	var entries map[string]any
	if json.Unmarshal(authRaw, &entries) != nil {
		return true
	}
	_, has := entries[provider]
	return !has
}

// keylessProvider reports whether a provider entry needs no key: its options carry a baseURL
// and no apiKey field, so the harness reaches it (for example ollama on localhost) with no
// credential to be absent.
func keylessProvider(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	opts, ok := m["options"].(map[string]any)
	if !ok {
		return false
	}
	baseURL, _ := opts["baseURL"].(string)
	if baseURL == "" {
		return false
	}
	_, hasKey := opts["apiKey"]
	return !hasKey
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
