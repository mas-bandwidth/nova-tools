package hostload

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The volume a working directory lives on, measured for a beat (docs/SPEC-SPRINT.md section
// 8, "Disk watermark"): the
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
	// DiskTop is how many of the largest directories under the AI root a reading names.
	DiskTop = 5
)

// DirSize is one directory under the AI root and the bytes the bounded scan found in it;
// Partial says the scan stopped at its bound inside it, so it holds at least that.
type DirSize struct {
	Path    string `json:"path"`
	Bytes   int64  `json:"bytes"`
	Partial bool   `json:"partial,omitempty"`
}

// Disk is one reading of the volume a working directory lives on: when, which machine,
// the directory, the volume's mount point, its size and free bytes, its inodes and free
// inodes (zero when the file system does not count them), and the largest directories
// under the AI root (Root) by a bounded scan, or why they could not be listed.
type Disk struct {
	At         time.Time `json:"at"`
	Host       string    `json:"host"`
	Dir        string    `json:"dir"`
	Volume     string    `json:"volume"`
	Size       uint64    `json:"size"`
	Free       uint64    `json:"free"`
	Inodes     uint64    `json:"inodes,omitempty"`
	InodesFree uint64    `json:"inodes_free,omitempty"`
	Root       string    `json:"root,omitempty"`
	Top        []DirSize `json:"top,omitempty"`
	TopErr     string    `json:"top_err,omitempty"`
}

// pct is used of all as a whole percent, rounded up so a volume a byte from full never
// reads under 100 only by rounding; 0 when all is zero.
func pct(used, all uint64) int {
	if all == 0 || used == 0 {
		return 0
	}
	return int((used*100 + all - 1) / all)
}

// BytesUsed and InodesUsed are the percent of the volume's bytes and inodes in use.
func (d Disk) BytesUsed() int  { return pct(d.Size-min(d.Free, d.Size), d.Size) }
func (d Disk) InodesUsed() int { return pct(d.Inodes-min(d.InodesFree, d.Inodes), d.Inodes) }

// Used is the volume's use: the larger of its bytes and its inodes used.
func (d Disk) Used() int { return max(d.BytesUsed(), d.InodesUsed()) }

// Valid says the reading names its volume and its machine and has a size.
func (d Disk) Valid() bool { return d.Host != "" && d.Volume != "" && d.Size > 0 && !d.At.IsZero() }

// Text is the reading as a row's disk cell says it: the percent used, the free bytes and,
// when the file system counts them, the free inodes, "81% used, 120G free, 3.1M inodes
// free".
func (d Disk) Text() string {
	out := fmt.Sprintf("%d%% used, %s free", d.Used(), HumanBytes(d.Free))
	if d.Inodes > 0 {
		out += ", " + HumanCount(d.InodesFree) + " inodes free"
	}
	return out
}

// HumanBytes is a count of bytes with a binary unit and one decimal under ten: 512B,
// 3.4K, 120G.
func HumanBytes(n uint64) string {
	const units = "KMGTPE"
	if n < 1024 {
		return strconv.FormatUint(n, 10) + "B"
	}
	v, u := float64(n), -1
	for v >= 1024 && u < len(units)-1 {
		v /= 1024
		u++
	}
	if v < 10 {
		return strconv.FormatFloat(v, 'f', 1, 64) + string(units[u])
	}
	return strconv.FormatFloat(v, 'f', 0, 64) + string(units[u])
}

// HumanCount is a count with a decimal unit: 950, 3.1K, 12M.
func HumanCount(n uint64) string {
	switch {
	case n < 1000:
		return strconv.FormatUint(n, 10)
	case n < 1_000_000:
		return strconv.FormatFloat(float64(n)/1e3, 'f', 1, 64) + "K"
	case n < 1_000_000_000:
		return strconv.FormatFloat(float64(n)/1e6, 'f', 1, 64) + "M"
	}
	return strconv.FormatFloat(float64(n)/1e9, 'f', 1, 64) + "G"
}

// TopText is the largest directories as a judgment names them, "jobs 310G, cache 41G+"
// (a + where the scan stopped at its bound inside it), or why they are not listed.
func (d Disk) TopText() string {
	if len(d.Top) == 0 {
		if d.TopErr != "" {
			return "not listed: " + d.TopErr
		}
		return "none listed"
	}
	parts := make([]string, len(d.Top))
	for i, t := range d.Top {
		parts[i] = t.Path + " " + HumanBytes(uint64(max(t.Bytes, 0)))
		if t.Partial {
			parts[i] += "+"
		}
	}
	return strings.Join(parts, ", ")
}

// DiskStat is a volume's figures as the file system reports them for a directory on it.
type DiskStat struct {
	Volume                         string
	Size, Free, Inodes, InodesFree uint64
}

// ErrNoStatfs is the answer of a system this tool cannot ask for a volume's figures.
var ErrNoStatfs = errors.New("this system cannot report a volume's free bytes")

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
