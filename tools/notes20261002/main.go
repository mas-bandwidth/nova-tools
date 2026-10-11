// Command notes20261002 writes the notes of 2026-10-02 into nova-config: the
// reason each route the first real sprint disabled was disabled, and why one
// machine is held, as the `note` of the row (pkg/config, migration 0015;
// docs/SPEC-CONFIG.md, "The note"). The reasons were recorded on nova-tools#5101
// until the field existed; the numbers below are that issue's comments (ok /
// total over the day's work and reads), nothing re-measured.
//
// The coordinator runs it once, after `nova-config migrate` has applied 0015 to
// the store, naming the machine that was held (the one whose reads ran
// kernel-bound at 1:46 PM), first as a dry run:
//
//	NOVA_PG_DSN=postgres://... NOVA_FRIEND=<actor> notes20261002 <machine> --dry-run
//	NOVA_PG_DSN=postgres://... NOVA_FRIEND=<actor> notes20261002 <machine>
//
// It reads and writes through nova-config only: one `route set <name> --note`
// per disabled route and one `machine set <machine> --note`, each recorded in the
// row's history under the actor. A route that is not disabled when the program
// reaches it is skipped (a note that says why it is off must not sit on a route
// that is on), and a route that is no row is counted missing and the program
// ends 1; nothing is enabled, disabled or removed here. Run again, it writes the
// same notes again, one more history row each.
//
// env: NOVA_CONFIG the binary (default nova-config); NOTES_AS the actor (default
// NOVA_FRIEND, which nova-config reads itself); NOTES_CONN extra flags for every
// call, split on white space, such as --pg <dsn> or --file <path> (default none:
// NOVA_PG_DSN names the store).
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
)

// routeNote is one disabled route and the reason it carries.
type routeNote struct{ route, note string }

// routeNotes is the day's routes in the order they were decided: 1:25 PM EDT
// (comment of 2026-10-02 on nova-tools#5101); 2:05 PM, the body of #5101 (revs
// 300, 301) and the 2:25 PM record; the body (rev 304), use mercury only
// direct; 2:25 PM EDT, the six of the record over about 1,200 outcomes.
var routeNotes = []routeNote{
	{"flash-mimo26pro-openrouter",
		"disabled 2026-10-02 1:25 PM ET by the coordinator: 3 ok of 7 (4 ran to the 1200 s deadline with no RESULT.md, each a 20-minute slot on the held machine); not a limit or balance refusal, so not a rest; re-enable only with a measured reason; nova-tools#5101"},
	{"flash-luna6-opencode",
		"disabled 2026-10-02 2:05 PM ET (rev 300), the owner: feel free to disable luna: 2 ok of 12 on the day's record; not suited to flash work on this card shape; nova-tools#5101"},
	{"flash-luna6-openrouter",
		"disabled 2026-10-02 2:05 PM ET (rev 301), the owner: feel free to disable luna: 4 ok of 14 on the day's record; not suited to flash work on this card shape; nova-tools#5101"},
	{"flash-mercury-openrouter",
		"disabled 2026-10-02 (rev 304), the owner: use mercury only direct: direct 8 ok of 8, via openrouter 10 ok of 16; flash-mercury (direct) stays enabled; nova-tools#5101"},
	{"flash-nemotron-openrouter",
		"disabled 2026-10-02 2:25 PM ET by the coordinator: 4 ok of 52 on the day's record (48 ended with no result); re-enable only with a measured reason; nova-tools#5101"},
	{"flash-luna56-opencode",
		"disabled 2026-10-02 2:25 PM ET by the coordinator: 32 ok of 58 on the day's record (55%, under the 64% every kept route reached); re-enable only with a measured reason; nova-tools#5101"},
	{"flash-gemini31lite-openrouter",
		"disabled 2026-10-02 2:25 PM ET by the coordinator: 34 ok of 58 on the day's record (59%, under the 64% every kept route reached); re-enable only with a measured reason; nova-tools#5101"},
	{"flash-mimo26-openrouter",
		"disabled 2026-10-02 2:25 PM ET by the coordinator: 36 ok of 62 on the day's record (58%, under the 64% every kept route reached); re-enable only with a measured reason; nova-tools#5101"},
}

