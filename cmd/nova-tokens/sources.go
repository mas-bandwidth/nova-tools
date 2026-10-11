// sources.go holds the sources verb: its flags, its run and the helpers only it uses.

package main

import (
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/tokens"
	"github.com/mas-bandwidth/nova-tools/pkg/bounded"
)

// cmdSources reads the declared sources exactly as fold does, through the same code -- a
// second reader would drift -- prints what each yielded, and writes nothing. It exists so
// a person can see what a fold would count before it writes, and it exits 0 whenever it
// ran, because asserting is not its job.
func cmdSources(args []string, stdout, stderr io.Writer, now time.Time) int {
	fs := newFlagSet("sources")
	day := fs.String("day", "", "one UTC day to inspect as YYYY-MM-DD")
	all := fs.Bool("all", false, "inspect every day named by the sources")
	max := fs.Int("max", bounded.Default, "maximum rows to print; 0 prints all")
	unattributed := fs.Bool("unattributed", false, "list seen paths that matched no repo rule, with the mentions each got (one per message that touched it)")
	var sf sourceFlags
	sf.declare(fs, true)
	s, code, ok := start(fs, args, "SOURCES", stdout, stderr)
	if !ok {
		return code
	}
	r := &refusals{token: "SOURCES", s: s}
	checkDay(r, *day, *all)
	sf.check(r)
	checkMax(r, *max)
	if len(r.list) > 0 {
		return r.print(stderr)
	}
	rules, err := tokens.LoadRules(sf.repos)
	if err != nil {
		r.add("--repos " + sf.repos + ": " + err.Error() + "; it wants " + wantsRepos)
		return r.print(stderr)
	}
	// The tally is switched on BEFORE any source is read, because it is taken inside the
	// attribution ladder as the paths go past; there is no second walk of the transcripts.
	if *unattributed {
		rules.WatchUnattributed()
		if !*all {
			rules.FilterDay(*day)
		}
	}
	sources, copyNotes := sf.read(rules, now, true)
	if !*all {
		// When --day is specified (without --all), message counts, row keys, and unattributed
		// path tallies are scoped to the selected day. Source-wide inventory and error metadata
		// (files, unreadable sources, and unparsed lines) remain source-wide because unreadable
		// or unparsed files may lack valid dates and describe properties of the declared source.
		for _, src := range sources {
			var dayStream []tokens.Message
			keys := map[tokens.Key]bool{}
			for _, m := range src.Stream {
				if m.Day == *day {
					dayStream = append(dayStream, m)
					keys[tokens.Key{Day: m.Day, Model: m.Model, Repo: m.Repo}] = true
				}
			}
			src.Stream = dayStream
			if tokens.Applies(src.Kind, "messages") {
				src.Stat.Messages = len(dayStream)
			}
			src.Stat.Rows = len(keys)
		}
	}

	srcList := s.list(false, *max, "SOURCES", "source", maxRemedy("sources"))
	unreadable := s.list(true, *max, "SOURCES", "unreadable", maxRemedy("sources"))
	unparsed := s.list(true, *max, "SOURCES", "unparsed", maxRemedy("sources"))
	stems := s.list(false, *max, "SOURCES", "unattributed", maxRemedy("sources"))
	files, messages, rows := 0, 0, 0
	for _, src := range sources {
		srcList.Line(sourceLine(s, "SOURCES", src))
		files += src.Stat.Files
		messages += src.Stat.Messages
		rows += src.Stat.Rows
	}
	srcList.More()
	for _, src := range sources {
		for _, u := range src.Unreadables {
			unreadable.Line(unreadableLine(s, "SOURCES", u))
		}
	}
	unreadable.More()
	for _, src := range sources {
		for _, u := range src.Unparseds {
			unparsed.Line(unparsedLine(s, "SOURCES", u))
		}
	}
	unparsed.More()
	// The listing that says WHICH paths `other` is made of. Without it a person reads
	// `other=81%` on a day line and has nowhere to go but grep; with it the top stems ARE
	// the rules the file is missing, written in the shape a rule matches.
	unattributedField := count(tokens.Dash)
	if *unattributed {
		for _, u := range rules.Unattributed() {
			stems.Line(s.line("SOURCES", "UNATTRIBUTED", "", "stem", u.Stem, "mentions", u.Count))
		}
		stems.More()
		unattributedField = count(strconv.Itoa(rules.TotalUnattributed()))
	}
	counts := []any{"sources", len(sources), "files", files, "messages", messages, "unreadable", unreadable.Total(),
		"unparsed", unparsed.Total(), "rows", rows, "unattributed", unattributedField}
	fmt.Fprintf(s.out(), "SOURCES OK%s\n", s.factFields(counts...))
	copyNote(s, "SOURCES", copyNotes)
	return s.done(0, *max)
}
