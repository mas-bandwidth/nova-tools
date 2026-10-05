package friend

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
)

// Settings is what nova-friend install writes into one harness's own config.
// Prepare holds that config in memory, the twin of the files; Write puts the
// twin into Home. A directory it records is a real directory, never a symlink
// (docs/SPEC-FRIEND.md, harness settings).
type Settings struct {
	Friend, Harness, Home, Dir string
	Model                      string // opencode: provider/model; empty writes no model
	Wake                       string // grok: the wake file; empty is Dir/nova-friend.wake
	ConfigDir                  string // claude: CLAUDE_CONFIG_DIR; empty is Home/.claude
	CodexHome                  string // CODEX_HOME when set; else Home/.codex
	DSHHome                    string // DSH_HOME when set; else Home/.dsh
}

// Prepared is the in-memory twin of the harness files install writes.
// It writes nothing until Write.
type Prepared struct {
	settings  Settings
	Files     map[string]string // absolute path -> the bytes install writes
	Dirs      []string          // directories install creates, each a real directory
	Wake      string            // grok: the wake file the plist names
	ConfigDir string            // claude: CLAUDE_CONFIG_DIR the plist sets
	drifts    []string
}

// Prepare plans the harness files into memory. A symlinked directory is an
// error and nothing is planned for writing. A file that already matches is
// kept byte for byte; a file that does not is the twin, and drifts names the
// difference.
func (s Settings) Prepare() (Prepared, error) {
	if s.Friend == "" || s.Home == "" {
		return Prepared{}, errors.New("harness settings want a friend and a home")
	}
	if err := requireRealDir(s.Home); err != nil {
		return Prepared{}, fmt.Errorf("home: %w", err)
	}
	p := Prepared{settings: s, Files: map[string]string{}}
	var err error
	switch s.Harness {
	case "codex":
		err = planCodex(&p)
	case "dsh":
		err = planDSH(&p)
	case "grok":
		err = planGrok(&p)
	case "claude":
		err = planClaude(&p)
	case "opencode":
		err = planOpenCode(&p)
	case "antigravity":
		err = planAntigravity(&p)
	default:
		return p, nil
	}
	if err != nil {
		return Prepared{}, err
	}
	slices.Sort(p.drifts)
	return p, nil
}

// Write puts the twin into the home and records what install wrote, so check
// can name a later drift. A symlinked directory is refused and nothing is written.
func (p Prepared) Write() error {
	if !p.owns() {
		return nil
	}
	for _, dir := range p.Dirs {
		if err := ensureRealDir(dir); err != nil {
			return err
		}
	}
	var paths []string
	for path := range p.Files {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		if err := writeRealFile(path, []byte(p.Files[path])); err != nil {
			return err
		}
	}
	if p.Wake != "" {
		if err := createWakeFile(p.Wake); err != nil {
			return err
		}
	}
	return writeIntent(p)
}

// Paths is the files and directories install would write, sorted, for a dry run.
func (p Prepared) Paths() []string {
	var paths []string
	for path := range p.Files {
		paths = append(paths, path)
	}
	paths = append(paths, p.Dirs...)
	if p.Wake != "" {
		paths = append(paths, p.Wake)
	}
	slices.Sort(paths)
	return slices.Compact(paths)
}

func (p Prepared) owns() bool {
	switch p.settings.Harness {
	case "codex", "dsh", "grok", "claude", "antigravity":
		return true
	case "opencode":
		return p.settings.Model != ""
	default:
		return false
	}
}

// SettingsDrift is what nova-friend check reports: where the harness files
// differ from the twin of what install wrote. No record means install wrote
// no settings for this friend (nil, nil). A symlinked directory is one line,
// not a failure of the check.
func SettingsDrift(home, friendName string) ([]string, error) {
	doc, found, err := readIntent(home, friendName)
	if err != nil || !found {
		return nil, err
	}
	p, err := doc.settings(home, friendName).Prepare()
	if err != nil {
		return []string{err.Error()}, nil
	}
	return p.drifts, nil
}

// intentPath is the record of what install wrote, beside the daemon's state.
func intentPath(home, friendName string) string {
	return filepath.Join(DefaultStateDir(home, friendName), "harness-settings.json")
}

