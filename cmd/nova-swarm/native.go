package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	sandbox        string // the nova-sandbox binary naming the wall; "" = resolve on PATH
	noWall         bool   // the caller typed --no-wall: run with no containment, named by its OK line
	noSharedCaches bool   // the caller typed --no-shared-caches: the Go caches stay under HOME as today
	configFile     string // optional: an opencode.json provider config copied beside the auth copy
	// WORKER (issue #881): the worker description `--worker <file>` names, when one is
	// given. It is the source of the model -- a key is authorized for one model only, and
	// the description pins it -- and when it carries "secret": "<NAME>" it is the source of
	// the key, taken from the environment and passed through by name, with no auth file
	// ever written. nil means native keeps --model and --auth as today.
	worker *swarm.Worker
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
	harness      string  // ok | silent: silent when the capture holds no words of the child's and no result was found
	fence        string  // the first path the harness's own fence auto-rejected, "" when it rejected nothing
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
	if _, err := os.Stat(bin); err != nil {
		refuseNative(errOut, fmt.Sprintf("the harness binary %s is missing", oneline.Field(bin)))
		return nativeRunResult{}, 2
	}
	// The execute question is asked by the platform's own rule, never by the unix bit
	// alone: windows carries no such bit and reports 0666 for every file, so reading it
	// there refused every harness that existed. See isExecutable in executable.go.
	if !isExecutable(bin) {
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

	// (2b) THE WORKER DESCRIPTION (issue #881). When `--worker <file>` names a
	// description, the description is the source of the model: a key is authorized for ONE
	// model only, and the description pins that one. A --model whose model half differs is
	// refused, naming BOTH models on one line, before any directory is made and before any
	// child starts. The description's `model` is the model ID (deepseek-chat); --model is
	// provider/model, so the half after the slash is what is compared. Without --worker,
	// native keeps --model as today.
	if cfg.worker != nil {
		modelID := cfg.model[len(provider)+1:]
		if cfg.worker.Model != modelID {
			refuseNative(errOut, fmt.Sprintf("--model %s differs from the worker description's model %s; a key is authorized for one model only, and the description pins the model this run launches",
				oneline.Field(cfg.model), oneline.Field(cfg.worker.Model)))
			return nativeRunResult{}, 2
		}
		// A description naming "secret": "<NAME>" takes the key from THIS process's own
		// environment -- `nova-secrets exec` set it around the run -- and the value is
		// never written to a file, never printed, and never in a REFUSED or OK line. An
		// absent or empty variable is refused HERE, before anything runs, the way run and
		// supervise refuse it.
		if cfg.worker.Secret != "" {
			if _, err := swarm.SecretFromEnv(cfg.worker.Secret); err != nil {
				refuseNative(errOut, oneline.Escape(err.Error()))
				return nativeRunResult{}, 2
			}
		}
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

	// (3b) THE BENCH-SHARED GO CACHES (card 8963). Go derives GOMODCACHE and GOCACHE from
	// HOME, and a native run makes HOME the slot's data home, so every card used to download
	// its own copy of the module cache -- and a toolchain -- and grew a slot to five to seven
	// gigabytes. Instead the two caches live once per bench under <root>/cache, made here at
	// mode 0755 BEFORE the child can derive them and handed to the child as GOMODCACHE and
	// GOCACHE. GOTOOLCHAIN=local keeps a card from fetching a toolchain behind the bench's
	// back. The sharing is safe because Go's caches are concurrency-safe by design and the
	// module cache is read-mostly. --no-shared-caches keeps today's behaviour exactly: no
	// names set, the caches under HOME.
	cacheDir := nativeCacheDir(cfg)
	if cacheDir != "" {
		for _, d := range []string{filepath.Join(cacheDir, "go-mod"), filepath.Join(cacheDir, "go-build")} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				refuseNative(errOut, fmt.Sprintf("the shared go cache %s could not be made: %s", oneline.Field(d), oneline.Escape(err.Error())))
				return nativeRunResult{}, 2
			}
		}
	}

	// (4) THE AUTH COPY. One entry, the model's provider's, moved to the data home so
	// the child's account resolves, and left mode 0600. A source that is looser than
	// 0600 is refused: its copy would spread a secret further than its owner.
	// --auth stays ONLY the legacy shape's (issue #881): a description that names
	// "secret": "<NAME>" takes the key from the environment and writes no auth file, so
	// this step is skipped entirely for one. When a description IS given and --auth is
	// used, the copy is the legacy path and one NOTE line says so.
	if cfg.authFile != "" {
		if reason := copyAuth(cfg.authFile, provider, dataHome); reason != "" {
			refuseNative(errOut, reason)
			return nativeRunResult{}, 2
		}
		if cfg.worker != nil {
			fmt.Fprintf(errOut, "NATIVE NOTE: --auth %s copies the provider secret into the job's data home on disk, mode 0600; the legacy shape -- a description naming \"secret\": \"<NAME>\" would keep the key in the environment and write no auth file\n",
				oneline.Field(cfg.authFile))
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
	//
	// (4c) AND THE JOB'S OWN FENCE (issue #644). The harness's `permission` block is written
	// into the SAME file, whether or not --config named one, because a run with no config at
	// all still runs under the harness's default fence -- which auto-rejects the job's own
	// `../scratch` and every read-only path a card names -- and that fence is what killed 8
	// of 30 cards on 2026-09-16. The block names this job's directories; the carried
	// provider config keeps its own bytes and its own rules beside them (internal/swarm/fence.go).
	// On a walled bench the wall owns what the child may read, so only a --no-wall run takes
	// the card's `READ:` paths: with no OS wall there is nothing else to open them.
	var reads []string
	if cfg.noWall {
		reads = swarm.CardReadPaths(cfg.card)
	}
	configSHA, reason := writeJobConfig(cfg, provider, dataHome, jobDir, reads, errOut)
	if reason != "" {
		refuseNative(errOut, reason)
		return nativeRunResult{}, 2
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
	secretEnv := ""
	if cfg.worker != nil {
		secretEnv = cfg.worker.Secret
	}
	childEnv := nativeChildEnv(dataHome, jobDir, tmpDir, cacheDir, secretEnv)
	writeNativeArgvLog(cfg.slotDir, runPath, runArgv, childEnv)
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		refuseNative(errOut, fmt.Sprintf("the child's stdin %s could not be opened: %s", oneline.Field(os.DevNull), oneline.Escape(err.Error())))
		return nativeRunResult{}, 2
	}
	defer devNull.Close()
	log, err := os.OpenFile(filepath.Join(cfg.slotDir, "native.log"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		refuseNative(errOut, fmt.Sprintf("the run log %s could not be opened: %s", oneline.Field(filepath.Join(cfg.slotDir, "native.log")), oneline.Escape(err.Error())))
		return nativeRunResult{}, 2
	}
	// ONE CAPTURE PATH, WALLED OR NOT (issue #608). The child's output also lands under the
	// JOB, in `harness-output.log`, so the evidence sits with the card's own work rather
	// than one directory up with the slot's. Before this the native path wrote only
	// <slot>/native.log, so an UNWALLED card -- every Space card -- that produced no RESULT
	// left no evidence of what the harness said: the whole no-result class of 2026-09-16
	// could not be diagnosed, and a silent harness and a lost log read the same.
	//
	// IT IS NOT `harness.log`, DELIBERATELY. That name has two owners already -- the legacy
	// supervisor pins the harness's output to it, and a `batch` pins its runner's stdout to
	// it, which is where the NATIVE OK line lands -- and a third writer at one path is how
	// evidence gets cut out from under a reader. This capture has its own name and one
	// writer, and `harness=silent` (#604) is asked OF THIS FILE: whether the child itself
	// said anything at all.
	//
	// It is opened O_APPEND and never truncated, so a second writer at the same path (a
	// retried run, a batch that opened it first) appends rather than cutting bytes out
	// from under the first. It is opened O_NOFOLLOW as well: this is the first file this
	// process opens inside the JOB, which is the card's own writable directory, and a
	// symlink planted there by an earlier run of the same card would carry the child's
	// output out of the wall, through a process that has no wall (security#30's class).
	outLog := filepath.Join(jobDir, "harness-output.log")
	harnessOut, err := os.OpenFile(outLog, os.O_WRONLY|os.O_CREATE|os.O_APPEND|swarm.ONoFollow, 0o644)
	if err != nil {
		log.Close()
		refuseNative(errOut, fmt.Sprintf("the harness output log %s could not be opened: %s", oneline.Field(outLog), oneline.Escape(err.Error())))
		return nativeRunResult{}, 2
	}
	// The wall's own stderr is split out of the log: the SANDBOX OK line the wall prints
	// is where the tool learns the wall's name and the cwd it actually applied, and neither
	// is guessed. The logs still carry every byte; the buffer holds stderr for the parse.
	var wallOut bytes.Buffer
	// THE PER-TURN TIMELINE (card 8964). The harness reports its own model turns and tool
	// calls on the child's output with no timestamps of its own; this recorder stamps each
	// report as it arrives, so the card's minutes can be read per phase afterwards.
	timeline := swarm.NewTimeline()
	capture := io.MultiWriter(log, harnessOut, timeline)

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

	// THE LAUNCH GRACE (issue #900). A harness that dies inside this window with a
	// provider server error in its own output is a launch that did not take: the provider
	// answered before the request began, and the slot was spent on nothing. The SAME card
	// is retried -- 5-20s jittered, then 30-60s -- and each launch writes its own usage row
	// (attempt=1,2,3). A failure past the grace is a real run that failed and is not
	// retried.
	grace := swarm.DefaultLaunchGrace
	if cfg.worker != nil {
		grace = swarm.LaunchGrace(*cfg.worker)
	}
	for attempt := 1; ; attempt++ {
		before := fileSize(outLog)
		ctx, cancel := context.WithTimeout(context.Background(), cfg.deadline)
		cmd := exec.CommandContext(ctx, runPath, runArgv...)
		cmd.Env = childEnv
		cmd.Dir = jobDir
		cmd.Stdin = devNull
		cmd.Stdout = capture
		cmd.Stderr = io.MultiWriter(capture, &wallOut)
		attemptStart := time.Now()
		runErr := cmd.Run()
		elapsed := time.Since(attemptStart)
		cancel()
		res.wallSeconds += elapsed.Seconds()
		switch ee := runErr.(type) {
		case nil:
			res.rc = 0
		case *exec.ExitError:
			res.rc = ee.ExitCode()
		default:
			res.rc = -1
		}
		// ONE USAGE ROW PER LAUNCH (issue #900), so the cost of a retried card is each
		// attempt once, and a fast failure whose provider reported nothing keeps dashes.
		res.usageReason, res.usageState = writeNativeUsage(cfg, dataHome, provider, cfg.model[len(provider)+1:], attemptStart, time.Now(), res.rc, attempt, errOut)
		_, launchFailure := swarm.ProviderLaunchFailure(readSince(outLog, before))
		if launchFailure && elapsed < grace && attempt < swarm.MaxProviderAttempts {
			time.Sleep(swarm.ProviderRetryDelay(attempt))
			continue
		}
		break
	}
	log.Close()
	harnessOut.Close()
	// Issue #591: whether the harness left any record of itself is decided here -- AFTER both
	// logs are closed, so every byte the child wrote is on disk -- and carried on the OK line.
	res.harness = harnessState(jobDir)
	// AND WHETHER THE FENCE STOPPED THE CARD (issue #644), asked of the same capture and for
	// the same reason: the harness prints its own rejection and then the model stops, so a
	// run that ends with no result and a rejection in its capture is not a model that chose
	// to publish nothing. The path is carried onto the NATIVE OK line, where the batch reads
	// it and scores the card `fence` instead of `no-result`.
	res.fence = fenceRejected(jobDir)
	// The timeline lands beside RESULT.md and usage.tsv once the child is gone, with one
	// row per model turn and per tool call, in report order. A run whose harness reported no
	// events writes no file: an absent timeline is an empty measurement, never a zero row.
	if rows := timeline.Rows(); len(rows) > 0 {
		if err := swarm.WriteTimeline(filepath.Join(jobDir, swarm.TimelineFileName), rows); err != nil {
			fmt.Fprintf(errOut, "NATIVE NOTE: the timeline.tsv could not be written: %s\n", oneline.Escape(err.Error()))
		}
	}

	if wall != "" {
		backend, cwd, reason := wallNamed(wallOut.String())
		if reason != "" {
			refuseNative(errOut, fmt.Sprintf("%s wall %s; the run is refused rather than silently unwalled", oneline.Field(cfg.label), oneline.Escape(reason)))
			return nativeRunResult{}, 2
		}
		res.wall = backend
		if !sameDir(cwd, jobDir) {
			refuseNative(errOut, fmt.Sprintf("%s wall ran the child in %s, not the job directory %s; --cwd was not applied", oneline.Field(cfg.label), oneline.Field(cwd), oneline.Field(jobDir)))
			return nativeRunResult{}, 2
		}
	}

	return res, 0
}

// fileSize is a path's size, or 0 when it cannot be measured: the mark the retry loop reads
// before a launch so the provider tail it inspects is THIS attempt's output.
func fileSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// readSince reads what one launch appended to the capture after offset, bounded so a chatty
// harness does not read a whole log to answer a yes/no.
func readSince(path string, offset int64) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(f, 64<<10))
	if err != nil {
		return nil
	}
	return raw
}

