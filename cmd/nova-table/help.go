package main

import (
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
)

// A command's example is the lines its help prints under example:, each a
// whole command that runs as written once the banner's example: has run (a
// line before the verb's own makes what it needs), the verb's own line last.
type command struct {
	name, syntax, example string
	run                   func(*application, []string, io.Writer, io.Writer) int
}

// The dispatcher and all help entry points share this list. Leaf flags come
// from each handler's real FlagSet, so help cannot advertise imaginary flags.
var commands []command

func init() {
	commands = []command{
		{"create", "<table> --columns <name[:projection[:fold[:label]]],...> [--footer <label>] [--width <col=n,...>]", "nova-table create notes --columns 'todo,done,note:text,progress:pct(done)'", (*application).cmdCreate},
		{"set", "<table> [--footer <label>] [--rename <name>] [--columns <spec>] [--hide <cols>] [--show <cols>] [--hidden | --visible]", "nova-table set demo --hide ready", (*application).cmdSet},
		{"drop", "<table> [--definition]", "nova-table drop demo", (*application).cmdDrop},
		{"list", "", "nova-table list", (*application).cmdList},
		{"row add", "<table> <row>... [--label <text>] [--exclude <member>] [--owner <verb>] [<col>=<key> ...]", "nova-table row add demo review", (*application).cmdRowAdd},
		{"row set", "<table> <row> <col>=<value>...", "nova-table col add demo note:text --after done\nnova-table row set demo build 'note=Checks passed'", (*application).cmdRowSet},
		{"row hide", "<table> <row>...", "nova-table row hide demo build", func(app *application, a []string, o, e io.Writer) int { return app.cmdRowsHide(a, o, e, true) }},
		{"row show", "<table> <row>...", "nova-table row show demo build", func(app *application, a []string, o, e io.Writer) int { return app.cmdRowsHide(a, o, e, false) }},
		{"row del", "<table> <row>", "nova-table row del demo build", (*application).cmdRowDel},
		{"row move", "<table> <row> --first | --last | --before <row> | --after <row>", "nova-table row add demo review\nnova-table row move demo review --before build", (*application).cmdRowMove},
		{"row order", "<table> <row>...", "nova-table row add demo review\nnova-table row order demo review build", (*application).cmdRowOrder},
		{"row sort", "<table> [--by name|label|<col>] [--desc] [--keep] | --manual", "nova-table row sort demo --by name --keep", (*application).cmdRowSort},
		{"col add", "<table> <name[:projection[:fold[:label]]]> [--first | --last | --before <col> | --after <col>]", "nova-table col add demo note:text --after done", (*application).cmdColAdd},
		{"col del", "<table> <col>", "nova-table col add demo note:text\nnova-table col del demo note", (*application).cmdColDel},
		{"col move", "<table> <col> --first | --last | --before <col> | --after <col>", "nova-table col move demo done --first", (*application).cmdColMove},
		{"cell add", "<table> <row> <col> <member>... [--score <n>]", "nova-table cell add demo build ready b3 b4", (*application).cmdCellAdd},
		{"cell remove", "<table> <row> <col> <member>...", "nova-table cell remove demo build ready b2", (*application).cmdCellRemove},
		{"cell move", "<table> <row> <from-col> <to-col> <member>...", "nova-table cell move demo build ready done b2", (*application).cmdCellMove},
		{"cell members", "<table> <row> <col>", "nova-table cell members demo build working", (*application).cmdCellMembers},
		{"member create", "<table> <id>", "nova-table member create demo b5", func(app *application, a []string, o, e io.Writer) int {
			return app.cmdMember(append([]string{"create"}, a...), o, e)
		}},
		{"member find", "<table> <id>", "nova-table member find demo b1", (*application).cmdMemberFind},
		{"member read", "<table> <id>... | <table> --cell <row:col>", "nova-table member read demo b1 b2", (*application).cmdMemberRead},
		{"batch", "(<manifest-file> | - | '<json>')", "nova-table show demo\nnova-table member read demo b1\nnova-table batch --dry-run '{\"schema\":1,\"table\":\"demo\",\"epoch\":\"0\",\"expected_table_revision\":\"2\",\"operation_id\":\"create-b1\",\"members\":[{\"id\":\"b1\",\"expect\":{\"absent\":true},\"create\":{\"row\":\"build\",\"col\":\"ready\",\"score\":0}}]}'", (*application).cmdBatch},
		{"check", "<table>", "nova-table check demo", (*application).cmdCheck},
		{"clear", "<table>", "nova-table clear demo", (*application).cmdClear},
		{"show", "<table> [--at-epoch <n>]", "nova-table show demo", (*application).cmdShow},
		{"render", "<table> | --view <name> [--at-epoch <n>] [--width <col=n,...>] [--label-width <n>]", "nova-table render demo", (*application).cmdRender},
		{"watch", "<table>[,<table>...] | --view <name> [--every <duration>] [--out <file>] [--title <text>] [--width <col=n,...>] [--label-width <n>] [--check] [--once]", "nova-table watch demo --once", (*application).cmdWatch},
		{"view set", "<name> --tables <a,b,...> [--title <text>] [--summary <count-column>]", "nova-table view set work --tables demo --title Work --summary done", func(app *application, a []string, o, e io.Writer) int {
			return app.cmdView(append([]string{"set"}, a...), o, e)
		}},
		{"view state", "<name> (<text> | --clear)", "nova-table view set work --tables demo\nnova-table view state work STOPPED", func(app *application, a []string, o, e io.Writer) int {
			return app.cmdView(append([]string{"state"}, a...), o, e)
		}},
		{"view show", "<name>", "nova-table view set work --tables demo\nnova-table view show work", func(app *application, a []string, o, e io.Writer) int {
			return app.cmdView(append([]string{"show"}, a...), o, e)
		}},
		{"view list", "", "nova-table view list", func(app *application, a []string, o, e io.Writer) int {
			return app.cmdView(append([]string{"list"}, a...), o, e)
		}},
		{"view del", "<name>", "nova-table view set work --tables demo\nnova-table view del work", func(app *application, a []string, o, e io.Writer) int {
			return app.cmdView(append([]string{"del"}, a...), o, e)
		}},
		{"shell", "[--redis <addr> | --seat <name>] [--keep-going] [--epoch <n>] [--receipt=false]", "printf 'show demo\\nrender demo\\n' | nova-table shell", (*application).cmdShell},
		{"version", "", "nova-table version", func(app *application, a []string, o, e io.Writer) int {
			if len(a) == 1 && isHelp(a[0]) {
				panic(verbflag.Help{FS: verbflag.New("version")})
			}
			if len(a) > 0 {
				return refuse(e, "version", "takes no arguments")
			}
			fmt.Fprintln(o, buildinfo.Line("nova-table", version))
			return 0
		}},
	}
}

