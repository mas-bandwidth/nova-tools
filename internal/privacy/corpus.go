package privacy

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The read bounds. Each refusal or warning that one of them causes names it.
const (
	// MaxConfigBytes bounds the configuration file.
	MaxConfigBytes = 64 << 10
	// MaxSourceBytes bounds one source of private material. A larger source
	// is not read, and so is unreadable.
	MaxSourceBytes = 8 << 20
	// MaxDocBytes bounds one background document. A larger one is left out
	// of the rarity model, with a warning.
	MaxDocBytes = 1 << 20
	// MaxPayloadBytes bounds the payload.
	MaxPayloadBytes = 4 << 20
	// MaxWalkEntries bounds how many directory entries one recursive root
	// walk visits before it stops, with a warning.
	MaxWalkEntries = 200000
)

// ErrTooLarge marks a read that exceeded its bound.
var ErrTooLarge = errors.New("larger than its bound")

// SourceLoad is what happened to one declared source. Err is nil only when
// the file was read end to end.
type SourceLoad struct {
	Path     string
	FromFlag bool
	Err      error
	Blocks   int
	Private  int
}

// RootLoad is what happened to one background root: how many files matched,
// how many were read into the rarity model, and why any were not.
type RootLoad struct {
	Dir       string
	Recursive bool
	Pattern   string
	Found     int
	Read      int
	Err       error
}

// Corpus is everything one run opened, including what it could not open.
type Corpus struct {
	Config         string
	Rules          Rules
	Sources        []SourceLoad
	Roots          []RootLoad
	Blocks         []Block
	Private        []Block
	BackgroundDocs int
	BackgroundFreq map[string]int
	Warnings       []string
}

// FirstErr returns the first source that could not be read, or nil.
func (c Corpus) FirstErr() *SourceLoad {
	for i := range c.Sources {
		if c.Sources[i].Err != nil {
			return &c.Sources[i]
		}
	}
	return nil
}

// ReadBounded reads a whole file of at most max bytes. A directory, a
// missing file and a file over the bound are all errors.
func ReadBounded(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s is a directory", path)
	}
	return readAtMost(f, max, path)
}

// ReadPayload reads a payload of at most MaxPayloadBytes from r.
func ReadPayload(r io.Reader, name string) ([]byte, error) {
	return readAtMost(r, MaxPayloadBytes, name)
}

func readAtMost(r io.Reader, max int64, name string) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%s is %w of %d bytes", name, ErrTooLarge, max)
	}
	return b, nil
}

// Load opens every source and every background root. It never returns an
// error: each failure is a row in Sources or Roots, or a warning, for Judge
// to weigh.
//
// A source that cannot be read hides private material, so it is an error
// on its row. A background problem only coarsens the rarity model, which
// makes the screen flag more rather than less, so it is a warning.
func Load(s Spec) Corpus {
	c := Corpus{Config: s.Config, Rules: s.Rules, BackgroundFreq: map[string]int{}}
	if c.Rules.stop == nil {
		c.Rules = DefaultRules()
	}
	dups := duplicateSources(s.Sources)
	for i, src := range s.Sources {
		sl := SourceLoad{Path: src.Display, FromFlag: src.FromFlag}
		if j, ok := dups[i]; ok {
			sl.Err = fmt.Errorf("it is the same file as %s; a source declared twice doubles every count in it, so declare it once", s.Sources[j].Display)
			c.Sources = append(c.Sources, sl)
			continue
		}
		b, err := ReadBounded(src.Path, MaxSourceBytes)
		if err != nil {
			if errors.Is(err, ErrTooLarge) {
				err = fmt.Errorf("%w (MaxSourceBytes)", err)
			}
			sl.Err = err
			c.Sources = append(c.Sources, sl)
			continue
		}
		blocks := c.Rules.ParseBlocks(src.Display, string(b))
		sl.Blocks = len(blocks)
		for _, blk := range blocks {
			c.Blocks = append(c.Blocks, blk)
			if c.Rules.IsPrivate(blk) {
				sl.Private++
				c.Private = append(c.Private, blk)
			}
		}
		c.Sources = append(c.Sources, sl)
	}
	maxDocs := s.MaxDocs
	if maxDocs < 1 {
		maxDocs = DefaultMaxDocs
	}
	for _, root := range s.Roots {
		c.loadRoot(root, maxDocs)
	}
	return c
}

