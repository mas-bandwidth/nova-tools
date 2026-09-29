package privacy

import (
	"errors"
	"fmt"
	"hash/fnv"
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

// ErrNoEntries marks a declared source that was read and yields no entry: it
// may hold private material in a shape the entry tokens do not open, so it is
// unreadable, never an empty part of the corpus.
var ErrNoEntries = errors.New("it yields no entries")

func tokenList(tokens []string) string {
	q := make([]string, len(tokens))
	for i, t := range tokens {
		q[i] = "`" + t + " `"
	}
	return strings.Join(q, " or ")
}

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
// how many of those an earlier root already holds (one file is one
// document), how many were read into the rarity model, and why any were not.
type RootLoad struct {
	Dir       string
	Recursive bool
	Pattern   string
	Found     int
	Shared    int
	Read      int
	Err       error
}

// The sample rules a Corpus reports.
const (
	// SampleAll means every matching document was read.
	SampleAll = "all"
	// SampleLowestHash means a bound bit, and the documents read are those
	// with the lowest FNV-1a 64 hash of "<root as written>/<relative path>"
	// that fit within both bounds.
	SampleLowestHash = "lowest-path-hash"
)

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
	// BackgroundFound is how many distinct matching documents the roots
	// hold; BackgroundBytes is how many bytes were read; SampleRule says
	// which documents were read, under the bounds MaxDocs and MaxBytes.
	BackgroundFound int
	BackgroundBytes int64
	SampleRule      string
	MaxDocs         int
	MaxBytes        int64
	Warnings        []string
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

func (c Corpus) unreadable() int {
	n := 0
	for _, s := range c.Sources {
		if s.Err != nil {
			n++
		}
	}
	return n
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
		text, err := DecodeText(b, src.Display)
		if err != nil {
			sl.Err = err
			c.Sources = append(c.Sources, sl)
			continue
		}
		blocks := c.Rules.ParseBlocks(src.Display, text)
		sl.Blocks = len(blocks)
		if len(blocks) == 0 {
			sl.Err = fmt.Errorf("%w: no line starts with %s followed by a blank", ErrNoEntries, tokenList(c.Rules.EntryTokens))
			c.Sources = append(c.Sources, sl)
			continue
		}
		for _, blk := range blocks {
			c.Blocks = append(c.Blocks, blk)
			if c.Rules.IsPrivate(blk) {
				sl.Private++
				c.Private = append(c.Private, blk)
			}
		}
		if sl.Private == 0 {
			c.warn("source: %s has %d entr%s and none is marked %s; if it holds private material, its marker is missing", src.Display, sl.Blocks, plural(sl.Blocks), c.Rules.Marker)
		}
		c.Sources = append(c.Sources, sl)
	}
	c.MaxDocs, c.MaxBytes = s.MaxDocs, s.MaxBytes
	if c.MaxDocs < 1 {
		c.MaxDocs = DefaultMaxDocs
	}
	if c.MaxBytes < 1 {
		c.MaxBytes = DefaultMaxBytes
	}
	c.loadBackground(s.Roots)
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

// doc is one matching background document: where it is, the key it sorts by
// in a sample, its size, and the root that holds it first.
type doc struct {
	path string
	rel  string
	key  uint64
	size int64
	root int
}

// loadBackground lists every root's matching files, counts a file reachable
// from two roots once (by file identity, under the first root that holds
// it), and reads them into the rarity model. Under both bounds every
// document is read. Over either, the documents are taken in order of the
// FNV-1a 64 hash of "<root as written>/<relative path>" while they fit, so
// the sample follows neither names nor dates and is the same every run.
func (c *Corpus) loadBackground(roots []RootSpec) {
	seen := map[string]bool{}
	var pool []doc
	var total int64
	for i, root := range roots {
		rl := RootLoad{Dir: root.Display, Recursive: root.Recursive, Pattern: root.Pattern}
		for _, d := range c.listRoot(root, &rl) {
			info, err := os.Stat(d.path)
			if err != nil {
				c.warn("background: cannot stat %s (%v); the rarity model has one document fewer", d.path, err)
				continue
			}
			k := fileKey(d.path, info)
			if seen[k] {
				rl.Shared++
				continue
			}
			seen[k] = true
			if info.Size() > MaxDocBytes {
				c.warn("background: %s is over MaxDocBytes (%d) and is left out of the rarity model", d.path, MaxDocBytes)
				continue
			}
			d.root, d.size = i, info.Size()
			d.key = sampleKey(root.Display, d.rel)
			pool = append(pool, d)
			total += d.size
		}
		c.Roots = append(c.Roots, rl)
	}
	c.BackgroundFound = len(pool)
	c.SampleRule = SampleAll
	if len(pool) > c.MaxDocs || total > c.MaxBytes {
		c.SampleRule = SampleLowestHash
		sort.Slice(pool, func(i, j int) bool {
			if pool[i].key != pool[j].key {
				return pool[i].key < pool[j].key
			}
			return pool[i].path < pool[j].path
		})
		var bytes int64
		n := 0
		for n < len(pool) && n < c.MaxDocs && bytes+pool[n].size <= c.MaxBytes {
			bytes += pool[n].size
			n++
		}
		c.warn("background: found %d documents (%d bytes), over max-docs %d or max-bytes %d; the rarity model reads %d, those with the lowest hash of their relative path (FNV-1a 64 of <root>/<path>), a sample that follows neither names nor dates and is the same every run",
			len(pool), total, c.MaxDocs, c.MaxBytes, n)
		pool = pool[:n]
	}
	for _, d := range pool {
		b, err := ReadBounded(d.path, MaxDocBytes)
		var text string
		if err == nil {
			text, err = DecodeText(b, d.path)
		}
		if err != nil {
			if errors.Is(err, ErrTooLarge) {
				c.warn("background: %s is over MaxDocBytes (%d) and is left out of the rarity model", d.path, MaxDocBytes)
			} else {
				c.warn("background: cannot read %s (%v); the rarity model has one document fewer", d.path, err)
			}
			continue
		}
		c.Roots[d.root].Read++
		c.BackgroundDocs++
		c.BackgroundBytes += int64(len(b))
		for w := range c.Rules.Terms(text) {
			c.BackgroundFreq[w]++
		}
	}
}

func sampleKey(root, rel string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(root + "/" + rel))
	return h.Sum64()
}