func rootNames() string {
	names := []string{"help"}
	seen := map[string]bool{}
	for _, c := range commands {
		n, _, _ := strings.Cut(c.name, " ")
		if !seen[n] {
			names = append(names, n)
			seen[n] = true
		}
	}
	return strings.Join(names, ", ")
}
func isGroup(name string) bool {
	return slices.ContainsFunc(commands, func(c command) bool { return strings.HasPrefix(c.name, name+" ") })
}
func isHelp(s string) bool { return s == "-h" || s == "--help" || s == "-help" || s == "--h" }
func (app *application) dispatch(args []string, out, errout io.Writer) int {
	if len(args) == 1 && args[0] == "--version" {
		args = []string{"version"}
	}
	for _, c := range commands {
		words := strings.Fields(c.name)
		if len(args) >= len(words) && strings.Join(args[:len(words)], " ") == c.name {
			return c.run(app, args[len(words):], out, errout)
		}
	}
	if len(args) > 0 && isGroup(args[0]) {
		var names []string
		for _, c := range commands {
			if strings.HasPrefix(c.name, args[0]+" ") {
				names = append(names, strings.TrimPrefix(c.name, args[0]+" "))
			}
		}
		why := args[0] + " wants one of its verbs;"
		if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
			why = fmt.Sprintf("unknown verb %q in %s;%s", args[0]+" "+args[1], args[0], nearest(args[1], names))
		}
		return refuse(errout, args[0], why+" the verbs are "+strings.Join(names, ", "))
	}
	return refuse(errout, "", fmt.Sprintf("unknown verb %q;%s the verbs are %s", args[0], nearest(args[0], strings.Split(rootNames(), ", ")), rootNames()))
}

