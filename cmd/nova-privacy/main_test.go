package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	rareWords   = "zarquon flibberty wumpus"
	commonWords = "consider morning evening garden letter"
)

const ideasFixture = `## An ordinary idea about the garden
consider the morning and the evening letter

## The zarquon engine (private)
flibberty wumpus consider morning evening garden letter

## Another ordinary idea
nothing much here at all
`

const configFixture = `source private/ideas.md
source private/upkeep.md
background flat *.md journal
refuse home-path /home/[a-z]+(/[A-Za-z0-9._-]+)*
warn tool-name \bacme-[a-z]+\b
`

// tree is a throwaway corpus: two sources, eight background pages, and the
// configuration at its root.
type tree struct{ root string }

func (tr tree) path(rel string) string { return filepath.Join(tr.root, filepath.FromSlash(rel)) }

func (tr tree) write(t *testing.T, rel, body string) string {
	t.Helper()
	p := tr.path(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func newTree(t *testing.T) tree {
	t.Helper()
	tr := tree{root: t.TempDir()}
	tr.write(t, "private/ideas.md", ideasFixture)
	tr.write(t, "private/upkeep.md", "- rotate the logs before they get silly\n")
	for i := 0; i < 8; i++ {
		tr.write(t, fmt.Sprintf("journal/page-%03d.md", i), "Ordinary prose. "+commonWords+" and some other words about the work.\n")
	}
	tr.write(t, ".nova-privacy", configFixture)
	return tr
}

func runTool(stdin string, args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, strings.NewReader(stdin), &out, &errb)
	return code, out.String(), errb.String()
}

func screenStdin(tr tree, payload string, extra ...string) (int, string, string) {
	args := append([]string{"screen", "--root", tr.root}, extra...)
	return runTool(payload, append(args, "-")...)
}

func TestABareCommandRefusesInOneLineAndNamesTheDoor(t *testing.T) {
	t.Parallel()
	code, out, errOut := runTool("")
	if code != exitCouldNotRun || out != "" {
		t.Fatalf("exit %d stdout %q", code, out)
	}
	if strings.Count(errOut, "\n") != 1 || !strings.Contains(errOut, "run: nova-privacy help") {
		t.Errorf("stderr %q", errOut)
	}
}

func TestAnUnknownVerbIsNamed(t *testing.T) {
	t.Parallel()
	code, _, errOut := runTool("", "scan", "x.md")
	if code != exitCouldNotRun || !strings.Contains(errOut, `unknown verb "scan"`) || !strings.Contains(errOut, "screen, corpus") {
		t.Errorf("exit %d stderr %q", code, errOut)
	}
}

// An unknown option is refused, never screened as a file name.
func TestAnUnknownOptionIsRefusedNotScreened(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	code, out, errOut := runTool("", "screen", "--root", tr.root, "--txt", "hello")
	if code != exitCouldNotRun || out != "" || !strings.Contains(errOut, "-txt") || !strings.Contains(errOut, "run: nova-privacy screen -h") {
		t.Errorf("exit %d stdout %q stderr %q", code, out, errOut)
	}
}

func TestOnePayloadAtATime(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	code, _, errOut := runTool("", "screen", "--root", tr.root, "a.md", "b.md")
	if code != exitCouldNotRun || !strings.Contains(errOut, "one payload at a time") {
		t.Errorf("exit %d stderr %q", code, errOut)
	}
	code, _, errOut = runTool("", "screen", "--root", tr.root)
	if code != exitCouldNotRun || !strings.Contains(errOut, "no payload named") {
		t.Errorf("exit %d stderr %q", code, errOut)
	}
}

func TestAnUnnamedCorpusIsRefused(t *testing.T) {
	t.Parallel()
	code, out, errOut := runTool("some outgoing text", "screen", "-")
	if code != exitCouldNotRun || out != "" {
		t.Fatalf("exit %d stdout %q", code, out)
	}
	for _, want := range []string{"--root <dir>", "--config <file>", "--source <file>", "refusing to guess"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr %q lacks %q", errOut, want)
		}
	}
}

func TestRootAndConfigTogetherAreRefused(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	code, _, errOut := runTool("text", "screen", "--root", tr.root, "--config", tr.path(".nova-privacy"), "-")
	if code != exitCouldNotRun || !strings.Contains(errOut, "give one") {
		t.Errorf("exit %d stderr %q", code, errOut)
	}
}

