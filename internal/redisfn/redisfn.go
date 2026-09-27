/*
Package redisfn builds, loads and checks a Redis function library from Lua
source held in a file system, which for a tool is the Lua it embeds. It knows
no library by name: a tool describes its own with a Library value and calls
Source, Digest, Functions, Load, Check and LoadMissing on it.

# The source

One library is one text, built the same way every time:

	#!lua name=<name>
	<the prelude, when there is one>
	-- <file>: its line 1 is line <n> of this library
	do
	<the file's text>
	end -- <file>

with one such block for every file the glob matches, in the order of their
names sorted byte by byte. A file's block is what keeps a library loadable as it grows: Lua
refuses a function that holds more than 200 local variables at once, the
library's text is one function, and without the blocks every top-level local
of every file counts towards the 200 (the library this was lifted from went
from 199 to 206 in one merge and the store refused it). Inside its block a
file's locals leave scope at the file's end, so the count is the prelude's
locals and the largest file's, not the sum. What one file hands to a later one
goes through a table the prelude declares.

The block holds only when a file cannot reach outside it, so a file is
refused when a string or a long comment of it does not close, when its block
words do not balance, or when it returns outside every function. Those, and
every other refusal, come before the store is touched; each wraps ErrRefused.

# Line numbers

Redis names a failing line of the library as user_function:<n>, counted in
the text above. Locate maps that line back to its file and the line there,
and Explain does the same to an error's text. A file's header says where the
file starts, so the same can be done by hand from the library's code as the
store returns it (FUNCTION LIST WITHCODE). A carriage return is refused in a
file: Lua counts "\r", "\r\n" and "\n\r" each as one line break and an editor
may not, and a line number has to mean one thing.

# The store

Load replaces the library with one command, FUNCTION LOAD REPLACE, after
which the store holds the whole library it held before or the whole of the
new one, never a part of either. Check reads the library's code back from
the store and compares it with Source: the code is the library's identity,
and no key, no counter and no answer of a function stands in for it.
LoadMissing is Check, and then Load when the store does not hold the
library. Each of the three returns within a bound, whatever the client's
timeouts are.

The store under several loaders is modelled in tla/RedisFn.tla: one holder
to a function name, a refusal that writes nothing, no moment without the
library, and what two deployers of two builds do to each other (the model is
written and not yet run; tla/README.md says what is owed). What this package
says of Redis is held against a redis-server by its functional tests.
*/
package redisfn

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// MaxSourceBytes is the largest source a library may have, 4 MiB: a library
// whose Source would be longer is refused. Check reads the whole source back
// from the store on every call, so the bound is what one Check may cost. The
// largest library lifted into this form is under 1 MiB.
const MaxSourceBytes = 4 << 20

// DigestLength is the number of characters of a digest, all lower-case hex.
const DigestLength = 16

// DefaultBound is how long one call of Load, Check or LoadMissing waits on
// the store when the Library names no Bound of its own.
const DefaultBound = 10 * time.Second

// ErrRefused is wrapped by every error that refuses a library for what it is
// (its name, its files, their text, its size) and so before the store was
// touched. errors.Is(err, ErrRefused) tells a defect of the build, which no
// retry mends, from a failure of the store.
var ErrRefused = errors.New("library refused")

// Library describes one Redis function library. It is a value: copying it is
// safe, the methods change nothing in it, and each method reads the files
// again, so two calls agree exactly when the files did not change between
// them (an embed.FS never does).
type Library struct {
	// Name is the library's name on the store: one or more ASCII letters,
	// digits or underscores, which is Redis's own rule. Redis tells names
	// apart by case.
	Name string

	// Files holds the library's Lua files.
	Files fs.FS

	// Glob names the files of Files that belong to the library, in the
	// syntax of path.Match: "lua/*.lua". Every match must be a file that can
	// be read and that holds more than white space.
	Glob string

	// Prelude is Lua placed before the first file, outside every file's
	// block: its locals are in scope in every file. It may be empty. It is
	// held to the rules of a file.
	Prelude string

	// Remedy is what an operator does to put this library on the store, in
	// the tool's own words ("mytool fn load --redis <addr>"). Check's
	// error ends with it. When it is empty the error names Load.
	Remedy string

	// Bound is the longest one call of Load, Check or LoadMissing waits on
	// the store. Zero or less is DefaultBound.
	Bound time.Duration
}

// Origin is where one line of a library's source came from.
type Origin struct {
	// File is the file the line belongs to, as the glob matched it. It is
	// empty for the library's first line and for the lines of the prelude.
	File string

	// Line is the line's number in File, or in the prelude, from 1. It is 0
	// for a line the loader wrote: the library's first line, and the header,
	// the do and the end around File.
	Line int

	// Prelude is true for the lines of the prelude.
	Prelude bool
}