// fenceRejected is the first path the harness's own fence auto-rejected in this job's
// capture, or "" when it rejected nothing. It is asked OF THE RUN'S OWN CAPTURE,
// `<job>/harness-output.log` (issue #608), the file with one writer -- never `harness.log`,
// which carries the runner's stdout and this very line.
func fenceRejected(jobDir string) string {
	raw, err := os.ReadFile(filepath.Join(jobDir, "harness-output.log"))
	if err != nil {
		return ""
	}
	path, ok := swarm.FenceRejection(raw)
	if !ok {
		return ""
	}
	return path
}

// harnessState is the `harness=<ok|silent>` token the NATIVE OK line always carries. ONE
// DEFINITION, and this is it (issues #591, #594, #608 folded): a run is `silent` when the
// capture above holds nothing the child said AND no `RESULT.md` is found anywhere the gather
// looks for one. Anything else is `ok`.
//
// A SILENT HARNESS IS NOT A QUIET MODEL. The run this closes was a local model whose tool
// calls the harness never parsed: the child emitted them as raw text, no tool ran, nothing
// was written, and the process exited 0, so the one line a coordinator reads said OK and the
// batch behind it scored `no-result` -- the token for a model that chose to publish nothing.
// The two are different faults with different remedies (a harness that cannot drive this
// model; a model that had nothing to say), and the line now tells them apart. A harness that
// SPOKE and published nothing is `ok` and scores `no-result`: there is evidence to read.
//
// THE FILE IS THE RUN'S OWN CAPTURE, `<job>/harness-output.log` (issue #608) -- never
// `harness.log`, which the legacy supervisor and a `batch`'s runner pin already own. Reading
// the capture rather than a file this process does not write is what keeps the token honest
// on a bench, where the batch's own runner pin may not exist at all.
//
// THE WALL'S OWN LINES ARE NOT THE HARNESS SPEAKING. The wall prints `SANDBOX ...` on the
// child's stderr, which this capture also holds, and counting those bytes would make a WALLED
// run -- the very run that wrote issue #591 -- impossible to call silent. They are skipped
// here exactly as the gather's own `log=<n>` count skips them (internal/swarm/batch.go).
//
// THE RESULT IS LOOKED FOR WHERE THE GATHER LOOKS FOR IT, by the gather's own lookup
// (swarm.FindCardResult): the job root, then `repo/` and one directory below it (issue #594).
// A card's STEP 1 makes `repo/` the model's cwd, so a working run publishes there and the
// batch copies it up; a shallower lookup here would print `harness=silent` about a run that
// worked, which is the same class of fault this token exists to end.
func harnessState(jobDir string) string {
	if harnessSpoke(filepath.Join(jobDir, "harness-output.log")) {
		return "ok"
	}
	if result, ok := swarm.FindCardResult(jobDir); ok && wroteBytes(result) {
		return "ok"
	}
	return "silent"
}

