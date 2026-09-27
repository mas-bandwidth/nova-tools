package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
)

type command struct {
	name, syntax, example string
	run                   func([]string, io.Writer, io.Writer) int
}

// The dispatcher and all help entry points share this list. Leaf flags come
// from each handler's real FlagSet, so help cannot advertise imaginary flags.
var commands = []command{
	{"create", "<table> --columns <name[:projection[:fold[:label]]],...> [--footer <label>] [--width <col=n,...>]", "create demo --columns 'ready,working,done,note:text,progress:pct(done)'", cmdCreate},
	{"set", "<table> [--footer <label>] [--rename <name>] [--columns <spec>] [--hide <cols>] [--show <cols>] [--hidden | --visible]", "set demo --hide ready", cmdSet},
	{"drop", "<table> [--definition]", "drop demo", cmdDrop},
	{"list", "", "list", cmdList},
	{"row add", "<table> <row>... [--label <text>] [--exclude <member>] [--owner <verb>] [<col>=<key> ...]", "row add demo build review", cmdRowAdd},
	{"row set", "<table> <row> <col>=<value>...", "row set demo build 'note=Checks passed'", cmdRowSet},
	{"row hide", "<table> <row>...", "row hide demo build", func(a []string, o, e io.Writer) int { return cmdRowsHide(a, o, e, true) }},
	{"row show", "<table> <row>...", "row show demo build", func(a []string, o, e io.Writer) int { return cmdRowsHide(a, o, e, false) }},
	{"row del", "<table> <row>", "row del demo build", cmdRowDel},
	{"row move", "<table> <row> --first | --last | --before <row> | --after <row>", "row move demo review --before build", cmdRowMove},
	{"row order", "<table> <row>...", "row order demo review build", cmdRowOrder},
	{"row sort", "<table> [--by name|label|<col>] [--desc] [--keep] | --manual", "row sort demo --by name --keep", cmdRowSort},
	{"col add", "<table> <name[:projection[:fold[:label]]]> [--first | --last | --before <col> | --after <col>]", "col add demo note:text --after done", cmdColAdd},
	{"col del", "<table> <col>", "col del demo note", cmdColDel},
	{"col move", "<table> <col> --first | --last | --before <col> | --after <col>", "col move demo done --after working", cmdColMove},
	{"cell add", "<table> <row> <col> <member>... [--score <n>]", "cell add demo build ready b1 b2", cmdCellAdd},
	{"cell remove", "<table> <row> <col> <member>...", "cell remove demo build ready b1 b2", cmdCellRemove},
	{"cell move", "<table> <row> <from-col> <to-col> <member>...", "cell move demo build ready working b1 b2", cmdCellMove},
	{"cell members", "<table> <row> <col>", "cell members demo build ready", cmdCellMembers},
	{"member create", "<table> <id>", "member create demo b3", func(a []string, o, e io.Writer) int { return cmdMember(append([]string{"create"}, a...), o, e) }},
	{"member find", "<table> <id>", "member find demo b1", cmdMemberFind},
	{"check", "<table>", "check demo", cmdCheck},
	{"clear", "<table>", "clear demo", cmdClear},
	{"show", "<table> [--at-epoch <n>]", "show demo", cmdShow},
	{"render", "<table> [--at-epoch <n>] [--hide-zero-rows] [--width <col=n,...>] [--label-width <n>]", "render demo --label-width 16", cmdRender},
	{"watch", "<table>[,<table>...] | --view <name> [--every <duration>] [--out <file>] [--title <text>] [--hide-zero-rows] [--width <col=n,...>] [--label-width <n>] [--once]", "watch --view work --once", cmdWatch},
	{"view set", "<name> --tables <a,b,...> [--title <text>] [--summary <count-column>]", "view set work --tables demo --title Work --summary done", func(a []string, o, e io.Writer) int { return cmdView(append([]string{"set"}, a...), o, e) }},
	{"view show", "<name>", "view show work", func(a []string, o, e io.Writer) int { return cmdView(append([]string{"show"}, a...), o, e) }},
	{"view list", "", "view list", func(a []string, o, e io.Writer) int { return cmdView(append([]string{"list"}, a...), o, e) }},
	{"view del", "<name>", "view del work", func(a []string, o, e io.Writer) int { return cmdView(append([]string{"del"}, a...), o, e) }},
	{"version", "", "version", func(a []string, o, e io.Writer) int {
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
	for _, c := range commands {
		if strings.HasPrefix(c.name, name+" ") {
			return true
		}
	}
	return false
}
func isHelp(s string) bool { return s == "-h" || s == "--help" || s == "-help" || s == "--h" }
func dispatch(args []string, out, errout io.Writer) int {
	if len(args) == 1 && args[0] == "--version" {
		args = []string{"version"}
	}
	for _, c := range commands {
		words := strings.Fields(c.name)
		if len(args) >= len(words) && strings.Join(args[:len(words)], " ") == c.name {
			return c.run(args[len(words):], out, errout)
		}
	}
	if len(args) > 0 && isGroup(args[0]) {
		var names []string
		for _, c := range commands {
			if strings.HasPrefix(c.name, args[0]+" ") {
				names = append(names, strings.TrimPrefix(c.name, args[0]+" "))
			}
		}
		return refuse(errout, args[0], "unknown or missing subverb; wants "+strings.Join(names, ", "))
	}
	return refuse(errout, "", "unknown verb "+strings.Join(args, " ")+"; available: "+rootNames())
}
func helpCommand(path []string, out, errout io.Writer) int {
	name := strings.Join(path, " ")
	if name == "" {
		fmt.Fprintln(out, "nova-table: tables of ordered sets, text and percentages over Redis\n\nusage:\n  nova-table help [<verb> [<subverb>]]")
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
			return c.run([]string{"--help"}, out, errout)
		}
	}
	return refuse(errout, "help", "unknown command "+name+"; available: "+rootNames())
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
	fmt.Fprintln(out, "  nova-table "+c.example)
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
				fmt.Fprintln(out, "  --seat <name>  use a configured nova-sprint seat")
			}
		}
	}
	fmt.Fprintln(out, "\nexit codes: 0 done, 1 refused, 2 usage")
}