// duplicateSources maps the index of every source that is the same file as
// an earlier one, by file identity after the path is resolved, to the index
// of the first. A source that cannot be opened has no identity and is left
// for the read to report.
func duplicateSources(srcs []SourceSpec) map[int]int {
	out := map[int]int{}
	infos := make([]os.FileInfo, len(srcs))
	for i, src := range srcs {
		info, err := os.Stat(src.Path)
		if err != nil {
			continue
		}
		infos[i] = info
		for j := 0; j < i; j++ {
			if infos[j] != nil && os.SameFile(infos[j], info) {
				out[i] = j
				break
			}
		}
	}
	return out
}

// checkDistinctSources refuses two sources that are one file, naming both.
func checkDistinctSources(srcs []SourceSpec) error {
	dups := duplicateSources(srcs)
	var errs []error
	for i := range srcs {
		if j, ok := dups[i]; ok {
			errs = append(errs, fmt.Errorf("sources %s and %s are the same file; a source declared twice doubles every count in it and can silence a flag, so declare it once", srcs[j].Display, srcs[i].Display))
		}
	}
	return errors.Join(errs...)
}

func (c *Corpus) warn(format string, a ...any) {
	c.Warnings = append(c.Warnings, fmt.Sprintf(format, a...))
}

// loadRoot lists one root's matching files in a fixed order, keeps the first
// maxDocs of them, and reads those into the rarity model. The order is the
// walk's: depth first, names sorted within each directory. A flat root is
// its directory's entries, sorted.
func (c *Corpus) loadRoot(root RootSpec, maxDocs int) {
	rl := RootLoad{Dir: root.Display, Recursive: root.Recursive, Pattern: root.Pattern}
	var found []string
	match := func(name string) bool {
		ok, err := filepath.Match(root.Pattern, name)
		return err == nil && ok
	}
	if root.Recursive {
		visited := 0
		errStop := errors.New("stop")
		err := filepath.WalkDir(root.Dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if p == root.Dir {
					return err
				}
				c.warn("background: cannot walk %s (%v); part of the rarity model is missing", p, err)
				return nil
			}
			visited++
			if visited > MaxWalkEntries {
				return errStop
			}
			if !d.IsDir() && d.Type().IsRegular() && match(d.Name()) {
				found = append(found, p)
			}
			return nil
		})
		switch {
		case errors.Is(err, errStop):
			c.warn("background: %s holds more than MaxWalkEntries (%d) entries; the walk stopped there, so the sample is the matches found before it", root.Display, MaxWalkEntries)
		case err != nil:
			rl.Err = err
			c.warn("background: %s cannot be walked (%v); ordinary words may score as rare and flag", root.Display, err)
		}
	} else {
		entries, err := os.ReadDir(root.Dir)
		if err != nil {
			rl.Err = err
			c.warn("background: %s cannot be listed (%v); ordinary words may score as rare and flag", root.Display, err)
		}
		for _, e := range entries {
			if e.Type().IsRegular() && match(e.Name()) {
				found = append(found, filepath.Join(root.Dir, e.Name()))
			}
		}
		sort.Strings(found)
	}
	rl.Found = len(found)
	if len(found) > maxDocs {
		c.warn("background: %s holds %d matching documents and max-docs is %d; the rarity model reads the first %d in walk order (depth first, names sorted within each directory), the same sample every run", root.Display, len(found), maxDocs, maxDocs)
		found = found[:maxDocs]
	}
	for _, p := range found {
		b, err := ReadBounded(p, MaxDocBytes)
		if err != nil {
			if errors.Is(err, ErrTooLarge) {
				c.warn("background: %s is over MaxDocBytes (%d) and is left out of the rarity model", p, MaxDocBytes)
			} else {
				c.warn("background: cannot read %s (%v); the rarity model has one document fewer", p, err)
			}
			continue
		}
		rl.Read++
		c.BackgroundDocs++
		for w := range c.Rules.Terms(string(b)) {
			c.BackgroundFreq[w]++
		}
	}
	c.Roots = append(c.Roots, rl)
}

// Screen loads the corpus a Spec names and judges one payload against it.
func Screen(s Spec, payload string) Result { return Judge(Load(s), payload) }

