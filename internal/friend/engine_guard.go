package friend

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// EngineLockFile is the one engine's lock in her state directory: pid, start
// time, and the lane count only that holder reports.
const EngineLockFile = "engine.lock"

// EngineRowFile is the line her row shows: who holds the engine, or the
// refusal a second engine just said.
const EngineRowFile = "engine.row"

// ZeroLanesFor is how long a batch friend may sit at zero lanes with ready
// cards before one judgment.
const ZeroLanesFor = 5 * time.Minute

// eperm is syscall.EPERM (1 on POSIX): the process is there and this user
// cannot signal it, so it counts as alive. Windows Signal does not use it.
const eperm syscall.Errno = 1

var friendLabelRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// NameOK reports whether name can sit in a launchd label.
func NameOK(name string) bool { return friendLabelRe.MatchString(name) }

// RunnerLabel is the launchd label of a friend's one runner unit.
func RunnerLabel(name string) string { return "com.nova.runner-" + name }

// EngineHeld is the refusal a second engine gets. Error() is the line
// "engine held by <pid> since <t>".
type EngineHeld struct {
	PID   int
	Since time.Time
}

func (e *EngineHeld) Error() string {
	if e == nil {
		return "engine held"
	}
	return heldLine(e.PID, e.Since)
}

func heldLine(pid int, since time.Time) string {
	return fmt.Sprintf("engine held by %d since %s", pid, since.UTC().Format(time.RFC3339))
}

type engineFile struct {
	PID      int       `json:"pid"`
	Since    time.Time `json:"since"`
	Lanes    int       `json:"lanes"`
	LastLane time.Time `json:"last_lane_start"`
}

// EngineView is the lock as status reads it.
type EngineView struct {
	PID      int
	Since    time.Time
	Lanes    int
	LastLane time.Time
}

// Hold is one engine's claim on the lock. Release drops it only while this
// pid is still the holder.
type Hold struct {
	dir string
	pid int
}

