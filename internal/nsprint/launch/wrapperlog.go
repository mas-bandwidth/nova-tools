package launch

import (
	"os"
	"path/filepath"
	"strings"
)

// The wrapper log (Glenn's failure-guidance requirement, 2026-09-26): a
// detached wrapper's stdout and stderr never go to /dev/null. They go to one
// per-attempt file the launch receipt names, so a line the wrapper printed
// after LAUNCHED -- nova-card's keepRefusal line when Redis would not take a
// refusal, a crash on the way to the harness -- is on the bench for the
// coordinator to read.
//
// The file lives under the bench's results root, <NOVA_CARD_RESULTS>/launch/,
// beside the refused/ records nova-card keeps there: the job directory is
// removed at card end (SPEC-JOBS), the results root is not. A launcher with no
// results root in its environment keeps the log in launch/ beside the wrapper.

// WrapperLogEnv names the bench's results root; the wrapper log is kept under
// it. It is the same variable nova-card reads for its own records.
const WrapperLogEnv = "NOVA_CARD_RESULTS"

// WrapperLogDir is the directory the logs live in, under the results root or
// beside the wrapper.
const WrapperLogDir = "launch"

// WrapperLogPath is the per-attempt wrapper log for a wrapper started with
// args (argv[0] is the command name): <root>/launch/<argv[1:] as a path>.log,
// with root the results root named in env (the bench's card.env, last value
// wins) or else in this process's environment, or else the wrapper's own
// directory. A card's log is <root>/launch/<sprint>/<label>/<attempt>.log; a
// copy's is <root>/launch/copy/<copy>.log.
func WrapperLogPath(wrapper string, env []string, args []string) string {
	name := WrapperLogName(args)
	if root := envValue(env, WrapperLogEnv); filepath.IsAbs(root) {
		return filepath.Join(root, WrapperLogDir, name)
	}
	return filepath.Join(filepath.Dir(wrapper), WrapperLogDir, name)
}

// WrapperLogName is the log's path under WrapperLogDir for argv args: every
// argument after the command name, split on "/", one path element each,
// with any character outside [A-Za-z0-9._~-] written as "_" and an empty,
// "." or ".." element written as "_", so the name never leaves the log
// directory whatever the argv held.
func WrapperLogName(args []string) string {
	var parts []string
	for _, a := range args[min(1, len(args)):] {
		for _, e := range strings.Split(a, "/") {
			parts = append(parts, safeLogElement(e))
		}
	}
	if len(parts) == 0 {
		parts = []string{"wrapper"}
	}
	return filepath.Join(parts...) + ".log"
}

func safeLogElement(e string) string {
	if e == "" || e == "." || e == ".." {
		return "_"
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '~', r == '-':
			return r
		}
		return '_'
	}, e)
}

// envValue is name's value in env (the last assignment wins, as execve
// resolves it), else in this process's environment.
func envValue(env []string, name string) string {
	val, found := "", false
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, name+"="); ok {
			val, found = v, true
		}
	}
	if found {
		return val
	}
	return os.Getenv(name)
}