// Options name a corpus the way the command's flags do. Paths given here
// resolve against the working directory; paths inside a configuration file
// resolve against the file's own directory.
type Options struct {
	// Root is a directory whose ConfigName file is read.
	Root string
	// Config is a configuration file read instead.
	Config string
	// Sources replace the configuration's sources when any are given.
	Sources []string
	// Recursive and Flat replace the configuration's background roots when
	// either is given; Pattern is their file pattern, DefaultPattern when
	// empty.
	Recursive []string
	Flat      []string
	Pattern   string
	// Marker replaces the configuration's marker when set.
	Marker string
	// MaxDocs replaces the configuration's max-docs when above zero.
	MaxDocs int
}

// ErrNoCorpus is a corpus nobody named: no root, no configuration, no source.
var ErrNoCorpus = errors.New("name the corpus with --root <dir>, --config <file> or --source <file>; refusing to guess")

// ConfigPath is the configuration file the options read, or "" for none.
func (o Options) ConfigPath() string {
	if o.Config != "" {
		return o.Config
	}
	if o.Root != "" {
		// Joined as typed rather than cleaned, so the path a line prints is
		// the path the caller wrote.
		if strings.HasSuffix(o.Root, string(filepath.Separator)) || strings.HasSuffix(o.Root, "/") {
			return o.Root + ConfigName
		}
		return o.Root + string(filepath.Separator) + ConfigName
	}
	return ""
}

// Spec reads the configuration the options name, if any, applies the
// options over it, and returns the corpus to load. A configuration named by
// --config must exist; one found under --root may be absent only when
// --source names the sources. A corpus with no source is refused.
func (o Options) Spec() (Spec, error) {
	if o.Root == "" && o.Config == "" && len(o.Sources) == 0 {
		return Spec{}, ErrNoCorpus
	}
	if o.Root != "" && o.Config != "" {
		return Spec{}, errors.New("--root and --config both name a configuration; give one")
	}
	if o.Pattern == "" {
		o.Pattern = DefaultPattern
	}
	if _, err := filepath.Match(o.Pattern, ""); err != nil {
		return Spec{}, fmt.Errorf("--pattern %q: %w", o.Pattern, err)
	}
	if o.MaxDocs < 0 {
		return Spec{}, fmt.Errorf("--max-docs wants a whole number of one or more, got %d", o.MaxDocs)
	}
	var file File
	cfg, base := o.ConfigPath(), ""
	if cfg != "" {
		raw, err := ReadBounded(cfg, MaxConfigBytes)
		switch {
		case err == nil:
			f, perr := ParseConfig(string(raw))
			if perr != nil {
				return Spec{}, fmt.Errorf("%s: %w", cfg, perr)
			}
			file, base = f, filepath.Dir(cfg)
		case o.Config == "" && len(o.Sources) > 0 && errors.Is(err, fs.ErrNotExist):
			cfg = ""
		case errors.Is(err, fs.ErrNotExist):
			return Spec{}, fmt.Errorf("no configuration at %s; write one (nova-privacy help shows the format) or name sources with --source", cfg)
		case errors.Is(err, ErrTooLarge):
			return Spec{}, fmt.Errorf("%w (MaxConfigBytes)", err)
		default:
			return Spec{}, err
		}
	}
	if o.Marker != "" {
		file.Marker = o.Marker
	}
	if o.MaxDocs > 0 {
		file.MaxDocs = o.MaxDocs
	}
	spec, err := file.Resolve(base, cfg)
	if err != nil {
		return Spec{}, err
	}
	if len(o.Sources) > 0 {
		spec.Sources = nil
		for _, p := range o.Sources {
			spec.Sources = append(spec.Sources, SourceSpec{Path: p, Display: p, FromFlag: true})
		}
	}
	if len(o.Recursive)+len(o.Flat) > 0 {
		spec.Roots = nil
		for _, d := range o.Recursive {
			spec.Roots = append(spec.Roots, RootSpec{Root: Root{Dir: d, Recursive: true, Pattern: o.Pattern}, Display: d})
		}
		for _, d := range o.Flat {
			spec.Roots = append(spec.Roots, RootSpec{Root: Root{Dir: d, Pattern: o.Pattern}, Display: d})
		}
	}
	if len(spec.Sources) == 0 {
		return Spec{}, fmt.Errorf("%s declares no source of private material; add a line `source <file>` to it, or name one with --source", cfg)
	}
	if err := checkDistinctSources(spec.Sources); err != nil {
		return Spec{}, err
	}
	return spec, nil
}
