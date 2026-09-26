package main

import (
	"context"
	"flag"
	"io"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
)

// helpCases is every verb and subverb nova-sprint dispatches to a flag set.
// A verb added to the registry fails TestEveryVerbHasHelpCase until it is
// listed here (#3254).
var helpCases = [][]string{
	{"acl", "check"},
	{"adopt", "receipt"}, {"adopt", "matrix"}, {"adopt", "status"},
	{"backpressure", "check"},
	{"bench", "beat"}, {"bench", "release"}, {"bench", "reindex"}, {"bench", "ls"},
	{"capacity", "friend"}, {"capacity", "bench"}, {"capacity", "machine"}, {"capacity", "budget"},
	{"capacity", "take"}, {"capacity", "give"}, {"capacity", "renew"}, {"capacity", "reap"},
	{"capacity", "hook"},
	{"card", "cut"}, {"card", "push"}, {"card", "release"}, {"card", "stop"}, {"card", "show"},
	{"card", "run"}, {"card", "launch"}, {"card", "fsck"}, {"card", "ls"},
	{"card", "launched"}, {"card", "beat"}, {"card", "end"},
	{"census"},
	{"ci", "request"}, {"ci", "run"}, {"ci", "status"}, {"ci", "compare"}, {"ci", "cut"},
	{"ci", "show"}, {"ci", "rerun"}, {"ci", "dispose"}, {"ci", "parity"},
	{"consume", "ok-to-friend", "once"},
	{"consume", "pr-to-read", "once"},
	{"cost", "import"},
	{"digest"},
	{"dev-red", "status"}, {"dev-red", "check"}, {"dev-red", "watch"}, {"dev-red", "unwatch"},
	{"drain"}, {"est"}, {"file"},
	{"fleet", "state"}, {"fleet", "is-up"}, {"fleet", "hold"}, {"fleet", "release"}, {"fleet", "config"},
	{"fleet", "build"}, {"fleet", "build", "set"}, {"fleet", "build", "compile"}, {"fleet", "build", "duty"}, {"fleet", "churn"},
	{"fn", "load"}, {"fn", "check"}, {"fn", "deploy"},
	{"fold"},
	{"friend", "hello"}, {"friend", "bye"}, {"friend", "wake"}, {"friend", "row"},
	{"friend", "roles"}, {"friend", "report"}, {"friend", "show"}, {"friend", "sweep"},
	{"friend", "down"}, {"friend", "up"}, {"friend", "declare"}, {"friend", "wake-health"},
	{"hold", "ingest"}, {"hold", "show"}, {"hold", "release"}, {"hold", "route"},
	{"idem", "resolve"},
	{"jev", "mech"},
	{"redis-cli"}, {"redis"},
	{"land"}, {"land", "status"}, {"land", "flaky", "list"}, {"land", "flaky", "observe"}, {"land", "writer"}, {"land", "eval"}, {"land", "stream"},
	{"land", "merge"}, {"land", "pr"}, {"land", "run"}, {"land", "offer"}, {"land", "list"},
	{"lander"},
	{"lesson", "append"}, {"lesson", "supersede"},
	{"life", "event"}, {"life", "wake-mode"},
	{"line", "post"}, {"line", "list"}, {"line", "import"},
	{"lineup"}, {"lineup", "publish"},
	{"mirror", "refresh"}, {"mirror", "check"}, {"mirror", "status"},
	{"note"},
	{"pitstop", "set"}, {"pitstop", "clear"}, {"pitstop", "status"},
	{"plan", "apply"}, {"plan", "show"},
	{"pr", "record"}, {"pr", "lines"}, {"pr", "reap"},
	{"preflight"}, {"quack", "cut"}, {"quack", "run"}, {"rank"},
	{"read", "brief"}, {"read", "post"}, {"read", "digest"}, {"read", "carry"},
	{"ready"}, {"reconcile"}, {"review", "post"},
	{"result", "contract"}, {"result", "check"}, {"result", "show"}, {"result", "disposition"},
	{"rote"}, {"route", "report"}, {"routes"},
	{"scope", "keep"}, {"scope", "park"}, {"scope", "unpark"}, {"scope", "ls"},
	{"self", "update"},
	{"spec", "mark"}, {"spec", "list"},
	{"sprint", "open"}, {"sprint", "close"}, {"sprint", "status"},
	{"stream", "ls"}, {"stream", "order"}, {"stream", "rename"},
	{"table"}, {"table", "clear"},
	{"task", "push"}, {"task", "take"}, {"task", "beat"}, {"task", "done"}, {"task", "cancel"},
	{"task", "list"}, {"task", "width"},
	{"verbs", "unused"},
	{"why"}, {"width"},
	{"worker", "pause"}, {"worker", "resume"}, {"worker", "show"},
	{"ws", "counts"}, {"ws", "checkpoint"},
}

