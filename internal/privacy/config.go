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

// DefaultMaxDocs is how many documents each background root contributes to
// the rarity model before the rest are left out of a deterministic sample.
const DefaultMaxDocs = 900

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
//	max-docs <n>                               documents per background root
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
		default:
			bad(n, "unknown keyword %q; the keywords are source, background, marker, entry, stop, refuse, warn, allow, max-docs", key)
		}
	}
	return f, errors.Join(errs...)
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
	MaxDocs int
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
	s := Spec{Config: configPath, Rules: rules, MaxDocs: f.MaxDocs}
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
	return s, nil
}

func join(base, p string) string {
	if filepath.IsAbs(p) || base == "" {
		return p
	}
	return filepath.Join(base, p)
}