// listRoot is one root's matching regular files, with their paths relative
// to the root in slash form. The file pattern matches names
// case-insensitively.
func (c *Corpus) listRoot(root RootSpec, rl *RootLoad) []doc {
	var found []doc
	if info, err := os.Stat(root.Dir); err == nil && !info.IsDir() {
		rl.Err = fmt.Errorf("%s is a file, not a directory", root.Display)
		c.warn("background: %s is a file, not a directory; nothing is read from it, and ordinary words may score as rare and flag", root.Display)
		return nil
	}
	if root.Recursive {
		// A root that is a symlink to a directory is followed, once; links
		// below it are not.
		if dir, err := filepath.EvalSymlinks(root.Dir); err == nil {
			root.Dir = dir
		}
	}
	pattern := strings.ToLower(root.Pattern)
	match := func(name string) bool {
		ok, err := filepath.Match(pattern, strings.ToLower(name))
		return err == nil && ok
	}
	add := func(p string) {
		rel, err := filepath.Rel(root.Dir, p)
		if err != nil {
			rel = p
		}
		found = append(found, doc{path: p, rel: filepath.ToSlash(rel)})
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
				add(p)
			}
			return nil
		})
		switch {
		case errors.Is(err, errStop):
			c.warn("background: %s holds more than MaxWalkEntries (%d) entries; the walk stopped there, so the documents are the matches found before it", root.Display, MaxWalkEntries)
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
				add(filepath.Join(root.Dir, e.Name()))
			}
		}
	}
	rl.Found = len(found)
	return found
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
	// MaxDocs and MaxBytes replace the configuration's max-docs and
	// max-bytes when above zero.
	MaxDocs  int
	MaxBytes int64
}

// ErrNoCorpus is a corpus nobody named: no root, no configuration, no source.
var ErrNoCorpus = errors.New("name the corpus with --root <dir>, --config <file> or --source <file>; refusing to guess")

// ErrRootAndConfig is --root and --config given together.
var ErrRootAndConfig = errors.New("--root and --config both name a configuration; give one")

// ErrOption marks an option whose value is refused, such as a bad --pattern.
var ErrOption = errors.New("option")

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
// --config or --root must exist, even when --source names the sources: a
// misspelt root must not drop the configuration's shapes and background
// unseen. With no root and no configuration, --source alone names the corpus.
// A corpus with no source is refused.
func (o Options) Spec() (Spec, error) {
	if o.Root == "" && o.Config == "" && len(o.Sources) == 0 {
		return Spec{}, ErrNoCorpus
	}
	if o.Root != "" && o.Config != "" {
		return Spec{}, ErrRootAndConfig
	}
	if o.Pattern == "" {
		o.Pattern = DefaultPattern
	}
	if _, err := filepath.Match(o.Pattern, ""); err != nil {
		return Spec{}, fmt.Errorf("%w --pattern %q: %w", ErrOption, o.Pattern, err)
	}
	if o.MaxDocs < 0 {
		return Spec{}, fmt.Errorf("%w --max-docs wants a whole number of one or more, got %d", ErrOption, o.MaxDocs)
	}
	if o.MaxBytes < 0 {
		return Spec{}, fmt.Errorf("%w --max-bytes wants a whole number of one or more, got %d", ErrOption, o.MaxBytes)
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
		case errors.Is(err, fs.ErrNotExist):
			return Spec{}, fmt.Errorf("no configuration at %s; write one (nova-privacy help shows the format), or name sources with --source and no --root", cfg)
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
	if o.MaxBytes > 0 {
		file.MaxBytes = o.MaxBytes
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
	for _, r := range spec.Roots {
		if info, err := os.Stat(r.Dir); err == nil && !info.IsDir() {
			return Spec{}, fmt.Errorf("background root %s is a file, not a directory; name the directory that holds it", r.Display)
		}
	}
	return spec, nil
}

// resolvedPath is a path made absolute with every symlink resolved, or as
// clean as it can be made when it cannot be resolved.
func resolvedPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}