// nearest is " did you mean <name>?" for the name nearest to got, else "".
func nearest(got string, names []string) string {
	if n := verbflag.Nearest(got, names); n != "" {
		return " did you mean " + n + "?"
	}
	return ""
}

// opening is the banner's first three answers: what the tool does (line 1,
// the README's sentence), how it works, and the first run (ONBOARDING.md
// point 6): what a first run needs, what runs with no store (help, -h, every
// write under --dry-run), the commands that start a throwaway store, and
// what the refusal says when there is none.
const opening = `nova-table: tables whose cells are ordered sets, kept in Redis and drawn as text

how it works: a table is rows and columns in one Redis store; each cell is an
ordered set of members (a card, a job, any id). A column's projection prints
the set's count, its members, a text or a percentage, and the footer folds each
column. Every write names the epoch it read and prints a receipt; a view stacks
tables into one frame that watch redraws in place.
first run: needs a Redis 7 or later you may write to; an empty one is enough (the first verb loads
the functions nova-table calls). With no store at all, help and -h answer, and every verb that writes
runs under --dry-run: it makes every check the real run makes before sending, then prints what it would send:
  nova-table create demo --columns ready,working,done --dry-run
A throwaway store, with redis-server on PATH (stop it: redis-cli -s "$d/redis.sock" shutdown nosave):
  d=$(mktemp -d)
  redis-server --port 0 --unixsocket "$d/redis.sock" --save '' --appendonly no --daemonize yes
  for _ in $(seq 50); do redis-cli -s "$d/redis.sock" ping >/dev/null 2>&1 && break; sleep 0.1; done
  unset NOVA_SEAT NOVA_SPRINT_SEAT NOVA_SPRINT_REDIS_USER; export NOVA_SPRINT_REDIS="$d/redis.sock"
then run the lines under example: in order (or give each verb --redis <host:port or socket path>).
A verb that finds no store refuses at exit 2 naming the address it tried, what came back, and
this throwaway command (LIST REFUSED: redis at <addr> ...: unreachable: ...; run: d=$(mktemp -d) ...).`

