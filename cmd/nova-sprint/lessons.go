package main

import (
	"context"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/lessons"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
)

func init() {
	register(Verb{
		Name:    "lesson",
		Summary: "append or supersede an owner-reviewed lesson in a repository's capped docs/LESSONS.md",
		Run:     cmdLesson,
	})
}

func cmdLesson(_ context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return refuseVerb(stderr, "lesson", "want append or supersede")
	}
	switch args[0] {
	case "append":
		return cmdLessonAppend(args[1:], stdout, stderr)
	case "supersede":
		return cmdLessonSupersede(args[1:], stdout, stderr)
	default:
		return refuseVerb(stderr, "lesson", "want append or supersede")
	}
}

func cmdLessonAppend(args []string, stdout, stderr io.Writer) int {
	fs := verbflag.New("lesson append")
	repo := fs.String("repo", "", verbflag.HelpRepo)
	l := lessons.Lesson{}
	fs.StringVar(&l.ID, "ids", "", verbflag.HelpIDs)
	fs.StringVar(&l.Component, "component", "", "the component the lesson is about")
	fs.StringVar(&l.Kind, "kind", "", "the lesson's kind")
	fs.StringVar(&l.Failure, "failure", "", "what failed")
	fs.StringVar(&l.Prevention, "prevention", "", "what prevents it next time")
	fs.StringVar(&l.Evidence, "evidence", "", "the evidence url")
	fs.StringVar(&l.Status, "status", "", "the lesson's status")
	fs.StringVar(&l.ReviewedBy, "reviewed-by", "", "who reviewed it")
	if err := fs.Parse(args); err != nil {
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

func cmdLessonSupersede(args []string, stdout, stderr io.Writer) int {
	fs := verbflag.New("lesson supersede")
	repo := fs.String("repo", "", verbflag.HelpRepo)
	id := fs.String("ids", "", verbflag.HelpIDs)
	if err := fs.Parse(args); err != nil {
		return refuseVerb(stderr, "lesson supersede", err.Error())
	}
	if fs.NArg() != 0 {
		return refuseVerb(stderr, "lesson supersede", "takes flags, not positional arguments")
	}
	r, err := lessons.Supersede(*repo, *id)
	if err != nil {
		return refuseVerb(stderr, "lesson supersede", err.Error())
	}
	status := "UNCHANGED"
	if r.Moved {
		status = "SUPERSEDED"
	}
	fmt.Fprintf(stdout, "LESSON %s id=%s file=%s archive=%s lines=%d/%d\n", status, *id, r.Path, r.ArchivePath, r.Lines, lessons.MaxLines)
	return 0
}