// String is the origin as a person reads it, on one line whatever the file
// is called: "lua/a.lua:12" for line 12 of a file and "prelude:3" for line 3
// of the prelude.
func (o Origin) String() string {
	switch {
	case o.Prelude:
		return "prelude:" + strconv.Itoa(o.Line)
	case o.File == "":
		return "the library's first line"
	case o.Line == 0:
		return "the loader's lines around " + oneline.Escape(o.File)
	}
	return oneline.Escape(o.File) + ":" + strconv.Itoa(o.Line)
}

// DigestOf is the digest of a library's source: the first DigestLength hex
// digits of its SHA-256. Two sources have one digest only when they are the
// same bytes, as far as 64 bits of SHA-256 can say. It is the digest Check
// reports for the code a store holds.
func DigestOf(source string) string {
	sum := sha256.Sum256([]byte(source))
	return hex.EncodeToString(sum[:])[:DigestLength]
}

// Source returns the library's whole text, the argument of FUNCTION LOAD. It
// is a function of the name, the prelude and the matched files' names and
// bytes and of nothing else: not of the order in which the file system lists
// its files, not of a clock, not of the machine. Its error refuses the
// library and wraps ErrRefused.
func (l Library) Source() (string, error) {
	b, err := l.build()
	if err != nil {
		return "", err
	}
	return b.source, nil
}

// Digest returns DigestOf(Source): the identity of the library this binary
// was built with, which changes when any byte of Source changes. Its error is
// Source's.
func (l Library) Digest() (string, error) {
	b, err := l.build()
	if err != nil {
		return "", err
	}
	return b.digest, nil
}