func TestAMissingConfigurationIsRefusedWithItsPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	code, _, errOut := runTool("text", "screen", "--root", dir, "-")
	if code != exitCouldNotRun || !strings.Contains(errOut, filepath.Join(dir, ".nova-privacy")) || !strings.Contains(errOut, "--source") {
		t.Errorf("exit %d stderr %q", code, errOut)
	}
	code, _, errOut = runTool("text", "screen", "--config", filepath.Join(dir, "gone.conf"), "-")
	if code != exitCouldNotRun || !strings.Contains(errOut, "gone.conf") {
		t.Errorf("exit %d stderr %q", code, errOut)
	}
}

func TestSourcesByFlagNeedNoConfiguration(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	code, out, errOut := runTool("thinking about the "+rareWords+" again", "screen",
		"--source", tr.path("private/ideas.md"), "--background-flat", tr.path("journal"), "-")
	if code != exitFlagged || !strings.Contains(errOut, "SCREEN FLAGGED") || !strings.Contains(errOut, "config=-") {
		t.Errorf("exit %d stdout %q stderr %q", code, out, errOut)
	}
}

// A misspelt --root beside --source is refused, never read as "no
// configuration": the configuration's refuse shape would be lost.
func TestAMisspeltRootIsRefusedEvenWithSources(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	code, out, errOut := runTool("the evidence is at /home/ada/work/logs", "screen", "--root", tr.path("typo"),
		"--source", tr.path("private/ideas.md"), "-")
	if code != exitCouldNotRun || out != "" || !strings.Contains(errOut, "no configuration at") {
		t.Errorf("exit %d stdout %q stderr %q", code, out, errOut)
	}
}

func TestAConfigurationWithNoSourceIsRefused(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	tr.write(t, ".nova-privacy", "background flat *.md journal\n")
	code, _, errOut := runTool("text", "screen", "--root", tr.root, "-")
	if code != exitCouldNotRun || !strings.Contains(errOut, "declares no source") || !strings.Contains(errOut, "run: nova-privacy corpus --root") {
		t.Errorf("exit %d stderr %q", code, errOut)
	}
}

// One run names every malformed line.
func TestAMalformedConfigurationNamesEveryBadLine(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	tr.write(t, ".nova-privacy", "source private/ideas.md\nsorce x\nwarn broken (unclosed\n")
	code, out, errOut := runTool("text", "screen", "--root", tr.root, "-")
	if code != exitCouldNotRun || out != "" {
		t.Fatalf("exit %d stdout %q", code, out)
	}
	if !strings.Contains(errOut, "line 2:") || !strings.Contains(errOut, "line 3:") {
		t.Errorf("stderr %q", errOut)
	}
}

func TestAnUnreadablePayloadIsRefusedAndNamed(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	missing := tr.path("not-here.md")
	code, out, errOut := runTool("", "screen", "--root", tr.root, missing)
	if code != exitCouldNotRun || out != "" || !strings.Contains(errOut, missing) || !strings.Contains(errOut, "nothing was screened") {
		t.Errorf("exit %d stdout %q stderr %q", code, out, errOut)
	}
}

// An empty payload cannot be told from content that never arrived.
func TestAnEmptyPayloadIsNotCleared(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	for _, in := range []string{"", "   \n\t \n"} {
		code, out, errOut := screenStdin(tr, in)
		if code != exitCouldNotRun || strings.Contains(out, "UNPROVEN-CLEAN") || !strings.Contains(errOut, "empty") {
			t.Errorf("payload %q: exit %d stdout %q stderr %q", in, code, out, errOut)
		}
	}
}

