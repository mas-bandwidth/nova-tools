// sources.go holds the sources verb: its flags, its run and the helpers only it uses.

package main

import (
	"strconv"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/tokens"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// sourcesVerb declares the sources verb.
func sourcesVerb(now time.Time) tool.Verb {
	var sf sourceFlags
	return tool.Verb{
		Name:    "sources",
		Token:   "SOURCES",
		Usage:   "sources --repos <file> (--day <YYYY-MM-DD> | --all) [<source flags>] [--unattributed] [--max <n>]",
		Example: "sources --repos ./repos.tsv --all --claude bench=./transcripts\n  sources --repos ./repos.tsv --all --claude bench=./transcripts --unattributed --max 20",
		Effect:  tool.Effect(string(tool.Inspection) + " (--opencode reads a copy made in a new directory under --scratch and removed before it exits)"),
		Flags: func(f *tool.Flags) {
			f.String("day", "", "one UTC day to inspect as YYYY-MM-DD")
			f.Bool("all", false, "inspect every day named by the sources")
			f.Bool("unattributed", false, "list seen paths that matched no repo rule, with the mentions each got (one per message that touched it)")
			f.Max()
			sf.declare(f, true, true)
			f.Check(func(c *tool.Call) {
				checkDay(c, c.Str("day"), c.Bool("all"))
				sf.check(c)
			})
		},
		Run: func(c *tool.Call) *tool.Out {
			return runSources(c, sf, now)
		},
	}
}

// runSources reads the declared sources exactly as fold does, through the same code -- a
// second reader would drift -- prints what each yielded, and writes nothing. It exists so
// a person can see what a fold would count before it writes, and it exits 0 whenever it
// ran, because asserting is not its job.
func runSources(c *tool.Call, sf sourceFlags, now time.Time) *tool.Out {
	day := c.Str("day")
	all := c.Bool("all")
	unattributed := c.Bool("unattributed")

	rules, err := tokens.LoadRules(sf.repos)
	if err != nil {
		return tool.Refuse("--repos " + sf.repos + ": " + err.Error() + "; it wants " + wantsRepos)
	}
	// The tally is switched on BEFORE any source is read, because it is taken inside the
	// attribution ladder as the paths go past; there is no second walk of the transcripts.
	if unattributed {
		rules.WatchUnattributed()
		if !all {
			rules.FilterDay(day)
		}
	}
	sources, copyNotesList := sf.read(rules, now, true)
	if !all {
		// When --day is specified (without --all), message counts, row keys, and unattributed
		// path tallies are scoped to the selected day. Source-wide inventory and error metadata
		// (files, unreadable sources, and unparsed lines) remain source-wide because unreadable
		// or unparsed files may lack valid dates and describe properties of the declared source.
		for _, src := range sources {
			var dayStream []tokens.Message
			keys := map[tokens.Key]bool{}
			for _, m := range src.Stream {
				if m.Day == day {
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

	o := tool.Done()

	files, messages, rows := 0, 0, 0
	for _, src := range sources {
		addSourceItem(o, src)
		files += src.Stat.Files
		messages += src.Stat.Messages
		rows += src.Stat.Rows
	}
	for _, src := range sources {
		for _, u := range src.Unreadables {
			addUnreadableItem(o, u)
		}
	}
	for _, src := range sources {
		for _, u := range src.Unparseds {
			addUnparsedItem(o, u)
		}
	}
	// The listing that says WHICH paths `other` is made of. Without it a person reads
	// `other=81%` on a day line and has nowhere to go but grep; with it the top stems ARE
	// the rules the file is missing, written in the shape a rule matches.
	unattributedField := count(tokens.Dash)
	if unattributed {
		for _, u := range rules.Unattributed() {
			o.Item("unattributed", "stem", u.Stem, "mentions", u.Count)
		}
		unattributedField = count(strconv.Itoa(rules.TotalUnattributed()))
	}

	unreadableTotal := countItems(o, "unreadable")
	unparsedTotal := countItems(o, "unparsed")

	o.Fact("sources", len(sources)).
		Fact("files", files).
		Fact("messages", messages).
		Fact("unreadable", unreadableTotal).
		Fact("unparsed", unparsedTotal).
		Fact("rows", rows).
		Fact("unattributed", unattributedField)

	copyNotes(o, copyNotesList)
	return o
}
