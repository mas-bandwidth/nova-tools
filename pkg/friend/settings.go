package friend

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
)

// Harness settings are what a friend's harness must hold in its own config
// for a delivery to work headless (docs/SPEC-FRIEND.md, Harness settings):
// install writes them and check compares what is there with what install
// would write, so no coordinator edits a harness's config by hand. The
// findings of 2026-10-05: a Codex writable root that was a symlink (no work
// for ten hours), a DeepSeek Harness on the minimal agent preset (every
// headless turn refused), a Grok wake file and a Claude config directory
// typed into units by hand.

// The kinds a path is, as a setting shows it.
const (
	KindDir     = "directory"
	KindFile    = "file"
	KindMissing = "missing"
	KindSymlink = "symlink" // shown as "symlink to <target>"
)

// Entry is what a path is, the path itself and never what a symlink names.
type Entry struct {
	Kind   string // KindDir, KindFile, KindSymlink or KindMissing
	Target string // a symlink's target
}

// SettingsFS is the filesystem the settings are read and written through:
// OSFS for install and check, friendtest.MemFS (the in-memory twin) for a test.
type SettingsFS interface {
	Lstat(path string) (Entry, error)
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, data []byte, perm fs.FileMode) error // whole, atomically
	MkdirAll(path string, perm fs.FileMode) error
}

// OSFS is the real filesystem; a write is atomicfile's.
type OSFS struct{}

func (OSFS) Lstat(p string) (Entry, error) {
	info, err := os.Lstat(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Entry{Kind: KindMissing}, nil
	case err != nil:
		return Entry{}, err
	case info.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(p)
		return Entry{Kind: KindSymlink, Target: target}, err
	case info.IsDir():
		return Entry{Kind: KindDir}, nil
	}
	return Entry{Kind: KindFile}, nil
}

func (OSFS) ReadFile(p string) ([]byte, error) { return os.ReadFile(p) }
func (OSFS) WriteFile(p string, data []byte, perm fs.FileMode) error {
	return atomicfile.WriteFile(p, data, perm)
}
func (OSFS) MkdirAll(p string, perm fs.FileMode) error { return os.MkdirAll(p, perm) }

// Setting is one setting a harness needs: the file (or directory) it lives
// in, its name, the value install writes and the value there now. A setting
// whose Have is not its Want has drifted.
type Setting struct {
	Harness, File, Name, Want, Have string
}

// Drifted says whether what is there is not what install would write.
func (s Setting) Drifted() bool { return s.Want != s.Have }

// ErrNotRealDir is the refusal when a path a harness setting names is a
// symlink or not a directory: a harness that resolves it works elsewhere or
// refuses (Codex took no writes into a symlinked writable root, 2026-10-05).
// Install never replaces a symlink; the remedy is to name the real path.
var ErrNotRealDir = errors.New("not a real directory")

// HarnessSettings is what install writes for one friend's harness and what
// check compares: the friend's directory and, per harness, the flags that
// name a setting. The writer of each harness is one small function
// (settings_<harness>.go).
type HarnessSettings struct {
	Harness, Friend, Dir string
	Home                 string // the friend's home: ~ of every default below
	StateDir             string // the daemon's state directory; DefaultStateDir when empty (grok's default wake file)
	Wake                 string // grok: the wake file the session's monitor tails; <state>/<friend>.wake when empty
	ConfigDir            string // claude: CLAUDE_CONFIG_DIR, the friend's own config directory
	Model                string // opencode: the model, provider/model; empty leaves it alone
	CodexHome            string // codex: CODEX_HOME; Home/.codex when empty
	DSHHome              string // dsh: DSH_HOME; Home/.dsh when empty
	FS                   SettingsFS
}

// setting is a Setting with its reader and writer. A path setting (Want a
// kind) is a guard: a symlink or the wrong kind is refused before anything
// is written, and a missing one is made only when write is set.
type setting struct {
	Setting
	read  func() (string, error)
	write func() error // nil: install refuses rather than writes it
	guard bool
}

// writers is each harness's settings writer; a harness not here needs none.
var writers = map[string]func(h HarnessSettings) ([]setting, error){
	"codex":    codexSettings,
	"dsh":      dshSettings,
	"grok":     grokSettings,
	"claude":   claudeSettings,
	"opencode": openCodeSettings,
}

func (h HarnessSettings) fs() SettingsFS {
	if h.FS == nil {
		return OSFS{}
	}
	return h.FS
}

// list is the friend's directory, always a real directory, then the
// harness's own settings.
func (h HarnessSettings) list() ([]setting, error) {
	if !filepath.IsAbs(h.Dir) {
		return nil, fmt.Errorf("the friend's directory %q is not absolute: a harness's settings name it by its absolute path", h.Dir)
	}
	out := []setting{h.realDir("dir", h.Dir, false)}
	w, ok := writers[h.Harness]
	if !ok {
		return out, nil
	}
	own, err := w(h)
	if err != nil {
		return nil, err
	}
	return append(out, own...), nil
}

// Check is every setting with what is there now; nothing is written.
func (h HarnessSettings) Check() ([]Setting, error) {
	list, err := h.list()
	if err != nil {
		return nil, err
	}
	if err := readAll(list); err != nil {
		return nil, err
	}
	out := make([]Setting, 0, len(list))
	for _, s := range list {
		out = append(out, s.Setting)
	}
	return out, nil
}

