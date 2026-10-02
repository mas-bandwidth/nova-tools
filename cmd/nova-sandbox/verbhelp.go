package main

import (
	"flag"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
)

// verbDoc is what `<verb> -h` prints beyond the banner's own lines for that verb: every
// flag the verb takes with what it wants, and the verb's own exit codes. One exit
// paragraph for every verb read a probe's 1 as a wrapped command's 1 (docs/STANDARD.md
// §2-exit: every verb's -h quotes its table).
type verbDoc struct {
	// flags are name and usage; the word in backquotes is the value the flag wants
	// (flag.UnquoteUsage prints it as <value>), and a usage with none is a switch.
	flags [][2]string
	exits string
}

// The flags the inspection verbs share with the bare form, described once.
var (
	readFlag       = [2]string{"read", "a `dir` readable and NOT writable, recursively; it carries execute. Repeatable"}
	readNoexecFlag = [2]string{"read-noexec", "a `dir` readable, NOT writable and NOT executable: a cache or a data tree. Repeatable"}
	writeFlag      = [2]string{"write", "a `dir` readable and writable, recursively. Repeatable and REQUIRED; HOME must sit inside one"}
	netDenyFlag    = [2]string{"net-deny", "an ENFORCED network denial, or a refusal; without it the line says net=nopromise"}
	netListenFlag  = [2]string{"net-listen", "grant inbound ip as well; never with --net-deny"}
	gpuFlag        = [2]string{"gpu", "the local GPU capability `mode`: none (the default) or metal"}
	jsonFlag       = [2]string{"json", "print the result as one JSON object on stdout, a refusal included"}
	egressExits    = "0 done; 1 the plan's invariants or nft said NO; 2 could not run (a usage error, no nft, not linux)"
)

var verbDocs = map[string]verbDoc{
	"check":   {flags: [][2]string{jsonFlag}, exits: "0 the report printed, whatever this machine can enforce; 2 could not run (a usage error)"},
	"version": {exits: "0 the version line printed; 2 an argument was given"},
	"policy": {flags: [][2]string{readFlag, readNoexecFlag, writeFlag,
		{"cwd", "the command's working `dir`, inside a --write (default: the first --write)"},
		{"tmp", "the child's TMPDIR, a `dir` inside a --write (default: <first --write>/.nova-sandbox-tmp)"},
		{"name", "the windows `container` name; accepted and ignored on darwin and linux"},
		netDenyFlag, netListenFlag,
		{"net-allow", "open the loopback `host:port` named. Repeatable"},
		gpuFlag, jsonFlag},
		exits: "0 the policy printed on stdout and nothing ran; 2 could not run (a usage error, or a path or HOME the wall refuses)"},
	"probe": {flags: [][2]string{readFlag, readNoexecFlag, writeFlag, netDenyFlag, netListenFlag,
		{"secret", "a file `path` the probe proves it cannot read, outside every --read and --write; its contents are never read"},
		gpuFlag, jsonFlag},
		exits: "0 every step got what it expected; 1 a step did not (the wall is broken: run no job under it); 2 could not run (a usage error, or a machine that cannot answer)"},
	"worktree": {flags: [][2]string{
		{"repo", "the repository `dir` the worktree is made from, an absolute path"},
		{"scratch", "an existing `dir` the worktrees and their records live in; never created"},
		{"pr", "the pull request `id` whose head the worktree is placed at"},
		{"base", "the `branch` it is compared against (default: the pull request's base)"},
		{"prune", "remove the worktrees under --scratch whose pull request merged or closed, or that sat unused and stale; never with --pr"}},
		exits: "0 done; 2 could not run (a usage error, or a repository or forge that did not answer)"},
	"egress": {exits: egressExits},
	"egress plan": {flags: [][2]string{
		{"run", "the run `id` this wall belongs to; the table is nova_egress_<id>"},
		{"policy", "the reviewed allowlist `file` in git"},
		{"model-host", "the ONE model `host` of this run, already a line in the policy"},
		{"resolver", "the only `ip` UDP 53 is allowed to"},
		{"bench-cidr", "another bench's `cidr`, denied. Repeatable"},
		{"uid", "the container's `uid` on the host; a plan needs --uid or --veth"},
		{"veth", "the container's `interface`; a plan needs --uid or --veth"},
		{"out", "the `file` the ruleset is written to"}},
		exits: egressExits},
	"egress apply": {flags: [][2]string{{"plan", "the ruleset `file` egress plan wrote"}, {"run", "the run `id` the plan belongs to"}}, exits: egressExits},
	"egress check": {flags: [][2]string{{"plan", "the ruleset `file` to read back and check"}}, exits: egressExits},
	"egress drop":  {flags: [][2]string{{"run", "the run `id` whose table is taken away"}}, exits: egressExits},
}

// helpIfAsked is verbflag.HelpIfAsked with the verb's flags described: when args ask for
// help (before any --), it raises Help with a flag set built from verbDocs.
func helpIfAsked(args []string, verb string) {
	if !verbflag.Asked(args) {
		return
	}
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	for _, f := range verbDocs[verb].flags {
		if strings.Contains(f[1], "`") {
			fs.String(f[0], "", f[1])
		} else {
			fs.Bool(f[0], false, f[1])
		}
	}
	panic(verbflag.Help{FS: fs})
}

// recoverVerbHelp is verbflag.Recover with the verb's own exit codes: the banner it quotes
// from carries that verb's line in place of the by-verb paragraph.
func recoverVerbHelp(stdout io.Writer, code *int) {
	r := recover()
	if r == nil {
		return
	}
	h, ok := r.(verbflag.Help)
	if !ok {
		panic(r)
	}
	banner := usage
	verb := verbflag.Verb("nova-sandbox", h.FS)
	if doc, ok := verbDocs[verb]; ok && doc.exits != "" {
		banner = usageHead + usageExamples + "\n" + exitsLabel + "\n  " + verb + ": " + doc.exits + "\n"
	}
	verbflag.Print(stdout, "nova-sandbox", banner, h.FS)
	*code = 0
}
