package pulse

import (
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/cliflags"
)

// pulseVerbs is SPEC-PULSE's verbs block, byte for byte (docs/SPEC-PULSE.md, "The verbs").
const pulseVerbs = `nova-pulse pool    --sources <file> --root <dir> [--out <pool.tsv>] [--timeout <s>] [--max <n>]
nova-pulse cut     --pool <pool.tsv> --templates <dir> --out <dir> --root <dir> [--max <n>]
nova-pulse launch  --cards <cards.tsv> --root <dir> --slots <n> --deadline <s> [--queue] [--routes <routes.tsv>] [--floor <f>] [--key-env <name>] [--base-url <url>] [--max <n>]
nova-pulse harvest --id <pulse id> --root <dir> [--sources <file>] [--templates <dir>] [--launched <dir>] [--done <dir>] [--failed <dir>] [--max-body-bytes <n>] [--max <n>] [--decide] [--floor 0.9] [--key-env JEV_API_KEY] [--base-url <url>]
nova-pulse harvest --bench <name> --root <bench root>[,<root>] --clone [<o/n>=]<dir>... [--session <id>] [--branch-prefix rowan/] [--base <branch>] [--since <d>] [--launched <dir>] [--done <dir>] [--failed <dir>] [--ssh <path>] [--max <n>]
nova-pulse beat    --queue <dir> --cairn <file> --title <text> [--resume <text>]
nova-pulse manager --policy <file> --queue <dir> --roots <dirs> --bus <clone> --as <name> --hours <n>
nova-pulse progress --queue <dir> --roots <dirs> [--day <d>]
nova-pulse wake    --bench <name>... --registry <file> [--timeout <duration, default 8m>]
nova-pulse sleep   --bench <name>... [--idle <duration, default 30m>]
nova-pulse width   --root <dir> --pool <pool.tsv>  (not yet implemented)
nova-pulse version
nova-pulse help`

func help(w io.Writer) {
	fmt.Fprintln(w, pulseVerbs)
	fmt.Fprintln(w, "Defaults: --max 20 (0 = all), --max-body-bytes 4096.")
}

// Main is nova-pulse's entry point; it parses the verb and its flags and runs harvest.
func Main(name string, args []string, stamp string, out, errs io.Writer) int {
	if len(args) == 0 {
		return refusal(errs, "PULSE", fmt.Errorf("a verb is required (run: %s help)", name))
	}
	verb := args[0]
	args = args[1:]
	switch verb {
	case "help", "-h", "--help":
		if len(args) != 0 {
			return refusal(errs, "PULSE", fmt.Errorf("help takes no arguments (run %s help)", name))
		}
		help(out)
		return 0
	case "version", "--version":
		fmt.Fprintln(out, buildinfo.Line(name, stamp))
		return 0
	case "harvest":
		return harvestVerb(args, out, errs)
	case "beat":
		return beatVerb(args, out, errs)
	case "watch":
		return watchVerb(args, out, errs)
	case "status":
		return statusVerb(args, out, errs)
	case "progress":
		return progressVerb(args, out, errs)
	}
	return refusal(errs, "PULSE", fmt.Errorf("unknown verb %s (run %s help)", verb, name))
}