// readAll reads every setting, the paths first: a setting under a path
// that is refused (a symlink, the wrong kind) is not read through it, and
// shows what blocked it.
func readAll(list []setting) error {
	blocked := map[string]string{}
	for i, s := range list {
		if !s.guard {
			continue
		}
		have, err := s.read()
		if err != nil {
			return err
		}
		list[i].Have = have
		if have != s.Want && have != KindMissing {
			blocked[s.File] = s.File + " is " + have
		}
	}
	for i, s := range list {
		if s.guard {
			continue
		}
		why := ""
		for p, w := range blocked {
			if s.File == p || strings.HasPrefix(s.File, strings.TrimRight(p, "/")+"/") {
				why = w
			}
		}
		if why != "" {
			list[i].Have = "unread: " + why
			continue
		}
		have, err := s.read()
		if err != nil {
			return err
		}
		list[i].Have = have
	}
	return nil
}

// Write makes every setting what install wants and answers the ones it
// wrote. Every path is checked first: a symlink, the wrong kind, or a
// missing path install does not make is refused (ErrNotRealDir) and
// nothing is written. Each write is read back; a setting that still
// differs is an error naming it.
func (h HarnessSettings) Write() ([]Setting, error) {
	list, err := h.plan()
	if err != nil {
		return nil, err
	}
	var wrote []Setting
	for _, s := range list {
		if s.write == nil {
			return wrote, fmt.Errorf("%s: %s is %q, want %q, and install does not write it", s.File, s.Name, s.Have, s.Want)
		}
		if err := s.write(); err != nil {
			return wrote, fmt.Errorf("%s: writing %s: %w", s.File, s.Name, err)
		}
		if s.Have, err = s.read(); err != nil {
			return wrote, err
		}
		if s.Drifted() {
			return wrote, fmt.Errorf("%s: wrote %s %q and it reads %q", s.File, s.Name, s.Want, s.Have)
		}
		wrote = append(wrote, s.Setting)
	}
	return wrote, nil
}

// Plan is what Write would write, with Write's refusals; nothing is
// written (install --dry-run).
func (h HarnessSettings) Plan() ([]Setting, error) {
	list, err := h.plan()
	out := make([]Setting, 0, len(list))
	for _, s := range list {
		out = append(out, s.Setting)
	}
	return out, err
}

// plan reads every setting and answers the drifted ones, refusing a path
// that is a symlink, the wrong kind, or missing where install makes none.
func (h HarnessSettings) plan() ([]setting, error) {
	list, err := h.list()
	if err != nil {
		return nil, err
	}
	if err := readAll(list); err != nil {
		return nil, err
	}
	var drifted []setting
	for _, s := range list {
		if s.guard && s.Drifted() && (s.Have != KindMissing || s.write == nil) {
			return nil, fmt.Errorf("%w: %s (%s) is %s, want a %s; name the real path, install never replaces it", ErrNotRealDir, s.File, s.Name, s.Have, s.Want)
		}
		if s.Drifted() {
			drifted = append(drifted, s)
		}
	}
	return drifted, nil
}

// Drift is the settings of a Check that differ from what install writes.
func Drift(all []Setting) []Setting {
	var out []Setting
	for _, s := range all {
		if s.Drifted() {
			out = append(out, s)
		}
	}
	return out
}

// kindOf is what p is, as a setting shows it.
func kindOf(fsys SettingsFS, p string) (string, error) {
	e, err := fsys.Lstat(p)
	if err != nil {
		return "", err
	}
	if e.Kind == KindSymlink {
		return "symlink to " + e.Target, nil
	}
	return e.Kind, nil
}

// realDir is a guard: p is a real directory, made (private) when missing
// and create is set, refused as a symlink or a file.
func (h HarnessSettings) realDir(name, p string, create bool) setting {
	s := setting{Setting: Setting{Harness: h.Harness, File: p, Name: name, Want: KindDir}, guard: true,
		read: func() (string, error) { return kindOf(h.fs(), p) }}
	if create {
		s.write = func() error { return h.fs().MkdirAll(p, 0o700) }
	}
	return s
}

// realFile is a guard on a config file install edits: a symlink is refused
// (an atomic write would replace the link, a dotfile repository's, with a
// file); a missing file is fine, the write makes it.
func (h HarnessSettings) realFile(p string) setting {
	return setting{Setting: Setting{Harness: h.Harness, File: p, Name: "file", Want: "file or missing"}, guard: true,
		read: func() (string, error) {
			k, err := kindOf(h.fs(), p)
			if k == KindFile || k == KindMissing {
				return "file or missing", err
			}
			return k, err
		}}
}

// readOrEmpty is a config file's text, empty when it is missing.
func (h HarnessSettings) readOrEmpty(p string) (string, error) {
	raw, err := h.fs().ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return string(raw), err
}

// writeConfig writes a config file whole, its directory made first.
func (h HarnessSettings) writeConfig(p, text string, perm fs.FileMode) error {
	if err := h.fs().MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return h.fs().WriteFile(p, []byte(text), perm)
}

// homeDir is h.Home's sub-path, or override when set.
func (h HarnessSettings) homeDir(override string, sub ...string) string {
	if override != "" {
		return override
	}
	return filepath.Join(append([]string{h.Home}, sub...)...)
}

// noWhitespace refuses a path a harness cannot carry whole.
func noWhitespace(what, p string) error {
	if strings.ContainsAny(p, " \t\n") {
		return fmt.Errorf("%s %q contains whitespace, which the harness cannot carry whole", what, p)
	}
	return nil
}
