package main

// Class J at this binary's own refusal points (#828): a forced refusal writes ONE row into
// <queue>/EDGES.tsv, and a second identical refusal writes none.

import (
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/edges"
)

// TestARefusalFilesItsOwnEdge drives the two refusal points a flag set has: a flag that is
// not there, and an input the caller did not give.
func TestARefusalFilesItsOwnEdge(t *testing.T) {
	queue := t.TempDir()
	var out, errs strings.Builder

	// Refusal point one: a flag nobody defined.
	if code := run([]string{"reap", "--queue", queue, "--roots", ".", "--deadline", "60", "--queu"}, &out, &errs, time.Now()); code != 2 {
		t.Fatalf("an unknown flag = %d, want 2", code)
	}
	rows, err := edges.Read(queue)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("an unknown flag wrote %d rows, want 1: %+v", len(rows), rows)
	}
	if rows[0].Tool != "nova-pulse" || rows[0].Verb != "reap" {
		t.Errorf("the row is %+v, want the tool and the verb that refused", rows[0])
	}
	if !strings.Contains(rows[0].Line, "queu") {
		t.Errorf("the row does not carry the line verbatim: %q", rows[0].Line)
	}
	if strings.TrimSpace(rows[0].Expected) == "" {
		t.Error("the row carries no expectation; the issue would have nothing to ask for")
	}

	// The SAME refusal again writes nothing.
	out.Reset()
	errs.Reset()
	if code := run([]string{"reap", "--queue", queue, "--roots", ".", "--deadline", "60", "--queu"}, &out, &errs, time.Now()); code != 2 {
		t.Fatal("the second run did not refuse")
	}
	if rows, _ := edges.Read(queue); len(rows) != 1 {
		t.Fatalf("a second identical refusal wrote a second row (%d rows)", len(rows))
	}

	// Refusal point two: a required input nobody gave. A different line, so a different row.
	out.Reset()
	errs.Reset()
	if code := run([]string{"reap", "--queue", queue, "--deadline", "60"}, &out, &errs, time.Now()); code != 2 {
		t.Fatal("a missing --roots was not refused")
	}
	rows, _ = edges.Read(queue)
	if len(rows) != 2 {
		t.Fatalf("a missing input wrote %d rows in total, want 2: %+v", len(rows), rows)
	}
	if !strings.Contains(rows[1].Line, "--roots") {
		t.Errorf("the second row does not name the missing flag: %q", rows[1].Line)
	}
}

// TestAnUnknownSubcommandFilesItsOwnEdge is the third shape of "the flag is not there": the
// verb is not there either, and the caller learns nothing about which verb to use.
func TestAnUnknownSubcommandFilesItsOwnEdge(t *testing.T) {
	queue := t.TempDir()
	t.Setenv("NOVA_QUEUE", queue)
	var out, errs strings.Builder
	if code := run([]string{"reapp"}, &out, &errs, time.Now()); code != 2 {
		t.Fatalf("an unknown subcommand = %d, want 2", code)
	}
	rows, _ := edges.Read(queue)
	if len(rows) != 1 || !strings.Contains(rows[0].Line, "reapp") {
		t.Fatalf("the unknown subcommand filed %+v", rows)
	}
}
