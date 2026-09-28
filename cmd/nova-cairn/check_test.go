package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func assertCheckFail(t *testing.T, line string, lineNo int, where, mask, text string) {
	t.Helper()
	for _, want := range []string{
		"CHECK FAIL",
		"where=" + oneline.Field(where),
		"line=" + strconv.Itoa(lineNo),
		"mask=" + mask,
		"text=" + oneline.Field(text),
		"cause=unpastable-clock-mask",
		"state=unchanged",
		"next=" + oneline.Field(clockNext),
	} {
		if !strings.Contains(line, want) {
			t.Errorf("line %q does not contain %q", line, want)
		}
	}
	if !strings.HasPrefix(line, "CHECK FAIL ") {
		t.Errorf("line does not name the operation: %q", line)
	}
}

func TestCheckRefusesATypedClock(t *testing.T) {
	t.Parallel()

	// Added lines carry the retired forms. The full time on its own added
	// line must not become a hit: a pasted clock has no x. 04:01:0x is
	// caught by the retired form inside it, 01:0x.
	diff := strings.Join([]string{
		"+++ b/record.md",
		"+~19:0x",
		"+~23:1x",
		"+~23:5x",
		"+~00:3x",
		"+03:4x",
		"+03:36:42",
		"+04:01:0x",
	}, "\n")
	code, stdout, stderr := runCode("", "check", "--staged", "--text", diff)
	if code != 1 {
		t.Fatalf("staged check exited %d, want 1; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("a mask wrote to stdout: %q", stdout)
	}
	if strings.Contains(stderr, "03:36:42") {
		t.Fatalf("a full time was refused:\n%s", stderr)
	}
	want := []struct {
		line int
		mask string
		text string
	}{
		{2, "~19:0x", "~19:0x"},
		{3, "~23:1x", "~23:1x"},
		{4, "~23:5x", "~23:5x"},
		{5, "~00:3x", "~00:3x"},
		{6, "03:4x", "03:4x"},
		{8, "01:0x", "04:01:0x"},
	}
	lines := strings.Split(strings.TrimSuffix(stderr, "\n"), "\n")
	if len(lines) != len(want) {
		t.Fatalf("got %d refusal lines, want %d:\n%s", len(lines), len(want), stderr)
	}
	for i, w := range want {
		assertCheckFail(t, lines[i], w.line, "record.md", w.mask, w.text)
	}

	code, stdout, stderr = runCode("", "check", "--message", "--text", "Sealed at 04:01:0x (pasted)")
	if code != 1 || stdout != "" {
		t.Fatalf("message check exited %d stdout=%q stderr=%q", code, stdout, stderr)
	}
	msgLines := strings.Split(strings.TrimSuffix(stderr, "\n"), "\n")
	if len(msgLines) != 1 {
		t.Fatalf("message check wrote %d lines:\n%s", len(msgLines), stderr)
	}
	assertCheckFail(t, msgLines[0], 1, "message", "01:0x", "Sealed at 04:01:0x (pasted)")
}

func TestCheckPassesAPastedClock(t *testing.T) {
	t.Parallel()

	// The first command: a pasted date on an added line.
	code, stdout, stderr := runCode("", "check", "--staged", "--text", "+kept at Mon Aug 10 03:57:53 UTC 2026")
	if code != 0 || stderr != "" || stdout != "CHECK OK added=1 masks=0\n" {
		t.Fatalf("pasted added line exited %d stdout=%q stderr=%q", code, stdout, stderr)
	}

	for _, line := range []string{
		"Mon Aug 10 03:57:53 UTC 2026",
		"03:36:42",
		"98 minutes",
		"11m47s",
		"the constant 0x1F is hex, not a clock",
		"ratio was 123:4x scaled",
		"19:5xray",
		"x19:5x",
		"ordinary prose with no clock claim at all",
	} {
		code, stdout, stderr = runCode("", "check", "--message", "--text", line)
		if code != 0 || stderr != "" || stdout != "CHECK OK lines=1 masks=0\n" {
			t.Errorf("message %q exited %d stdout=%q stderr=%q", line, code, stdout, stderr)
		}
	}

	// A typed mask that is not an added line is not a claim being committed.
	code, stdout, stderr = runCode("", "check", "--staged", "--text", "~19:0x\n")
	if code != 0 || stderr != "" || stdout != "CHECK OK added=0 masks=0\n" {
		t.Fatalf("non-added mask exited %d stdout=%q stderr=%q", code, stdout, stderr)
	}

	// Context, a removal, and a header path are not judged. The added line
	// is a pasted date.
	raw := strings.Join([]string{
		"--- a/notes-19:5x.md",
		"+++ b/notes-19:5x.md",
		"@@ -1,2 +1,2 @@",
		" ~19:0x stays",
		"-~23:1x removed",
		"+Mon Aug 10 03:57:53 UTC 2026",
	}, "\n")
	code, stdout, stderr = runCode("", "check", "--staged", "--text", raw)
	if code != 0 || stderr != "" || stdout != "CHECK OK added=1 masks=0\n" {
		t.Fatalf("context and removal exited %d stdout=%q stderr=%q", code, stdout, stderr)
	}

	msg := strings.Join([]string{
		"kept at Mon Aug 10 03:57:53 UTC 2026",
		"# a comment names ~19:0x and is not committed",
		"# ------------------------ >8 ------------------------",
		"~23:5x below the scissors",
		" ~00:3x below the scissors",
	}, "\n")
	code, stdout, stderr = runCode("", "check", "--message", "--text", msg)
	if code != 0 || stderr != "" || stdout != "CHECK OK lines=1 masks=0\n" {
		t.Fatalf("comments and scissors exited %d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestCheckSkipsASpecimenOnlyWhenTheTokenIsEarlierOnTheSameLine(t *testing.T) {
	t.Parallel()

	pass := []string{
		"+MASK-SPECIMEN ~19:0x",
		"+MASK-SPECIMEN 19:5x and 03:4x",
	}
	for _, text := range pass {
		code, stdout, stderr := runCode("", "check", "--staged", "--text", text)
		if code != 0 || stderr != "" || stdout != "CHECK OK added=1 masks=0\n" {
			t.Errorf("%q exited %d stdout=%q stderr=%q, want a clean scan", text, code, stdout, stderr)
		}
	}

	code, stdout, stderr := runCode("", "check", "--staged", "--text", "+~19:0x MASK-SPECIMEN")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "mask=~19:0x") {
		t.Fatalf("a token after the mask exited %d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, stdout, stderr = runCode("", "check", "--message", "--text", "mask-specimen ~19:0x")
	if code != 1 || !strings.Contains(stderr, "mask=~19:0x") {
		t.Fatalf("a lower-case token exited %d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, stdout, stderr = runCode("", "check", "--staged", "--text", "+MASK-SPECIMEN ~19:0x\n+~19:0x")
	if code != 1 || !strings.Contains(stderr, "line=2") || !strings.Contains(stderr, "mask=~19:0x") {
		t.Fatalf("a token on the previous line exited %d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if strings.Contains(stderr, "line=1") {
		t.Fatalf("the specimen line itself was refused:\n%s", stderr)
	}

	code, stdout, stderr = runCode("", "check", "--message", "--text", "19:5x MASK-SPECIMEN 03:4x")
	if code != 1 || stdout != "" {
		t.Fatalf("a token between two masks exited %d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "mask=19:5x") || strings.Contains(stderr, "mask=03:4x") {
		t.Fatalf("want only the mask before the token:\n%s", stderr)
	}
}

func TestCheckNamesEveryMissingInputAndWritesNothing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	note := filepath.Join(dir, "note.diff")
	body := []byte("+++ b/record.md\n+note ~19:5x\n")
	if err := os.WriteFile(note, body, 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCode("", "check", "--staged", "--file", note)
	if code != 1 || stdout != "" || !strings.Contains(stderr, "mask=~19:5x") || !strings.Contains(stderr, "state=unchanged") {
		t.Fatalf("file check exited %d stdout=%q stderr=%q", code, stdout, stderr)
	}
	got, err := os.ReadFile(note)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatalf("check wrote the file: %q", got)
	}
	after, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("check created files: before %d after %d", len(before), len(after))
	}

	code, stdout, stderr = runCode("+~03:4x\n", "check", "--staged", "--file", "-")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "mask=~03:4x") || !strings.Contains(stderr, "where=-") {
		t.Fatalf("stdin check exited %d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runCode("11m47s\n", "check", "--message", "--file", "-")
	if code != 0 || stderr != "" || stdout != "CHECK OK lines=1 masks=0\n" {
		t.Fatalf("stdin duration exited %d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runCode("", "check", "--message", "--text", "")
	if code != 0 || stderr != "" || stdout != "CHECK OK lines=0 masks=0\n" {
		t.Fatalf("empty message exited %d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runCode("", "check", "--staged", "--text", "")
	if code != 0 || stderr != "" || stdout != "CHECK OK added=0 masks=0\n" {
		t.Fatalf("empty diff exited %d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, stdout, stderr = runCode("", "check")
	if code != 2 || stdout != "" || !strings.HasSuffix(strings.TrimSuffix(stderr, "\n"), "; run: nova-cairn help") {
		t.Fatalf("bare check exited %d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, want := range []string{"nova-cairn check:", "no input was named", "no lines were named", "nothing was checked"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("bare check missing %q:\n%s", want, stderr)
		}
	}

	code, stdout, stderr = runCode("", "check", "--staged", "--message", "--text", "03:36:42", "--file", note, "extra")
	if code != 2 || stdout != "" {
		t.Fatalf("bad invocation exited %d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if strings.Contains(stderr, "CHECK ") {
		t.Fatalf("a bad invocation scanned the lines:\n%s", stderr)
	}
	for _, want := range []string{
		"--staged and --message both name the input",
		"--text and --file both name the lines",
		"unexpected argument \"extra\"",
		"nothing was checked",
		"; run: nova-cairn help",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("refusal missing %q:\n%s", want, stderr)
		}
	}

	missing := filepath.Join(dir, "no-such")
	code, stdout, stderr = runCode("", "check", "--message", "--file", missing, "nope")
	if code != 2 || stdout != "" {
		t.Fatalf("missing file exited %d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, want := range []string{"no-such", "nothing was checked", "unexpected argument \"nope\"", "name a readable path or --file -"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("missing-file refusal missing %q:\n%s", want, stderr)
		}
	}

	code, _, stderr = runCode("", "check", "--store", dir, "--message", "--text", "03:36:42")
	if code != 2 || !strings.Contains(stderr, "store") || !strings.Contains(stderr, "nova-cairn check:") {
		t.Fatalf("check --store exited %d stderr=%q, want exit 2 naming the unknown flag", code, stderr)
	}
}

func TestCheckWarnsAndPassesWhenTheScanCannotFinish(t *testing.T) {
	t.Parallel()

	// The mask is first, so a scan that reported partial findings would
	// refuse. The overlong line stops the scan. The check warns and passes.
	text := "~19:0x\n" + strings.Repeat("a", maxLine+1) + "\n"
	code, stdout, stderr := runCode("", "check", "--message", "--text", text)
	if code != 0 {
		t.Fatalf("an unscannable line exited %d, want 0; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("a warning wrote to stdout: %q", stdout)
	}
	if strings.Contains(stderr, "CHECK FAIL") || strings.Contains(stderr, "unpastable-clock-mask") {
		t.Fatalf("a scan that did not finish refused a mask:\n%s", stderr)
	}
	for _, want := range []string{"CHECK WARN", "cause=cannot-scan", "state=passed", "next=proceed", "token"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("warning missing %q:\n%s", want, stderr)
		}
	}
}
