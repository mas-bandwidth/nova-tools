// nova-play: shared reading annotations. Participants anchor notes to
// passages in a source text, reply to each other's notes, and resume
// across sessions. A changed source produces an explicit anchor conflict
// rather than silently moving notes.
//
// Exit 0 on success, 1 on anchor conflict, 2 on bad invocation.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/play"
)

const usage = `nova-play: shared reading annotations (see docs/SPEC-PLAY.md)

usage:
  nova-play version                                      print this build identity
  nova-play help                                         print this text
  nova-play annotate --source <file> --author <name> --passage <text> --note <text>
  nova-play read --source <file>
  nova-play reply --source <file> --id <note-id> --author <name> --body <text>

Anchor notes to exact passages. Two people can annotate the same text,
answer each other, and come back later. When the source changes, the tool
says ANCHOR STALE instead of silently reassigning notes to the wrong place.

  --source <file>   the text being annotated (required for every verb)
  --author <name>   who is speaking (required for annotate and reply)
  --passage <text>  the exact passage to anchor to (annotate only)
  --note <text>     the annotation text (annotate only)
  --id <note-id>    the note to reply to (reply only)
  --body <text>     the reply text (reply only)

Flags come before positional arguments. Exit codes: 0 success, 1 anchor
conflict, 2 could not run.

example:
  nova-play annotate --source story.txt --author Emma --passage "The lantern room held a brass fitting." --note "I wonder what alloy this is."
  nova-play read --source story.txt
  nova-play reply --source story.txt --id f24beb35f0df --author Stella --body "Ship's brass, probably 70/30."
`

func refuse(stderr io.Writer, what string) int {
	fmt.Fprintf(stderr, "nova-play: %s; run: nova-play help\n", oneline.Escape(what))
	return 2
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return refuse(stderr, "no verb; refusing to guess")
	}

	switch args[0] {
	case "help":
		fmt.Fprint(stdout, usage)
		return 0
	case "version", "--version":
		if len(args) > 1 && args[0] != "--version" {
			return refuse(stderr, "version takes no arguments")
		}
		fmt.Fprintln(stdout, buildinfo.Line("nova-play", version))
		return 0
	case "annotate":
		return cmdAnnotate(args[1:], stdout, stderr)
	case "read":
		return cmdRead(args[1:], stdout, stderr)
	case "reply":
		return cmdReply(args[1:], stdout, stderr)
	default:
		return refuse(stderr, fmt.Sprintf("unknown verb: %s", args[0]))
	}
}

func cmdAnnotate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("annotate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	source := fs.String("source", "", "the text being annotated")
	author := fs.String("author", "", "who is speaking")
	passage := fs.String("passage", "", "the exact passage to anchor to")
	note := fs.String("note", "", "the annotation text")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if *source == "" || *author == "" || *passage == "" || *note == "" {
		return refuse(stderr, "annotate requires --source, --author, --passage, and --note")
	}

	n, err := play.Annotate(*source, *author, *passage, *note)
	if err != nil {
		fmt.Fprintf(stderr, "ANNOTATE FAIL %s\n", oneline.Escape(err.Error()))
		return 1
	}
	fmt.Fprintf(stdout, "ANNOTATE OK id=%s author=%s created=%s\n",
		n.ID, n.Author, n.CreatedAt.Format("2006-01-02T15:04:05Z"))
	return 0
}

func cmdRead(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("read", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	source := fs.String("source", "", "the text being annotated")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if *source == "" {
		return refuse(stderr, "read requires --source")
	}

	notes, anchorStatus, err := play.ReadNotes(*source)
	if err != nil {
		return refuse(stderr, err.Error())
	}

	if anchorStatus != "" {
		if strings.HasPrefix(anchorStatus, "ANCHOR STALE") {
			fmt.Fprintf(stdout, "READ %s source=%s notes=%d\n",
				anchorStatus, *source, len(notes))
			return 1
		}
		fmt.Fprintf(stdout, "READ OK source=%s notes=%d\n", *source, len(notes))
	} else {
		fmt.Fprintf(stdout, "READ OK source=%s notes=%d\n", *source, len(notes))
	}

	for _, n := range notes {
		fmt.Fprintf(stdout, "NOTE id=%s author=%s created=%s\n",
			n.ID, n.Author, n.CreatedAt.Format("2006-01-02T15:04:05Z"))
		fmt.Fprintf(stdout, "  PASSAGE %s\n", oneline.Escape(n.Passage))
		fmt.Fprintf(stdout, "  BODY %s\n", oneline.Escape(n.Note))
		for _, r := range n.Replies {
			fmt.Fprintf(stdout, "  REPLY id=%s author=%s created=%s\n",
				r.ID, r.Author, r.CreatedAt.Format("2006-01-02T15:04:05Z"))
			fmt.Fprintf(stdout, "    BODY %s\n", oneline.Escape(r.Note))
		}
	}
	return 0
}

func cmdReply(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("reply", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	source := fs.String("source", "", "the text being annotated")
	noteID := fs.String("id", "", "the note id to reply to")
	author := fs.String("author", "", "who is speaking")
	body := fs.String("body", "", "the reply text")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if *source == "" || *noteID == "" || *author == "" || *body == "" {
		return refuse(stderr, "reply requires --source, --id, --author, and --body")
	}

	r, err := play.ReplyTo(*source, *noteID, *author, *body)
	if err != nil {
		fmt.Fprintf(stderr, "REPLY FAIL %s\n", oneline.Escape(err.Error()))
		return 1
	}
	fmt.Fprintf(stdout, "REPLY OK id=%s author=%s created=%s\n",
		r.ID, r.Author, r.CreatedAt.Format("2006-01-02T15:04:05Z"))
	return 0
}
