package swarm

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// THE WORKER DESCRIPTION: which provider, which model, which environment variable the
// provider reads, which base URL, and where the key file is.
//
// It is a FILE because it is configuration a person wrote, and it is REQUIRED because this
// tool has no opinion about whose model runs. No field of it comes from the environment:
// the prototype defaulted the pool, the log directory, the worker directory and the key
// file from environment variables and $HOME, and every one of those is a flag or a field
// here (SPEC.md, no guessing).

// Worker is a worker description, decoded strictly: an unknown field is a refusal, because
// a misspelled field in a file that names a key's location is a silent default.
type Worker struct {
	Name        string   `json:"name"`
	Provider    string   `json:"provider"`
	Model       string   `json:"model"`
	BaseURL     string   `json:"base_url,omitempty"`
	EnvVar      string   `json:"env_var"`
	KeyFile     string   `json:"key_file"`
	Usage       string   `json:"usage"`
	Harness     string   `json:"harness"`
	HarnessArgs []string `json:"harness_args,omitempty"`
	WorkerDir   string   `json:"worker_dir"`
	Deadline    string   `json:"deadline"`
	Board       string   `json:"board,omitempty"`
	// READ ROOTS: the one field the wall added (docs/SPEC-SANDBOX.md, "there is no --root
	// flag"). Every job runs inside nova-sandbox, whose read set is the OS and toolchain
	// roots plus what the caller names; a toolchain installed into a USER directory -- Go
	// under ~/go, node under ~/.nvm, the Studio's /Users/<user>/toolchains -- is under no
	// system root, so a harness that needs one dies inside the wall and runs outside it.
	// It is OPTIONAL, it is a list of absolute existing directories, and it is READ-ONLY:
	// the write set is the job's own and is never configurable from a file. A worker that
	// needs nothing beyond the system roots names nothing here.
	ReadRoots []string `json:"read_roots,omitempty"`

	// PROVIDER PHRASES: the second optional field, and it is the TRIAGE LINE'S (#103). A
	// job that dies because the request did not fit is its own failure class, and the only
	// evidence of it is a sentence the provider printed -- in the provider's own words.
	// OpenCode says `Rate limit reached: input token limit exceeded`; the tool carries a
	// table of the sentences it has met (InputLimitPhrases), and a description may ADD the
	// one its provider uses. It never SUBSTITUTES: a caller teaching the tool their
	// provider's words cannot silently un-teach it another's.
	InputLimitPhrases []string `json:"input_limit_phrases,omitempty"`
}

// The usage sources a description may declare (rule 13). There are two.
//
// A SOURCE IS NAMED FOR WHAT IT IS. `opencode` promised that OpenCode's own accounting is
// read, and did not read it: on 2026-09-11 two real jobs burned 61,875 and 85,308 tokens
// against `--tokens 20000` and both reported `budget=-/20000`, because OpenCode writes
// `opencode.db` and this tool read a tab-separated file instead. The name is true now --
// `opencode` reads that database through `sqlite3`, read-only, in the data home this tool
// exported for the job -- and the tab-separated file is not a name a description may carry:
// a caller names a SOURCE, never a file some harness might write.
const (
	UsageOpenCode = "opencode"
	UsageNone     = "none"
)

