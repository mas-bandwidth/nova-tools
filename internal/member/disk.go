package member

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// The disk a member's beat carries (docs/SPEC-SPRINT.md section 8, "Disk
// watermarks"; sprint.DiskReading): the member runs on the machine, so it
// measures the volume its working directory lives on and names it on the beat
// as one JSON flag, --disk, which the sprint server cannot measure for it. The
// field names are sprint.DiskReading's, and this package cannot import it (the
// sprint imports the member), so the JSON is the seam between them.

// diskWarnDefault mirrors sprint.DiskWarnDefault: the bounded scan of the AI
// root runs only over it, so a beat never walks the tree for a volume with
// room.
const diskWarnDefault = 80

// diskScanDepth and diskScanTop mirror sprint.DiskScanDepth and
// sprint.DiskScanTop.
const (
	diskScanDepth = 2
	diskScanTop   = 5
)

// diskReading is the beat's reading, the shape of sprint.DiskReading.
type diskReading struct {
	At          time.Time `json:"at"`
	Volume      string    `json:"volume,omitempty"`
	Path        string    `json:"path,omitempty"`
	Free        uint64    `json:"free,omitempty"`
	Total       uint64    `json:"total,omitempty"`
	InodesFree  uint64    `json:"inodes_free,omitempty"`
	InodesTotal uint64    `json:"inodes_total,omitempty"`
	Top         []diskDir `json:"top,omitempty"`
}

type diskDir struct {
	Path  string `json:"path"`
	Bytes uint64 `json:"bytes"`
}

// use is the volume's use, in percent, as df counts it.
func (d diskReading) use() float64 {
	if d.Total == 0 || d.Free >= d.Total {
		return 0
	}
	return float64(d.Total-d.Free) / float64(d.Total) * 100
}

// diskArg is the member's --disk flag value: the reading of the volume its
// working directory lives on, "" when it cannot be read. The largest
// directories are scanned only over the warn line.
func diskArg() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	d, err := measureDisk(wd, time.Now())
	if err != nil {
		return ""
	}
	if d.use() >= diskWarnDefault {
		if root := aiRoot(); root != "" {
			d.Top = diskLargestDirs(root)
		}
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return ""
	}
	return string(raw)
}

// aiRoot is the directory whose largest subdirectories a scan names:
// NOVA_AI_ROOT, else ~/ai when it is a directory.
func aiRoot() string {
	if root := os.Getenv("NOVA_AI_ROOT"); root != "" {
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			return root
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	root := filepath.Join(home, "ai")
	if info, err := os.Stat(root); err == nil && info.IsDir() {
		return root
	}
	return ""
}

// diskLargestDirs is the n largest directories under root by the total size of
// the regular files directly inside them, at most depth levels below root: a
// bounded scan, symlinks skipped, an unreadable entry skipped.
func diskLargestDirs(root string) []diskDir {
	var dirs []diskDir
	var walk func(string, int)
	walk = func(dir string, level int) {
		entries, err := os.ReadDir(dir)
		if err != nil { // ignored: an unreadable directory is skipped; the scan is best-effort evidence for a judgment
			return
		}
		var total uint64
		for _, e := range entries {
			if e.Type()&os.ModeSymlink != 0 {
				continue
			}
			if e.IsDir() {
				if level < diskScanDepth {
					walk(filepath.Join(dir, e.Name()), level+1)
				}
				continue
			}
			if info, err := e.Info(); err == nil && info.Mode().IsRegular() {
				total += uint64(info.Size())
			}
		}
		dirs = append(dirs, diskDir{Path: dir, Bytes: total})
	}
	walk(root, 1)
	sort.Slice(dirs, func(i, j int) bool {
		if dirs[i].Bytes != dirs[j].Bytes {
			return dirs[i].Bytes > dirs[j].Bytes
		}
		return dirs[i].Path < dirs[j].Path
	})
	if len(dirs) > diskScanTop {
		dirs = dirs[:diskScanTop]
	}
	return dirs
}
