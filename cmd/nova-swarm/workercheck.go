package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// workerDrift is one reason a worker description cannot launch, named by the field it
// belongs to. Its String is the one line the verb prints for it.
type workerDrift struct {
	field string
	why   string
}

func (d workerDrift) String() string {
	return "WORKER DRIFT " + d.field + ": " + d.why
}

// cmdWorker is the `worker` verb. Its one subcommand, `check`, validates a description
// BEFORE any launch: the loader's own field errors, then everything the loader does not ask
// -- whether the harness can be run, whether both harness placeholders are present, whether
// a named secret is in this process's environment, whether the worker directory can exist,
// and whether the optional class and budget fields hold. A description with no drift prints
// one WORKER OK line, exit 0; one with drifts prints them, exit 2, and starts nothing.
func cmdWorker(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "check" {
		return refuse(stderr, " worker", "the only worker subcommand is `check <description.json> [--env] [--max <n>]`")
	}
	rest := args[1:]
	path, requireEnv, max := "", false, bounded.Default
	for i := 0; i < len(rest); i++ {
		switch {
		case rest[i] == "--env":
			requireEnv = true
		case rest[i] == "--max":
			if i+1 >= len(rest) {
				return refuse(stderr, " worker check", "--max wants a count")
			}
			i++
			n, err := strconv.Atoi(rest[i])
			if err != nil {
				return refuse(stderr, " worker check", fmt.Sprintf("--max wants a count, got %q", rest[i]))
			}
			max = n
		case strings.HasPrefix(rest[i], "-"):
			return refuse(stderr, " worker check", fmt.Sprintf("unknown flag %q", rest[i]))
		default:
			if path != "" {
				return refuse(stderr, " worker check", "takes exactly one worker description")
			}
			path = rest[i]
		}
	}
	if path == "" {
		return refuse(stderr, " worker check", "wants a worker description JSON file")
	}
	if max < 0 {
		return refuse(stderr, " worker check", fmt.Sprintf("--max is 0 or more, got %d; 0 shows all", max))
	}

	w, drifts := checkWorkerDescription(path, requireEnv, os.Getenv)
	if len(drifts) == 0 {
		fmt.Fprintf(stdout, "WORKER OK %s model=%s provider=%s class=%s\n",
			oneline.Field(w.Name), oneline.Field(w.Model), oneline.Field(w.Provider), oneline.Field(dash(w.Class)))
		return 0
	}
	list := bounded.Capped(stdout, max, "WORKER", "drift", "--max <n> (0 = all)")
	for _, d := range drifts {
		list.Line(d.String())
	}
	list.More()
	return 2
}

// checkWorkerDescription loads a description with the existing loader and returns the
// loaded worker plus one drift per independent problem. requireEnv asks this process's own
// environment for a `secret`'s value; the value is never returned, printed or named in a
// drift -- only the variable is.
func checkWorkerDescription(path string, requireEnv bool, env func(string) string) (swarm.Worker, []workerDrift) {
	w, problems := swarm.LoadWorker(path)
	var drifts []workerDrift
	seen := map[string]bool{}
	for _, p := range problems {
		d := driftOf(p)
		if !seen[d.field] {
			seen[d.field] = true
			drifts = append(drifts, d)
		}
	}
	add := func(field, why string) {
		if seen[field] {
			return
		}
		seen[field] = true
		drifts = append(drifts, workerDrift{field: field, why: why})
	}

	// The harness the loader only demanded be non-empty must actually run on this machine.
	if !seen["harness"] {
		if why, ok := harnessProblem(w.Harness); !ok {
			add("harness", why)
		}
	}
	// Both placeholders reach the harness: the model this description names and the prompt
	// file the tool appends. The loader pins {model}; {prompt} is asked here.
	if !seen["harness_args"] {
		if why, ok := placeholdersProblem(w.HarnessArgs); !ok {
			add("harness_args", why)
		}
	}
	// A secret is named by the description and, with --env, must be present and non-empty
	// in this process's environment -- the value is never printed.
	if requireEnv && w.Secret != "" && strings.TrimSpace(env(w.Secret)) == "" {
		add("secret", fmt.Sprintf("the worker description's secret %s is absent or empty in this process's environment; run this command under `nova-secrets exec --only %s -- <this command>` (the value is never a file and is never printed)", oneline.Field(w.Secret), oneline.Field(w.Secret)))
	}
	// The worker directory must exist or be creatable; the loader only demanded a name.
	if !seen["worker_dir"] {
		if why, ok := workerDirProblem(w.WorkerDir); !ok {
			add("worker_dir", why)
		}
	}
	// The optional class and budgets, each checked only when present.
	if w.Class != "" && w.Class != "public" && w.Class != "paid" {
		add("class", fmt.Sprintf("class %q is neither public nor paid", w.Class))
	}
	if w.MaxCacheRead < 0 {
		add("max_cache_read", fmt.Sprintf("max_cache_read is at least 1, got %d", w.MaxCacheRead))
	}
	if w.MaxTurns < 0 {
		add("max_turns", fmt.Sprintf("max_turns is at least 1, got %d", w.MaxTurns))
	}
	return w, drifts
}