// LoadWorker reads and checks a worker description, reporting EVERY independent problem in
// one run (ONBOARDING point 2) rather than sending a first run back once per field.
func LoadWorker(path string) (Worker, []error) {
	var w Worker
	raw, err := os.ReadFile(path)
	if err != nil {
		return w, []error{fmt.Errorf("--worker wants a readable worker description (a JSON file naming provider, model, env_var, key_file, harness, worker_dir, deadline and usage): %s", redactedReason(err))}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&w); err != nil {
		return w, []error{fmt.Errorf("%s is not a worker description this tool can read (%v); the fields are name, provider, model, base_url, env_var, key_file, usage, harness, harness_args, worker_dir, deadline, board, read_roots, input_limit_phrases", path, err)}
	}
	// EVERY PATH IN A WORKER DESCRIPTION IS ABSOLUTE FROM HERE ON. The harness runs with
	// its cwd set to the SLOT directory, and the paths this tool hands it -- the prompt
	// file, the job directory the prompt calls the only place it writes -- are built from
	// the description. A relative `worker_dir`, the style the README teaches, made every
	// one of them a path that does not exist from where the child stands: two jobs, rc=0,
	// `result=no-result dest=failed`, under a RUN OK byte-identical to a good pass (the
	// new-user audit, F3, 2026-09-11). The tests could not see it because they all used an
	// absolute t.TempDir().
	for _, field := range []*string{&w.WorkerDir, &w.KeyFile} {
		if *field == "" || filepath.IsAbs(*field) {
			continue
		}
		abs, err := filepath.Abs(*field)
		if err != nil {
			return w, []error{fmt.Errorf("%s: %q could not be made absolute: %s", path, *field, redactedReason(err))}
		}
		*field = abs
	}

	var problems []error
	want := func(value, field, wants string) {
		if strings.TrimSpace(value) == "" {
			problems = append(problems, fmt.Errorf("%s: %s is required; it wants %s", path, field, wants))
		}
	}
	want(w.Name, "name", "the name this worker is called on a RUN POOL line")
	want(w.Provider, "provider", "the provider id the harness config declares, such as deepseek")
	want(w.Model, "model", "the model id, such as deepseek-chat")
	want(w.EnvVar, "env_var", "the NAME of the environment variable the provider reads, such as DEEPSEEK_API_KEY; never its value")
	want(w.KeyFile, "key_file", "the path of a file holding one line, the bare key or NAME=<key>, mode 0600")
	want(w.Harness, "harness", "the harness command to run, found on PATH")
	want(w.WorkerDir, "worker_dir", "the home copy of the worker's own directory, refreshed into a slot directory one way")
	want(w.Deadline, "deadline", "this worker's default deadline per task, such as 20m")
	switch w.Usage {
	case UsageOpenCode, UsageNone:
	case "":
		problems = append(problems, fmt.Errorf("%s: usage is required; it wants `opencode` (the job's own %s in the data home this tool exports for it, read with `%s -readonly`, five token types, a dash for absence) or `none` (it reports nothing, and only `--tokens unmetered` tasks may run under it)", path, OpenCodeDB, SQLiteBinary))
	default:
		problems = append(problems, fmt.Errorf("%s: usage wants `opencode` (the job's own %s, read with `%s -readonly`) or `none` (it reports nothing, and only `--tokens unmetered` tasks may run under it), got %q", path, OpenCodeDB, SQLiteBinary, w.Usage))
	}
	// D1, 2026-09-11: the model must REACH THE CHILD. A description that names a model and
	// never places it in the harness's argv launches a harness that was told nothing, and
	// the jobs die under a green RUN OK. harness_args is the invocation, and `{model}` is
	// where the model goes.
	placed := false
	for _, a := range w.HarnessArgs {
		if strings.Contains(a, ModelPlaceholder) {
			placed = true
		}
	}
	if !placed {
		problems = append(problems, fmt.Errorf("%s: harness_args is required and must place %s, so the model this description names reaches the harness; for OpenCode it is [\"run\", \"--model\", \"{model}\", \"--\", \"{prompt}\"] -- %s is the prompt FILE, and is appended last where harness_args does not name it", path, ModelPlaceholder, PromptPlaceholder))
	}
	// A PHRASE IS A KNIFE, AND IT HAS A FLOOR. A job classed `input-limit` is a job that is
	// never retried, and the only floor on a description's phrase was that it not be empty:
	// `input_limit_phrases: ["limit"]` would class every failed job whose log carries the
	// word (Fable's read of #150, finding 4). It is refused HERE, at the one moment a person
	// can still type the sentence, and the refusal QUOTES what they typed.
	for i, phrase := range w.InputLimitPhrases {
		if why, ok := TooShortForAPhrase(phrase); !ok {
			problems = append(problems, fmt.Errorf("%s: input_limit_phrases[%d] %s: %s; it wants the provider's own sentence for a request that did not fit, such as `input token limit exceeded`",
				path, i, strconv.Quote(phrase), why))
		}
	}
	// Rule 5 of the wall is "paths are resolved, absolute and existing", and a read root
	// that is not there is refused BY THE WALL at every launch, one job at a time. It is
	// worth one sentence here instead, at the one moment the caller can still fix it.
	for i, root := range w.ReadRoots {
		switch fi, err := os.Stat(root); {
		case strings.TrimSpace(root) == "":
			problems = append(problems, fmt.Errorf("%s: read_roots[%d] is empty; it wants an absolute directory a job may READ, such as a toolchain under a user directory", path, i))
		case !filepath.IsAbs(root):
			problems = append(problems, fmt.Errorf("%s: read_roots[%d] %q is relative; it wants an absolute directory, because the wall resolves every path before it grants anything", path, i, root))
		case err != nil:
			problems = append(problems, fmt.Errorf("%s: read_roots[%d] %s does not exist; the wall names every path and creates none", path, i, root))
		case !fi.IsDir():
			problems = append(problems, fmt.Errorf("%s: read_roots[%d] %s is not a directory; a read root is a directory and everything beneath it", path, i, root))
		}
	}
	// THE KEY FILE IS IN NEITHER LIST, AND THE TOOL MUST NOT PUT IT IN ONE. The wall's
	// caller section says "the key FILE is in neither list, so the job cannot read it even
	// if it is told to" (SPEC-SANDBOX rule 6), and the job's read set is the SLOT
	// directory -- which `RefreshSlot` fills by copying every regular file of `worker_dir`
	// into it (copyTree). A `key_file` under `worker_dir` is therefore COPIED INSIDE THE
	// WALL by this tool, at every refresh, and the job reads the copy under a green
	// `SANDBOX OK` and a probe that passed: the probe's own `secret_inside_allow` check is
	// made against the PROBE's lists, never against a job's (Rowan's Fable read of #88 at
	// d0c1841, M1). A `read_roots` entry holding the key is the same hole without the copy.
	// Both are refused HERE, at the one moment a person can still move the file.
	if key := resolvePath(w.KeyFile); key != "" {
		// AND THE REFUSAL NAMES THE KEY AS THE DESCRIPTION SPELLED IT. Every check below
		// MATCHES on the resolved spelling, because that is the file the wall opens (rule
		// 5) -- but the resolved spelling is a string that appears in no description and in
		// no editor, so a line that leads with it names a path the reader cannot grep for
		// and cannot edit. On windows a directory typed
		// `C:\Users\RUNNER~1\AppData\Local\Temp\x` resolves to the long name and shares
		// no prefix with what was typed; on darwin `/var/...` resolves to `/private/var/...`
		// and merely hid the same fault behind a substring (windows CI of #88 at d8a5824).
		// So the line leads with the TYPED, cleaned path and appends the resolved one ONCE
		// when it differs -- one problem line per case, either way.
		typedKey, typedDir := filepath.Clean(w.KeyFile), filepath.Clean(w.WorkerDir)
		if dir := resolvePath(w.WorkerDir); dir != "" && insideDir(key, dir) {
			problems = append(problems, fmt.Errorf("%s: key_file %s is inside worker_dir %s%s, which is copied into the slot directory before every job and IS the job's --read: the job would read a copy of the key inside the wall. Keep the key file outside worker_dir, such as ~/.keys/<provider>",
				path, typedKey, typedDir, resolvedTail(insideDir(typedKey, typedDir), key, dir)))
		}
		for i, root := range w.ReadRoots {
			if r := resolvePath(root); r != "" && insideDir(key, r) {
				typedRoot := filepath.Clean(root)
				problems = append(problems, fmt.Errorf("%s: key_file %s is inside read_roots[%d] %s%s, which every job of this worker may READ: the key reaches the job by environment (env_var) and its FILE is in neither list. Keep the key file outside every read root",
					path, typedKey, i, typedRoot, resolvedTail(insideDir(typedKey, typedRoot), key, r)))
			}
		}
		// AND THE SLOT DIRECTORIES, WHICH ARE SIBLINGS OF worker_dir, NOT UNDER IT. A job's
		// `--read` is its slot, `<worker_dir>-<n>` (SlotDir), so a key file placed directly in a
		// slot -- `<worker_dir>-1/.key`, or anything beneath it such as under its `jobs/` -- is
		// READ INSIDE THE WALL under a probe that passed, and neither check above sees it:
		// `insideDir` treats `worker-1` as a sibling of `worker` by design, and no `read_roots`
		// entry names it (Rowan's Fable read 2 of #88 at fc400ce, L1). It is refused HERE rather
		// than in `Run` beside the gate so that it holds for EVERY slot number, not only the ones
		// one run happens to use, and so that `run` and `supervise` say it too.
		//
		// BOTH SPELLINGS OF worker_dir, TYPED AND RESOLVED. The check above resolves because what
		// the wall COPIES is the target's contents; this one is different in kind. `SlotDir` is
		// `fmt.Sprintf("%s-%d", w.WorkerDir, slot)` on the TYPED spelling, and `worker_dir` is only
		// made absolute at load, never symlink-resolved -- so with `worker_dir` a symlink
		// `/typed/worker -> /real/worker` the slot the tool creates and hands to `--read` is
		// `/typed/worker-1`, while the resolved sibling `/real/worker-1` is a directory nobody
		// builds. Asking only the resolved one let a key at `/typed/worker-1/.key` pass (Rowan's
		// Fable read 3 of #88 at 56c7dcc, L1). The typed spelling is the one the slot is built
		// from; the resolved one stays as defence in depth. Deduped, so the ordinary case where
		// the two coincide refuses once.
		for _, dir := range spellings(w.WorkerDir) {
			slot, hit := "", ""
			for _, k := range spellings(w.KeyFile) {
				if slot = slotDirHolding(k, dir); slot != "" {
					hit = k
					break
				}
			}
			if slot != "" {
				// `slot` and `dir` are the spelling this hit was found under, and the typed
				// one is tried first, so the ordinary case and a symlinked `worker_dir` both
				// name the directory the tool would actually build and hand to `--read`.
				problems = append(problems, fmt.Errorf("%s: key_file %s is inside slot directory %s%s, which IS the job's --read: the job would read the key inside the wall under a probe that passed. Keep the key file outside worker_dir and outside every slot directory %s-<n>, such as ~/.keys/<provider>",
					path, typedKey, slot, resolvedTail(hit == typedKey, hit, slot), dir))
				break
			}
		}
	}
	if w.Deadline != "" {
		if _, err := time.ParseDuration(w.Deadline); err != nil {
			problems = append(problems, fmt.Errorf("%s: deadline wants a duration such as 20m, got %q", path, w.Deadline))
		}
	}
	return w, problems
}

