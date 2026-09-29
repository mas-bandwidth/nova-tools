package privacy_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/privacy"
)

// Two vocabularies live in the private entries. The rare words are invented,
// so no background document holds them. The common words are planted in
// forty background documents, so they are ordinary here. Inside the private
// sources both sets are rare; only the background model tells them apart.
const (
	rareWords   = "zarquon flibberty wumpus"
	commonWords = "consider morning evening garden letter"
)

// headingsFixture carries no preamble of its own: it follows laterFixture,
// and a rule sentence under its last heading would become a private entry.
const headingsFixture = `## An ordinary idea about the garden
consider the morning and the evening letter

## The zarquon engine (private)
flibberty wumpus consider morning evening garden letter

## Another ordinary idea
nothing much here at all

## A third ordinary idea
still nothing much here

## A fourth ordinary idea
words about other words
`

const laterFixture = "`(private)` entries are never quoted, paraphrased, or used\n" +
	"as an example.\n" +
	"\n" +
	"- [2026-01-05] **(private)** the snorkelwick project — bramblethorn and thistledown\n" +
	"- [2026-01-05] an ordinary parked idea about ordinary parked things\n"

const upkeepFixture = `- rotate the logs before they get silly
- check the tests still run on the bench
`

type fixture struct {
	root string
	spec privacy.Spec
}

func (f *fixture) path(rel string) string { return filepath.Join(f.root, filepath.FromSlash(rel)) }

func (f *fixture) write(t *testing.T, rel, body string) {
	t.Helper()
	p := f.path(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fixtureConfig is the configuration every fixture tree carries: two sources,
// the whole tree as a recursive root and the journal as a flat one.
const fixtureConfig = `# the fixture corpus
source private/later.md
source private/upkeep.md
background recursive *.md .
background flat *.md journal
`

// newFixture writes the sources and, when background is true, forty
// documents of ordinary prose. The journal directory always exists, so an
// empty background is "no documents", never "cannot be listed".
func newFixture(t *testing.T, background bool) *fixture {
	t.Helper()
	f := &fixture{root: t.TempDir()}
	f.write(t, "private/later.md", laterFixture+"\n"+headingsFixture)
	f.write(t, "private/upkeep.md", upkeepFixture)
	if background {
		for i := 0; i < 40; i++ {
			f.write(t, fmt.Sprintf("journal/page-%03d.md", i), "Ordinary prose. "+commonWords+" and some other words about the work.\n")
		}
	} else if err := os.MkdirAll(f.path("journal"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.write(t, privacy.ConfigName, fixtureConfig)
	file, err := privacy.ParseConfig(fixtureConfig)
	if err != nil {
		t.Fatal(err)
	}
	f.spec, err = file.Resolve(f.root, privacy.ConfigName)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// withConfig returns the fixture's spec with the given configuration lines
// appended, parsed as one file.
func (f *fixture) withConfig(t *testing.T, extra string) privacy.Spec {
	t.Helper()
	file, err := privacy.ParseConfig(fixtureConfig + extra)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := file.Resolve(f.root, privacy.ConfigName)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}