// driftOf turns one loader problem into a drift named by its field. The loader's messages
// lead with the description's own path and a ": "; stripping it lets the line lead with the
// FIELD, which is what a reader greps for. A message that names no field is filed under
// `worker`, and the key-shape messages name `key`.
func driftOf(err error) workerDrift {
	msg := err.Error()
	rest := msg
	if i := strings.Index(msg, ": "); i >= 0 {
		rest = msg[i+2:]
	}
	field, why := rest, rest
	if j := strings.IndexByte(rest, ' '); j >= 0 {
		field, why = rest[:j], rest[j+1:]
	}
	field = strings.TrimRight(field, ",:;")
	switch {
	case field == "a" || field == "the" || field == "":
		field, why = "key", rest
	case strings.HasPrefix(msg, "--worker wants") ||
		strings.Contains(msg, "is not a worker description this tool can read") ||
		strings.HasPrefix(field, "\"") || strings.HasPrefix(field, "/"):
		field, why = "worker", rest
	}
	return workerDrift{field: field, why: why}
}

// harnessProblem reports whether a description's harness can be run: a name on PATH, or a
// path that exists, is a regular file and is executable by this machine's own rule.
func harnessProblem(harness string) (string, bool) {
	if strings.TrimSpace(harness) == "" {
		return "harness is empty; it wants the harness command to run", false
	}
	candidate := harness
	if !filepath.IsAbs(harness) && !strings.ContainsRune(harness, os.PathSeparator) {
		found, err := exec.LookPath(harness)
		if err != nil {
			return fmt.Sprintf("harness %s is neither on PATH nor a path that exists", oneline.Field(harness)), false
		}
		candidate = found
	}
	fi, err := os.Stat(candidate)
	switch {
	case err != nil:
		return fmt.Sprintf("harness %s does not exist", oneline.Field(candidate)), false
	case fi.IsDir() || !fi.Mode().IsRegular():
		return fmt.Sprintf("harness %s is not a regular file", oneline.Field(candidate)), false
	case !isExecutable(candidate):
		return fmt.Sprintf("harness %s is not executable by this machine", oneline.Field(candidate)), false
	}
	return "", true
}

// placeholdersProblem reports whether harness_args carries both the model and the prompt
// placeholder, and names whichever is missing.
func placeholdersProblem(args []string) (string, bool) {
	havePrompt, haveModel := false, false
	for _, a := range args {
		if strings.Contains(a, swarm.PromptPlaceholder) {
			havePrompt = true
		}
		if strings.Contains(a, swarm.ModelPlaceholder) {
			haveModel = true
		}
	}
	if havePrompt && haveModel {
		return "", true
	}
	var missing []string
	if !havePrompt {
		missing = append(missing, swarm.PromptPlaceholder)
	}
	if !haveModel {
		missing = append(missing, swarm.ModelPlaceholder)
	}
	return "harness_args must carry " + strings.Join(missing, " and ") + ", so the prompt file and the model this description names both reach the harness", false
}

// workerDirProblem reports whether the worker directory exists as a directory or, when it
// does not, whether its nearest existing ancestor is a writable directory that can hold it.
func workerDirProblem(dir string) (string, bool) {
	if strings.TrimSpace(dir) == "" {
		return "worker_dir is empty", false
	}
	if fi, err := os.Stat(dir); err == nil {
		if fi.IsDir() {
			return "", true
		}
		return fmt.Sprintf("worker_dir %s is not a directory", oneline.Field(dir)), false
	}
	for parent := dir; ; {
		up := filepath.Dir(parent)
		if up == parent {
			return fmt.Sprintf("worker_dir %s does not exist and has no existing ancestor to hold it", oneline.Field(dir)), false
		}
		parent = up
		fi, err := os.Stat(parent)
		if err != nil {
			continue
		}
		if !fi.IsDir() {
			return fmt.Sprintf("worker_dir %s does not exist and %s is not a directory", oneline.Field(dir), oneline.Field(parent)), false
		}
		if fi.Mode().Perm()&0o222 == 0 {
			return fmt.Sprintf("worker_dir %s does not exist and %s is not writable", oneline.Field(dir), oneline.Field(parent)), false
		}
		return "", true
	}
}
