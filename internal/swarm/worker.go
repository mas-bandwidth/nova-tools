package swarm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
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
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	BaseURL  string `json:"base_url,omitempty"`
	EnvVar   string `json:"env_var"`
	KeyFile  string `json:"key_file"`
	// SECRET: the name of the environment variable that holds the secret value in this
	// process's own environment, delivered by `nova-secrets exec`. It replaces `key_file`
	// when the key is sealed once and delivered at use, never written to disk. Exactly one
	// of `key_file` and `secret` is set; run and supervise require the variable to be
	// present and non-empty, and the value is never written to a file, never printed,
	// and never surfaces in a RUN or SUPERVISE line.
	Secret string `json:"secret,omitempty"`
	Usage  string `json:"usage"`
	// CLASS (CARD-8390): `public` means this worker never sees private source
	// (the public-class gate); `paid` is the default. Empty decodes as paid so
	// no description written before the gate changes meaning by being re-read.
	//
	// CLASS (worker check): `class` is the one word public or paid, an OPTIONAL
	// field a description may carry and the launcher never requires. `nova-swarm
	// worker check` validates it when it is present. The two budgets below are
	// the same fields worker check validates.
	Class       string   `json:"class,omitempty"`
	Harness     string   `json:"harness"`
	HarnessArgs []string `json:"harness_args,omitempty"`
	WorkerDir   string   `json:"worker_dir"`
	Deadline    string   `json:"deadline"`
	Board       string   `json:"board,omitempty"`
	// READ ROOTS: an optional field, and it names directories the sandbox may grant
	// read access to beyond the OS and toolchain roots. A toolchain installed in a
	// user directory -- Go under ~/go, node under ~/.nvm, etc. -- sits outside those
	// roots, so without this list a harness that needs it dies inside the sandbox.
	// It is OPTIONAL, it is a list of absolute existing directories, and it is
	// READ-ONLY: the write set belongs to the job alone and is not configurable from
	// a description file. A worker needing nothing beyond standard roots declares none.
	ReadRoots []string `json:"read_roots,omitempty"`

	// CARD BUDGET (CARD-8317): the optional per-card stop. A runaway card is a
	// prompt defect, not a model one: max_turns caps the harness output log's
	// assistant turns and max_cache_read caps the observed cache_read, and run
	// stops the card at either with end=budget and a PROMPT-DEFECT line. Zero
	// is unset; negative is refused at load.
	MaxTurns     int `json:"max_turns,omitempty"`
	MaxCacheRead int `json:"max_cache_read,omitempty"`

	// PROVIDER PHRASES: captures the error sentences that providers print when a request
	// does not fit their limits. Each provider uses its own wording; the tool carries a
	// lookup table of known phrases and a description may add entries for its provider. It
	// never replaces an existing entry: teaching the tool one provider's words is additive
	// and cannot silently un-teach another's.
	InputLimitPhrases []string `json:"input_limit_phrases,omitempty"`

	// LAUNCH GRACE: how long a harness may run before its exit stops counting as a launch
	// failure. A death inside this window whose log tail names a provider server error is
	// retried by the dispatcher rather than filed; a slow failure takes longer and is a
	// real run that failed, so it is never retried. OPTIONAL: the default is
	// DefaultLaunchGrace (15s).
	LaunchGrace string `json:"launch_grace,omitempty"`

	// KEEP DATA: when true, asks `run` to preserve the finished slots' data/ and tmp/
	// directories at task end. It is a debugging toggle: the default is false, and the
	// shared per-bench caches are never touched by it either way.
	KeepData bool `json:"keep_data,omitempty"`
}

// The usage sources a description may declare (rule 13). There are two.
//
// `opencode` reads the job's own SQLite database read-only through `sqlite3 -readonly`,
// which is what it actually does now -- five token types, a dash for absence. A caller
// names the SOURCE, never the underlying file that a harness might write.
const (
	UsageOpenCode = "opencode"
	UsageNone     = "none"
)

// The placeholders a worker description may write into harness_args.
const (
	ModelPlaceholder  = "{model}"
	PromptPlaceholder = "{prompt}"
)

