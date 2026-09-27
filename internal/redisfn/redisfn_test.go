package redisfn

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"go/parser"
	gotoken "go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

// The package stands on the standard library, on go-redis and on
// internal/oneline, and knows no other package of this repository: a tool's
// library is described to it, never imported by it.
func TestThePackageImportsTheStandardLibraryGoRedisAndOnelineOnly(t *testing.T) {
	t.Parallel()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	read := 0
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(gotoken.NewFileSet(), name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		read++
		for _, imp := range file.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			first, _, _ := strings.Cut(path, "/")
			standard := !strings.Contains(first, ".")
			if !standard && path != "github.com/redis/go-redis/v9" && path != "github.com/mas-bandwidth/nova-tools/internal/oneline" {
				t.Errorf("%s imports %s; the package may import the standard library, go-redis and internal/oneline", name, path)
			}
		}
	}
	if read < 3 {
		t.Fatalf("%d source files of the package were read, of %q; the walk is broken, not the package", read, names)
	}
}

// tree is a file system that holds the texts under their names.
func tree(texts map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for name, text := range texts {
		fsys[name] = &fstest.MapFile{Data: []byte(text)}
	}
	return fsys
}

// fn is one line of Lua that registers a function which returns its own name.
func fn(name string) string {
	return "redis.register_function('" + name + "', function(keys, args) return '" + name + "' end)\n"
}

// two is the library most tests start from: two files under the glob, one
// without a line break at its end, a prelude without one, and two files the
// glob does not match.
func two() Library {
	return Library{
		Name: "lib_one",
		Files: tree(map[string]string{
			"lua/b.lua":     "local b = 1\n" + fn("fb"),
			"lua/a.lua":     strings.TrimSuffix(fn("fa"), "\n"),
			"lua/notes.txt": "not Lua, and not matched",
			"other/c.lua":   fn("fc"),
		}),
		Glob:    "lua/*.lua",
		Prelude: "local NS = {}",
	}
}

const twoSource = `#!lua name=lib_one
local NS = {}
-- lua/a.lua: its line 1 is line 5 of this library
do
redis.register_function('fa', function(keys, args) return 'fa' end)
end -- lua/a.lua
-- lua/b.lua: its line 1 is line 9 of this library
do
local b = 1
redis.register_function('fb', function(keys, args) return 'fb' end)
end -- lua/b.lua
`

// twoDigest is DigestOf(twoSource), written down: the digest of a library is
// the same on every machine and in every run, and a change of the source's
// form changes the digest of every library that is deployed.
const twoDigest = "89e7277fa241abdf"

func TestSourceIsEveryMatchedFileInItsOwnBlockInSortedOrder(t *testing.T) {
	t.Parallel()
	got, err := two().Source()
	if err != nil {
		t.Fatal(err)
	}
	if got != twoSource {
		t.Fatalf("Source:\n%s\nwant:\n%s", got, twoSource)
	}
}

func TestSourceWithoutAPreludeStartsWithTheFirstFile(t *testing.T) {
	t.Parallel()
	lib := two()
	lib.Prelude = ""
	got, err := lib.Source()
	if err != nil {
		t.Fatal(err)
	}
	want := "#!lua name=lib_one\n-- lua/a.lua: its line 1 is line 4 of this library\ndo\n"
	if !strings.HasPrefix(got, want) {
		t.Fatalf("Source starts:\n%s\nwant it to start:\n%s", got, want)
	}
}

// A prelude of several lines, with its own line break at the end, is written
// as it is and moves every file down by its lines.
func TestSourcePutsThePreludeBeforeEveryBlock(t *testing.T) {
	t.Parallel()
	lib := two()
	lib.Prelude = "local NS = {}\nlocal shared = 1\n"
	got, err := lib.Source()
	if err != nil {
		t.Fatal(err)
	}
	want := "#!lua name=lib_one\nlocal NS = {}\nlocal shared = 1\n-- lua/a.lua: its line 1 is line 6 of this library\ndo\n"
	if !strings.HasPrefix(got, want) {
		t.Fatalf("Source starts:\n%s\nwant it to start:\n%s", got, want)
	}
	if strings.Count(got, "local shared = 1") != 1 {
		t.Fatalf("the prelude is in the source %d times, want once:\n%s", strings.Count(got, "local shared = 1"), got)
	}
}