func TestAPayloadOverItsBoundIsRefused(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	big := tr.write(t, "big.md", strings.Repeat("a", 4<<20+1))
	code, _, errOut := runTool("", "screen", "--root", tr.root, big)
	if code != exitCouldNotRun || !strings.Contains(errOut, "MaxPayloadBytes") {
		t.Errorf("exit %d stderr %q", code, errOut)
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("pipe went away") }

func TestABrokenStdinIsRefused(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	var out, errb bytes.Buffer
	code := run([]string{"screen", "--root", tr.root, "-"}, brokenReader{}, &out, &errb)
	if code != exitCouldNotRun || !strings.Contains(errb.String(), "pipe went away") {
		t.Errorf("exit %d stderr %q", code, errb.String())
	}
}

// The exit contract: only 0 clears, and each could-not-verify outcome says
// that nothing was verified.
func TestExitContract(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		setup   func(t *testing.T, tr tree)
		payload string
		code    int
		token   string
	}{
		{"0 is the only clearance", nil, "The tests pass and the build is green.", exitClean, "SCREEN UNPROVEN-CLEAN"},
		{"1 is FLAGGED", nil, "thinking about the " + rareWords + " again", exitFlagged, "SCREEN FLAGGED"},
		{"3 when a declared source is gone", func(t *testing.T, tr tree) {
			if err := os.Remove(tr.path("private/ideas.md")); err != nil {
				t.Fatal(err)
			}
		}, "harmless outgoing text", exitUnverified, "SCREEN CORPUS-UNREADABLE"},
		{"3 when nothing is marked private", func(t *testing.T, tr tree) {
			tr.write(t, "private/ideas.md", "## An ordinary idea\nnothing marked here\n")
		}, "harmless outgoing text", exitUnverified, "SCREEN NO-PRIVATE-CORPUS"},
		{"3 when no private entry could ever fire", func(t *testing.T, tr tree) {
			tr.write(t, "private/ideas.md", "## A generic secret (private)\n"+commonWords+"\n")
		}, "harmless outgoing text", exitUnverified, "SCREEN NOTHING-CAN-EVER-FIRE"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			tr := newTree(t)
			if c.setup != nil {
				c.setup(t, tr)
			}
			code, out, errOut := screenStdin(tr, c.payload)
			if code != c.code || !strings.Contains(out+errOut, c.token) {
				t.Fatalf("exit %d, want %d with %q\nstdout %s\nstderr %s", code, c.code, c.token, out, errOut)
			}
			if code != exitClean && strings.Contains(out+errOut, "UNPROVEN-CLEAN") {
				t.Error("only a verified 0 prints UNPROVEN-CLEAN")
			}
			if code == exitUnverified && !strings.Contains(errOut, "nothing was verified") {
				t.Errorf("a run that verified nothing says so: %s", errOut)
			}
		})
	}
}

// Four different facts share exit 3 and never share a sentence.
func TestTheCouldNotVerifyOutcomesAreSpelledApart(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for i, mutate := range []func(t *testing.T, tr tree){
		func(t *testing.T, tr tree) { tr.write(t, ".nova-privacy", configFixture+"source private/gone.md\n") },
		func(t *testing.T, tr tree) { tr.write(t, "private/ideas.md", "## Ordinary\nnothing marked\n") },
		func(t *testing.T, tr tree) {
			tr.write(t, "private/ideas.md", "## Generic (private)\n"+commonWords+"\n")
		},
		func(t *testing.T, tr tree) {},
	} {
		tr := newTree(t)
		mutate(t, tr)
		payload := "harmless outgoing text"
		if i == 3 {
			payload = "... --- ..."
		}
		code, _, errOut := screenStdin(tr, payload)
		if code != exitUnverified {
			t.Fatalf("exit %d: %s", code, errOut)
		}
		seen[strings.ReplaceAll(errOut, tr.root, "")] = true
	}
	if len(seen) != 4 {
		t.Errorf("four facts printed %d distinct outputs", len(seen))
	}
}

// The remedy for an unreadable source names the configuration and the next
// command, with the caller's own inputs.
func TestAnUnreadableSourceNamesTheConfigurationAndTheNextCommand(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	tr.write(t, ".nova-privacy", configFixture+"source private/gone.md\n")
	_, _, errOut := screenStdin(tr, "harmless outgoing text")
	cfg := tr.root + string(filepath.Separator) + ".nova-privacy"
	for _, want := range []string{"private/gone.md", "remove it from " + cfg + " or restore the file", "then run: nova-privacy corpus --root " + tr.root} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr lacks %q:\n%s", want, errOut)
		}
	}
}

func TestTheVerdictLineNeverSaysCleanOnItsOwn(t *testing.T) {
	t.Parallel()
	code, out, _ := screenStdin(newTree(t), "an ordinary paragraph about ordinary things")
	first := strings.SplitN(out, "\n", 2)[0]
	if code != exitClean || !strings.HasPrefix(first, "SCREEN UNPROVEN-CLEAN ") {
		t.Errorf("exit %d first line %q", code, first)
	}
	if !strings.Contains(out, "SCREEN NOTE nothing was proven") {
		t.Errorf("stdout %q", out)
	}
}