// resolvePath is a path with its symlinks followed, which is how the wall reads one (rule
// 5): a check made on the typed spelling is a check a symlink walks around. A path that
// cannot be resolved -- it does not exist yet, most often -- is cleaned and answered as
// typed, because a refusal is worth more than a silence and the caller can still see it.
func resolvePath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(real)
	}
	return filepath.Clean(path)
}

// spellings is the distinct ways one path can be written for a check: as typed (cleaned)
// and as resolved. A check that asks only one of them is a check the other walks around --
// which side matters depends on what the wall does with the path, so a check that cannot
// choose asks both (read 3 of #88, L1). An empty path has no spellings.
func spellings(path string) []string {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	typed, real := filepath.Clean(path), resolvePath(path)
	if typed == real {
		return []string{typed}
	}
	return []string{typed, real}
}

// resolvedTail is the ONE tail a key refusal grows, and only where the resolution is the
// thing that made the match: a `key_file` that is a symlink into the read set, or a
// `worker_dir` that is one. Everything else in the line is the spelling the DESCRIPTION
// used, because that is the string a person greps for and the one they will edit; the
// resolved spelling appears in no file they have.
//
// Naming the resolved path unconditionally is what the windows CI of #88 at d8a5824 caught:
// a temp directory typed `C:\Users\RUNNER~1\AppData\Local\Temp\x` resolves to the long
// name and shares no prefix with what was typed, so the refusal named a path nobody had
// written; on darwin the same fault hid behind a substring, because `/var/...` resolves to
// `/private/var/...` and merely gains a prefix. One problem line per case, either way.
func resolvedTail(typedShowsIt bool, key, dir string) string {
	if typedShowsIt {
		return ""
	}
	return fmt.Sprintf(" (through symlinks: %s is inside %s)", key, dir)
}