// ownHelp is the verbs whose hand-written usage answers -h, with the exit
// code they keep and the argv that reaches their flag set past that usage
// (nil: -h is a bool flag of the batch set in task_batch.go).
var ownHelp = map[string]struct {
	code  int
	probe []string
}{
	"file": {2, []string{"file", "--repo", "r"}},
}

// helpFlagSet runs the verb with -h below the dispatcher and returns the
// flag set whose Parse raised verbflag.Help, or nil when none did.
func helpFlagSet(args []string) (fs *flag.FlagSet) {
	defer func() {
		if r := recover(); r != nil {
			h, ok := r.(verbflag.Help)
			if !ok {
				panic(r)
			}
			fs = h.FS
		}
	}()
	argv := append(append([]string{}, args[1:]...), "-h")
	if args[0] == "table" {
		cmdTable(argv, io.Discard, io.Discard)
		return nil
	}
	verbs[args[0]].Run(context.Background(), argv, io.Discard, io.Discard)
	return nil
}

// TestVerbHelpNamesEveryFlag is #3254's DONE-WHEN: every verb and subverb
// answers -h with its usage line and each flag its FlagSet defines on
// stdout, nothing on stderr, exit 2. No Redis is dialled: -h stops Parse.
func TestVerbHelpNamesEveryFlag(t *testing.T) {
	t.Setenv("NOVA_SPRINT_REDIS", "127.0.0.1:1")
	t.Setenv("NOVA_REDIS_ADDR", "127.0.0.1:1")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	for _, args := range helpCases {
		name := strings.Join(args, " ")
		if own, ok := ownHelp[name]; ok {
			code, stdout, stderr := runSprint(append(append([]string{}, args...), "-h")...)
			if code != own.code || stderr != "" || !strings.Contains(stdout, "nova-sprint "+name) {
				t.Errorf("%s -h: exit %d stdout %q stderr %q; want its usage on stdout, exit %d", name, code, stdout, stderr, own.code)
			}
			if own.probe == nil {
				continue
			}
			fs := helpFlagSet(own.probe)
			if fs == nil {
				t.Errorf("%s: probe %v reached no flag set", name, own.probe)
				continue
			}
			fs.VisitAll(func(f *flag.Flag) {
				if !strings.Contains(stdout, "--"+f.Name) {
					t.Errorf("%s -h does not name --%s", name, f.Name)
				}
			})
			continue
		}
		fs := helpFlagSet(args)
		if fs == nil {
			t.Errorf("%s -h: no flag set answered -h", name)
			continue
		}
		code, stdout, stderr := runSprint(append(append([]string{}, args...), "-h")...)
		if code != 2 || stderr != "" || !strings.HasPrefix(stdout, "usage: nova-sprint "+args[0]) {
			t.Errorf("%s -h: exit %d stdout %q stderr %q; want the usage line on stdout, exit 2", name, code, stdout, stderr)
			continue
		}
		fs.VisitAll(func(f *flag.Flag) {
			if !strings.Contains(stdout, "\n  --"+f.Name+" ") && !strings.Contains(stdout, "\n  --"+f.Name+"\n") {
				t.Errorf("%s -h does not name --%s:\n%s", name, f.Name, stdout)
			}
		})
	}
}

// TestEveryVerbHasHelpCase keeps helpCases complete as verbs are registered.
func TestEveryVerbHasHelpCase(t *testing.T) {
	t.Parallel()

	listed := map[string]bool{}
	for _, args := range helpCases {
		listed[args[0]] = true
	}
	for name := range verbs {
		if !listed[name] {
			t.Errorf("verb %s has no -h case in helpCases", name)
		}
	}
}

// TestHelpKeepsParseErrorsQuiet: an undefined flag is still the verb's own
// one-line refusal on stderr, exit 2, not the help text.
func TestHelpKeepsParseErrorsQuiet(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := runSprint("task", "push", "--nope")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "flag provided but not defined: -nope") {
		t.Fatalf("task push --nope: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
}
