package redisfn

import (
	"fmt"
	"io/fs"
	"maps"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// The guarantees of Source, Locate and Digest over arbitrary libraries, not
// over examples: a fixed-seed generator makes sets of small Lua files whose
// lines hold what the loader reads (block words, comments, strings and long
// strings over several lines, registers) under names that sort in ways a
// person would not guess. Fixed seeds, so a failure names a library that
// fails again.

// propertyItems are the pieces a file is made of. Each is whole Lua on its
// own, of one line or more, so any sequence of them is a file the loader takes.
var propertyItems = []string{
	"local v = 1",
	"",
	"   ",
	"\t-- a comment with end and do and return in it",
	"local s = 'a string with end in it'",
	`local q = "until \" function"`,
	"local n = 0x1F + 1e5 + .5",
	"if true then local w = 2 end",
	"local function f() return 1 end",
	"--[[ a long comment on one line ]]",
	"local z = [[ a long string ]]",
	"for i = 1, 2 do local u = i end",
	"repeat local r = 1 until true",
	"while false do end",
	"do end",
	"local m = [[\nthe second line of a long string, with an end\n]]",
	"--[==[\n a long comment ]] that goes on\n\n]==]",
	"local c = 'a string\\\nover two lines'",
	"local function g()\n  return 2\nend",
	"local t = {\n  1,\n  2,\n}",
}

// propertyNames are file names under lua/: names that are prefixes of one
// another, that differ by case, and that hold what a line must not.
var propertyNames = []string{
	"a.lua", "b.lua", "B.lua", "ab.lua", "a_b.lua", "a-b.lua", "a.b.lua", "a b.lua",
	"0.lua", "00.lua", "10.lua", "9.lua", "_.lua", "z.lua", "Z.lua", "\u00e9.lua",
	"a\nb.lua", "a\tb.lua", "\u2028.lua", "a=b.lua", "-- end.lua", "]].lua",
}

// propertyLibrary is one arbitrary library: the texts of its files by name,
// and its prelude. Every text is made of lines; lines[name] are those of a
// file, and lines[""] those of the prelude.
type propertyLibrary struct {
	texts   map[string]string
	prelude string
	lines   map[string][]string
}

func (p propertyLibrary) library(fsys fstest.MapFS) Library {
	return Library{Name: "lib_p", Files: fsys, Glob: "lua/*.lua", Prelude: p.prelude}
}

func (p propertyLibrary) files() fstest.MapFS {
	fsys := fstest.MapFS{"readme.txt": {Data: []byte("not under the glob")}}
	for name, text := range p.texts {
		fsys["lua/"+name] = &fstest.MapFile{Data: []byte(text)}
	}
	return fsys
}

// propertyText is one arbitrary text and its lines, with or without a line
// break at its end. A text of a file is never empty of everything but white
// space: it holds a register of its own name.
func propertyText(r *rand.Rand, register string) (string, []string) {
	var items []string
	if register != "" {
		items = append(items, "redis.register_function('"+register+"', function(keys, args) return 1 end)")
	}
	for n := r.IntN(7); n > 0; n-- {
		items = append(items, propertyItems[r.IntN(len(propertyItems))])
	}
	r.Shuffle(len(items), func(i, j int) { items[i], items[j] = items[j], items[i] })
	text := strings.Join(items, "\n")
	if len(items) > 0 && r.IntN(2) == 0 {
		text += "\n"
	}
	return text, propertyLines(text)
}

// propertyLines are the lines of a text as an editor counts them: a line
// break ends a line, so the one at the text's end starts no line after it,
// and a text of nothing has no line.
func propertyLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func propertyCase(r *rand.Rand) propertyLibrary {
	p := propertyLibrary{texts: map[string]string{}, lines: map[string][]string{}}
	for n := 1 + r.IntN(5); n > 0; n-- {
		name := propertyNames[r.IntN(len(propertyNames))]
		if _, ok := p.texts[name]; ok {
			continue
		}
		p.texts[name], p.lines["lua/"+name] = propertyText(r, fmt.Sprintf("f%d", len(p.texts)))
	}
	if r.IntN(3) > 0 {
		p.prelude, p.lines[""] = propertyText(r, "")
	}
	return p
}

func propertyCases(t *testing.T, seed uint64, check func(t *testing.T, r *rand.Rand, p propertyLibrary)) {
	t.Helper()
	r := rand.New(rand.NewPCG(seed, 0x6e6f7661))
	for i := 0; i < 3000; i++ {
		p := propertyCase(r)
		check(t, r, p)
		if t.Failed() {
			t.Fatalf("seed %d case %d: prelude %q files %q", seed, i, p.prelude, p.texts)
		}
	}
}

// The oracle is the form of the source as the package's doc states it, and it
// shares nothing with the loader: it sorts the names itself, and it walks the
// source's lines from the top and says what each must be.
func TestPropertyEveryLineOfTheSourceMapsToItsFileAndLine(t *testing.T) {
	t.Parallel()
	propertyCases(t, 1, func(t *testing.T, r *rand.Rand, p propertyLibrary) {
		lib := p.library(p.files())
		source, err := lib.Source()
		if err != nil {
			t.Errorf("Source: %v", err)
			return
		}
		if !strings.HasSuffix(source, "\n") {
			t.Errorf("the source does not end with a line break")
			return
		}
		got := strings.Split(strings.TrimSuffix(source, "\n"), "\n")

		var names []string
		for name := range p.texts {
			names = append(names, "lua/"+name)
		}
		slices.Sort(names)

		// Of a header and of an end the oracle states the form, below; of
		// every other line it states the text.
		type expected struct {
			origin Origin
			text   string
			formed bool
		}
		want := []expected{{origin: Origin{}, text: "#!lua name=lib_p"}}
		for i, line := range p.lines[""] {
			want = append(want, expected{origin: Origin{Line: i + 1, Prelude: true}, text: line})
		}
		for _, name := range names {
			header := len(want) + 1
			want = append(want, expected{origin: Origin{File: name}, formed: true}, expected{origin: Origin{File: name}, text: "do"})
			for i, line := range p.lines[name] {
				want = append(want, expected{origin: Origin{File: name, Line: i + 1}, text: line})
			}
			want = append(want, expected{origin: Origin{File: name}, formed: true})
			// The header is one line whatever the name holds, and says where
			// the file's first line is; so is the end.
			if header > len(got) || !strings.HasPrefix(got[header-1], "-- lua/") ||
				!strings.HasSuffix(got[header-1], fmt.Sprintf(": its line 1 is line %d of this library", header+2)) {
				t.Errorf("line %d is not the header of %q, which starts at line %d:\n%s", header, name, header+2, source)
				return
			}
			if end := len(want); end > len(got) || !strings.HasPrefix(got[end-1], "end -- lua/") {
				t.Errorf("line %d is not the end of %q:\n%s", end, name, source)
				return
			}
		}
		if len(got) != len(want) {
			t.Errorf("the source has %d lines, want %d:\n%s", len(got), len(want), source)
			return
		}
		built, err := lib.build()
		if err != nil {
			t.Errorf("build: %v", err)
			return
		}
		for i, w := range want {
			origin, ok := built.locate(i + 1)
			if !ok || origin != w.origin {
				t.Errorf("line %d comes from %+v (%v), want %+v", i+1, origin, ok, w.origin)
			}
			if !w.formed && got[i] != w.text {
				t.Errorf("line %d is %q, want %q, which is %+v", i+1, got[i], w.text, w.origin)
			}
		}
		// Locate is the same mapping, asked one line at a time: a line of the
		// case's own choosing, the first, the last, and the two outside.
		for _, line := range []int{1 + r.IntN(len(want)), 1, len(want)} {
			if origin, err := lib.Locate(line); err != nil || origin != want[line-1].origin {
				t.Errorf("Locate(%d) = %+v %v, want %+v", line, origin, err, want[line-1].origin)
			}
		}
		for _, line := range []int{0, len(want) + 1} {
			if _, err := lib.Locate(line); err == nil {
				t.Errorf("Locate(%d) found a line outside the source's %d", line, len(want))
			}
		}
	})
}

// shuffled lists every directory in an order of the case's own choosing.
type shuffled struct {
	inner fstest.MapFS
	r     *rand.Rand
}

func (s shuffled) Open(name string) (fs.File, error) { return s.inner.Open(name) }

func (s shuffled) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := s.inner.ReadDir(name)
	s.r.Shuffle(len(entries), func(i, j int) { entries[i], entries[j] = entries[j], entries[i] })
	return entries, err
}