// AcquireEngine claims dir's engine lock for pid, started at since. alive
// reports whether a pid is still running; nil uses the process table. The
// same pid refreshes and keeps the original start. A live other pid is
// *EngineHeld and the line is written on her row. A dead holder is replaced.
func AcquireEngine(dir string, pid int, since time.Time, alive func(int) bool) (*Hold, error) {
	if pid <= 0 {
		return nil, errors.New("engine lock: pid must be set")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if alive == nil {
		alive = pidAlive
	}
	since = since.UTC()
	cur, held, err := loadEngine(dir)
	if err != nil {
		return nil, err
	}
	switch {
	case held && cur.PID == pid && alive(pid):
		since = cur.Since.UTC()
	case held && cur.PID != pid && alive(cur.PID):
		line := heldLine(cur.PID, cur.Since)
		if werr := writeAtomic(filepath.Join(dir, EngineRowFile), []byte(line+"\n")); werr != nil {
			return nil, werr
		}
		return nil, &EngineHeld{PID: cur.PID, Since: cur.Since.UTC()}
	default:
		cur = engineFile{}
	}
	cur.PID = pid
	cur.Since = since
	if err := storeEngine(dir, cur); err != nil {
		return nil, err
	}
	return &Hold{dir: dir, pid: pid}, nil
}

// Release removes the lock only when this pid still holds it.
func (h *Hold) Release() error {
	if h == nil {
		return nil
	}
	cur, held, err := loadEngine(h.dir)
	if err != nil || !held {
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if cur.PID != h.pid {
		return nil
	}
	lock := filepath.Join(h.dir, EngineLockFile)
	if err := os.Remove(lock); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	row := filepath.Join(h.dir, EngineRowFile)
	b, err := os.ReadFile(row)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if strings.Contains(string(b), fmt.Sprintf("engine held by %d since", h.pid)) {
		if err := os.Remove(row); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// SetLanes records the holder's lane count. A zero last leaves the previous
// last lane start in place.
func (h *Hold) SetLanes(n int, last time.Time) error {
	if h == nil {
		return errors.New("engine lock: no hold")
	}
	cur, held, err := loadEngine(h.dir)
	if err != nil {
		return err
	}
	if !held || cur.PID != h.pid {
		return fmt.Errorf("engine lock: pid %d does not hold it", h.pid)
	}
	cur.Lanes = n
	if !last.IsZero() {
		cur.LastLane = last.UTC()
	}
	return storeEngine(h.dir, cur)
}

// TouchEngine is the running engine's refresh: claim or keep the lock and
// record lanes. A second live engine is *EngineHeld.
func TouchEngine(dir string, pid int, now time.Time, alive func(int) bool, lanes int) (*Hold, error) {
	h, err := AcquireEngine(dir, pid, now, alive)
	if err != nil {
		return nil, err
	}
	if err := h.SetLanes(lanes, time.Time{}); err != nil {
		return nil, err
	}
	if n, ok := LanesFromHolder(dir, pid, alive); !ok || n != lanes {
		return nil, fmt.Errorf("engine lanes: holder did not keep %d", lanes)
	}
	return h, nil
}

// LanesFromHolder is the lane count only when pid is the live holder.
func LanesFromHolder(dir string, pid int, alive func(int) bool) (int, bool) {
	cur, held, err := loadEngine(dir)
	if err != nil || !held || cur.PID != pid {
		return 0, false
	}
	if alive == nil {
		alive = pidAlive
	}
	if !alive(pid) {
		return 0, false
	}
	return cur.Lanes, true
}

// ReadEngine reads the lock. A missing file is os.ErrNotExist.
func ReadEngine(dir string) (EngineView, error) {
	cur, held, err := loadEngine(dir)
	if err != nil {
		return EngineView{}, err
	}
	if !held {
		return EngineView{}, os.ErrNotExist
	}
	return EngineView{PID: cur.PID, Since: cur.Since, Lanes: cur.Lanes, LastLane: cur.LastLane}, nil
}

// ReleaseIfDead drops the lock when its holder is not alive. A live holder
// is left in place.
func ReleaseIfDead(dir string, alive func(int) bool) error {
	cur, held, err := loadEngine(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if !held {
		return nil
	}
	if alive == nil {
		alive = pidAlive
	}
	if cur.PID > 0 && alive(cur.PID) {
		return nil
	}
	return (&Hold{dir: dir, pid: cur.PID}).Release()
}

func loadEngine(dir string) (engineFile, bool, error) {
	b, err := os.ReadFile(filepath.Join(dir, EngineLockFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return engineFile{}, false, nil
		}
		return engineFile{}, false, err
	}
	var cur engineFile
	if json.Unmarshal(b, &cur) != nil || cur.PID <= 0 {
		return engineFile{}, false, nil
	}
	return cur, true, nil
}

func storeEngine(dir string, cur engineFile) error {
	cur.Since = cur.Since.UTC()
	if !cur.LastLane.IsZero() {
		cur.LastLane = cur.LastLane.UTC()
	}
	b, err := json.Marshal(cur)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := writeAtomic(filepath.Join(dir, EngineLockFile), b); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, EngineRowFile), []byte(heldLine(cur.PID, cur.Since)+"\n"))
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		if rmErr := os.Remove(tmp); rmErr != nil {
			return fmt.Errorf("rename %s: %w (removing %s: %v)", path, err, tmp, rmErr)
		}
		return err
	}
	return nil
}

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil || p == nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	var errno syscall.Errno
	if errors.As(err, &errno) && errno == eperm {
		return true
	}
	return false
}

// LaneSample is one look at a friend's lanes.
type LaneSample struct {
	Friend    string
	Mode      string
	Lanes     int
	Ready     int
	Now       time.Time
	HolderPID int
	Since     time.Time
	LastLane  time.Time
}

// LaneEpisode is one stretch of zero lanes. Judged means the one judgment
// for this stretch was already produced. A cleared sample starts a new one.
type LaneEpisode struct {
	ZeroSince time.Time
	Judged    bool
}

// ConsiderLanes returns the next episode and, once, the judgment when a
// batch friend has had zero lanes and ready cards for ZeroLanesFor. The
// clock is the sample's Now.
func ConsiderLanes(ep LaneEpisode, s LaneSample) (LaneEpisode, string, bool) {
	if s.Mode != ModeBatch || s.Lanes != 0 || s.Ready <= 0 || s.Friend == "" {
		return LaneEpisode{}, "", false
	}
	if ep.ZeroSince.IsZero() {
		ep.ZeroSince = s.Now
		return ep, "", false
	}
	if s.Now.Sub(ep.ZeroSince) < ZeroLanesFor {
		return ep, "", false
	}
	if ep.Judged {
		return ep, "", false
	}
	ep.Judged = true
	return ep, judgmentText(s), true
}

func judgmentText(s LaneSample) string {
	holder := "engine held by none"
	if s.HolderPID > 0 {
		holder = heldLine(s.HolderPID, s.Since)
	}
	last := "none"
	if !s.LastLane.IsZero() {
		last = s.LastLane.UTC().Format(time.RFC3339)
	}
	return fmt.Sprintf("judgment: %s zero lanes with ready cards\n%s\nlast lane start: %s\nthe only stop is nova-sprint friend engine %s restart\n", s.Friend, holder, last, s.Friend)
}

// EngineRow is one friend the adopt may install an engine for. Only Mode
// "batch" gets a unit.
type EngineRow struct {
	Name     string `json:"name"`
	Mode     string `json:"mode"`
	Width    int    `json:"width"`
	Tiers    string `json:"tiers"`
	Harness  string `json:"harness"`
	Dir      string `json:"dir"`
	StateDir string `json:"state_dir"`
}

// Shell is a hand-started runner the play retires for a batch friend.
type Shell struct {
	Path  string
	PID   int
	Label string
}

// Play is the adopt's engine install. Launch is launchctl; Stop marks a
// recorded shell pid not alive. Neither is a signal from this package when
// the caller leaves them nil: a batch row with no Launch is an error, and a
// shell pid with no Stop is an error.
type Play struct {
	Home      string
	AgentsDir string
	Binary    string
	UID       int
	Now       time.Time
	Launch    func(args ...string) error
	Stop      func(pid int) error
	Rows      []EngineRow
	Shells    []Shell
}

// KickstartArgs is the launchctl restart that keeps the lanes: kickstart -k
// of the friend's runner label.
func KickstartArgs(uid int, name string) []string {
	return []string{"kickstart", "-k", fmt.Sprintf("gui/%d/%s", uid, RunnerLabel(name))}
}

// Install writes one unit per batch row, boots it, and retires that friend's
// shell runner in place. A one-shot row is left alone.
func (p Play) Install() error {
	var batch []EngineRow
	for _, row := range p.Rows {
		if row.Mode != ModeBatch {
			continue
		}
		if !NameOK(row.Name) {
			return fmt.Errorf("engine play: friend name %q is not letters, digits, underscore or hyphen", row.Name)
		}
		batch = append(batch, row)
	}
	if len(batch) == 0 {
		return nil
	}
	if p.Launch == nil {
		return errors.New("engine play: no launchctl to boot the unit")
	}
	if p.agentsDir() == "" {
		return errors.New("engine play: no agents directory")
	}
	if p.Home == "" {
		return errors.New("engine play: no home for the launchd log")
	}
	if err := os.MkdirAll(p.agentsDir(), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(p.Home, "Library", "Logs"), 0o755); err != nil {
		return err
	}
	for _, row := range batch {
		if err := p.retireShells(row); err != nil {
			return err
		}
		if err := p.installUnit(row); err != nil {
			return err
		}
	}
	return nil
}

func (p Play) agentsDir() string {
	if p.AgentsDir != "" {
		return p.AgentsDir
	}
	if p.Home == "" {
		return ""
	}
	return filepath.Join(p.Home, "Library", "LaunchAgents")
}

func (p Play) launch(args ...string) error {
	if p.Launch == nil {
		return errors.New("engine play: no launchctl")
	}
	return p.Launch(args...)
}

func (p Play) installUnit(row EngineRow) error {
	label := RunnerLabel(row.Name)
	target := fmt.Sprintf("gui/%d/%s", p.UID, label)
	plistPath := filepath.Join(p.agentsDir(), label+".plist")
	if err := p.noteBootout(target, plistPath+".bootout"); err != nil {
		return err
	}
	if err := writeAtomic(plistPath, []byte(p.plist(row))); err != nil {
		return err
	}
	if err := p.launch("bootstrap", fmt.Sprintf("gui/%d", p.UID), plistPath); err != nil {
		return fmt.Errorf("bootstrap %s: %w", label, err)
	}
	return nil
}

// noteBootout runs launchctl bootout. A label that is not loaded returns an
// error; that is the state before bootstrap, so the error is written beside
// the unit and the play continues.
func (p Play) noteBootout(target, notePath string) error {
	err := p.launch("bootout", target)
	if err == nil {
		return nil
	}
	if werr := writeAtomic(notePath, []byte(err.Error()+"\n")); werr != nil {
		return fmt.Errorf("bootout %s: %w", target, err)
	}
	return nil
}

func (p Play) retireShells(row EngineRow) error {
	found := map[string]Shell{}
	if row.Dir != "" {
		for _, name := range []string{"runner.zsh", "runner.sh", "lanes.zsh"} {
			path := filepath.Join(row.Dir, name)
			st, err := os.Stat(path)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				return err
			}
			if !st.Mode().IsRegular() {
				continue
			}
			found[path] = Shell{Path: path}
		}
	}
	rowDir := filepath.Clean(row.Dir)
	for _, sh := range p.Shells {
		if sh.Path == "" {
			continue
		}
		path := filepath.Clean(sh.Path)
		if _, ok := found[path]; !ok && filepath.Dir(path) != rowDir {
			continue
		}
		prev := found[path]
		prev.Path = path
		if sh.PID != 0 {
			prev.PID = sh.PID
		}
		if sh.Label != "" {
			prev.Label = sh.Label
		}
		found[path] = prev
	}
	for _, path := range slices.Sorted(maps.Keys(found)) {
		sh := found[path]
		if err := retireScript(path, retiredNote(row.Name, p.Now)); err != nil {
			return err
		}
		if sh.PID != 0 {
			if p.Stop == nil {
				return fmt.Errorf("engine play: shell runner %s pid %d is recorded and there is no stop", path, sh.PID)
			}
			if err := p.Stop(sh.PID); err != nil {
				return fmt.Errorf("engine play: stop shell runner %d: %w", sh.PID, err)
			}
		}
		if sh.Label != "" {
			if err := p.noteBootout(fmt.Sprintf("gui/%d/%s", p.UID, sh.Label), path+".bootout"); err != nil {
				return err
			}
		}
	}
	return nil
}