// backwards lists every directory in the reverse of its sorted order, which
// is what a file system that does not sort can do.
type backwards struct{ inner fstest.MapFS }

func (b backwards) Open(name string) (fs.File, error) { return b.inner.Open(name) }

func (b backwards) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := b.inner.ReadDir(name)
	slices.Reverse(entries)
	return entries, err
}

func TestSourceDoesNotFollowTheOrderTheFileSystemListsIn(t *testing.T) {
	t.Parallel()
	lib := two()
	lib.Files = backwards{lib.Files.(fstest.MapFS)}
	// The control: this file system does hand the files over last first.
	listed, err := fs.Glob(lib.Files, lib.Glob)
	if err != nil || !slices.Equal(listed, []string{"lua/b.lua", "lua/a.lua"}) {
		t.Fatalf("the backwards file system lists %v (%v), want b before a", listed, err)
	}
	got, err := lib.Source()
	if err != nil {
		t.Fatal(err)
	}
	if got != twoSource {
		t.Fatalf("Source over a file system that lists backwards:\n%s\nwant:\n%s", got, twoSource)
	}
}

func TestDigestIsTheFirstSixteenHexDigitsOfTheSourcesSHA256(t *testing.T) {
	t.Parallel()
	sum := sha256.Sum256([]byte(twoSource))
	want := hex.EncodeToString(sum[:])[:16]
	if got := DigestOf(twoSource); got != want || len(got) != DigestLength {
		t.Fatalf("DigestOf = %q, want %q (%d characters)", got, want, DigestLength)
	}
	got, err := two().Digest()
	if err != nil || got != want {
		t.Fatalf("Digest = %q %v, want %q", got, err, want)
	}
	if want != twoDigest {
		t.Fatalf("the digest of the fixed library is %s and was %s: the form of the source changed, and with it the digest of every library on a store", want, twoDigest)
	}
}

// Every byte of a file, of the prelude, of the library's name and of a file's
// name is in the digest: each single change gives a digest of its own.
func TestDigestChangesWhenAnyByteChanges(t *testing.T) {
	t.Parallel()
	const text = "local value = 12\n-- a note\n" // no change of one byte to Z or Y makes it a text the loader refuses
	build := func(name, file, prelude, body string) Library {
		return Library{Name: name, Files: tree(map[string]string{"lua/" + file: body + fn("fa")}), Glob: "lua/*", Prelude: prelude}
	}
	base, err := build("lib_one", "a.lua", text, text).Digest()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{base: "the library unchanged"}
	try := func(what string, lib Library) {
		t.Helper()
		got, err := lib.Digest()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if was, ok := seen[got]; ok {
			t.Fatalf("%s has the digest %s of %s", what, got, was)
		}
		seen[got] = what
	}
	changed := func(s string, i int) string {
		to := byte('Z')
		if s[i] == to {
			to = 'Y'
		}
		return s[:i] + string(to) + s[i+1:]
	}
	for i := range text {
		try(fmt.Sprintf("the file with byte %d changed", i), build("lib_one", "a.lua", text, changed(text, i)))
		try(fmt.Sprintf("the prelude with byte %d changed", i), build("lib_one", "a.lua", changed(text, i), text))
	}
	try("the file with a byte added", build("lib_one", "a.lua", text, text+" "))
	try("the file with a byte taken", build("lib_one", "a.lua", text, text[1:]))
	try("the library under another name", build("lib_onf", "a.lua", text, text))
	try("the file under another name", build("lib_one", "b.lua", text, text))
	try("the library with no prelude", build("lib_one", "a.lua", "", text))
}