func harvestVerb(args []string, out, errs io.Writer) int {
	o := struct {
		id, root, sources, templates string
		maxBodyBytes, max            int
	}{maxBodyBytes: 4096, max: 20}
	f := flag.NewFlagSet("harvest", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.StringVar(&o.id, "id", "", "the pulse id")
	f.StringVar(&o.root, "root", "", "the root directory")
	f.StringVar(&o.sources, "sources", "", "the sources file")
	f.StringVar(&o.templates, "templates", "", "the templates directory")
	f.IntVar(&o.maxBodyBytes, "max-body-bytes", 4096, "cap on the PR body")
	f.IntVar(&o.max, "max", 20, "per-kind output cap")
	if err := f.Parse(args); err != nil {
		// `nova-pulse <verb> --help` is a question, not a parse failure: see
		// internal/cliflags for what the flag package would otherwise print.
		if cliflags.Help(out, err, cliflags.Usage(pulseVerbs, "harvest")) {
			return 0
		}
		return refusal(errs, "HARVEST", fmt.Errorf("%s (run nova-pulse help)", err))
	}
	if len(f.Args()) != 0 {
		return refusal(errs, "HARVEST", fmt.Errorf("harvest takes no positional arguments (run nova-pulse help)"))
	}
	if o.id == "" {
		return refusal(errs, "HARVEST", fmt.Errorf("missing --id; refusing to guess (supply the pulse id)"))
	}
	if o.root == "" {
		return refusal(errs, "HARVEST", fmt.Errorf("missing --root; refusing to guess (supply the root directory)"))
	}
	if o.max < 0 || o.maxBodyBytes <= 0 {
		return refusal(errs, "HARVEST", fmt.Errorf("invalid bound (use --max >= 0 and a positive --max-body-bytes)"))
	}
	return Harvest(HarvestInput{
		ID: o.id, Root: o.root, Sources: o.sources, Templates: o.templates,
		MaxBodyBytes: o.maxBodyBytes, Max: o.max, Stdout: out, Stderr: errs,
	})
}

func statusVerb(args []string, out, errs io.Writer) int {
	o := struct {
		queue, roots, slotsStore, day string
		timeout, max                  int
		expandingHours                int
	}{timeout: 120, max: 20, expandingHours: 2}
	f := flag.NewFlagSet("status", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.StringVar(&o.queue, "queue", "", "the queue directory")
	f.StringVar(&o.roots, "roots", "", "the benches to report, comma separated")
	f.StringVar(&o.slotsStore, "slots-store", "", "bench slot-lease stores, comma separated")
	f.StringVar(&o.day, "day", "", "the day the window starts at, YYYY-MM-DD")
	f.IntVar(&o.timeout, "timeout", 120, "bound on each gh child, seconds")
	f.IntVar(&o.max, "max", 20, "per-kind output cap")
	f.IntVar(&o.expandingHours, "expanding-hours", 2, "hours above the threshold before the verdict reads EXPANDING")
	if err := f.Parse(args); err != nil {
		if cliflags.Help(out, err, cliflags.Usage(pulseVerbs, "status")) {
			return 0
		}
		return refusal(errs, "STATUS", fmt.Errorf("%s (run nova-pulse help)", err))
	}
	if len(f.Args()) != 0 {
		return refusal(errs, "STATUS", fmt.Errorf("status takes no positional arguments (run nova-pulse help)"))
	}
	if o.queue == "" {
		return refusal(errs, "STATUS", fmt.Errorf("missing --queue; refusing to guess (supply the queue directory)"))
	}
	if o.roots == "" {
		return refusal(errs, "STATUS", fmt.Errorf("missing --roots; refusing to guess (supply the benches, comma separated)"))
	}
	if o.max < 0 || o.timeout < 1 || o.expandingHours < 1 {
		return refusal(errs, "STATUS", fmt.Errorf("invalid bound (use --max >= 0, --timeout >= 1 and --expanding-hours >= 1)"))
	}
	return Status(StatusInput{
		Queue: o.queue, Roots: o.roots, SlotsStores: o.slotsStore, Day: o.day, Max: o.max,
		Timeout: time.Duration(o.timeout) * time.Second, ExpandingHours: o.expandingHours,
		Stdout: out, Stderr: errs,
	})
}

func progressVerb(args []string, out, errs io.Writer) int {
	o := struct {
		queue, roots, day string
	}{}
	f := flag.NewFlagSet("progress", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.StringVar(&o.queue, "queue", "", "the queue directory")
	f.StringVar(&o.roots, "roots", "", "the benches to measure, comma separated")
	f.StringVar(&o.day, "day", "", "the day the window starts at, YYYY-MM-DD")
	if err := f.Parse(args); err != nil {
		if cliflags.Help(out, err, cliflags.Usage(pulseVerbs, "progress")) {
			return 0
		}
		return refusal(errs, "PROGRESS", fmt.Errorf("%s (run nova-pulse help)", err))
	}
	if len(f.Args()) != 0 {
		return refusal(errs, "PROGRESS", fmt.Errorf("progress takes no positional arguments (run nova-pulse help)"))
	}
	if o.queue == "" {
		return refusal(errs, "PROGRESS", fmt.Errorf("missing --queue; refusing to guess (supply the queue directory)"))
	}
	if o.roots == "" {
		return refusal(errs, "PROGRESS", fmt.Errorf("missing --roots; refusing to guess (supply the benches, comma separated)"))
	}
	return Progress(ProgressInput{
		Queue: o.queue, Roots: o.roots, Day: o.day, Stdout: out, Stderr: errs,
	})
}

func beatVerb(args []string, out, errs io.Writer) int {
	o := struct {
		queue, cairn, title, resume string
	}{}
	f := flag.NewFlagSet("beat", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.StringVar(&o.queue, "queue", "", "the queue directory")
	f.StringVar(&o.cairn, "cairn", "", "the cairn file this beat is appended to")
	f.StringVar(&o.title, "title", "", "the one-line title of this beat")
	f.StringVar(&o.resume, "resume", "", "the resume rule this beat writes")
	if err := f.Parse(args); err != nil {
		if cliflags.Help(out, err, cliflags.Usage(pulseVerbs, "beat")) {
			return 0
		}
		return refusal(errs, "BEAT", fmt.Errorf("%s (run nova-pulse help)", err))
	}
	if len(f.Args()) != 0 {
		return refusal(errs, "BEAT", fmt.Errorf("beat takes no positional arguments (run nova-pulse help)"))
	}
	if o.queue == "" {
		return refusal(errs, "BEAT", fmt.Errorf("missing --queue; refusing to guess (supply the queue directory)"))
	}
	if o.cairn == "" {
		return refusal(errs, "BEAT", fmt.Errorf("missing --cairn; refusing to guess (supply the cairn file)"))
	}
	if o.title == "" {
		return refusal(errs, "BEAT", fmt.Errorf("missing --title; refusing to guess (supply the one-line title)"))
	}
	return Beat(BeatInput{
		Queue: o.queue, Cairn: o.cairn, Title: o.title, Resume: o.resume,
		Stdout: out, Stderr: errs,
	})
}

func watchVerb(args []string, out, errs io.Writer) int {
	o := struct {
		queue, bus, jobs, until, cap string
	}{}
	f := flag.NewFlagSet("watch", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.StringVar(&o.queue, "queue", "", "the merge queue directory")
	f.StringVar(&o.bus, "bus", "", "the nova-bus checkout")
	f.StringVar(&o.jobs, "jobs", "", "the job roots")
	f.StringVar(&o.until, "until", "", "the event that ends the watch")
	f.StringVar(&o.cap, "cap", "", "the wall the watch never runs past")
	if err := f.Parse(args); err != nil {
		if cliflags.Help(out, err, cliflags.Usage(pulseVerbs, "watch")) {
			return 0
		}
		return refusal(errs, "WATCH", fmt.Errorf("%s (run nova-pulse help)", err))
	}
	if len(f.Args()) != 0 {
		return refusal(errs, "WATCH", fmt.Errorf("watch takes no positional arguments (run nova-pulse help)"))
	}
	var capDur time.Duration
	if o.cap != "" {
		d, err := time.ParseDuration(o.cap)
		if err != nil {
			fmt.Fprintf(errs, "WATCH REFUSED cap=%s (a duration like 90s or 5m)\n", o.cap)
			return 2
		}
		capDur = d
	}
	return Watch(WatchInput{
		Queue: o.queue, Bus: o.bus, Jobs: o.jobs, Until: o.until, Cap: capDur,
		Stdout: out, Stderr: errs,
	})
}