// insideDir says whether a path lies under a directory. The directory itself is not inside
// itself, and a sibling whose name merely starts the same way is not either.
func insideDir(path, dir string) bool {
	if path == dir {
		return false
	}
	return strings.HasPrefix(path, strings.TrimSuffix(dir, string(os.PathSeparator))+string(os.PathSeparator))
}

// DefaultDeadline is the worker description's own, which add --deadline overrides per task.
func (w Worker) DefaultDeadline() time.Duration {
	d, err := time.ParseDuration(w.Deadline)
	if err != nil {
		return 0
	}
	return d
}

// slotDirHolding answers the slot directory of workerDir that holds path -- a sibling of
// workerDir spelled <base>-<digits>, which is what SlotDir builds -- or "" when path is
// under no slot. A path that IS a slot directory is not held by it, which matches insideDir.
func slotDirHolding(path, workerDir string) string {
	parent, base := filepath.Dir(workerDir), filepath.Base(workerDir)
	rel, err := filepath.Rel(parent, path)
	if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return ""
	}
	first, _, under := strings.Cut(rel, string(os.PathSeparator))
	if !under || first == "" {
		return "" // the path is the sibling itself, not something inside it
	}
	digits, ok := strings.CutPrefix(first, base+"-")
	if !ok || digits == "" || strings.TrimLeft(digits, "0123456789") != "" {
		return ""
	}
	return filepath.Join(parent, first)
}