type intentDoc struct {
	Harness   string  `json:"harness"`
	Dir       string  `json:"dir,omitempty"`
	Model     string  `json:"model,omitempty"`
	Wake      string  `json:"wake,omitempty"`
	ConfigDir string  `json:"config_dir,omitempty"`
	CodexHome string  `json:"codex_home,omitempty"`
	DSHHome   string  `json:"dsh_home,omitempty"`
	Data      string  `json:"data,omitempty"`
	Preset    *string `json:"preset,omitempty"`
}

func (p Prepared) intent() intentDoc {
	s := p.settings
	d := intentDoc{Harness: s.Harness, CodexHome: s.CodexHome, DSHHome: s.DSHHome}
	switch s.Harness {
	case "codex":
		d.Dir = s.Dir
	case "dsh":
		empty := ""
		d.Preset = &empty
	case "grok":
		d.Dir = s.Dir
		d.Wake = p.Wake
	case "claude":
		d.ConfigDir = p.ConfigDir
	case "opencode":
		d.Model = s.Model
	case "antigravity":
		d.Data = filepath.Join(s.Home, filepath.FromSlash(AntigravityData))
	}
	return d
}

func (d intentDoc) settings(home, friendName string) Settings {
	return Settings{
		Friend: friendName, Harness: d.Harness, Home: home, Dir: d.Dir,
		Model: d.Model, Wake: d.Wake, ConfigDir: d.ConfigDir,
		CodexHome: d.CodexHome, DSHHome: d.DSHHome,
	}
}

func writeIntent(p Prepared) error {
	body, err := json.MarshalIndent(p.intent(), "", "  ")
	if err != nil {
		return err
	}
	return writeRealFile(intentPath(p.settings.Home, p.settings.Friend), append(body, '\n'))
}

func readIntent(home, friendName string) (intentDoc, bool, error) {
	raw, err := os.ReadFile(intentPath(home, friendName))
	if err != nil {
		if os.IsNotExist(err) {
			return intentDoc{}, false, nil
		}
		return intentDoc{}, false, err
	}
	var doc intentDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return intentDoc{}, false, fmt.Errorf("%s is not harness settings: %v", intentPath(home, friendName), err)
	}
	return doc, true, nil
}

func writeRealFile(path string, body []byte) error {
	path = filepath.Clean(path)
	if err := ensureRealDir(filepath.Dir(path)); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink; name a real file and run nova-friend install again", path)
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return atomicfile.WriteFile(path, body, 0o644)
}

// requireRealDir refuses a path that is not an absolute directory, and a
// symlink most of all: a symlinked writable root is not a directory the
// harness can use.
func requireRealDir(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%s is not an absolute real directory; name the real directory and run nova-friend install again", path)
	}
	exists, err := classifyDir(path)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%s is not a real directory; name the real directory and run nova-friend install again", path)
	}
	return nil
}

// ensureRealDir creates path as a real directory. An existing symlink is
// refused, and so is a symlink among its parents: nothing is created through one.
func ensureRealDir(path string) error {
	path = filepath.Clean(path)
	exists, err := classifyDir(path)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	parent := filepath.Dir(path)
	if parent == path {
		return fmt.Errorf("%s is not a real directory", path)
	}
	if err := ensureRealDir(parent); err != nil {
		return err
	}
	if err := os.Mkdir(path, 0o755); err != nil && !os.IsExist(err) {
		return err
	}
	_, err = classifyDir(path)
	return err
}

// classifyDir reports whether path exists as a real directory. A symlink is
// an error even when its target is a directory.
func classifyDir(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return true, fmt.Errorf("%s is a symlink, not a real directory; name the real directory and run nova-friend install again", path)
	}
	if !info.IsDir() {
		return true, fmt.Errorf("%s is not a real directory", path)
	}
	return true, nil
}

// stageDir adds dir to the directories Write creates, when it is absent.
// A symlink is refused.
func stageDir(p *Prepared, dir string) error {
	exists, err := classifyDir(dir)
	if err != nil {
		return err
	}
	if !exists {
		p.Dirs = append(p.Dirs, dir)
	}
	return nil
}
