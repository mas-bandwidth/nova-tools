package life

// The process cell of the bench beat (#4338): what runs on the bench, read by
// `nova-sprint fleet ps` from the beat with no ssh. One ps read at most every
// fleet.PSEvery and the nova unit files, reduced by fleet.SamplePS to a
// bounded sample (top processes by CPU, the nova units declared|undeclared,
// the oldest processes outside every declared unit) and carried in the beat's
// ps field; between reads the beat carries the last sample.

import (
	"context"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleet"
)

// psTimeout bounds the one ps read.
const psTimeout = 5 * time.Second

// procSampler takes the sample; the seams are what a test replaces.
type procSampler struct {
	mu    sync.Mutex
	at    time.Time
	last  string
	now   func() time.Time
	ps    func(ctx context.Context, argv []string) (string, error)
	dirs  func() []string
	read  func(dir string) []fleet.UnitFile
	goos  string
	uid   int
	self  int
	users map[int]string
}

var procs = &procSampler{
	now:  time.Now,
	ps:   runPS,
	dirs: unitDirs,
	read: readUnitDir,
	goos: runtime.GOOS,
	uid:  os.Getuid(),
	self: os.Getpid(),
}

// ProcsNow is the beat's ps field: a fresh sample when the last is
// fleet.PSEvery old, else the last one.
func ProcsNow() string { return procs.sample(context.Background()) }

func (p *procSampler) sample(ctx context.Context) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	if p.last != "" && now.Sub(p.at) < fleet.PSEvery {
		return p.last
	}
	p.at = now
	argv, err := fleet.PSFullCommand(p.goos)
	if err != nil {
		p.last = fleet.PSSample{At: now.Unix(), Err: err.Error()}.Encode()
		return p.last
	}
	ctx, cancel := context.WithTimeout(ctx, psTimeout)
	defer cancel()
	out, err := p.ps(ctx, argv)
	if err != nil {
		p.last = fleet.PSSample{At: now.Unix(), Err: "ps: " + err.Error()}.Encode()
		return p.last
	}
	var units []fleet.UnitFile
	for _, d := range p.dirs() {
		units = append(units, p.read(d)...)
	}
	p.last = fleet.SamplePS(fleet.PSInput{
		GOOS: p.goos, Out: out, At: now, UID: p.uid, Self: p.self, Units: units, User: p.userName,
	}).Encode()
	return p.last
}

// userName is a uid's login name, looked up once per uid.
func (p *procSampler) userName(uid int) string {
	if p.users == nil {
		p.users = map[int]string{}
	}
	if n, ok := p.users[uid]; ok {
		return n
	}
	n := strconv.Itoa(uid)
	if u, err := user.LookupId(n); err == nil && u.Username != "" {
		n = u.Username
	}
	p.users[uid] = n
	return n
}

func runPS(ctx context.Context, argv []string) (string, error) {
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).Output()
	return string(out), err
}

// unitDirs are where the fleet play writes nova units on this OS.
func unitDirs() []string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return []string{filepath.Join(home, "Library", "LaunchAgents"), "/Library/LaunchDaemons"}
	case "linux":
		return []string{filepath.Join(home, ".config", "systemd", "user"), "/etc/systemd/system"}
	}
	return nil
}

// readUnitDir reads the nova unit files in dir. An unreadable dir has
// nothing to say (a bench without LaunchDaemons is normal); a unit file it
// cannot read, or one over 64 KiB, is carried with no body, so it reads as
// undeclared and is printed, never passed as clean.
func readUnitDir(dir string) []fleet.UnitFile {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []fleet.UnitFile
	for _, e := range entries {
		if e.IsDir() || !fleet.IsNovaUnit(e.Name()) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil || len(b) > 64<<10 {
			b = nil
		}
		out = append(out, fleet.UnitFile{File: e.Name(), Body: string(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].File < out[j].File })
	return out
}
