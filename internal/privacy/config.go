package privacy

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

// ConfigName is the configuration file read from the root a caller names.
const ConfigName = ".nova-privacy"

// DefaultPattern is the file pattern of a background root named by flag.
const DefaultPattern = "*.md"

// The background bounds. Under both, every matching document is read; over
// either, the sample is the documents with the lowest hash of their relative
// path that fit. The defaults read 5,000 documents of up to 13 KiB each
// whole.
const (
	// DefaultMaxDocs bounds how many background documents are read.
	DefaultMaxDocs = 20000
	// DefaultMaxBytes bounds how many bytes of background are read.
	DefaultMaxBytes int64 = 64 << 20
)

// Root is one background root: a directory of the author's ordinary writing,
// walked recursively or read flat, keeping the files whose names match
// Pattern.
type Root struct {
	Dir       string
	Recursive bool
	Pattern   string
}

// Mode names how a root is read.
func (r Root) Mode() string {
	if r.Recursive {
		return "recursive"
	}
	return "flat"
}

// File is a parsed configuration, its paths as written.
type File struct {
	Sources     []string
	Roots       []Root
	Marker      string
	EntryTokens []string
	Stop        []string
	Refuse      []Pattern
	Warn        []Pattern
	Allow       []string
	MaxDocs     int
	MaxBytes    int64
}

// ParseConfig reads configuration text. It is line-oriented: a blank line or
// one starting with # is ignored, and every other line is a keyword and its
// value.
//
//	source <file>                              a source of private material
//	background recursive|flat <pattern> <dir>  a root of ordinary writing
//	marker <text>                              the marker; default (private)
//	entry <token>                              a line starting "<token> " opens an entry; default ## and -
//	stop <word>...                             extra stop words
//	refuse <class> <regexp>                    a structure shape that refuses
//	warn <class> <regexp>                      a structure shape that warns
//	allow <text>                               a specimen that never fires
//	max-docs <n>                               background documents read, at most
//	max-bytes <n>[K|M|G]                       background bytes read, at most
//
// Every malformed line is reported, each with its line number.
func ParseConfig(text string) (File, error) {
	var f File
	var errs []error
	bad := func(n int, format string, a ...any) {
		errs = append(errs, fmt.Errorf("line %d: %s", n, fmt.Sprintf(format, a...)))
	}
	markerLine := 0
	for i, raw := range strings.Split(text, "\n") {
		n := i + 1
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, rest := cutField(line)
		fields := strings.Fields(rest)
		switch key {
		case "source":
			if rest == "" {
				bad(n, "source wants a file")
				continue
			}
			f.Sources = append(f.Sources, rest)
		case "background":
			if len(fields) < 3 || (fields[0] != "recursive" && fields[0] != "flat") {
				bad(n, "background wants recursive|flat, a file pattern and a directory, such as: background recursive *.md notes")
				continue
			}
			if _, err := filepath.Match(fields[1], ""); err != nil {
				bad(n, "background pattern %q: %v", fields[1], err)
				continue
			}
			_, dir := cutField(rest)
			_, dir = cutField(dir)
			f.Roots = append(f.Roots, Root{Dir: dir, Recursive: fields[0] == "recursive", Pattern: fields[1]})
		case "marker":
			if rest == "" {
				bad(n, "marker wants the text that declares an entry private")
				continue
			}
			if markerLine != 0 {
				bad(n, "a second marker; line %d already names one", markerLine)
				continue
			}
			f.Marker, markerLine = rest, n
		case "entry":
			if len(fields) != 1 {
				bad(n, "entry wants one token, such as ## or -")
				continue
			}
			f.EntryTokens = append(f.EntryTokens, fields[0])
		case "stop":
			if len(fields) == 0 {
				bad(n, "stop wants one or more words")
				continue
			}
			f.Stop = append(f.Stop, fields...)
		case "refuse", "warn":
			class, expr := cutField(rest)
			if class == "" || expr == "" {
				bad(n, "%s wants a class name and a regular expression", key)
				continue
			}
			p, err := CompilePattern(class, expr)
			if err != nil {
				bad(n, "%s %s: %v", key, class, err)
				continue
			}
			if p.RE.MatchString("") {
				bad(n, "%s %s: %q matches the empty string, so it matches every payload; make it need at least one character", key, class, expr)
				continue
			}
			if key == "refuse" {
				f.Refuse = append(f.Refuse, p)
			} else {
				f.Warn = append(f.Warn, p)
			}
		case "allow":
			if rest == "" {
				bad(n, "allow wants the text that never fires")
				continue
			}
			f.Allow = append(f.Allow, rest)
		case "max-docs":
			v, err := strconv.Atoi(rest)
			if err != nil || v < 1 {
				bad(n, "max-docs wants a whole number of one or more, got %q", rest)
				continue
			}
			f.MaxDocs = v
		case "max-bytes":
			v, err := ParseBytes(rest)
			if err != nil {
				bad(n, "max-bytes: %v", err)
				continue
			}
			f.MaxBytes = v
		default:
			bad(n, "unknown keyword %q; the keywords are source, background, marker, entry, stop, refuse, warn, allow, max-docs, max-bytes", key)
		}
	}
	return f, errors.Join(errs...)
}