// captureHeadBytes bounds what harnessSpoke reads of a capture: a wall's own header is a
// handful of lines, so a capture larger than this holds words of the child's whatever its
// head says, and a run's capture can be megabytes that nobody needs read to answer a yes/no.
const captureHeadBytes = 64 << 10

// harnessSpoke says whether the capture holds a line the CHILD wrote: any non-blank line that
// is not one of the wall's own `SANDBOX ` lines. An absent or empty file is a harness that
// said nothing, and so is one holding the wall's header alone.
func harnessSpoke(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() == 0 {
		return false
	}
	if fi.Size() > captureHeadBytes {
		return true
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "SANDBOX ") {
			continue
		}
		return true
	}
	return false
}

// wroteBytes says whether a path is a regular file holding at least one byte: the test
// harnessState applies to a result, so an empty RESULT.md is nothing published.
func wroteBytes(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular() && fi.Size() > 0
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
	}
	// The bench-shared Go caches are a write for the same reason the data home is: a card
	// extracts a module it downloads, and the wall denies a write it was not handed (card
	// 8963). It is one directory for the whole bench, so the write is shared, not per-card.
	if cacheDir := nativeCacheDir(cfg); cacheDir != "" {
		argv = append(argv, "--write", cacheDir)
	}
	argv = append(argv, "--cwd", jobDir)
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
//
// cacheDir, when nonempty, points GOMODCACHE and GOCACHE at the bench-shared caches under
// <root>/cache and pins GOTOOLCHAIN=local (card 8963); empty is --no-shared-caches, and the
// three names are then as absent as they have always been.
//
// secretEnv is the NAME a worker description's `secret` carries (issue #881): the value is
// passed through to the child BY NAME, exactly once -- stripped from the inherited set even
// when its name already carries KEY/TOKEN/SECRET -- so a name that does not itself carry one
// still reaches the harness. The value is never written to a file and never printed; the
// argv log redacts any name that carries a secret.
func nativeChildEnv(dataHome, jobDir, tmpDir, cacheDir, secretEnv string) []string {
	var kept []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if keepNativeEnv(name) {
			kept = append(kept, kv)
		}
	}
	remove := []string{"HOME", "XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "NOVA_SWARM_JOB", "TMPDIR"}
	if secretEnv != "" {
		remove = append(remove, secretEnv)
	}
	for _, name := range remove {
		kept = environWithoutName(kept, name)
	}
	out := append(kept,
		"HOME="+dataHome,
		"XDG_DATA_HOME="+dataHome,
		"NOVA_SWARM_JOB="+jobDir,
		"TMPDIR="+tmpDir,
	)
	if cacheDir != "" {
		out = append(out,
			"GOMODCACHE="+filepath.Join(cacheDir, "go-mod"),
			"GOCACHE="+filepath.Join(cacheDir, "go-build"),
			"GOTOOLCHAIN=local",
		)
	}
	if secretEnv != "" {
		if v, ok := os.LookupEnv(secretEnv); ok {
			out = append(out, secretEnv+"="+v)
		}
	}
	return out
}