// The digest is of the content: the same files read from a directory on disk
// and from memory, under any remedy and bound, have one digest.
func TestDigestIsOfTheContentAndOfNothingElse(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for name, file := range two().Files.(fstest.MapFS) {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, file.Data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	lib := two()
	lib.Files = os.DirFS(dir)
	lib.Remedy = "mytool fn load"
	lib.Bound = 1
	got, err := lib.Digest()
	if err != nil || got != twoDigest {
		t.Fatalf("Digest of the files on disk = %q %v, want %q", got, err, twoDigest)
	}
}

// The texts the loader accepts although they hold the words and signs it
// reads: in a comment, in a string or inside a function they are not the
// file's own.
func TestSourceAcceptsWhatOnlyLooksLikeAFault(t *testing.T) {
	t.Parallel()
	for name, text := range map[string]string{
		"block words in a comment":       "-- end end until do function\n" + fn("fa"),
		"block words in a long comment":  "--[[ end\nreturn\n]] " + fn("fa"),
		"a level in a long comment":      "--[==[ ]] end ]=] return ]==] " + fn("fa"),
		"block words in a string":        "local s = 'end return' .. \"until do\"\n" + fn("fa"),
		"block words in a long string":   "local s = [[\nend\nreturn\n]]\n" + fn("fa"),
		"a quote in a string":            "local s = 'it\\'s' .. \"a \\\" b\" .. '\\\\'\n" + fn("fa"),
		"a string over two lines":        "local s = 'one\\\ntwo'\n" + fn("fa"),
		"a return in a function":         "local function f(x) if x then return 1 end return 2 end\n" + fn("fa"),
		"a return in a nested function":  "local function f() return function() return 1 end end\n" + fn("fa"),
		"every kind of block":            "do local a = 1 end\nif true then elseif false then else end\nfor i = 1, 2 do end\nwhile false do end\nrepeat until true\n" + fn("fa"),
		"numerals":                       "local n = 1e5 + 0x1F + .5 + 3.25e-2 + 1E+2\n" + fn("fa"),
		"a bracket that opens no string": "local t = {} t[1] = 2 t[ [[k]] ] = 3\n" + fn("fa"),
		"a comment that ends the file":   fn("fa") + "-- the last line has no line break",
		"a register in a comment":        "-- redis.register_function('fa', f)\n" + fn("fa"),
		"a register in a string":         "local s = \"redis.register_function('fa', f)\"\n" + fn("fa"),
		"a register of another table":    "local x = {redis = redis}\nx.redis.register_function('fa', function() end)\n" + fn("fa"),
		"a register of a computed name":  "local p = 'f'\nredis.register_function(p .. 'a', function() end)\nredis.register_function('f' .. 'a', function() end)\n" + fn("fa"),
		"a table that computes its name": "redis.register_function{function_name = 'f' .. 'a', callback = function() end}\n" + fn("fa"),
		"a name set inside a callback":   "redis.register_function{callback = function() local function_name = 'fa' end, function_name = 'fb'}\n" + fn("fa"),
		"a table with no name":           "redis.register_function{callback = function() end}\n" + fn("fa"),
		"a register that is not a call":  "local r = redis.register_function\n" + fn("fa"),
		"names that differ":              fn("fa") + fn("fa_") + fn("f_a") + fn("fa2"),
	} {
		lib := Library{Name: "lib_one", Files: tree(map[string]string{"lua/a.lua": text}), Glob: "lua/*.lua"}
		source, err := lib.Source()
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !strings.Contains(source, "do\n"+text) {
			t.Errorf("%s: the source does not hold the file as it is:\n%s", name, source)
		}
	}
}

func TestFunctionsAreTheNamesTheFilesRegisterSorted(t *testing.T) {
	t.Parallel()
	if got, err := two().Functions(); err != nil || !slices.Equal(got, []string{"fa", "fb"}) {
		t.Fatalf("Functions = %q %v, want fa and fb", got, err)
	}
	lib := Library{Name: "lib_one", Files: tree(map[string]string{
		"b.lua": fn("zeta") + fn("Alpha") + "redis.register_function{function_name = 'in_a_table', callback = function() end}\n",
		"a.lua": fn("beta") + "local name = 'comp' .. 'uted'\nredis.register_function(name, function() end)\n-- " + fn("in_a_comment"),
	}), Glob: "*.lua", Prelude: fn("of_the_prelude")}
	if got, err := lib.Functions(); err != nil || !slices.Equal(got, []string{"Alpha", "beta", "in_a_table", "of_the_prelude", "zeta"}) {
		t.Fatalf("Functions = %q %v", got, err)
	}
	none := Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": "local x = 1\n"}), Glob: "*.lua"}
	if got, err := none.Functions(); err != nil || len(got) != 0 {
		t.Fatalf("Functions of a library that registers nothing = %q %v", got, err)
	}
}

// unreadable is a file system whose glob matches a file that cannot be read.
type unreadable struct{ fstest.MapFS }

func (u unreadable) ReadFile(name string) ([]byte, error) {
	return nil, &fs.PathError{Op: "read", Path: name, Err: errors.New("the disk\nfailed")}
}

// Every refusal: what the library is, and what the error's one line names.
// The store is never touched (TestARefusedLibraryNeverReachesTheStore).
var refusals = []struct {
	name string
	lib  Library
	want []string
}{
	{"no name", Library{Name: "", Files: tree(map[string]string{"a.lua": fn("fa")}), Glob: "*.lua"},
		[]string{`library "" refused`, "ASCII letters, digits or underscores"}},
	{"a name with a dash", Library{Name: "lib-one", Files: tree(map[string]string{"a.lua": fn("fa")}), Glob: "*.lua"},
		[]string{`library "lib-one" refused`, "ASCII letters, digits or underscores"}},
	{"a name with a space", Library{Name: "lib one", Files: tree(map[string]string{"a.lua": fn("fa")}), Glob: "*.lua"},
		[]string{`library "lib one" refused`}},
	{"a name with a letter outside ASCII", Library{Name: "libé", Files: tree(map[string]string{"a.lua": fn("fa")}), Glob: "*.lua"},
		[]string{`refused`, "ASCII letters, digits or underscores"}},
	{"a name that holds a second line", Library{Name: "lib\nname=other", Files: tree(map[string]string{"a.lua": fn("fa")}), Glob: "*.lua"},
		[]string{`library "lib\nname=other" refused`}},
	{"a name with a line break at its end", Library{Name: "lib_one\n", Files: tree(map[string]string{"a.lua": fn("fa")}), Glob: "*.lua"},
		[]string{`library "lib_one\n" refused`}},
	{"no file system", Library{Name: "lib_one", Glob: "*.lua"},
		[]string{`library "lib_one" refused`, "no file system"}},
	{"a malformed glob", Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": fn("fa")}), Glob: "[a"},
		[]string{`glob "[a" is malformed`}},
	{"no files", Library{Name: "lib_one", Files: tree(map[string]string{"a.txt": fn("fa")}), Glob: "*.lua"},
		[]string{"it has no files", `the glob "*.lua" matches nothing`}},
	{"no glob", Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": fn("fa")})},
		[]string{"it has no files", `the glob "" matches nothing`}},
	{"a directory under the glob", Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": fn("fa"), "b.lua/inner": fn("fb")}), Glob: "*.lua"},
		[]string{"b.lua cannot be read"}},
	{"a file that cannot be read", Library{Name: "lib_one", Files: unreadable{tree(map[string]string{"a.lua": fn("fa")})}, Glob: "*.lua"},
		[]string{"a.lua cannot be read", `the disk\x0afailed`}},
	{"an empty file", Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": fn("fa"), "b.lua": ""}), Glob: "*.lua"},
		[]string{"b.lua is empty"}},
	{"a file of white space", Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": fn("fa"), "b.lua": " \n\t\n"}), Glob: "*.lua"},
		[]string{"b.lua is empty"}},
	{"a file whose name holds a line break", Library{Name: "lib_one", Files: tree(map[string]string{"a\nb.lua": ""}), Glob: "*.lua"},
		[]string{`a\x0ab.lua is empty`}},
	{"a carriage return in a file", Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": fn("fa") + "local x = 1\r\n"}), Glob: "*.lua"},
		[]string{"a.lua line 2 holds a carriage return"}},
	{"a carriage return in a string of a file", Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": "local s = [[\n\n\r]]\n" + fn("fa")}), Glob: "*.lua"},
		[]string{"a.lua line 3 holds a carriage return"}},
	{"a carriage return in the prelude", Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": fn("fa")}), Glob: "*.lua", Prelude: "local NS = {}\r"},
		[]string{"the prelude line 1 holds a carriage return"}},
	{"a string that does not end", Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": fn("fa") + "local s = 'open\n"}), Glob: "*.lua"},
		[]string{"a.lua line 2", "the string opened here does not end on its line"}},
	{"a string that ends the file open", Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": fn("fa") + "local s = \"open"}), Glob: "*.lua"},
		[]string{"a.lua line 2", "the string opened here does not end on its line"}},
	{"a string that ends the file on a backslash", Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": fn("fa") + "local s = 'open\\"}), Glob: "*.lua"},
		[]string{"a.lua line 2", "the string opened here does not end on its line"}},
	{"a long string that does not close", Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": fn("fa") + "\nlocal s = [==[ open ]] ]=]\n"}), Glob: "*.lua"},
		[]string{"a.lua line 3", "the long string opened here never closes"}},
	{"a long comment that does not close", Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": fn("fa") + "--[[ open\n"}), Glob: "*.lua"},
		[]string{"a.lua line 2", "the long comment opened here never closes"}},
	{"an end that closes nothing", Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": fn("fa") + "end\nlocal leaked = 1\ndo\n"}), Glob: "*.lua"},
		[]string{"a.lua line 2", "this end closes no block of the file"}},
	{"an until that closes nothing", Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": fn("fa") + "until true\n"}), Glob: "*.lua"},
		[]string{"a.lua line 2", "this until closes no block of the file"}},
	{"a function left open", Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": fn("fa") + "local function f()\n  if true then end\n"}), Glob: "*.lua"},
		[]string{"a.lua line 2", "the function opened here never closes"}},
	{"a do left open", Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": fn("fa") + "\n\ndo\n"}), Glob: "*.lua"},
		[]string{"a.lua line 4", "the do opened here never closes"}},
	{"an if left open", Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": "if true then\n" + fn("fa")}), Glob: "*.lua"},
		[]string{"a.lua line 1", "the if opened here never closes"}},
	{"a repeat left open", Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": "repeat\n" + fn("fa")}), Glob: "*.lua"},
		[]string{"a.lua line 1", "the repeat opened here never closes"}},
	{"an until that closes a do", Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": fn("fa") + "do\nuntil true\n"}), Glob: "*.lua"},
		[]string{"a.lua line 3", "this until closes the do of line 2"}},
	{"an end that closes a repeat", Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": fn("fa") + "repeat\n\nend\n"}), Glob: "*.lua"},
		[]string{"a.lua line 4", "this end closes the repeat of line 2"}},
	{"a return outside every function", Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": fn("fa") + "return\n"}), Glob: "*.lua"},
		[]string{"a.lua line 2", "this return is outside every function"}},
	{"a return in a block outside every function", Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": "local function f() return 1 end\nif f() then return end\n" + fn("fa")}), Glob: "*.lua"},
		[]string{"a.lua line 2", "this return is outside every function"}},
	{"a return in the prelude", Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": fn("fa")}), Glob: "*.lua", Prelude: "local NS = {}\nreturn NS\n"},
		[]string{"the prelude line 2", "this return is outside every function"}},
	{"a block left open in the prelude", Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": fn("fa")}), Glob: "*.lua", Prelude: "do\n"},
		[]string{"the prelude line 1", "the do opened here never closes"}},
	{"a function two files register", Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": "\n" + fn("shared"), "b.lua": fn("fb") + fn("shared")}), Glob: "*.lua"},
		[]string{`function "shared" is registered twice, at a.lua line 2 and at b.lua line 2`}},
	{"a function two files register in two cases", Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": fn("shared"), "b.lua": fn("SHARED")}), Glob: "*.lua"},
		[]string{`function "SHARED" is registered twice, at a.lua line 1 and at b.lua line 1`, "compared without case"}},
	{"a function one file registers twice", Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": fn("fa") + fn("fb") + fn("fa")}), Glob: "*.lua"},
		[]string{`function "fa" is registered twice, at a.lua line 1 and at a.lua line 3`}},
	{"a function the prelude and a file register", Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": fn("fa")}), Glob: "*.lua", Prelude: fn("fa")},
		[]string{`function "fa" is registered twice, at the prelude line 1 and at a.lua line 1`}},
	{"a function the prelude registers twice", Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": fn("fb")}), Glob: "*.lua", Prelude: fn("fa") + "\n" + fn("Fa")},
		[]string{`function "Fa" is registered twice, at the prelude line 1 and at the prelude line 3`}},
	{"a function registered by a table and by a call", Library{Name: "lib_one", Files: tree(map[string]string{
		"a.lua": "redis.register_function{\n  callback = function(keys, args) local t = {1, {2}} return t end,\n  flags = {'no-writes'},\n  function_name = \"fa\"\n}\n",
		"b.lua": "redis.register_function(\n  'fa',\n  function() end)\n"}), Glob: "*.lua"},
		[]string{`function "fa" is registered twice, at a.lua line 4 and at b.lua line 2`}},
	{"a function registered by two tables", Library{Name: "lib_one", Files: tree(map[string]string{
		"a.lua": "redis.register_function({function_name = 'fa', callback = function() end})\n",
		"b.lua": "redis.register_function{function_name = [[fa]]; callback = function() end}\n"}), Glob: "*.lua"},
		[]string{`function "fa" is registered twice, at a.lua line 1 and at b.lua line 1`}},
	{"a function whose name is spelled with escapes", Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": fn("fa"), "b.lua": fn(`\102\97`)}), Glob: "*.lua"},
		[]string{`function "fa" is registered twice, at a.lua line 1 and at b.lua line 1`}},
	{"a source over the bound", Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": fn("fa"), "b.lua": "--" + strings.Repeat("x", MaxSourceBytes) + "\n"}), Glob: "*.lua"},
		[]string{"its source is over 4194304 bytes (MaxSourceBytes)", "split the library"}},
	{"a prelude over the bound", Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": fn("fa")}), Glob: "*.lua", Prelude: "--" + strings.Repeat("x", MaxSourceBytes)},
		[]string{"its source is over 4194304 bytes (MaxSourceBytes)"}},
}

func TestALibraryThatCannotBeLoadedIsRefusedWithTheReason(t *testing.T) {
	t.Parallel()
	for _, c := range refusals {
		source, err := c.lib.Source()
		if err == nil || source != "" {
			t.Errorf("%s: Source = %d bytes and %v, want a refusal and nothing", c.name, len(source), err)
			continue
		}
		if !errors.Is(err, ErrRefused) {
			t.Errorf("%s: the error does not wrap ErrRefused: %v", c.name, err)
		}
		line := err.Error()
		if strings.ContainsAny(line, "\n\r") {
			t.Errorf("%s: the error is more than one line: %q", c.name, line)
		}
		if !strings.HasPrefix(line, "redisfn: library ") {
			t.Errorf("%s: the error does not start with the package and the library: %q", c.name, line)
		}
		for _, want := range c.want {
			if !strings.Contains(line, want) {
				t.Errorf("%s: the error does not say %q: %q", c.name, want, line)
			}
		}
		if digest, derr := c.lib.Digest(); digest != "" || derr == nil || derr.Error() != line {
			t.Errorf("%s: Digest = %q %v, want Source's refusal", c.name, digest, derr)
		}
		if origin, lerr := c.lib.Locate(1); origin != (Origin{}) || lerr == nil || lerr.Error() != line {
			t.Errorf("%s: Locate = %v %v, want Source's refusal", c.name, origin, lerr)
		}
		if names, ferr := c.lib.Functions(); names != nil || ferr == nil || ferr.Error() != line {
			t.Errorf("%s: Functions = %q %v, want Source's refusal", c.name, names, ferr)
		}
	}
}

// The bound is the source's length: a source of exactly MaxSourceBytes is
// taken, and one byte more is refused.
func TestTheBoundIsOnTheSourcesLength(t *testing.T) {
	t.Parallel()
	with := func(filler int) Library {
		return Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": fn("fa") + "--" + strings.Repeat("x", filler) + "\n"}), Glob: "*.lua"}
	}
	small, err := with(0).Source()
	if err != nil {
		t.Fatal(err)
	}
	room := MaxSourceBytes - len(small)
	source, err := with(room).Source()
	if err != nil || len(source) != MaxSourceBytes {
		t.Fatalf("a source of MaxSourceBytes: %d bytes, %v", len(source), err)
	}
	if _, err := with(room + 1).Source(); !errors.Is(err, ErrRefused) {
		t.Fatalf("a source of MaxSourceBytes and one byte more: %v, want a refusal", err)
	}
}