// A flag is a reading assignment with its evidence: the source, the entry's
// title and the shared terms.
func TestAFlagCarriesItsEvidence(t *testing.T) {
	t.Parallel()
	code, _, errOut := screenStdin(newTree(t), "thinking about the "+rareWords+" again")
	if code != exitFlagged {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"SCREEN FLAG source=private/ideas.md entry=1 shared=3 terms=flibberty,wumpus,zarquon title=The zarquon engine (private)", "SCREEN REMEDY a mind reads this", "after an edit, run: nova-privacy screen --root"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr lacks %q:\n%s", want, errOut)
		}
	}
}

func TestOrdinaryProseDoesNotFire(t *testing.T) {
	t.Parallel()
	code, out, errOut := screenStdin(newTree(t), "I sat in the "+commonWords+" and thought about it for a while.")
	if code != exitClean || strings.Contains(errOut, "FLAG") {
		t.Errorf("exit %d stdout %q stderr %q", code, out, errOut)
	}
}

func TestFlagOutputIsEscapedBeforeItReachesATerminal(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	tr.write(t, "private/ideas.md", "## The zarquon engine \x1b[2Jwiped\x07 (private)\nflibberty wumpus\n")
	code, _, errOut := screenStdin(tr, "thinking about the "+rareWords+" again")
	if code != exitFlagged {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if strings.ContainsAny(errOut, "\x1b\x07") || !strings.Contains(errOut, "wiped") {
		t.Errorf("stderr %q", errOut)
	}
}

func TestAStructureRefusalFlagsWithItsSpecimen(t *testing.T) {
	t.Parallel()
	code, _, errOut := screenStdin(newTree(t), "the evidence is at /home/ada/work/logs/latest\n")
	if code != exitFlagged || !strings.Contains(errOut, "SCREEN STRUCTURE class=home-path specimen=/home/ada/work/logs/latest") ||
		!strings.Contains(errOut, "general terms") {
		t.Errorf("exit %d stderr %q", code, errOut)
	}
}

func TestAWarnShapeClearsAndIsSpoken(t *testing.T) {
	t.Parallel()
	code, out, errOut := screenStdin(newTree(t), "the acme-sync wrapper runs every hour\n")
	if code != exitClean || !strings.Contains(out, "UNPROVEN-CLEAN") || !strings.Contains(errOut, `SCREEN WARN structure: tool-name "acme-sync"`) {
		t.Errorf("exit %d stdout %q stderr %q", code, out, errOut)
	}
}

func TestTheFlagListIsCappedWithACount(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	var b strings.Builder
	for i := 0; i < 3; i++ {
		fmt.Fprintf(&b, "## Copy %d (private)\n%s\n", i, rareWords)
	}
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&b, "## Ordinary %d\nnothing here\n", i)
	}
	tr.write(t, "private/ideas.md", b.String())
	code, _, errOut := screenStdin(tr, rareWords, "--max", "1")
	if code != exitFlagged || strings.Count(errOut, "SCREEN FLAG ") != 1 || !strings.Contains(errOut, "SCREEN MORE kind=flag shown=1 total=3") || !strings.Contains(errOut, "flags=3") {
		t.Errorf("exit %d stderr %s", code, errOut)
	}
}

func TestScreenJSON(t *testing.T) {
	t.Parallel()
	code, out, _ := screenStdin(newTree(t), "thinking about the "+rareWords+" again", "--json")
	if code != exitFlagged {
		t.Fatalf("exit %d", code)
	}
	var rep report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, out)
	}
	if rep.Outcome != "FLAGGED" || rep.Exit != exitFlagged || rep.Cleared || len(rep.Flags) != 1 || strings.Join(rep.Flags[0].Shared, ",") != "flibberty,wumpus,zarquon" {
		t.Errorf("report %+v", rep)
	}
	if len(rep.Sources) != 2 || len(rep.Roots) != 1 || rep.Roots[0].Read != 8 {
		t.Errorf("sources %+v roots %+v", rep.Sources, rep.Roots)
	}
}

func TestCorpusPrintsWhatAScreenWouldLoad(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	code, out, errOut := runTool("", "corpus", "--root", tr.root)
	if code != exitClean {
		t.Fatalf("exit %d stderr %s", code, errOut)
	}
	for _, want := range []string{
		"CORPUS SOURCE path=private/ideas.md entries=3 private=1",
		"CORPUS SOURCE path=private/upkeep.md entries=1 private=0",
		"CORPUS BACKGROUND root=journal mode=flat pattern=*.md found=8 shared=0 read=8",
		"CORPUS SAMPLE found=8 read=8 bytes=",
		"rule=all max-docs=20000 max-bytes=67108864",
		"CORPUS OK sources=2 entries=4 private=1 checkable=1 background=8",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
}

func TestCorpusSaysWhenAScreenCouldNotVerify(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	tr.write(t, ".nova-privacy", configFixture+"source private/gone.md\nbackground flat *.md absent\n")
	code, _, errOut := runTool("", "corpus", "--root", tr.root)
	for _, want := range []string{"CORPUS SOURCE path=private/gone.md unreadable=", "CORPUS WARN background: absent cannot be listed", "CORPUS CORPUS-UNREADABLE", "or restore the file"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr lacks %q:\n%s", want, errOut)
		}
	}
	if code != exitUnverified {
		t.Errorf("exit %d, want %d", code, exitUnverified)
	}
}

