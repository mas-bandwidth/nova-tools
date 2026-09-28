package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// clockMask is the retired form. A pasted date has digits where this has x.
// The word boundaries keep a glued token and a longer number from matching,
// and they let 01:0x match inside 04:01:0x.
var clockMask = regexp.MustCompile(`~?\b[0-2]?[0-9]:[0-5]x\b`)

// specimenToken skips a mask when it appears earlier on the same line.
// It marks an example of the retired form. A real claim that carries the
// token is a lie the check cannot catch.
const specimenToken = "MASK-SPECIMEN"

// clockNext is the one next action for a mask: paste a clock, or drop the claim.
const clockNext = "paste the date output or drop the clock claim"

// maxLine is the scanner ceiling. A longer line is an error, and the check
// warns and passes: a courtesy check does not block on its own error.
const maxLine = 4 * 1024 * 1024

type clockHit struct {
	Line  int
	Where string
	Mask  string
	Text  string
}

type clockScan struct {
	Judged int
	Hits   []clockHit
}

// scanLines visits each line. fn returns false to stop. A token longer than
// maxLine is an error; a trailing CR is not part of the line.
func scanLines(text string, fn func(line string, n int) bool) error {
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64*1024), maxLine)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSuffix(sc.Text(), "\r")
		if !fn(line, n) {
			return nil
		}
	}
	return sc.Err()
}

// masksOn returns every retired mask on one line, in order. A mask whose
// prefix holds specimenToken is an example and is not a hit.
func masksOn(line string, lineNo int, where string) []clockHit {
	var hits []clockHit
	for _, loc := range clockMask.FindAllStringIndex(line, -1) {
		if strings.Contains(line[:loc[0]], specimenToken) {
			continue
		}
		hits = append(hits, clockHit{
			Line:  lineNo,
			Where: where,
			Mask:  line[loc[0]:loc[1]],
			Text:  line,
		})
	}
	return hits
}

// newFilePath reads the new-file path from a +++ header. An unrecognized
// header yields "" — the path is where the line was headed, and the line
// itself is the finding.
func newFilePath(header string) string {
	p := strings.TrimPrefix(header, "+++")
	p = strings.TrimLeft(p, " \t")
	if p == "/dev/null" {
		return ""
	}
	p = strings.TrimPrefix(p, "b/")
	return strings.Trim(p, `"`)
}

// scanStaged judges added lines of a unified diff. A line that begins with
// +++ is a file header, not an added line. Context and removed lines are
// not claims being added.
func scanStaged(text string) (clockScan, error) {
	var s clockScan
	path := ""
	err := scanLines(text, func(line string, n int) bool {
		switch {
		case strings.HasPrefix(line, "+++"):
			path = newFilePath(line)
		case strings.HasPrefix(line, "+"):
			s.Judged++
			where := path
			if where == "" {
				where = "-"
			}
			s.Hits = append(s.Hits, masksOn(line[1:], n, where)...)
		}
		return true
	})
	return s, err
}

// scanMessage judges a commit message. A line that begins with # is not
// kept, and a # line that contains >8 is the scissors: nothing below it
// is kept either.
func scanMessage(text string) (clockScan, error) {
	var s clockScan
	err := scanLines(text, func(line string, n int) bool {
		if strings.HasPrefix(line, "#") {
			if strings.Contains(line, ">8") {
				return false
			}
			return true
		}
		s.Judged++
		s.Hits = append(s.Hits, masksOn(line, n, "message")...)
		return true
	})
	return s, err
}

// cmdCheck judges a retired clock mask in staged diff text or in a commit
// message. It writes nothing. A mask is exit 1. A clean scan is exit 0.
// A bad invocation is exit 2. A line the scanner cannot read warns and passes.
func cmdCheck(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	staged := fs.Bool("staged", false, "judge a unified diff; only an added line, never a +++ header")
	message := fs.Bool("message", false, "judge a commit message; skip # lines, and a # line containing >8 ends it")
	text := fs.String("text", "", "`words` of the diff or the message")
	file := fs.String("file", "", "`path` of the diff or the message; - reads stdin")
	given, ok := parse(fs, args, stderr)
	if given == nil {
		return 2
	}
	bad := !ok
	if fs.NArg() > 0 {
		refuse(stderr, " check", fmt.Sprintf("unexpected argument %q; nothing was checked; drop the extra argument", fs.Arg(0)))
		bad = true
	}
	stagedOn := given["staged"] && *staged
	messageOn := given["message"] && *message
	switch {
	case stagedOn && messageOn:
		refuse(stderr, " check", "--staged and --message both name the input; nothing was checked; pass exactly one")
		bad = true
	case !stagedOn && !messageOn:
		refuse(stderr, " check", "no input was named; nothing was checked; pass exactly one of --staged or --message")
		bad = true
	}
	var words string
	switch {
	case given["text"] && given["file"]:
		refuse(stderr, " check", "--text and --file both name the lines; nothing was checked; give exactly one")
		bad = true
	case !given["text"] && !given["file"]:
		refuse(stderr, " check", "no lines were named; nothing was checked; name them with exactly one of --text or --file")
		bad = true
	case given["text"]:
		words = *text
	default:
		raw, err := readWords(*file, stdin)
		if err != nil {
			refuse(stderr, " check", fmt.Sprintf("%s; nothing was checked; name a readable path or --file -", err.Error()))
			bad = true
		} else {
			words = string(raw)
		}
	}
	if bad {
		return 2
	}
	var scan clockScan
	var err error
	if stagedOn {
		scan, err = scanStaged(words)
	} else {
		scan, err = scanMessage(words)
	}
	if err != nil {
		fmt.Fprintf(stderr, "CHECK WARN cause=cannot-scan state=passed next=proceed detail=%s\n",
			oneline.Field(oneline.Cap(err.Error(), oneline.TailBytes)))
		return 0
	}
	if len(scan.Hits) == 0 {
		if stagedOn {
			fmt.Fprintf(stdout, "CHECK OK added=%d masks=0\n", scan.Judged)
		} else {
			fmt.Fprintf(stdout, "CHECK OK lines=%d masks=0\n", scan.Judged)
		}
		return 0
	}
	for _, h := range scan.Hits {
		fmt.Fprintf(stderr, "CHECK FAIL where=%s line=%d mask=%s text=%s cause=unpastable-clock-mask state=unchanged next=%s\n",
			oneline.Field(h.Where),
			h.Line,
			oneline.Field(h.Mask),
			oneline.Field(oneline.Cap(h.Text, oneline.TailBytes)),
			oneline.Field(clockNext))
	}
	return 1
}
