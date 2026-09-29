// Package main implements nova-sprint: dual-table card lifecycle and sprint journal CLI.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

var version string

const (
	// JournalFileName is the canonical journal file in the sprint state directory.
	JournalFileName = "journal.nsjr"

	// MetaFileName is the sprint state directory metadata.
	MetaFileName = "sprint.json"
)

const usage = `nova-sprint: dual-table card lifecycle and sprint journal CLI

usage:
  nova-sprint version                                     print build identity (--version also accepted)
  nova-sprint help                                        print usage text (-h, --help also accepted)
  nova-sprint init --dir <path>                           initialize a sprint journal directory
  nova-sprint status --dir <path> [--max <n>]             report journal status and inspect frames
  nova-sprint step --dir <path> --action <action-json>    append an action frame to the journal
  nova-sprint replay --dir <path>                         replay and verify all journal frames
  nova-sprint check --dir <path>                          gate: verify directory and journal integrity

exit codes:
  0  success / ok
  1  domain refusal / check fail / error running verb
  2  usage error / missing flags / bad argument
`

func journalPath(dir string) string { return filepath.Join(dir, JournalFileName) }
func metaPath(dir string) string    { return filepath.Join(dir, MetaFileName) }

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, time.Now().UTC()))
}

func run(args []string, stdout, stderr io.Writer, now time.Time) (code int) {
	defer verbflag.Recover(stdout, "nova-sprint", usage, &code)
	if len(args) == 0 {
		fmt.Fprint(stderr, "nova-sprint: no command given; run: nova-sprint help\n")
		return 2
	}

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "help", "-h", "--help":
		if cmd == "help" && len(rest) > 0 && rest[0] != "help" && !verbflag.IsHelp(rest[0]) {
			return run(append(rest, "--help"), stdout, stderr, now)
		}
		fmt.Fprint(stdout, usage)
		return 0
	case "version", "--version":
		return cmdVersion(rest, stdout, stderr)
	case "init":
		return cmdInit(rest, stdout, stderr, now)
	case "status":
		return cmdStatus(rest, stdout, stderr)
	case "step":
		return cmdStep(rest, stdout, stderr, now)
	case "replay":
		return cmdReplay(rest, stdout, stderr)
	case "check":
		return cmdCheck(rest, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "nova-sprint: unknown command %q; run: nova-sprint help\n", cmd)
		return 2
	}
}

func cmdVersion(args []string, stdout, stderr io.Writer) int {
	verbflag.HelpIfAsked(args, "version")
	if len(args) > 0 {
		fmt.Fprintf(stderr, "nova-sprint version: takes no flags and no arguments, got %d; run: nova-sprint help\n", len(args))
		return 2
	}
	fmt.Fprintln(stdout, buildinfo.Line("nova-sprint", version))
	return 0
}

func cmdInit(args []string, stdout, stderr io.Writer, now time.Time) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dir := fs.String("dir", "", "path to sprint state directory")
	if err := verbflag.Parse(fs, args); err != nil {
		fmt.Fprintf(stderr, "nova-sprint init: %s; run: nova-sprint help\n", oneline.Err(err))
		return 2
	}
	if *dir == "" {
		fmt.Fprintln(stderr, "nova-sprint init: --dir is required; run: nova-sprint help")
		return 2
	}
	if len(fs.Args()) > 0 {
		fmt.Fprintf(stderr, "nova-sprint init: unexpected argument %q; run: nova-sprint help\n", fs.Args()[0])
		return 2
	}

	jPath := journalPath(*dir)
	// If journal already exists, refuse to overwrite.
	if _, err := os.Stat(jPath); err == nil {
		fmt.Fprintf(stderr, "SPRINT FAIL init dir=%s: already initialized\n", oneline.Field(*dir))
		return 1
	}

	if err := os.MkdirAll(*dir, 0755); err != nil {
		fmt.Fprintf(stderr, "SPRINT FAIL init dir=%s: create directory: %s\n", oneline.Field(*dir), oneline.Err(err))
		return 1
	}

	jw, err := sprint.OpenJournalWriter(jPath)
	if err != nil {
		fmt.Fprintf(stderr, "SPRINT FAIL init dir=%s: open journal: %s\n", oneline.Field(*dir), oneline.Err(err))
		return 1
	}
	if err := jw.Close(); err != nil {
		fmt.Fprintf(stderr, "SPRINT FAIL init dir=%s: close journal: %s\n", oneline.Field(*dir), oneline.Err(err))
		return 1
	}

	meta := map[string]interface{}{
		"version":        1,
		"initialized_at": now.UTC().Format(time.RFC3339),
		"journal":        JournalFileName,
	}
	metaBytes, _ := json.MarshalIndent(meta, "", "  ")
	_ = os.WriteFile(metaPath(*dir), metaBytes, 0644)

	fmt.Fprintf(stdout, "SPRINT OK init dir=%s journal=%s\n", oneline.Field(*dir), JournalFileName)
	return 0
}

func cmdStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dir := fs.String("dir", "", "path to sprint state directory")
	max := fs.Int("max", bounded.Default, "maximum frames to list")
	if err := verbflag.Parse(fs, args); err != nil {
		fmt.Fprintf(stderr, "nova-sprint status: %s; run: nova-sprint help\n", oneline.Err(err))
		return 2
	}
	if *dir == "" {
		fmt.Fprintln(stderr, "nova-sprint status: --dir is required; run: nova-sprint help")
		return 2
	}
	if *max < 0 {
		fmt.Fprintf(stderr, "nova-sprint status: --max must be >= 0 (got %d); run: nova-sprint help\n", *max)
		return 2
	}
	if len(fs.Args()) > 0 {
		fmt.Fprintf(stderr, "nova-sprint status: unexpected argument %q; run: nova-sprint help\n", fs.Args()[0])
		return 2
	}

	jPath := journalPath(*dir)
	if _, err := os.Stat(jPath); err != nil {
		fmt.Fprintf(stderr, "SPRINT FAIL status dir=%s: not initialized; run: nova-sprint init --dir <path>\n", oneline.Field(*dir))
		return 1
	}

	jr, err := sprint.OpenJournalReader(jPath)
	if err != nil {
		fmt.Fprintf(stderr, "SPRINT FAIL status dir=%s: open reader: %s\n", oneline.Field(*dir), oneline.Err(err))
		return 1
	}
	defer jr.Close()

	frames, err := jr.Scan()
	if err != nil {
		fmt.Fprintf(stderr, "SPRINT FAIL status dir=%s: journal corrupt: %s\n", oneline.Field(*dir), oneline.Err(err))
		return 1
	}

	var totalBytes int64
	var lastSeq uint64
	for _, f := range frames {
		totalBytes += f.TotalSize
		if f.Seq > lastSeq {
			lastSeq = f.Seq
		}
	}

	fmt.Fprintf(stdout, "SPRINT OK status dir=%s frames=%d bytes=%d last_seq=%d\n",
		oneline.Field(*dir), len(frames), totalBytes, lastSeq)

	list := bounded.Capped(stdout, *max, "SPRINT", "frames", "--max <n> raises ceiling, --max 0 shows all")
	for _, f := range frames {
		list.Line(fmt.Sprintf("SPRINT OK frame seq=%d ts=%s size=%d payload=%s",
			f.Seq, oneline.Field(f.Timestamp.Format(time.RFC3339)), f.TotalSize, oneline.Escape(string(f.Payload))))
	}
	list.More()
	return 0
}