// machineNote is the held machine's reason, with the load that was measured
// (nova-tools#5101, the reader widths).
const machineNote = "held 1:46 PM ET 2026-10-02: reads kernel-bound (reads run the Go gate; load 80 on 36 cores at 1:48 PM, 40 of 64 cards back with no result); member 8, readers 4 and 4; nova-tools#5101"

// runner runs one command, its output to the writers, and returns its exit code;
// the error is a command that could not start.
type runner func(ctx context.Context, name string, args []string, stdout, stderr io.Writer) (int, error)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Getenv, os.Stdout, os.Stderr, execRunner))
}

// execRunner is the real runner: the process, bounded by pkg/subproc.
func execRunner(ctx context.Context, name string, args []string, stdout, stderr io.Writer) (int, error) {
	cmd := subproc.Context(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	return 0, err
}

// notes is one run: the binary, the flags every call carries, the counts.
type notes struct {
	bin            string
	conn, as, dry  []string
	run            runner
	stdout, stderr io.Writer
	wrote, skipped int
	missing        int
}

// run is the program: the exit code of args under the environment getenv.
func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer, r runner) int {
	machine, dry := "", false
	if len(args) > 0 {
		machine = args[0]
	}
	if len(args) > 1 {
		switch args[1] {
		case "":
		case "--dry-run":
			dry = true
		default:
			fmt.Fprintf(stderr, "notes-2026-10-02 REFUSED: unknown argument %s; want <machine> or <machine> --dry-run\n", args[1])
			return 2
		}
	}
	if machine == "" || machine == "--dry-run" {
		fmt.Fprintln(stderr, "notes-2026-10-02 REFUSED: want the held machine's name first: notes20261002 <machine> [--dry-run]")
		return 2
	}
	n := &notes{bin: getenv("NOVA_CONFIG"), conn: strings.Fields(getenv("NOTES_CONN")), run: r, stdout: stdout, stderr: stderr}
	if n.bin == "" {
		n.bin = "nova-config"
	}
	if as := getenv("NOTES_AS"); as != "" {
		n.as = []string{"--as", as}
	}
	if dry {
		n.dry = []string{"--dry-run"}
	}
	for _, rn := range routeNotes {
		if code := n.routeNote(ctx, rn); code != 0 {
			return code
		}
	}
	if code := n.call(ctx, append([]string{"machine", "set", machine, "--note", machineNote}, n.tail()...)); code != 0 {
		return code
	}
	fmt.Fprintf(stdout, "NOTES DONE routes_written=%d routes_skipped=%d routes_missing=%d machine=%s dry_run=%t\n", n.wrote, n.skipped, n.missing, machine, dry)
	if n.missing > 0 {
		return 1
	}
	return 0
}

// tail is the flags that end every write: the actor, the dry run, the store.
func (n *notes) tail() []string {
	t := append([]string{}, n.as...)
	t = append(t, n.dry...)
	return append(t, n.conn...)
}

// call runs one nova-config write; its failure ends the program with its code.
func (n *notes) call(ctx context.Context, args []string) int {
	code, err := n.run(ctx, n.bin, args, n.stdout, n.stderr)
	if err != nil {
		fmt.Fprintf(n.stderr, "notes-2026-10-02 FAILED: %s %s: %v\n", n.bin, strings.Join(args[:2], " "), err)
		return 1
	}
	return code
}

// routeNote sets the note of a route that is disabled: a route that is no row
// is counted missing, one that is on is skipped.
func (n *notes) routeNote(ctx context.Context, rn routeNote) int {
	var row bytes.Buffer
	show := append([]string{"route", "show", rn.route}, n.conn...)
	code, err := n.run(ctx, n.bin, show, &row, n.stderr)
	if err != nil || code != 0 {
		fmt.Fprintf(n.stderr, "NOTES MISSING route=%s\n", rn.route)
		n.missing++
		return 0
	}
	if !strings.Contains(row.String(), " enabled=false ") {
		fmt.Fprintf(n.stdout, "NOTES SKIP route=%s reason=enabled\n", rn.route)
		n.skipped++
		return 0
	}
	if code := n.call(ctx, append([]string{"route", "set", rn.route, "--note", rn.note}, n.tail()...)); code != 0 {
		return code
	}
	n.wrote++
	return 0
}