func TestCorpusJSON(t *testing.T) {
	t.Parallel()
	code, out, _ := runTool("", "corpus", "--root", newTree(t).root, "--json")
	var rep report
	if err := json.Unmarshal([]byte(out), &rep); err != nil || code != exitClean {
		t.Fatalf("exit %d err %v\n%s", code, err, out)
	}
	if rep.Verb != "corpus" || rep.Outcome != "UNPROVEN-CLEAN" || rep.Private != 1 || rep.Checkable != 1 || rep.Background != 8 {
		t.Errorf("report %+v", rep)
	}
}

func TestCorpusTakesNoPositionalArguments(t *testing.T) {
	t.Parallel()
	code, _, errOut := runTool("", "corpus", "--root", newTree(t).root, "extra")
	if code != exitCouldNotRun || !strings.Contains(errOut, `"extra"`) {
		t.Errorf("exit %d stderr %q", code, errOut)
	}
}

func TestFlagsReplaceTheConfiguration(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	tr.write(t, "other/secret.md", "## Harbour plans [hush]\nbellrope tidewater saltmarsh quayside\n")
	code, _, errOut := runTool("a bellrope over the tidewater to the saltmarsh", "screen", "--root", tr.root,
		"--source", tr.path("other/secret.md"), "--marker", "[hush]", "--background-flat", tr.path("journal"), "-")
	if code != exitFlagged || !strings.Contains(errOut, "title=Harbour plans [hush]") {
		t.Errorf("exit %d stderr %s", code, errOut)
	}
}

func TestVersionPrintsOneLine(t *testing.T) {
	t.Parallel()
	code, out, _ := runTool("", "version")
	if code != exitClean || !strings.HasPrefix(out, "nova-privacy ") || strings.Count(out, "\n") != 1 {
		t.Errorf("exit %d stdout %q", code, out)
	}
	if code, _, _ := runTool("", "version", "extra"); code != exitCouldNotRun {
		t.Errorf("version with an argument exits %d", code)
	}
}

func TestHelpIsNotARefusal(t *testing.T) {
	t.Parallel()
	code, out, errOut := runTool("", "help")
	if code != exitClean || errOut != "" || !strings.Contains(out, "exit codes:") || !strings.Contains(out, "\nexample:\n") {
		t.Errorf("exit %d stderr %q", code, errOut)
	}
}

func utf16LE(s string, bom bool) string {
	var b []byte
	if bom {
		b = append(b, 0xFF, 0xFE)
	}
	for _, r := range s {
		b = append(b, byte(r), byte(r>>8))
	}
	return string(b)
}

// A payload the tool cannot read as text never clears: UTF-16 with a mark is
// decoded and screened, and bytes that are not text are refused.
func TestAPayloadThatIsNotTextNeverClears(t *testing.T) {
	t.Parallel()
	leak := "thinking about the " + rareWords + " again"
	for name, c := range map[string]struct {
		payload string
		code    int
		token   string
	}{
		"utf-16 with a mark is screened": {utf16LE(leak, true), exitFlagged, "SCREEN FLAGGED"},
		"utf-16 without a mark":          {utf16LE(leak, false), exitCouldNotRun, "NUL"},
		"invalid utf-8":                  {leak + " \xff\xfe", exitCouldNotRun, "not valid UTF-8"},
		"zero-width spaces only":         {"\u200b\u200b\u200b\u200b", exitUnverified, "SCREEN PAYLOAD-HAS-NO-WORDS"},
		"punctuation only":               {"!!! ... --- ???\n", exitUnverified, "SCREEN PAYLOAD-HAS-NO-WORDS"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			code, out, errOut := screenStdin(newTree(t), c.payload)
			if code != c.code || !strings.Contains(errOut, c.token) || strings.Contains(out, "UNPROVEN-CLEAN") {
				t.Errorf("exit %d, want %d with %q\nstdout %q\nstderr %q", code, c.code, c.token, out, errOut)
			}
		})
	}
}

