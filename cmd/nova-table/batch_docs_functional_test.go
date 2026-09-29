//go:build functional

package main

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
)

// codeBlock returns the body of the first fenced block of the given language
// that follows heading in the document.
func codeBlock(t *testing.T, doc, heading, lang string) string {
	t.Helper()
	i := strings.Index(doc, heading)
	if i < 0 {
		t.Fatalf("no %q in the document", heading)
	}
	rest := doc[i:]
	open := "```" + lang + "\n"
	j := strings.Index(rest, open)
	if j < 0 {
		t.Fatalf("no %s block after %q", lang, heading)
	}
	rest = rest[j+len(open):]
	return rest[:strings.Index(rest, "```")]
}

var volatileFields = regexp.MustCompile(`event=\S+|before=\d+ after=\d+|trips=\d+`)

func normalize(out string) string { return volatileFields.ReplaceAllString(out, "N") }

// batchFixture is the table the documents' example runs against: member m1 is
// placed at build:ready at revision 1 with role=builder, and the table stands
// at some revision the manifest is told.
func batchFixture(t *testing.T) (addr string, rev string) {
	t.Helper()
	addr = throwaway(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	ctx := context.Background()
	cols, err := ntable.ParseColumns("ready,working,done")
	if err != nil {
		t.Fatal(err)
	}
	if err := ntable.Create(ctx, c, ntable.Table{Name: "demo", Columns: cols}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}
	seed := `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"2","operation_id":"seed","members":[{"id":"m1","expect":{"absent":true},"create":{"row":"build","col":"ready","score":1},"set":{"role":"builder"}}]}`
	if code, _, stderr := runTable("batch", "--redis", addr, seed); code != 0 {
		t.Fatalf("seed: %d %s", code, stderr)
	}
	return addr, c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()
}

func specDoc(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The documents' example manifests run, and print what the documents print.
func TestBatchDocumentedExamplesRunAndPrintWhatIsDocumented(t *testing.T) {
	t.Parallel()
	spec, cli := specDoc(t, "SPEC-NOVA-TABLE.md"), specDoc(t, "CLI.md")
	want := normalize(codeBlock(t, spec, "#### Output format", "text"))
	if got := normalize(codeBlock(t, cli, "### Batch mutation", "text")); got != want {
		t.Errorf("CLI.md prints:\n%s\nSPEC-NOVA-TABLE.md prints:\n%s", got, want)
	}
	// the documented manifest, at the fixture's revision
	addr, rev := batchFixture(t)
	manifest := codeBlock(t, spec, "#### Manifest structure", "json")
	manifest = strings.Replace(manifest, `"expected_table_revision": "5"`, `"expected_table_revision": "`+rev+`"`, 1)
	code, stdout, stderr := runTable("batch", "--redis", addr, manifest)
	if code != 0 || stderr != "" {
		t.Fatalf("the documented manifest: exit %d, stderr %q", code, stderr)
	}
	if got := normalize(stdout); got != want {
		t.Errorf("the verb prints:\n%s\nthe documents print:\n%s", got, want)
	}

	// the documented runnable example, from a file, on a fresh fixture
	for _, doc := range []struct{ name, text string }{{"SPEC-NOVA-TABLE.md", spec}, {"CLI.md", cli}} {
		addr, rev := batchFixture(t)
		sh := codeBlock(t, doc.text, "Runnable example", "sh")
		body := sh[strings.Index(sh, "<<'EOF'\n")+len("<<'EOF'\n"):]
		body = body[:strings.Index(body, "\nEOF\n")]
		body = strings.Replace(body, `"expected_table_revision": "5"`, `"expected_table_revision": "`+rev+`"`, 1)
		file := filepath.Join(t.TempDir(), "manifest.json")
		if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if code, out, stderr := runTable("batch", "--redis", addr, file); code != 0 || !strings.Contains(out, "outcome=changed") {
			t.Errorf("%s runnable example: exit %d\n%s\n%s", doc.name, code, out, stderr)
		}
	}
}

// -h prints the banner the specification prints, and the stdin form works.
func TestBatchHelpMatchesTheSpecBanner(t *testing.T) {
	t.Parallel()
	want := codeBlock(t, specDoc(t, "SPEC-NOVA-TABLE.md"), "`-h` prints the complete usage banner", "text")
	code, stdout, stderr := runTable("batch", "-h")
	if code != 0 || stderr != "" || stdout != want {
		t.Errorf("exit %d stderr %q\n got:\n%s\nspec:\n%s", code, stderr, stdout, want)
	}
	if !strings.Contains(stdout, "nova-table batch - < manifest.json") {
		t.Errorf("the help does not carry the stdin form")
	}
}

func TestBatchReadsTheManifestFromStdin(t *testing.T) {
	t.Parallel()
	addr, rev := batchFixture(t)
	manifest := `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"` + rev + `","operation_id":"stdin","members":[{"id":"m1","expect":{"revision":"1"}}]}`
	var stdout, stderr strings.Builder
	app := &application{in: strings.NewReader(manifest)}
	code := app.run([]string{"batch", "-", "--redis", addr}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "TABLE BATCH table=demo operation=stdin") || !strings.Contains(stdout.String(), "selected=1 guards=1 changed=0") {
		t.Errorf("exit %d\nstdout %s\nstderr %s", code, stdout.String(), stderr.String())
	}
}

// The summary counts and each member line carry score and fields.
func TestBatchPrintsCountsScoresAndFields(t *testing.T) {
	t.Parallel()
	addr, rev := batchFixture(t)
	manifest := `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"` + rev + `","operation_id":"counts","actor":"me","members":[` +
		`{"id":"m1","expect":{"revision":"1"},"move":{"row":"build","col":"working","score":7},"set":{"role":"lead","extra":"x y"},"unset":["absent"]},` +
		`{"id":"m2","expect":{"absent":true},"create":{"row":"build","col":"done","score":2.5}},` +
		`{"id":"m3","expect":{"absent":true}}]}`
	code, stdout, stderr := runTable("batch", "--redis", addr, manifest)
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, w := range []string{
		"selected=3 guards=1 changed=2 trips=1",
		`MEMBER m1 place=build:ready->build:working score=1->7 rev=1->2 fields={"extra":[null,"x y"],"role":["builder","lead"]}`,
		`MEMBER m2 place=-->build:done score=-->2.5 rev=0->1 fields={}`,
		`MEMBER m3 place=-->- score=-->- rev=0->0 fields={}`,
	} {
		if !strings.Contains(stdout, w+"\n") && !strings.Contains(stdout, w+" ") {
			t.Errorf("stdout lacks %q:\n%s", w, stdout)
		}
	}
}

func TestBatchEpochFlagMustEqualTheManifestEpoch(t *testing.T) {
	t.Parallel()
	addr, rev := batchFixture(t)
	manifest := `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"` + rev + `","operation_id":"ep","members":[{"id":"m1","expect":{}}]}`
	code, stdout, stderr := runTable("batch", "--redis", addr, "--epoch", "3", manifest)
	if code != 2 || stdout != "" {
		t.Fatalf("a differing --epoch: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	for _, w := range []string{"--epoch 3", `manifest's epoch "0"`, "changed=no"} {
		if !strings.Contains(stderr, w) {
			t.Errorf("refusal lacks %q: %s", w, stderr)
		}
	}
	// nothing was written by the refusal
	code, stdout, stderr = runTable("batch", "--redis", addr, "--epoch", "0", manifest)
	if code != 0 || !strings.Contains(stdout, "epoch=0") {
		t.Errorf("an equal --epoch: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	// an actor flag that differs from the manifest's actor is refused; one that fills is accepted
	named := strings.Replace(manifest, `"operation_id":"ep"`, `"operation_id":"ep2","actor":"alice"`, 1)
	named = strings.Replace(named, `"expected_table_revision":"`+rev+`"`, `"expected_table_revision":"`+nextRev(rev)+`"`, 1)
	if code, _, stderr := runTable("batch", "--redis", addr, "--actor", "bob", named); code != 2 || !strings.Contains(stderr, `--actor "bob"`) || !strings.Contains(stderr, `"alice"`) {
		t.Errorf("a differing --actor: exit %d %s", code, stderr)
	}
}

func nextRev(rev string) string {
	n, err := strconv.ParseUint(rev, 10, 64)
	if err != nil {
		return rev
	}
	return strconv.FormatUint(n+1, 10)
}

func TestBatchUnreadableManifestNamesThePathAndTheError(t *testing.T) {
	t.Parallel()
	addr := throwaway(t)
	dir := t.TempDir()
	for name, path := range map[string]string{"missing": filepath.Join(dir, "no-such-manifest.json"), "a directory": dir} {
		code, stdout, stderr := runTable("batch", "--redis", addr, path)
		if code != 2 || stdout != "" {
			t.Errorf("%s: exit %d stdout %q", name, code, stdout)
		}
		if !strings.Contains(stderr, path) {
			t.Errorf("%s: the refusal does not name the path: %s", name, stderr)
		}
		if strings.Contains(stderr, "invalid character") || strings.Contains(stderr, "invalid batch manifest") {
			t.Errorf("%s: a file that cannot be read is reported as bad JSON: %s", name, stderr)
		}
	}
	_, _, stderr := runTable("batch", "--redis", addr, filepath.Join(dir, "no-such-manifest.json"))
	if !strings.Contains(stderr, "no such file or directory") {
		t.Errorf("the operating system's error is missing: %s", stderr)
	}
}
