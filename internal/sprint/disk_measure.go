package sprint

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

// Measuring a volume for a beat (docs/SPEC-SPRINT.md section 8, "Disk watermark"): the
// volume's figures are one statfs each beat; the largest directories under the AI root are a
// bounded scan, taken at most every DiskScanEvery, so a beat every second costs one system
// call and a full volume of millions of files costs one bounded walk in ten minutes.

const (
	// DiskScanEvery is how often a meter walks the AI root again.
	DiskScanEvery = 10 * time.Minute
	// DiskScanBound is how many entries one walk visits at most: a directory the walk is
	// inside when it reaches the bound, and every one after it, holds at least what it found
	// (DirSize.Partial).
	DiskScanBound = 200000
	// EnvAIRoot names the AI root a beat scans, when it is not the working directory.
	EnvAIRoot = "NOVA_AI_ROOT"
)

// DiskStat is a volume's figures as the file system reports them for a directory on it.
type DiskStat struct {
	Volume                         string
	Size, Free, Inodes, InodesFree uint64
}

// ErrNoStatfs is the answer of a system this tool cannot ask for a volume's figures.
var ErrNoStatfs = errors.New("this system cannot report a volume's free space")

// DiskMeter measures the volume Dir lives on, for each beat: Stat (the system's statfs
// when nil) gives the volume's figures, and FS (os.DirFS(Root) when nil) is walked for the
// largest directories under Root, at most every Every (DiskScanEvery when zero), visiting
// at most Bound entries (DiskScanBound when zero). Host is the machine's name (os.Hostname
// when empty); NoScan says the reading carries the figures alone. It is safe for
// concurrent beats.
type DiskMeter struct {
	Host, Dir, Root string
	Stat            func(dir string) (DiskStat, error)
	FS              fs.FS
	Every           time.Duration
	Bound           int
	NoScan          bool

	mu      sync.Mutex
	scanned time.Time
	top     []DirSize
	topErr  string
}

// Measure is the reading at now, or why the volume's figures could not be read.
func (m *DiskMeter) Measure(now time.Time) (*Disk, error) {
	stat := m.Stat
	if stat == nil {
		stat = StatVolume
	}
	st, err := stat(m.Dir)
	if err != nil {
		return nil, err
	}
	host := m.Host
	if host == "" {
		if host, err = os.Hostname(); err != nil || host == "" {
			host = "unknown"
		}
	}
	root := m.Root
	if root == "" {
		root = m.Dir
	}
	d := &Disk{At: now.UTC().Truncate(time.Second), Host: host, Dir: m.Dir, Volume: st.Volume, Size: st.Size, Free: st.Free,
		Inodes: st.Inodes, InodesFree: st.InodesFree, Root: root}
	if m.NoScan {
		return d, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	every := m.Every
	if every <= 0 {
		every = DiskScanEvery
	}
	if m.scanned.IsZero() || now.Sub(m.scanned) >= every || now.Before(m.scanned) {
		fsys := m.FS
		if fsys == nil {
			fsys = os.DirFS(root)
		}
		m.top, err = ScanTop(fsys, m.Bound, DiskTop)
		m.topErr = ""
		if err != nil {
			m.topErr = err.Error()
		}
		m.scanned = now
	}
	d.Top, d.TopErr = slices.Clone(m.top), m.topErr
	return d, nil
}

// Arg is the reading at now as friend beat --disk and fleet beat --disk take it, "" when the
// volume's figures could not be read (the beat then carries none).
func (m *DiskMeter) Arg(now time.Time) string {
	d, err := m.Measure(now)
	if err != nil {
		return ""
	}
	out, err := json.Marshal(d)
	if err != nil {
		return ""
	}
	return string(out)
}

// ParseDisk is a beat's --disk reading: the JSON Arg writes, refused unless it names its
// machine, its volume and a size, with a free count no larger than its size.
func ParseDisk(text string) (*Disk, error) {
	var d Disk
	if err := json.Unmarshal([]byte(text), &d); err != nil {
		return nil, fmt.Errorf("--disk wants the reading as JSON: %v", err)
	}
	switch {
	case !d.Valid():
		return nil, errors.New("--disk wants a reading with its at, host, volume and size")
	case d.Free > d.Size || d.InodesFree > d.Inodes:
		return nil, errors.New("--disk wants free bytes and free inodes no larger than the volume's")
	case len(d.Top) > DiskTop:
		d.Top = d.Top[:DiskTop]
	}
	d.At = d.At.UTC().Truncate(time.Second)
	return &d, nil
}

// ScanTop is the n largest directories at the top of fsys by the bytes of the regular
// files under each, visiting at most bound entries in all (DiskScanBound when zero or
// less): the directory the walk is inside when it reaches the bound, and every one it did
// not reach, are marked Partial. Unreadable entries are skipped; an unreadable top is the
// error.
func ScanTop(fsys fs.FS, bound, n int) ([]DirSize, error) {
	if bound <= 0 {
		bound = DiskScanBound
	}
	ents, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	visited := 0
	var out []DirSize
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		ds := DirSize{Path: e.Name()}
		if visited >= bound {
			ds.Partial = true
			out = append(out, ds)
			continue
		}
		stop := errors.New("bound")
		werr := fs.WalkDir(fsys, e.Name(), func(_ string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // an unreadable entry is skipped (a directory's error is a SkipDir by WalkDir)
			}
			visited++
			if visited > bound {
				return stop
			}
			if d.Type().IsRegular() {
				if info, ierr := d.Info(); ierr == nil {
					ds.Bytes += info.Size()
				}
			}
			return nil
		})
		if errors.Is(werr, stop) {
			ds.Partial = true
		}
		out = append(out, ds)
	}
	slices.SortStableFunc(out, func(a, b DirSize) int {
		if a.Bytes != b.Bytes {
			if a.Bytes > b.Bytes {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Path, b.Path)
	})
	if len(out) > n {
		out = out[:n]
	}
	return out, nil
}