func cmdStep(args []string, stdout, stderr io.Writer, now time.Time) int {
	fs := flag.NewFlagSet("step", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dir := fs.String("dir", "", "path to sprint state directory")
	action := fs.String("action", "", "action JSON payload")
	if err := verbflag.Parse(fs, args); err != nil {
		fmt.Fprintf(stderr, "nova-sprint step: %s; run: nova-sprint help\n", oneline.Err(err))
		return 2
	}
	if *dir == "" || *action == "" {
		fmt.Fprintln(stderr, "nova-sprint step: --dir and --action are required; run: nova-sprint help")
		return 2
	}
	if len(fs.Args()) > 0 {
		fmt.Fprintf(stderr, "nova-sprint step: unexpected argument %q; run: nova-sprint help\n", fs.Args()[0])
		return 2
	}

	trimmedAction := strings.TrimSpace(*action)
	if !json.Valid([]byte(trimmedAction)) {
		fmt.Fprintf(stderr, "nova-sprint step: --action must be valid JSON: %s\n", oneline.Escape(trimmedAction))
		return 2
	}

	jPath := journalPath(*dir)
	if _, err := os.Stat(jPath); err != nil {
		fmt.Fprintf(stderr, "SPRINT FAIL step dir=%s: not initialized; run: nova-sprint init --dir <path>\n", oneline.Field(*dir))
		return 1
	}

	jw, err := sprint.OpenJournalWriter(jPath)
	if err != nil {
		fmt.Fprintf(stderr, "SPRINT FAIL step dir=%s: open journal: %s\n", oneline.Field(*dir), oneline.Err(err))
		return 1
	}
	defer jw.Close()

	frame, err := jw.AppendWithTimestamp([]byte(trimmedAction), now)
	if err != nil {
		fmt.Fprintf(stderr, "SPRINT FAIL step dir=%s: append failed: %s\n", oneline.Field(*dir), oneline.Err(err))
		return 1
	}

	fmt.Fprintf(stdout, "SPRINT OK step dir=%s seq=%d offset=%d size=%d\n",
		oneline.Field(*dir), frame.Seq, frame.Offset, frame.TotalSize)
	return 0
}

func cmdReplay(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dir := fs.String("dir", "", "path to sprint state directory")
	if err := verbflag.Parse(fs, args); err != nil {
		fmt.Fprintf(stderr, "nova-sprint replay: %s; run: nova-sprint help\n", oneline.Err(err))
		return 2
	}
	if *dir == "" {
		fmt.Fprintln(stderr, "nova-sprint replay: --dir is required; run: nova-sprint help")
		return 2
	}
	if len(fs.Args()) > 0 {
		fmt.Fprintf(stderr, "nova-sprint replay: unexpected argument %q; run: nova-sprint help\n", fs.Args()[0])
		return 2
	}

	jPath := journalPath(*dir)
	if _, err := os.Stat(jPath); err != nil {
		fmt.Fprintf(stderr, "SPRINT FAIL replay dir=%s: not initialized; run: nova-sprint init --dir <path>\n", oneline.Field(*dir))
		return 1
	}

	jr, err := sprint.OpenJournalReader(jPath)
	if err != nil {
		fmt.Fprintf(stderr, "SPRINT FAIL replay dir=%s: open reader: %s\n", oneline.Field(*dir), oneline.Err(err))
		return 1
	}
	defer jr.Close()

	frames, err := jr.Scan()
	if err != nil {
		fmt.Fprintf(stderr, "SPRINT FAIL replay dir=%s: verification failed: %s\n", oneline.Field(*dir), oneline.Err(err))
		return 1
	}

	var totalBytes int64
	for _, f := range frames {
		totalBytes += f.TotalSize
	}

	fmt.Fprintf(stdout, "SPRINT OK replay dir=%s frames=%d bytes=%d verified=true\n",
		oneline.Field(*dir), len(frames), totalBytes)
	return 0
}

func cmdCheck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dir := fs.String("dir", "", "path to sprint state directory")
	if err := verbflag.Parse(fs, args); err != nil {
		fmt.Fprintf(stderr, "nova-sprint check: %s; run: nova-sprint help\n", oneline.Err(err))
		return 2
	}
	if *dir == "" {
		fmt.Fprintln(stderr, "nova-sprint check: --dir is required; run: nova-sprint help")
		return 2
	}
	if len(fs.Args()) > 0 {
		fmt.Fprintf(stderr, "nova-sprint check: unexpected argument %q; run: nova-sprint help\n", fs.Args()[0])
		return 2
	}

	jPath := journalPath(*dir)
	if _, err := os.Stat(jPath); err != nil {
		fmt.Fprintf(stderr, "SPRINT FAIL check dir=%s: not initialized; run: nova-sprint init --dir <path>\n", oneline.Field(*dir))
		return 1
	}

	jr, err := sprint.OpenJournalReader(jPath)
	if err != nil {
		fmt.Fprintf(stderr, "SPRINT FAIL check dir=%s: open reader: %s\n", oneline.Field(*dir), oneline.Err(err))
		return 1
	}
	defer jr.Close()

	frames, err := jr.Scan()
	if err != nil {
		fmt.Fprintf(stderr, "SPRINT FAIL check dir=%s: corrupted: %s\n", oneline.Field(*dir), oneline.Err(err))
		return 1
	}

	fmt.Fprintf(stdout, "SPRINT OK check dir=%s frames=%d intact=true\n",
		oneline.Field(*dir), len(frames))
	return 0
}