// LoadWorker reads and checks a worker description, reporting EVERY independent problem in
// one run (ONBOARDING point 2) rather than sending a first run back once per field.
func LoadWorker(path string) (Worker, []error) {
	var w Worker
	raw, err := os.ReadFile(path)
	if err != nil {
		return w, []error{fmt.Errorf("--worker wants a readable worker description (a JSON file naming provider, model, env_var, key_file or secret, harness, worker_dir, deadline and usage): %s", redactedReason(err))}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&w); err != nil {
		return w, []error{fmt.Errorf("%s is not a worker description this tool can read (%v); the fields are name, provider, model, base_url, env_var, key_file, secret, usage, class, harness, harness_args, worker_dir, deadline, board, max_turns, max_cache_read, read_roots, input_limit_phrases, launch_grace", path, err)}
	}
	// EVERY PATH IN A WORKER DESCRIPTION IS ABSOLUTE FROM HERE ON. The harness runs with
	// its cwd set to the SLOT directory, and the paths this tool hands it -- the prompt
	// file, the job directory the prompt calls the only place it writes -- are built from
	// the description. Relative worker_dir is rejected at load, so every path resolves
	// correctly from where the child process stands.
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
	// SECRET IMPLIES ENV_VAR: a description that names `secret` but no
	// `env_var` loads with env_var defaulting to the secret NAME -- the key is delivered
	// by `nova-secrets exec` under that NAME, so the variable name matches in both fields.
	// An explicit `env_var` beside `secret` is kept as typed.
	if w.Secret != "" && w.EnvVar == "" {
		w.EnvVar = w.Secret
	}
	want := func(value, field, wants string) {
		if strings.TrimSpace(value) == "" {
			problems = append(problems, fmt.Errorf("%s: %s is required; it wants %s", path, field, wants))
		}
	}
	want(w.Name, "name", "the name this worker is called on a RUN POOL line")
	want(w.Provider, "provider", "the provider id the harness config declares, such as deepseek")
	want(w.Model, "model", "the model id, such as deepseek-chat")
	want(w.EnvVar, "env_var", "the NAME of the environment variable the provider reads, such as DEEPSEEK_API_KEY; never its value")
	// THE KEY IS NAMED TWO WAYS, AND A DESCRIPTION CARRIES EXACTLY ONE. A
	// `key_file` names a path on disk that the harness reads as data. A `secret` names
	// the environment variable through which nova-secrets exec delivers the key at runtime.
	// Exactly one must be present; neither or both is refused here, where it can still be
	// corrected.
	switch {
	case w.Secret == "" && w.KeyFile == "":
		problems = append(problems, fmt.Errorf("%s: a description names its key either by key_file (a path, the old shape) or by secret (the NAME of the variable nova-secrets exec delivers, such as DEEPSEEK_API_KEY); this description has neither", path))
	case w.Secret != "" && w.KeyFile != "":
		problems = append(problems, fmt.Errorf("%s: key_file %s and secret %s both name a key; a description carries one or the other, never both -- the key is either a file a person wrote (key_file) or a variable nova-secrets exec delivers (secret)", path, w.KeyFile, w.Secret))
	}
	if w.Secret != "" && !validEnvName(w.Secret) {
		problems = append(problems, fmt.Errorf("%s: secret %q is not an environment variable NAME; it wants the NAME nova-secrets exec delivers, such as DEEPSEEK_API_KEY, and never a value or a path", path, w.Secret))
	}
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
	// CLASS (CARD-8390): `public` confines the worker to listed public source,
	// `paid` (and empty, the default) confines nothing. Anything else is a
	// misspelled confinement and is refused here, where it can still be fixed.
	switch w.Class {
	case "", WorkerClassPaid, WorkerClassPublic:
	default:
		problems = append(problems, fmt.Errorf("%s: class wants `public` (this worker never sees a card that clones an unlisted repo) or `paid` (the default), got %q", path, w.Class))
	}
	// THE MODEL MUST REACH THE CHILD: a description that names a model but never places
	// it in harness_args launches a harness that receives no model argument at all, so the
	// job fails silently under a green RUN OK. harness_args is the invocation line, and
	// {model} marks where the model goes.
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
	// never retried, and short phrases like a bare word would class too broadly by matching
	// noise in any log. This validation runs at description load, where the caller can still
	// correct what they typed, and the refusal QUOTES their exact input.
	for i, phrase := range w.InputLimitPhrases {
		if why, ok := TooShortForAPhrase(phrase); !ok {
			problems = append(problems, fmt.Errorf("%s: input_limit_phrases[%d] %s: %s; it wants the provider's own sentence for a request that did not fit, such as `input token limit exceeded`",
				path, i, strconv.Quote(phrase), why))
		}
	}
	// The wall resolves every path before granting access, and a read root that is not there
	// would be discovered by the sandbox at every launch. One validation sentence here is
	// better, at the moment the caller can still fix it.
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
	// THE KEY FILE IS IN NEITHER LIST, AND THE TOOL MUST NOT PUT IT IN ONE. The sandbox
	// ensures the key file itself is never in either list, so the job cannot read it even
	// if told to. The job's read set is the SLOT directory -- which RefreshSlot fills by
	// copying every regular file of worker_dir into it. A key_file under worker_dir is
	// therefore copied inside the sandbox at every refresh, and the job reads it under a
	// green SANDBOX OK through a probe that passed: the probe's own allow check validates
	// against its own lists, not the job's. A read_roots entry holding the key creates the
	// same vulnerability without the copy step. Both are refused here, at the moment the
	// caller can still move the file.
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
		// --read is its slot, <worker_dir>-<n> (SlotDir), so a key file placed directly in a
		// slot -- <worker_dir>-1/.key or anything beneath it such as under jobs/ -- is READ
		// INSIDE THE WALL under a probe that passed. The sibling check above does not see it:
		// insideDir treats the slot as a sibling of the base worker_dir by design. It is refused
		// HERE rather than at runtime so that the guard applies to every slot number, not only
		// the ones a particular run creates, and so that both run and supervise report it.
		//
		// BOTH SPELLINGS OF worker_dir, TYPED AND RESOLVED. The check above resolves because what
		// the sandbox copies is the target's contents; this one is different in kind. SlotDir
		// builds from the TYPED spelling, so with worker_dir a symlink the slot created is based
		// on the typed path while the resolved sibling directory may not exist. Asking only the
		// resolved one lets a key behind the typed name slip through. The typed spelling is the
		// one the slot is built from; the resolved one stays as defence in depth. Deduped, so the
		// ordinary case where the two coincide refuses once.
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
	if w.MaxTurns < 0 {
		problems = append(problems, fmt.Errorf("%s: max_turns wants a non-negative turn count, got %d", path, w.MaxTurns))
	}
	if w.MaxCacheRead < 0 {
		problems = append(problems, fmt.Errorf("%s: max_cache_read wants a non-negative token count, got %d", path, w.MaxCacheRead))
	}
	if w.LaunchGrace != "" {
		if d, err := time.ParseDuration(w.LaunchGrace); err != nil || d <= 0 {
			problems = append(problems, fmt.Errorf("%s: launch_grace wants a positive duration such as 15s, got %q", path, w.LaunchGrace))
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
//
// String comparison over cleaned paths is case-SENSITIVE but APFS is case-INsensitive by
// default: a key_file typed `/x/Worker/.key` under a worker_dir of `/x/worker` could be
// THE SAME FILE that slot copy picks up while the prefix check says no. Evaluating
// symlinks does not fold case on darwin, so resolving does not close this gap -- nor would
// lowercasing, which is wrong on a case-sensitive filesystem where two spellings differ in
// capitalization.
//
// The string prefix stays as the fast answer, and it is the only option available for paths
// that do not exist yet -- resolvePath answers as typed for one, and a refusal is worth more
// than silence. Where the prefix says no, each EXISTING ancestor of the path is asked
// os.SameFile against the directory: device and inode is the question the filesystem itself
// answers, so it holds for case folds, bind mounts at two names, and hard-linked directories.
// The walk starts at the path's parent, so a path identical to the directory under another
// spelling is still not inside it. This runs at load, once per description, never per job.
func insideDir(path, dir string) bool {
	if path == dir {
		return false
	}
	if strings.HasPrefix(path, strings.TrimSuffix(dir, string(os.PathSeparator))+string(os.PathSeparator)) {
		return true
	}
	target, err := os.Stat(dir)
	if err != nil {
		return false // a directory that is not there holds nothing
	}
	for parent := filepath.Dir(path); ; {
		if fi, err := os.Stat(parent); err == nil && os.SameFile(fi, target) {
			return true
		}
		up := filepath.Dir(parent)
		if up == parent {
			return false // the root is its own parent: the walk is over
		}
		parent = up
	}
}

// DefaultDeadline is the worker description's own, which add --deadline overrides per task.
func (w Worker) DefaultDeadline() time.Duration {
	d, err := time.ParseDuration(w.Deadline)
	if err != nil {
		return 0
	}
	return d
}

// validEnvName is the shape a `secret` may take: an environment variable NAME, the same
// shape a POSIX shell would accept for one. A name with a `=` is a value dressed as a
// name, and a name with a space or a dash is a typo the loader catches where the caller
// can still fix it rather than at the first run.
func validEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// slotDirHolding answers the slot directory of workerDir that holds path -- a sibling of
// workerDir spelled <base>-<digits>, which is what SlotDir builds -- or "" when path is
// under no slot. A path that IS a slot directory is not held by it, which matches insideDir.
//
// The candidate name is checked against the filesystem's own equality rather than text:
// case-insensitive volumes could let a mismatched capitalization slip through while a
// simple text comparison says no. Lowercasing is not the repair either -- on a
// case-sensitive filesystem two differently-cased names are distinct directories.
//
// The candidate comes from the path, not from workerDir, which is why this works
// differently from insideDir's approach. A slot directory need not exist at load since
// slots are created at run time, so there may be no inode to compare against. The
// ancestors of the path are walked toward filepath.Dir(workerDir), and each direct child
// has its name judged as digits for the number and the base compared under the filesystem's
// own equality via namesOneFile, which measures the fold rather than checking runtime.GOOS.
//
// The walk starts at the path's parent, so a path identical to a slot directory is still
// not held by it. Both spellings of workerDir are still asked by the caller: SlotDir
// builds the slot from the typed spelling.
func slotDirHolding(path, workerDir string) string {
	parent, base := filepath.Dir(workerDir), filepath.Base(workerDir)
	for dir := filepath.Dir(path); ; {
		up := filepath.Dir(dir)
		if up == dir {
			return "" // the root is its own parent: the walk is over
		}
		if sameDir(up, parent) {
			name := filepath.Base(dir)
			i := strings.LastIndex(name, "-")
			if i <= 0 {
				return "" // no slot number, so not a slot directory
			}
			digits := name[i+1:]
			if digits == "" || strings.TrimLeft(digits, "0123456789") != "" {
				return ""
			}
			// The full spellings: the candidate against what SlotDir would build for that number.
			// Where both directories exist, their inodes answer for themselves and no case fold
			// is guessed.
			if !namesOneFile(up, name, base+"-"+digits) {
				return "" // another worker's slot, or a neighbour that merely reads alike
			}
			return dir
		}
		dir = up
	}
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
		// THE SUBAGENT REFUSAL HOOK. A card that tries to spawn a subagent has the spawn
		// refused by this hook and logged once. A spawned agent multiplies exploration tokens,
		// hides its work from the log, and on a darwin bench goes silent under the wall where
		// the card is killed as idle. Preventing subagent spawns protects resources, preserves
		// observability, and avoids hangs in sandboxed environments.
		"hooks": map[string]any{
			"before_tool_use": []map[string]any{
				{
					"match":   map[string]any{"tool_name": "task"},
					"refuse":  true,
					"message": "subagent spawn refused (NO-SUBAGENTS): a spawned agent multiplies exploration tokens, hides its work from the log, and on a darwin bench goes silent under the wall",
				},
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

// HarnessTail returns the last bytes of the harness log, bounded by oneline.TailBytes.
// It is the primary diagnostic for a failed job, read from <job>/harness.log before the
// reclaim verb deletes the entire job directory. A malformed line key could prevent matching
// an unauthorized access error in the log.
func HarnessTail(jobDir string) string {
	kept, dropped := tailBytes(filepath.Join(jobDir, "harness.log"), oneline.TailBytes)
	if kept == "" {
		return ""
	}
	if dropped <= 0 {
		return kept
	}
	return fmt.Sprintf("...+%dB", dropped) + kept
}

// tailBytes streams the LAST n bytes of a worker-writable regular file, never holding the
// file whole, and returns the retained tail plus the number of bytes dropped in front of it.
// It opens on openRegularRead's terms (a symlink or FIFO is refused before a byte is asked
// for) and cuts on a line boundary, so the tail opens on a complete line rather than a
// fragment the seek landed in the middle of.
func tailBytes(path string, n int) (string, int64) {
	f, err := openRegularRead(path)
	if err != nil {
		return "", 0
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", 0
	}
	size := fi.Size()
	if size == 0 {
		return "", 0
	}
	if size <= int64(n) {
		raw, err := io.ReadAll(f)
		if err != nil {
			return "", 0
		}
		return strings.TrimSpace(string(raw)), 0
	}
	// Read only the tail window: the ceiling plus slack for the mark and for the leading
	// line the seek lands in the middle of.
	const slack = 4096
	window := int64(n + slack)
	off := max(size-window, 0)
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return "", 0
	}
	raw, err := io.ReadAll(io.LimitReader(f, window))
	if err != nil {
		return "", 0
	}
	s := string(raw)
	if nl := strings.IndexByte(s, '\n'); nl >= 0 {
		s = s[nl+1:]
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "", 0
	}
	// Reserve the widest the mark can be, then keep the last line-bounded bytes that fit
	// under the ceiling, the way oneline.Cap reserves its own mark before cutting.
	maxMark := len(fmt.Sprintf("...+%dB", size))
	budget := max(n-maxMark, 1)
	if len(s) > budget {
		s = s[len(s)-budget:]
		if nl := strings.IndexByte(s, '\n'); nl >= 0 {
			s = s[nl+1:]
		}
		s = strings.TrimSpace(s)
	}
	if s == "" {
		return "", 0
	}
	return s, size - int64(len(s))
}