func helpCommand(path []string, out, errout io.Writer) int {
	name := strings.Join(path, " ")
	if name == "" {
		fmt.Fprintln(out, opening+"\n\nusage:\n  nova-table help [<verb> [<subverb>]]")
		for _, c := range commands {
			fmt.Fprintln(out, "  nova-table "+strings.TrimSpace(c.name+" "+c.syntax))
		}
		fmt.Fprint(out, "\n"+usageDetails)
		return 0
	}
	if isGroup(name) {
		fmt.Fprintln(out, "usage:")
		for _, c := range commands {
			if strings.HasPrefix(c.name, name+" ") {
				fmt.Fprintln(out, "  nova-table "+strings.TrimSpace(c.name+" "+c.syntax))
			}
		}
		fmt.Fprintf(out, "\nFor flags and an example: nova-table help %s <subverb>\n", name)
		return 0
	}
	for _, c := range commands {
		if c.name == name {
			if name == "version" {
				printCommandHelp(out, c, nil)
				return 0
			}
			return c.run(&application{}, []string{"--help"}, out, errout)
		}
	}
	return refuse(errout, "help", fmt.Sprintf("unknown verb %q;%s the verbs are %s", name, nearest(name, strings.Split(rootNames(), ", ")), rootNames()))
}
func recoverHelp(out io.Writer, code *int) {
	r := recover()
	if r == nil {
		return
	}
	h, ok := r.(verbflag.Help)
	if !ok {
		panic(r)
	}
	for _, c := range commands {
		if c.name == h.FS.Name() {
			printCommandHelp(out, c, h.FS)
			*code = 0
			return
		}
	}
	panic(r)
}
func printCommandHelp(out io.Writer, c command, fs *flag.FlagSet) {
	fmt.Fprintln(out, "usage: nova-table "+strings.TrimSpace(c.name+" "+c.syntax))
	fmt.Fprintln(out, "\nexample:")
	for _, l := range strings.Split(c.example, "\n") {
		fmt.Fprintln(out, "  "+l)
	}
	if c.name == "batch" {
		fmt.Fprintln(out, "\n"+batchUsageDetails)
	}
	if fs != nil {
		// Product flags first, connection next, receipt metadata last. Never print
		// defaults: a store address or credential may come from the environment.
		groups := [][]*flag.Flag{nil, nil, nil}
		fs.VisitAll(func(f *flag.Flag) {
			group := 0
			switch f.Name {
			case "redis":
				group = 1
			case "epoch", "actor", "fence", "idem", "receipt":
				group = 2
			}
			groups[group] = append(groups[group], f)
		})
		for i, group := range groups {
			if len(group) == 0 {
				continue
			}
			fmt.Fprintln(out, "\n"+[]string{"flags:", "connection:", "write epoch and receipt:"}[i])
			for _, f := range group {
				kind, text := flag.UnquoteUsage(f)
				fmt.Fprintf(out, "  --%s", f.Name)
				if kind != "" {
					fmt.Fprintf(out, " <%s>", kind)
				}
				fmt.Fprintln(out, "  "+text)
			}
			if i == 1 {
				fmt.Fprintln(out, "  --seat <name>  dial as this seat: its seats.tsv row, else the nova-secrets seat of that name")
			}
		}
	}
	if c.name == "shell" {
		fmt.Fprintln(out, "\n"+shellUsageDetails)
	}
	if c.name == "row del" {
		fmt.Fprintln(out, "\nA missing row succeeds with existed=0 and leaves a no-op receipt.")
	}
	fmt.Fprintln(out, "\nexit codes: 0 done, 1 refused, 2 usage")
	fmt.Fprintln(out, "effect: "+effectOf(c.name))
}

// dryRunWords is what --dry-run does on every verb that writes.
const dryRunWords = "--dry-run runs the real run's own call up to its first command, so it refuses exactly where the real run refuses " +
	"before sending, and prints that command instead of sending it, dialling nothing; " +
	"what only the store can check (the table, its epoch, its rows and columns, a bound cell) is left to the real run"

// effectOf is what running a verb does to the world, the last line of its
// help (docs/STANDARD.md section 2: an inspection, a local write, a store
// write or a delivery; a verb that writes takes --dry-run).
func effectOf(verb string) string {
	switch verb {
	case "list", "cell members", "member find", "member read", "check", "show", "render", "view show", "view list":
		return "inspection: reads the store, writes nothing"
	case "watch":
		return "inspection: reads the store every --every and writes nothing to it; --out writes that one local file, by rename"
	case "version":
		return "inspection: reads nothing, writes nothing"
	case "shell":
		return "store write: runs each line's verb on one connection, so a line that writes changes the store; " +
			"entered with --dry-run, every write line is planned instead and nothing is written to the store, and a line saying --dry-run=false is refused " +
			"(a line that reads still reads the store, and a watch --out line still writes its one local file)"
	case "batch":
		return "store write: applies the manifest in one atomic call and prints a receipt; --dry-run makes every check made before sending " +
			"and prints the plan instead, dialling nothing; the epoch, the revision and each member's expectation are the store's to check, on the real run"
	case "view set", "view state", "view del":
		return "store write: changes the view in the store in one call (a view has no epoch and no receipt); " + dryRunWords
	}
	return "store write: changes the table in the store in one call and prints a receipt; " + dryRunWords
}