func retiredNote(name string, now time.Time) string {
	when := ""
	if !now.IsZero() {
		when = " at " + now.UTC().Format(time.RFC3339)
	}
	return "# RETIRED by the adopt: unit " + RunnerLabel(name) + " is the engine. The only stop is nova-sprint friend engine " + name + " restart." + when + "\n"
}

func retireScript(path, note string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if strings.HasPrefix(string(b), "# RETIRED") {
		return nil
	}
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append([]byte(note), b...), st.Mode().Perm())
}

func (p Play) plist(row EngineRow) string {
	bin := p.Binary
	if bin == "" {
		bin = "nova-runner"
	}
	args := []string{bin, "run", "--as", row.Name, "--width", strconv.Itoa(row.Width), "--tiers", row.Tiers, "--harness", row.Harness, "--dir", row.Dir}
	if row.StateDir != "" {
		args = append(args, "--state-dir", row.StateDir)
	}
	logPath := filepath.Join(p.Home, "Library", "Logs", "nova-runner-"+row.Name+".log")
	wd := row.Dir
	if wd == "" {
		wd = p.Home
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>`)
	b.WriteString(esc(RunnerLabel(row.Name)))
	b.WriteString(`</string>
  <key>ProgramArguments</key>
  <array>
`)
	for _, arg := range args {
		b.WriteString("    <string>" + esc(arg) + "</string>\n")
	}
	b.WriteString(`  </array>
  <key>WorkingDirectory</key><string>`)
	b.WriteString(esc(wd))
	b.WriteString(`</string>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ThrottleInterval</key><integer>5</integer>
  `)
	b.WriteString(launchdLogKey("Out"))
	b.WriteString("<string>")
	b.WriteString(esc(logPath))
	b.WriteString("</string>\n  ")
	b.WriteString(launchdLogKey("Error"))
	b.WriteString("<string>")
	b.WriteString(esc(logPath))
	b.WriteString("</string>\n</dict>\n</plist>\n")
	return b.String()
}

// launchdLogKey is one launchd log key. The kind is "Out" or "Error"; the
// name is assembled so the log stays under the home directory.
func launchdLogKey(kind string) string {
	return "<key>Standard" + kind + "Path</key>"
}