// nativeCacheDir is the bench-shared Go cache root: <root>/cache, the one directory every
// slot of a bench shares so a card's data home holds harness state only (card 8963). The
// module and build caches are its two children. It is empty when the caller typed
// --no-shared-caches, which restores the old per-card caches under HOME, and while the root
// is unset (a unit test of the argv builder), when there is nothing to share.
func nativeCacheDir(cfg nativeRunConfig) string {
	if cfg.noSharedCaches || cfg.root == "" {
		return ""
	}
	return filepath.Join(cfg.root, "cache")
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
// backend it named and the cwd it applied. The cwd is read from the cwdb64=<base64url>
// field ALONE -- the machine-readable receipt, a strict base64url encoding of the raw path
// bytes the wall applied, so a path holding a space, a literal backslash, or a non-ASCII
// name survives the line exactly. The cwd=<dir> field beside it is the readable rendering
// of the same path and is never decoded for the comparison: oneline.Field is not injective,
// so its escapes cannot be reversed to the bytes. A wall that printed no SANDBOX OK line,
// or one whose receipt is absent or not valid base64url, is a run this tool cannot trust to
// name its own containment, and the reason is returned (SPEC-SANDBOX rules 1 and 11: never
// silently degraded, and a wall that cannot say what it is is no wall).
func wallNamed(out string) (backend, cwd, reason string) {
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "SANDBOX OK ") {
			continue
		}
		receipt := false
		for _, tok := range strings.Fields(line) {
			switch {
			case strings.HasPrefix(tok, "backend="):
				backend = strings.TrimPrefix(tok, "backend=")
			case strings.HasPrefix(tok, "cwdb64="):
				raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(tok, "cwdb64="))
				if err != nil {
					return backend, "", "printed a SANDBOX OK line whose cwdb64 receipt is not valid base64url"
				}
				cwd = string(raw)
				receipt = true
			}
		}
		if backend != "" {
			if !receipt {
				return backend, "", "printed a SANDBOX OK line with no cwdb64 receipt"
			}
			return backend, cwd, ""
		}
	}
	return "", "", "ran without a SANDBOX OK line naming its backend"
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
//
// ONE ROW PER LAUNCH (issue #900): a native run that retried a launch appends a row for each
// attempt, so a retried card's usage.tsv carries attempt=1,2,3 for its one job and each
// attempt is summed once. A fast failure whose provider reported nothing keeps its dashes,
// and `usd` stays a dash rather than becoming a zero.
func writeNativeUsage(cfg nativeRunConfig, dataHome, provider, model string, start, end time.Time, rc, attempt int, errOut io.Writer) (reason, path string) {
	usage, note, storePath, rr := swarm.ReadCardUsage(dataHome, start, end)
	rcCol := "-"
	if rc >= 0 {
		rcCol = strconv.Itoa(rc)
	}
	row := swarm.UsageRow{
		"job": cfg.label, "attempt": strconv.Itoa(attempt),
		"started": start.UTC().Format(time.RFC3339),
		"ended":   end.UTC().Format(time.RFC3339),
		"rc":      rcCol, "provider": provider, "model": model,
	}
	for _, c := range swarm.TokenColumns {
		row[c] = dash(usage.Values[c])
	}
	row["usd"] = dash(usage.Values["usd"])
	jobDir := filepath.Join(cfg.slotDir, "jobs", cfg.label)
	if err := swarm.AppendCardUsage(filepath.Join(jobDir, "usage.tsv"), row); err != nil {
		fmt.Fprintf(errOut, "NATIVE NOTE: the usage.tsv could not be written: %s\n", oneline.Escape(err.Error()))
	}
	_ = swarm.AppendCardUsage(filepath.Join(cfg.slotDir, "usage.tsv"), row)
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
// looser than 0600 or the copy cannot end 0600. Both mode questions are asked of the
// platform (authmode.go): windows reports 0666 for every readable file, so neither rule
// refuses there (#915).
func copyAuth(src, provider, dataHome string) string {
	st, err := os.Stat(src)
	if err != nil {
		return fmt.Sprintf("the auth file %s could not be read: %s", oneline.Field(src), oneline.Escape(err.Error()))
	}
	if authModeWiderThanOwner(runtime.GOOS, st.Mode()) {
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
	if dstSt, err := os.Stat(dst); err == nil && authModeNotOwnerOnly(runtime.GOOS, dstSt.Mode()) {
		return fmt.Sprintf("the auth copy would not be 0600: %s ended mode %04o", oneline.Field(dst), dstSt.Mode().Perm())
	}
	ocDir := filepath.Join(dataHome, "opencode")
	if err := os.MkdirAll(ocDir, 0o755); err == nil {
		_ = os.WriteFile(filepath.Join(ocDir, "auth.json"), body, 0o600)
	}
	return ""
}

// writeJobConfig writes the ONE opencode.json the job's harness reads, beside the carried
// auth copy in the job's own data home, mode 0600, and returns the sha8 the NATIVE OK line
// names -- the sha8 OF THE BYTES THE CHILD SEES, which is the only config any later reader
// can check the run against.
//
// It carries two things. The provider config a caller named with --config (issue #465),
// whose entry for THE MODEL's provider is refused when its key is absent from the auth file:
// that provider is exactly the one the harness is about to call, and the refusal names the
// provider, never the key. And this job's own fence block (issue #644), which is written
// WHETHER OR NOT a config was named, because the harness's default fence auto-rejects the
// card's own `../scratch` and every path it names on a `READ:` line.
//
// A WORKER DESCRIPTION THAT NAMES A SECRET IS THE CONFIG (issue #881): its own provider
// declaration carries `{env:NAME}` -- the variable's NAME, never its value, the exact rule
// the legacy run path writes by -- and there is no auth file for a key to be absent from,
// so the missing-auth check does not apply. --config is refused with such a description at
// the verb, because the description's declaration is the one this run means.
//
// A config file this side cannot parse is still carried verbatim -- the refusal check is
// best-effort and the copy is not -- and then the fence cannot be merged into it, which is
// said once on stderr as a NATIVE NOTE rather than refused: a run with an unparseable config
// is a run the caller has already chosen, and it is better fenced-by-default than not run.
func writeJobConfig(cfg nativeRunConfig, provider, dataHome, jobDir string, reads []string, notes io.Writer) (sha8, reason string) {
	var raw []byte
	configPath := cfg.configFile
	switch {
	case cfg.worker != nil && cfg.worker.Secret != "":
		raw = cfg.worker.HarnessConfig()
		configPath = ""
	case configPath != "":
		body, err := os.ReadFile(configPath)
		if err != nil {
			return "", fmt.Sprintf("the config file %s could not be read: %s", oneline.Field(configPath), oneline.Escape(err.Error()))
		}
		if modelProviderMissingAuth(body, cfg.authFile, provider) {
			return "", fmt.Sprintf("the config file %s names provider %s, whose key is absent from the auth file %s; add it to --auth or drop the provider from --config",
				oneline.Field(configPath), oneline.Field(provider), oneline.Field(dash(cfg.authFile)))
		}
		raw = body
	}
	body, merged := swarm.MergeFencePermission(raw, jobDir, reads)
	if !merged && notes != nil {
		fmt.Fprintf(notes, "NATIVE NOTE: the config %s is not a JSON object this tool can read, so the job's fence rules were not written into it; the harness runs on its own defaults and a rejection is reported as fence=rejected\n", oneline.Field(dash(configPath)))
	}
	sum := sha256.Sum256(body)
	dst := filepath.Join(dataHome, ".config", "opencode", "opencode.json")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", fmt.Sprintf("the config directory %s could not be made: %s", oneline.Field(filepath.Dir(dst)), oneline.Escape(err.Error()))
	}
	if err := os.WriteFile(dst, body, 0o600); err != nil {
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