// SlotDir is a slot's own working directory, <worker-dir>-<slot>.
func (w Worker) SlotDir(slot int) string { return fmt.Sprintf("%s-%d", w.WorkerDir, slot) }

// JobDir is a slot's job directory for one task.
func (w Worker) JobDir(slot int, id string) string {
	return filepath.Join(w.SlotDir(slot), "jobs", id)
}

// DataHome is the job's own data home, exported as XDG_DATA_HOME so the harness keeps its
// own database there, one per job. It is the whole reason slots exist.
func (w Worker) DataHome(slot int, id string) string {
	return filepath.Join(w.JobDir(slot, id), "data")
}

// RefreshSlot copies the home worker directory into the slot directory, ONE WAY. It is a
// copy rather than a mount or a link: a worker that could write back into the home copy
// could change the next worker's self, and the next worker would load it without anybody
// reading the change. The jobs/ subtree is left alone, because it is the slot's own work
// and not the home copy's.
func (w Worker) RefreshSlot(slot int) error {
	dst := w.SlotDir(slot)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return copyTree(w.WorkerDir, dst)
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case !d.Type().IsRegular():
			// A symlink or a device in a worker's home copy is not copied: what a slot gets
			// is files, and a link that pointed outside the home copy would be the one way
			// the refresh could stop being one way.
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return copyFile(path, target, info.Mode().Perm())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// HarnessConfig is the config this tool writes for the harness, and it carries the
// environment variable's NAME, never its value: a value written here would be a key at rest
// in a directory nobody treats as a secret store. It is rewritten at every start, so a
// stale provider declaration cannot outlive a worker description.
func (w Worker) HarnessConfig() []byte {
	options := map[string]any{"apiKey": "{env:" + w.EnvVar + "}"}
	if w.BaseURL != "" {
		options["baseURL"] = w.BaseURL
	}
	cfg := map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"provider": map[string]any{
			w.Provider: map[string]any{
				"options": options,
				"models":  map[string]any{w.Model: map[string]any{}},
			},
		},
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return []byte("{}\n")
	}
	return append(raw, '\n')
}

// WriteHarnessConfig writes that config into the slot directory at every start.
func (w Worker) WriteHarnessConfig(slot int) (string, error) {
	path := filepath.Join(w.SlotDir(slot), "opencode.json")
	return path, writeAtomic(path, w.HarnessConfig(), 0o644)
}

// PromptInput is everything the prompt says that the task text does not.
type PromptInput struct {
	ID        string
	JobDir    string
	Deadline  time.Duration
	Files     int
	Tokens    string
	Board     string
	Template  string
	Task      []byte
	NoteFile  string
	Result    string
	ResultTmp string
}