func TestOneSourceDeclaredTwiceIsRefused(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	tr.write(t, ".nova-privacy", configFixture+"source private/./ideas.md\n")
	code, out, errOut := screenStdin(tr, "harmless outgoing text")
	if code != exitCouldNotRun || out != "" || !strings.Contains(errOut, "same file") || !strings.Contains(errOut, "private/./ideas.md") {
		t.Errorf("exit %d stdout %q stderr %q", code, out, errOut)
	}
}

func TestASourceWithNoPrivateEntryWarnsInBothVerbs(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	_, _, errOut := screenStdin(tr, "harmless outgoing text")
	if !strings.Contains(errOut, "SCREEN WARN source: private/upkeep.md has 1 entry and none is marked (private)") {
		t.Errorf("screen stderr %q", errOut)
	}
	_, _, errOut = runTool("", "corpus", "--root", tr.root)
	if !strings.Contains(errOut, "CORPUS WARN source: private/upkeep.md has 1 entry and none is marked (private)") {
		t.Errorf("corpus stderr %q", errOut)
	}
}

func TestAnEmptyExtraSourceIsUnreadable(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	tr.write(t, "private/extra.md", "")
	tr.write(t, ".nova-privacy", configFixture+"source private/extra.md\n")
	code, _, errOut := screenStdin(tr, "harmless outgoing text")
	if code != exitUnverified || !strings.Contains(errOut, "SCREEN CORPUS-UNREADABLE declared source private/extra.md") {
		t.Errorf("exit %d stderr %q", code, errOut)
	}
}

// A screen says how many background documents were found and read, and by
// what rule.
func TestAScreenNamesItsSample(t *testing.T) {
	t.Parallel()
	code, out, _ := screenStdin(newTree(t), "an ordinary paragraph about ordinary things")
	if code != exitClean || !strings.Contains(out, "background=8 found=8 sample=all ") {
		t.Errorf("exit %d stdout %q", code, out)
	}
	code, _, errOut := screenStdin(newTree(t), "an ordinary paragraph about ordinary things", "--max-docs", "3")
	if code != exitClean || !strings.Contains(errOut, "found 8") || !strings.Contains(errOut, "lowest hash") {
		t.Errorf("exit %d stderr %q", code, errOut)
	}
}

// With --json, every refusal prints one JSON object on stdout as well as its
// line on stderr: a caller that parses stdout is never left with nothing.
func TestJSONRefusalsPrintAnObject(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	for name, c := range map[string]struct {
		stdin string
		args  []string
	}{
		"empty payload":      {"", []string{"screen", "--json", "--root", tr.root, "-"}},
		"not text":           {"a\x00b", []string{"screen", "--root", tr.root, "--json", "-"}},
		"unknown flag":       {"x", []string{"screen", "--json", "--txt", "-"}},
		"no payload named":   {"", []string{"screen", "--json", "--root", tr.root}},
		"missing config":     {"x", []string{"screen", "--json", "--root", tr.path("typo"), "-"}},
		"root and config":    {"x", []string{"corpus", "--json", "--root", tr.root, "--config", tr.path(".nova-privacy")}},
		"corpus positional":  {"", []string{"corpus", "--json", "--root", tr.root, "extra"}},
		"negative max":       {"x", []string{"screen", "--json", "--max", "-1", "--root", tr.root, "-"}},
		"json=true spelling": {"", []string{"screen", "--json=true", "--root", tr.root, "-"}},
	} {
		code, out, errOut := runTool(c.stdin, c.args...)
		if code != exitCouldNotRun || !strings.Contains(errOut, "run: ") {
			t.Errorf("%s: exit %d stderr %q", name, code, errOut)
			continue
		}
		var rep report
		if err := json.Unmarshal([]byte(out), &rep); err != nil {
			t.Errorf("%s: stdout is not one JSON object: %v %q", name, err, out)
			continue
		}
		if rep.Exit != exitCouldNotRun || rep.Cleared || rep.Outcome != "COULD-NOT-RUN" || rep.Reason == "" || rep.Remedy == "" || rep.Verb != c.args[0] {
			t.Errorf("%s: report %+v", name, rep)
		}
	}
	if _, out, _ := runTool("", "screen", "--root", tr.root, "-"); out != "" {
		t.Errorf("without --json a refusal prints nothing on stdout: %q", out)
	}
}
