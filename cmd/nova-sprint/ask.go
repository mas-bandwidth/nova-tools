package main

import (
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

func askExtras(s *sprint.Snapshot) map[string][]string {
	var ids []string
	if s.Readers != nil {
		for _, c := range s.Work.Column(sprint.Review) {
			attempt := c.Int("attempt")
			for _, rd := range s.Readers.Rows() {
				for take := 1; take <= sprint.MaxReadTakebacks; take++ {
					ids = append(ids, sprint.ReadCardIDTake(c.ID, attempt, rd, take))
				}
			}
		}
	}
	return map[string][]string{sprint.Readers: ids}
}

func overrideAskVerb() {
	for i := range verbs {
		if verbs[i].name == "ask" {
			verbs[i].run = func(a *app, args []string, stdout, stderr io.Writer) int {
				var another *bool
				var ans, instead *string
				return a.setVerb("ask", args, stdout, stderr, false, func(fs flagSet) {
					another = fs.Bool("another", false, "one more reader for a primary already asked")
					ans = fs.String("answers", "", answersWords)
					instead = fs.String("instead", "", "take back this reader's read (asked or reading) of the one primary named and ask one other reader, as --another chooses")
				}, func(ids []string, s *sel) string {
					if *instead != "" && s.group != "" {
						return "--instead takes back one read of one primary and asks one other reader: ask <primary> --instead <reader>, with no --another, --group, --stream or --max; nothing was changed"
					}
					return ""
				}, func(ids []string, s *sel, c *common) store.Step {
					step := store.AskStep(sprint.AskReq{Sel: s.sel(ids), Another: *another, Answers: answers(*ans), Who: c.actor, Instead: *instead})
					step.Extras = askExtras
					return step
				})
			}
			break
		}
	}
}

func init() {
	overrideAskVerb()
}