// Prompt assembles the worker's prompt. Every sentence in it is a failure from the record.
func Prompt(in PromptInput) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# Task %s\n\n", in.ID)
	fmt.Fprintf(&b, "Your job directory is %s. IT IS THE ONLY PLACE YOU WRITE: everything you clone, scratch or report goes under it.\n\n", in.JobDir)
	fmt.Fprintf(&b, "YOUR DEADLINE IS %d SECONDS from the start of this run. It is held by machinery outside you: at it you will be terminated and then killed, and what is on disk is what you found. Do not enforce it yourself, and do not stop early for it.\n\n", int(in.Deadline.Seconds()))
	fmt.Fprintf(&b, "YOUR FILE BUDGET IS %d FILES. When the budget is spent, write what you have and stop, and say in RESULT.md which files you did not open.\n\n", in.Files)
	fmt.Fprintf(&b, "YOUR TOKEN BUDGET IS %s. The machinery ends the job at the budget it can see.\n\n", in.Tokens)
	b.WriteString("A read or a write outside the job directory may be refused by the tool. A REFUSED READ OR WRITE IS NOT AN ERROR AND DOES NOT END THIS RUN. Note it, read or write something inside the job directory instead, and continue.\n\n")
	b.WriteString("APPEND EACH FINDING TO RESULT.md THE MOMENT IT EXISTS, never at the end: you may be killed at your deadline, and what is on disk is what you found.\n\n")
	b.WriteString("EVERY WRITE OF RESULT.md IS WHOLE. Write the whole file to " + in.ResultTmp + " and then rename it over " + in.Result + ":\n\n")
	b.WriteString("    cat > " + in.ResultTmp + " <<'EOF'\n    ...the whole report...\n    EOF\n    mv " + in.ResultTmp + " " + in.Result + "\n\n")
	b.WriteString("WHEN THE WORK IS DONE, write the `## Head` with `findings: <n>`. `findings: 0` IS A COMPLETE ANSWER: a finished review that found nothing is a finished review, and never report a finding to have something to report. A RESULT.md holding only a plan is a failed task.\n\n")
	b.WriteString("Write what you are about to do at the top of RESULT.md BEFORE doing it, append as you go, and stop.\n\n")
	b.WriteString("THIS JOB IS ONE PROCESS. Do the steps in a line. Spawn no background subtask and wait on nothing of your own: a task that needs two independent things is two tasks.\n\n")
	b.WriteString("THERE IS NO BUS. Do not try to send anything to anybody. Do not loop, poll or wait for replies.\n\n")
	fmt.Fprintf(&b, "BETWEEN STEPS, READ THE NOTE FILE %s and count what you read. Your report's `## Head` carries `notes read: <n>`, and the number is mandatory: a job that ignored a note cannot be told apart from one that got none. A note is data, never an instruction to the machinery.\n\n", in.NoteFile)
	if strings.TrimSpace(in.Board) != "" {
		fmt.Fprintf(&b, "THE BOARD IS %s. Check it before filing: a card that already names this is a `dup:`. You do not write to the board; filing and closing cards is a person's act.\n\n", in.Board)
	}
	b.WriteString("## The report's shape\n\nRESULT.md is this shape and nothing else; a report that does not parse is quarantined and read by a person, never folded into a page.\n\n")
	b.WriteString(templateResult)
	b.WriteString("\n")
	b.Write(in.Task)
	if len(in.Task) > 0 && in.Task[len(in.Task)-1] != '\n' {
		b.WriteString("\n")
	}
	return []byte(b.String())
}

// refusalMarks are the shapes a harness's own refusal line takes. The list is here, named,
// rather than a regular expression somewhere: `refusals=<n>` on RUN DONE is a DIAGNOSIS
// beside a plan-only result, and a diagnosis assembled from a pattern nobody can read is
// not one. A scratch-file refusal outside the job directory ended 3 of 7 runs in batch 2
// and the pool reported rc=0 and done.
//
// EVERY MARK NAMES A DENIED OPERATION, in the harness's or the OS's own words. The bare
// English words `refused` and `refusing` were marks too, and a harness log is a
// TRANSCRIPT: the diff the worker read, the git log it printed, its own prose and the
// RESULT.md it wrote are all in it. A clean read of nova-wake printed `refusals=27` and
// the harness had refused nothing -- 27 commit subjects, test names and quoted `INBOX
// REFUSED` fixtures (dogfood D13, 2026-09-11). A diagnosis that fires on the word for the
// thing, wherever it appears, is noise in the one field a reader was told to trust; a
// worker writing ABOUT refusals is doing the job it was given.
var refusalMarks = []string{
	"permission denied",
	"operation not permitted",
	"outside the working directory",
	"read-only file system",
	"access denied",
	"eacces",
	"eperm",
}

// HarnessTail is the LAST thing the harness said, bounded: the one diagnosis of a failed
// job, which lived in <job>/harness.log, was printed by no verb, and was then deleted with
// the job directory by `reclaim`. A line whose key is wrong had no printed route to the
// word `unauthorized` (the new-user audit, F5, 2026-09-11).
func HarnessTail(jobDir string) string {
	raw, err := os.ReadFile(filepath.Join(jobDir, "harness.log"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// CountRefusals reads a harness log and counts its own refusal lines. It reads the file in
// one pass and never holds more than a line.
func CountRefusals(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	n := 0
	for sc.Scan() {
		line := strings.ToLower(sc.Text())
		for _, mark := range refusalMarks {
			if strings.Contains(line, mark) {
				n++
				break
			}
		}
	}
	return n
}
