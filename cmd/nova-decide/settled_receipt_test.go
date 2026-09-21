package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A DECISION THAT COST NO CALL IS STILL A DECISION, AND IT IS STILL OWED A ROW.
//
// Stella's expanded Go read of #1925 at 352dad03: the settled machinery path
// returned before the explicitly configured decision store was ever opened, so
// the real CLI with a fresh TSV --dsn, a settled security state, no key and a
// dead endpoint printed success and created no store and no row. Rule 8 says a
// decision is logged beside the outcome it predicted; a decision the machinery
// made is exactly the decision worth having a row for, because it is the one
// nobody can reconstruct from a provider's log.
//
// Two things are controlled here and they pull in opposite directions:
//
//   - the receipt must EXIST -- one row, one line saying so, zero provider
//     calls, and a storage failure reported rather than swallowed;
//   - the receipt must carry NO PROVIDER CONFIDENCE. The 1.00 printed beside a
//     settled decision was a display constant, and a display constant written
//     into a calibration table becomes evidence about a provider that never
//     answered. The row's confidence column is a dash and the line's is a
//     dash, and the source says which of the two decided.
func TestTheSettledCLIPathWritesItsDecisionRowAndFabricatesNoConfidence(t *testing.T) {
	settledState := []string{
		"security_shaped_package: yes -- the card execs the reaper",
		"design_defaults_taken: 2",
		"normative_spec_moved: yes",
		"holder_of_the_area: design-authority",
		"hold_is_open: yes",
	}

	t.Run("a configured table gets exactly one row, with no key and a dead endpoint", func(t *testing.T) {
		dsn := filepath.Join(t.TempDir(), "decisions.tsv")
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			_, _ = w.Write([]byte(`{"answers":{"reader":{"type":"choice","choice":"child-review","confidence":1}}}`))
		}))
		srv.Close() // a DEAD endpoint: any call at all is a failure, not a fake answer
		t.Setenv("JEV_API_KEY", "")
		t.Setenv("TYPESAFE_API_KEY", "")

		var out, errb bytes.Buffer
		code := run([]string{
			"--questions", shippedReaderQuestions,
			"--state", readerStateFile(t, settledState...),
			"--base-url", srv.URL, "--key-env", "JEV_API_KEY", "--floor", "0.50",
			"--dsn", dsn,
		}, &out, &errb)
		if code != 0 {
			t.Fatalf("exit %d, want 0; stdout=%q stderr=%q", code, out.String(), errb.String())
		}
		if calls != 0 {
			t.Errorf("the provider was called %d time(s) on a settled decision", calls)
		}

		line := strings.TrimRight(out.String(), "\n") + " "
		for _, want := range []string{" reader=security-designate ", " source=machinery ", " conf=- ", " receipt=recorded "} {
			if !strings.Contains(line, want) {
				t.Errorf("the settled line does not carry %q:\n%s", strings.TrimSpace(want), line)
			}
		}
		if strings.Contains(line, "conf=1.00") {
			t.Errorf("the display constant became a confidence beside a call nobody made:\n%s", line)
		}

		raw, err := os.ReadFile(dsn)
		if err != nil {
			t.Fatalf("the configured decisions table was never created: %v", err)
		}
		rows := nonEmptyLines(string(raw))
		if len(rows) != 2 { // header + exactly one row
			t.Fatalf("want a header and exactly one row, got %d line(s):\n%s", len(rows), raw)
		}
		header, row := strings.Split(rows[0], "\t"), strings.Split(rows[1], "\t")
		got := map[string]string{}
		for i, name := range header {
			if i < len(row) {
				got[name] = row[i]
			}
		}
		if got["answer"] != "security-designate" {
			t.Errorf("the row's answer is %q, want security-designate", got["answer"])
		}
		if got["source"] != "machinery" {
			t.Errorf("the row's source is %q; a settled decision must say which of the two decided", got["source"])
		}
		if got["provider_confidence"] != "-" {
			t.Errorf("the row carries provider_confidence %q for a call nobody made; want a dash", got["provider_confidence"])
		}
	})

	t.Run("no table configured says so on the line, rather than saying nothing", func(t *testing.T) {
		t.Setenv("JEV_API_KEY", "")
		t.Setenv("TYPESAFE_API_KEY", "")
		t.Setenv("NOVA_DSN", "")
		var out, errb bytes.Buffer
		if code := run([]string{
			"--questions", shippedReaderQuestions,
			"--state", readerStateFile(t, settledState...),
			"--base-url", "http://127.0.0.1:1", "--floor", "0.50",
		}, &out, &errb); code != 0 {
			t.Fatalf("exit %d: %s", code, errb.String())
		}
		if !strings.Contains(out.String(), "receipt=not-configured") {
			t.Errorf("a decision with nowhere to be recorded must say so:\n%s", out.String())
		}
	})

	t.Run("a table that cannot be written is reported, never swallowed", func(t *testing.T) {
		dir := t.TempDir() // a DIRECTORY is not an appendable TSV
		t.Setenv("JEV_API_KEY", "")
		t.Setenv("TYPESAFE_API_KEY", "")
		var out, errb bytes.Buffer
		code := run([]string{
			"--questions", shippedReaderQuestions,
			"--state", readerStateFile(t, settledState...),
			"--base-url", "http://127.0.0.1:1", "--floor", "0.50",
			"--dsn", dir,
		}, &out, &errb)
		if code != 2 {
			t.Fatalf("a decisions write that failed exited %d, want 2; stdout=%q stderr=%q", code, out.String(), errb.String())
		}
		if !strings.Contains(errb.String(), "decisions") {
			t.Errorf("the refusal does not name the decisions table: %q", errb.String())
		}
	})
}

// nonEmptyLines is the TSV's own lines, blank ones dropped.
func nonEmptyLines(s string) []string {
	out := []string{}
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}