// Functions returns the function names the library registers, as its files
// spell them, sorted. They are the names the loader can read: those written
// as a string in a call of redis.register_function, as its first argument or
// as the function_name of its table. A name the library computes when it
// loads is not among them, so the store may hold more. No two of them are the
// same name without case, or Source would refuse the library; the error is
// Source's.
func (l Library) Functions() ([]string, error) {
	b, err := l.build()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(b.names))
	for _, name := range b.names {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// Locate maps a line of Source, counted from 1 as Redis counts it in
// user_function:<n>, to the file and the line it came from. Every line of
// Source has an origin; a line outside Source is an error, and so is a
// library that Source refuses.
func (l Library) Locate(line int) (Origin, error) {
	b, err := l.build()
	if err != nil {
		return Origin{}, err
	}
	o, ok := b.locate(line)
	if !ok {
		return Origin{}, fmt.Errorf("redisfn: library %s has %d lines and line %d is not one of them; the line may be of another version of the library", l.Name, b.lines, line)
	}
	return o, nil
}

// Explain returns err with the origin of every line its text names as
// user_function:<n> written after it: "... [user_function:41 = lua/a.lua:7]".
// The error it returns wraps err. It returns err itself when err is nil,
// when its text names no line, and when Source refuses the library. The
// origin is true of the library this binary was built with, so it is true of
// an error from a store when Check says that store holds this library.
func (l Library) Explain(err error) error {
	if err == nil {
		return nil
	}
	b, berr := l.build()
	if berr != nil {
		return err
	}
	note := b.explain(err.Error())
	if note == "" {
		return err
	}
	return &explained{err, note}
}

type explained struct {
	err  error
	note string
}

func (e *explained) Error() string { return e.err.Error() + " " + e.note }

func (e *explained) Unwrap() error { return e.err }

// built is one library assembled: its source and what Locate, Check and Load
// need to know of it.
type built struct {
	name    string
	remedy  string
	source  string
	digest  string
	lines   int               // the lines of source
	prelude int               // how many of them are the prelude's, from line 2
	files   []span            // the files, in the order of the source
	names   map[string]string // every function name read from the files, by its lower-case spelling
}

// span is one file's place in the source.
type span struct {
	file  string
	head  int // the line of its header
	lines int // how many lines the file has; its line 1 is line head+2
}

var libraryName = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

func (l Library) refuse(format string, args ...any) error {
	return fmt.Errorf("redisfn: library %s refused: %s: %w", oneline.Quote(l.Name), fmt.Sprintf(format, args...), ErrRefused)
}

// text holds one file, or the prelude, to the rules of a file and returns the
// names it registers.
func (l Library) text(file, src string) ([]registration, error) {
	shown := oneline.Escape(file)
	if at := strings.IndexByte(src, '\r'); at >= 0 {
		return nil, l.refuse(`%s line %d holds a carriage return; write its line breaks as "\n" (Lua counts a carriage return as a line break and an editor may not, so a line number would mean two things)`,
			shown, 1+strings.Count(src[:at], "\n"))
	}
	regs, bad := scan(file, src)
	if bad != nil {
		return nil, l.refuse("%s line %d: %s", shown, bad.line, bad.why)
	}
	return regs, nil
}

const preludeName = "the prelude"

func (l Library) build() (*built, error) {
	if !libraryName.MatchString(l.Name) {
		return nil, l.refuse("a library's name is one or more ASCII letters, digits or underscores")
	}
	if l.Files == nil {
		return nil, l.refuse("it has no file system (Files is nil)")
	}
	files, err := fs.Glob(l.Files, l.Glob)
	if err != nil {
		return nil, l.refuse("its glob %s is malformed", oneline.Quote(l.Glob))
	}
	if len(files) == 0 {
		return nil, l.refuse("it has no files: the glob %s matches nothing", oneline.Quote(l.Glob))
	}
	sort.Strings(files)

	b := &built{name: l.Name, remedy: l.Remedy, names: map[string]string{}}
	first := map[string]registration{} // by lower-case name: Redis compares function names without case
	register := func(regs []registration) error {
		for _, reg := range regs {
			key := strings.ToLower(reg.name)
			if was, ok := first[key]; ok {
				return l.refuse("function %s is registered twice, at %s line %d and at %s line %d; the store keeps one function of a name, compared without case",
					oneline.Quote(reg.name), oneline.Escape(was.file), was.line, oneline.Escape(reg.file), reg.line)
			}
			first[key] = reg
			b.names[key] = reg.name
		}
		return nil
	}

	var out strings.Builder
	out.WriteString("#!lua name=" + l.Name + "\n")
	line := 2 // the line the next text written starts on
	// add holds one text to the rules of a file, takes the names it
	// registers, and writes what is to be written of it. The length is
	// looked at first, so a text that is too long is not read through.
	add := func(file, text, written string) error {
		if out.Len()+len(written) > MaxSourceBytes {
			return l.refuse("its source is over %d bytes (MaxSourceBytes); split the library", MaxSourceBytes)
		}
		regs, err := l.text(file, text)
		if err != nil {
			return err
		}
		if err := register(regs); err != nil {
			return err
		}
		out.WriteString(written)
		line += strings.Count(written, "\n")
		return nil
	}
	if l.Prelude != "" {
		if err := add(preludeName, l.Prelude, ended(l.Prelude)); err != nil {
			return nil, err
		}
		b.prelude = line - 2
	}
	for _, file := range files {
		raw, err := fs.ReadFile(l.Files, file)
		if err != nil {
			return nil, l.refuse("%s cannot be read: %s", oneline.Escape(file), oneline.Err(err))
		}
		src := string(raw)
		if strings.TrimSpace(src) == "" {
			return nil, l.refuse("%s is empty", oneline.Escape(file))
		}
		shown := oneline.Escape(file)
		body := ended(src)
		b.files = append(b.files, span{file: file, head: line, lines: strings.Count(body, "\n")})
		block := fmt.Sprintf("-- %s: its line 1 is line %d of this library\ndo\n%send -- %s\n", shown, line+2, body, shown)
		if err := add(file, src, block); err != nil {
			return nil, err
		}
	}
	b.source = out.String()
	b.digest = DigestOf(b.source)
	b.lines = line - 1
	return b, nil
}

// ended is the text with a line break at its end, so what is written after it
// starts a line.
func ended(text string) string {
	if strings.HasSuffix(text, "\n") {
		return text
	}
	return text + "\n"
}

func (b *built) locate(line int) (Origin, bool) {
	switch {
	case line < 1 || line > b.lines:
		return Origin{}, false
	case line == 1:
		return Origin{}, true
	case line < 2+b.prelude:
		return Origin{Line: line - 1, Prelude: true}, true
	}
	// The file whose header is the last one at or before the line.
	at := sort.Search(len(b.files), func(i int) bool { return b.files[i].head > line }) - 1
	f := b.files[at]
	if in := line - (f.head + 2) + 1; in >= 1 && in <= f.lines {
		return Origin{File: f.file, Line: in}, true
	}
	return Origin{File: f.file}, true
}

var lineNamed = regexp.MustCompile(`user_function:([0-9]+)`)

// explain is the note Explain writes after an error's text, or "" when the
// text names no line.
func (b *built) explain(text string) string {
	var notes []string
	seen := map[string]bool{}
	for _, m := range lineNamed.FindAllStringSubmatch(text, -1) {
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		where := fmt.Sprintf("no line of this library, which has %d", b.lines)
		if n, err := strconv.Atoi(m[1]); err == nil {
			if o, ok := b.locate(n); ok {
				where = o.String()
			}
		}
		notes = append(notes, m[0]+" = "+where)
	}
	if len(notes) == 0 {
		return ""
	}
	return "[" + strings.Join(notes, "; ") + "]"
}