func TestPropertyDigestIsAFunctionOfTheContentOnly(t *testing.T) {
	t.Parallel()
	seen := map[string]string{}
	propertyCases(t, 2, func(t *testing.T, r *rand.Rand, p propertyLibrary) {
		lib := p.library(p.files())
		source, err := lib.Source()
		if err != nil {
			t.Errorf("Source: %v", err)
			return
		}
		digest, err := lib.Digest()
		if err != nil || digest != DigestOf(source) || len(digest) != DigestLength || strings.Trim(digest, "0123456789abcdef") != "" {
			t.Errorf("Digest = %q %v, want the %d hex digits of the source's", digest, err, DigestLength)
		}
		// The same content in another file system, listed in another order,
		// with files beside it that the glob does not match.
		again := p.files()
		again["lua/notes.md"] = &fstest.MapFile{Data: []byte("not under the glob")}
		again["luas/a.lua"] = &fstest.MapFile{Data: []byte("not under the glob")}
		other := p.library(nil)
		other.Files = shuffled{again, r}
		other.Remedy, other.Bound = "another remedy", 1
		if got, err := other.Source(); err != nil || got != source {
			t.Errorf("the same content gives another source (%v):\n%s\nwant:\n%s", err, got, source)
		}
		// One digest, one source: over every case of this test no two
		// sources share a digest.
		if was, ok := seen[digest]; ok && was != source {
			t.Errorf("two sources have the digest %s:\n%s\nand:\n%s", digest, was, source)
		}
		seen[digest] = source
		// Any change of the content changes the source: one byte of one file.
		changed := p.files()
		name := "lua/" + slices.Sorted(maps.Keys(p.texts))[r.IntN(len(p.texts))]
		data := slices.Clone(changed[name].Data)
		at := r.IntN(len(data) + 1)
		data = slices.Insert(data, at, ' ')
		changed[name] = &fstest.MapFile{Data: data}
		mutant, err := p.library(changed).Source()
		if err != nil {
			// A space put into a word or a sign can make a text the loader
			// refuses ("en d"); it cannot make one it takes for the same.
			return
		}
		if mutant == source || DigestOf(mutant) == digest {
			t.Errorf("a space at byte %d of %q changes nothing: %s", at, name, digest)
		}
	})
}