// ParseBytes reads a byte count of one or more: digits, with an optional K,
// M or G suffix for KiB, MiB or GiB, in either case.
func ParseBytes(s string) (int64, error) {
	s = strings.TrimSpace(s)
	orig := s
	mult := int64(1)
	if n := len(s); n > 0 {
		switch s[n-1] {
		case 'k', 'K':
			mult, s = 1<<10, s[:n-1]
		case 'm', 'M':
			mult, s = 1<<20, s[:n-1]
		case 'g', 'G':
			mult, s = 1<<30, s[:n-1]
		}
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < 1 || v > (1<<50)/mult {
		return 0, fmt.Errorf("wants a whole number of bytes of one or more, with an optional K, M or G, got %q", orig)
	}
	return v * mult, nil
}

// cutField splits off the first whitespace-separated word and returns it
// with the trimmed remainder.
func cutField(s string) (string, string) {
	s = strings.TrimSpace(s)
	i := strings.IndexFunc(s, unicode.IsSpace)
	if i < 0 {
		return s, ""
	}
	return s[:i], strings.TrimSpace(s[i:])
}

// Rules builds the judgment's rules from the parsed file.
func (f File) Rules() (Rules, error) {
	return NewRules(f.Marker, f.EntryTokens, f.Stop, f.Refuse, f.Warn, f.Allow)
}

// SourceSpec is one declared source: the path read, the path shown, and
// whether a flag named it rather than the configuration.
type SourceSpec struct {
	Path     string
	Display  string
	FromFlag bool
}

// Spec is the whole corpus one screen reads.
type Spec struct {
	// Config is the configuration file as shown, "" when flags alone name
	// the corpus.
	Config  string
	Sources []SourceSpec
	Roots   []RootSpec
	Rules   Rules
	// MaxDocs and MaxBytes bound the background read; zero takes the
	// default.
	MaxDocs  int
	MaxBytes int64
}

// RootSpec is one background root with the path read and the path shown.
type RootSpec struct {
	Root
	Display string
}

// Resolve turns the file's paths into a Spec. A relative path resolves
// against base, the directory holding the configuration; configPath is the
// file as it is shown in remedies.
func (f File) Resolve(base, configPath string) (Spec, error) {
	rules, err := f.Rules()
	if err != nil {
		return Spec{}, err
	}
	s := Spec{Config: configPath, Rules: rules, MaxDocs: f.MaxDocs, MaxBytes: f.MaxBytes}
	for _, p := range f.Sources {
		s.Sources = append(s.Sources, SourceSpec{Path: join(base, p), Display: p})
	}
	for _, r := range f.Roots {
		rr := r
		rr.Dir = join(base, r.Dir)
		s.Roots = append(s.Roots, RootSpec{Root: rr, Display: r.Dir})
	}
	if s.MaxDocs == 0 {
		s.MaxDocs = DefaultMaxDocs
	}
	if s.MaxBytes == 0 {
		s.MaxBytes = DefaultMaxBytes
	}
	return s, nil
}

func join(base, p string) string {
	if filepath.IsAbs(p) || base == "" {
		return p
	}
	return filepath.Join(base, p)
}
