package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/lessons"
)

func init() {
	register(Verb{
		Name:    "lesson",
		Summary: "append one owner-reviewed operational lesson to a repository's capped docs/LESSONS.md",
		Run:     cmdLesson,
	})
}

func cmdLesson(_ context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "append" {
		return refuseVerb(stderr, "lesson", "want append --repo <dir> --id <id> --component <name> --kind <card-kind> --failure <text> --prevention <text> --evidence <ref> --status <active|superseded> --reviewed-by <owner>")
	}
	fs := flag.NewFlagSet("lesson append", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	repo := fs.String("repo", "", "")
	l := lessons.Lesson{}
	fs.StringVar(&l.ID, "id", "", "")
	fs.StringVar(&l.Component, "component", "", "")
	fs.StringVar(&l.Kind, "kind", "", "")
	fs.StringVar(&l.Failure, "failure", "", "")
	fs.StringVar(&l.Prevention, "prevention", "", "")
	fs.StringVar(&l.Evidence, "evidence", "", "")
	fs.StringVar(&l.Status, "status", "", "")
	fs.StringVar(&l.ReviewedBy, "reviewed-by", "", "")
	if err := fs.Parse(args[1:]); err != nil {
		return refuseVerb(stderr, "lesson append", err.Error())
	}
	if fs.NArg() != 0 {
		return refuseVerb(stderr, "lesson append", "takes flags, not positional arguments")
	}
	r, err := lessons.Append(*repo, l)
	if err != nil {
		return refuseVerb(stderr, "lesson append", err.Error())
	}
	status := "UNCHANGED"
	if r.Appended {
		status = "APPENDED"
	}
	fmt.Fprintf(stdout, "LESSON %s id=%s file=%s lines=%d/%d\n", status, l.ID, r.Path, r.Lines, lessons.MaxLines)
	return 0
}
