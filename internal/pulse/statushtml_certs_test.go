package pulse

// The certification column on the fleet page. Every test here drives the fakes: no ssh, no
// forge, no clock of its own. The certificates file is the ONLY thing the column reads, and
// that is the point -- the page says what the record says about a machine it has not asked.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// certsAt writes a certificates file from `machine class verdict build hash age` tuples.
func certsAt(t *testing.T, now time.Time, rows ...[6]string) string {
	t.Helper()
	var b strings.Builder
	for _, r := range rows {
		age, err := time.ParseDuration(r[5])
		if err != nil {
			t.Fatalf("bad age %q: %v", r[5], err)
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\tevidence\t%s\n",
			r[0], r[3], r[4], r[1], r[2], now.Add(-age).UTC().Format(time.RFC3339))
	}
	p := filepath.Join(t.TempDir(), "certs.tsv")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestTheFleetPageCarriesACertificationCellPerMachine.
func TestTheFleetPageCarriesACertificationCellPerMachine(t *testing.T) {
	now := time.Date(2026, 9, 18, 18, 0, 0, 0, time.UTC)
	certs := certsAt(t, now,
		// hulk: two current, one FAIL, one aged out -> certified=2/4 stale=2
		[6]string{"hulk", "go-test", "OK", "v0.17.0", "h1", "10m"},
		[6]string{"hulk", "c-build", "OK", "v0.17.0", "h1", "20m"},
		[6]string{"hulk", "sbcl", "FAIL", "v0.17.0", "h1", "15m"},
		[6]string{"hulk", "git-push", "OK", "v0.17.0", "h1", "30h"},
		// air: one current, one written under the build it had BEFORE the adopt
		[6]string{"air", "go-test", "OK", "v0.17.0", "h1", "5m"},
		[6]string{"air", "sbcl", "OK", "v0.16.0", "h1", "6m"},
		// vision has no row at all.
	)
	page := renderWithCerts(t, now, certs, []string{"hulk", "air", "vision"})
	for _, want := range []string{
		"<th>certification</th>",
		"certified=2/4 stale=2",
		"certified=1/2 stale=1",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page does not carry %q:\n%s", want, tableOf(page))
		}
	}
	// vision: a dash, never a zero. `certified=0/0` reads as a machine that failed.
	if strings.Contains(page, "certified=0/0") {
		t.Errorf("a machine nobody has certified printed a count:\n%s", tableOf(page))
	}
}

// TestNoCertsFileIsADashAndNeverAZero: --certs left out is "nobody asked".
func TestNoCertsFileIsADashAndNeverAZero(t *testing.T) {
	now := time.Date(2026, 9, 18, 18, 0, 0, 0, time.UTC)
	page := renderWithCerts(t, now, "", []string{"hulk"})
	if !strings.Contains(page, "<th>certification</th>") {
		t.Error("the column disappears when nothing was read; a fixed table writes every field every time")
	}
	if strings.Contains(page, "certified=") {
		t.Errorf("the page printed a certification count with no certificates file:\n%s", tableOf(page))
	}
}

// TestADownBenchStillCarriesWhatTheRecordSays. DOWN is read over ssh; the record is read
// from a file, and "DOWN and certified 2/2 an hour ago" sends a person somewhere different
// from "DOWN and never certified".
func TestADownBenchStillCarriesWhatTheRecordSays(t *testing.T) {
	now := time.Date(2026, 9, 18, 18, 0, 0, 0, time.UTC)
	certs := certsAt(t, now, [6]string{"hulk", "go-test", "OK", "v0.17.0", "h1", "10m"})
	html := filepath.Join(t.TempDir(), "status.html")
	code := StatusHTML(StatusHTMLInput{
		HTML: html, Benches: benchesFile(t, []string{"hulk"}), Queue: t.TempDir(),
		Certs: certs, Now: func() time.Time { return now },
		Reader: func(list []FleetBench, at time.Time) []BenchReading {
			return []BenchReading{{Name: "hulk", Down: true, Note: "no answer over ssh"}}
		},
		Stdout: io.Discard, Stderr: io.Discard,
	})
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	page := readFile(t, html)
	if !strings.Contains(page, "<b>DOWN</b>") {
		t.Fatalf("no DOWN row:\n%s", tableOf(page))
	}
	if !strings.Contains(page, "certified=1/1 stale=0") {
		t.Errorf("a DOWN bench lost its record:\n%s", tableOf(page))
	}
}

// TestACertificatesFileThatWillNotReadIsSaidOutLoud: a page full of dashes nobody can
// explain is the DOWN row all over again.
func TestACertificatesFileThatWillNotReadIsSaidOutLoud(t *testing.T) {
	now := time.Date(2026, 9, 18, 18, 0, 0, 0, time.UTC)
	bad := filepath.Join(t.TempDir(), "certs.tsv")
	if err := os.WriteFile(bad, []byte("this is not a certificate row\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	html := filepath.Join(t.TempDir(), "status.html")
	var errs strings.Builder
	code := StatusHTML(StatusHTMLInput{
		HTML: html, Benches: benchesFile(t, []string{"hulk"}), Queue: t.TempDir(),
		Certs: bad, Now: func() time.Time { return now },
		Reader: func(list []FleetBench, at time.Time) []BenchReading {
			return []BenchReading{{Name: "hulk", Cores: 64}}
		},
		Stdout: io.Discard, Stderr: &errs,
	})
	if code != 0 {
		t.Fatalf("exit = %d; an unreadable certificates file must not take the page down", code)
	}
	if !strings.Contains(errs.String(), "STATUS NOTE certs=") {
		t.Errorf("nothing said the file could not be read:\n%s", errs.String())
	}
	if strings.Contains(readFile(t, html), "certified=") {
		t.Error("the page printed counts from a file it could not read")
	}
}

// renderWithCerts writes the page for the named benches and answers its body.
func renderWithCerts(t *testing.T, now time.Time, certs string, names []string) string {
	t.Helper()
	html := filepath.Join(t.TempDir(), "status.html")
	code := StatusHTML(StatusHTMLInput{
		HTML: html, Benches: benchesFile(t, names), Queue: t.TempDir(),
		Certs: certs, Now: func() time.Time { return now },
		Reader: func(list []FleetBench, at time.Time) []BenchReading {
			out := make([]BenchReading, 0, len(list))
			for _, b := range list {
				out = append(out, BenchReading{Name: b.Name, Cores: 8, Allowed: 2})
			}
			return out
		},
		Stdout: io.Discard, Stderr: io.Discard,
	})
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	return readFile(t, html)
}

func benchesFile(t *testing.T, names []string) string {
	t.Helper()
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "%s\t%s\t/home/%s\t-\n", n, n, n)
	}
	p := filepath.Join(t.TempDir(), "benches.tsv")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// tableOf is the slots table alone, so a failure prints the rows and not the whole page.
func tableOf(page string) string {
	i := strings.Index(page, "<h3>slots</h3>")
	if i < 0 {
		return page
	}
	rest := page[i:]
	if j := strings.Index(rest, "</table>"); j >= 0 {
		return rest[:j]
	}
	return rest
}
