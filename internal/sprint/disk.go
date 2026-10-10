package sprint

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// A machine's disk (docs/SPEC-SPRINT.md section 8, "Disk watermarks"). On
// 2026-10-06 the AI volume reached 100% with no warning from the machine: the
// first sign was the bus store refusing writes and lanes dying. A stranger's
// machine would fail the same way unseen. Every beat measures the free space
// and the inode headroom of the volume the beat's working directory lives on,
// and, when the volume is over its warn line, a bounded scan of the AI root's
// largest directories (DiskReading.Top). The tick raises one judgment per
// volume over the warn line, naming the volume, the machines whose working
// directory is on it and the largest directories the scan found, and re-raises
// it every DiskReraise while it holds. Above the stop line no new lane is
// started on that machine: the deal and the read skip it with the held reason
// the wherever headline shows, until the watermark clears.

const (
	// DiskWarnDefault is the use, in percent, at or above which the tick raises
	// the volume's one judgment (PropDiskWarn).
	DiskWarnDefault = 80
	// DiskStopDefault is the use, in percent, at or above which no new lane is
	// started on a machine whose working directory is on the volume (PropDiskStop).
	DiskStopDefault = 95
	// DiskReraiseDefault is how often the tick raises the volume's judgment again
	// while it stays over the warn line (PropDiskReraise).
	DiskReraiseDefault = 30 * time.Minute
	// The work table's properties: the disk thresholds and the re-raise cadence.
	PropDiskWarn    = "disk_warn"
	PropDiskStop    = "disk_stop"
	PropDiskReraise = "disk_reraise"
	// DiskScanDepth and DiskScanTop bound the producer's scan of the AI root: how
	// deep it walks, and how many of the largest directories it carries.
	DiskScanDepth = 2
	DiskScanTop   = 5
	// NDiskWatermark is the judgment of a volume over its warn line.
	NDiskWatermark = "a volume is over its disk watermark"
)

// DiskDir is one directory the bounded scan of the AI root measured, its
// apparent size in bytes.
type DiskDir struct {
	Path  string `json:"path"`
	Bytes uint64 `json:"bytes"`
}

// DiskReading is one measurement of the volume a beat's working directory
// lives on: when, the volume's name (its mount point), the directory measured,
// the bytes available and the volume's size (available plus used, as df counts
// it), the free and total inodes, and the largest directories of the AI root
// the bounded scan found. Free and Total, and InodesFree and InodesTotal, are
// zero when that reading could not be taken, so a partial reading is no
// reading of the part it lacks.
type DiskReading struct {
	At          time.Time `json:"at"`
	Volume      string    `json:"volume,omitempty"`
	Path        string    `json:"path,omitempty"`
	Free        uint64    `json:"free,omitempty"`
	Total       uint64    `json:"total,omitempty"`
	InodesFree  uint64    `json:"inodes_free,omitempty"`
	InodesTotal uint64    `json:"inodes_total,omitempty"`
	Top         []DiskDir `json:"top,omitempty"`
}

// Use is the volume's use, in percent, as df counts it: the blocks in use over
// those in use and those an unprivileged writer may still use, rounded up.
// Zero when no reading was taken.
func (d DiskReading) Use() float64 {
	if d.Total == 0 || d.Free >= d.Total {
		return 0
	}
	used := d.Total - d.Free
	return float64(used) / float64(d.Total) * 100
}

// InodeUse is the volume's inode use, in percent, rounded up; zero when no
// inode reading was taken.
func (d DiskReading) InodeUse() float64 {
	if d.InodesTotal == 0 || d.InodesFree > d.InodesTotal {
		return 0
	}
	used := d.InodesTotal - d.InodesFree
	return float64(used) / float64(d.InodesTotal) * 100
}

// DiskAt is the beat's disk reading while both the beat and the reading are
// fresh: a beat that measured no disk carries none, and an older reading is no
// reading.
func DiskAt(b Beat, now time.Time) (DiskReading, bool) {
	d := b.Disk
	if d == nil && b.Friend != nil {
		d = b.Friend.Disk
	}
	if d == nil || d.At.IsZero() || !b.Fresh(now) || now.Sub(d.At) > BeatDeadline {
		return DiskReading{}, false
	}
	return *d, true
}

// DiskText is the disk cell: the free space and the inode headroom while a
// fresh reading stands, with the warn or stop word when the volume is over the
// line; empty with no reading. It is shown beside the load on the fleet and
// friends tables.
func DiskText(b Beat, now time.Time, warn, stop int) string {
	d, ok := DiskAt(b, now)
	if !ok {
		return ""
	}
	text := diskBytes(d.Free) + " free, " + fmt.Sprintf("%.0f%% inodes", d.InodeUse())
	switch {
	case stop > 0 && d.Use() >= float64(stop):
		text += " disk stop"
	case warn > 0 && d.Use() >= float64(warn):
		text += " disk warn"
	}
	return text
}

// DiskHeld is why no new lane is started on a member at now: its volume's use
// is at or above the stop line, and its reading is fresh. An empty reading or
// one under the line holds nothing.
func DiskHeld(b Beat, now time.Time, stop int) string {
	if stop <= 0 {
		return ""
	}
	d, ok := DiskAt(b, now)
	if !ok || d.Use() < float64(stop) {
		return ""
	}
	return fmt.Sprintf("its volume %s is %.0f%% full (the stop line is %d%%): no new lane starts until it clears", diskVolumeName(d), d.Use(), stop)
}

// diskVolumeName is a reading's volume as a line names it: its volume, else
// the directory measured, else "the volume".
func diskVolumeName(d DiskReading) string {
	switch {
	case d.Volume != "":
		return d.Volume
	case d.Path != "":
		return d.Path
	}
	return "the volume"
}

// DiskSubject is the open-judgment subject of a volume's disk judgment.
func DiskSubject(volume string) string { return "volume:" + volume }

// diskBytes is a byte count in the units df prints, one decimal, binary.
func diskBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for v := n / unit; v >= unit && exp < 4; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}

// DiskWarn is the warn line as the work table's property holds it, else
// DiskWarnDefault.
func (s *Snapshot) DiskWarn() int { return s.diskPercent(PropDiskWarn, DiskWarnDefault) }

// DiskStop is the stop line as the work table's property holds it, else
// DiskStopDefault.
func (s *Snapshot) DiskStop() int { return s.diskPercent(PropDiskStop, DiskStopDefault) }

func (s *Snapshot) diskPercent(prop string, def int) int {
	if s != nil && s.Work != nil {
		if v, ok := s.Work.Prop(prop); ok {
			if n, err := parsePercent(v); err == nil && n >= 1 && n <= 100 {
				return n
			}
		}
	}
	return def
}

// DiskReraise is the re-raise cadence as the work table's property holds it,
// else DiskReraiseDefault.
func (s *Snapshot) DiskReraise() time.Duration {
	if s != nil && s.Work != nil {
		if v, ok := s.Work.Prop(PropDiskReraise); ok {
			if d, err := time.ParseDuration(v); err == nil && d > 0 {
				return d
			}
		}
	}
	return DiskReraiseDefault
}

func parsePercent(v string) (int, error) {
	return strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(v, "%")))
}

// diskRow is one row whose working directory is on a volume: a machine or a
// friend, its reading and the volume it is on.
type diskRow struct {
	Name    string
	Reading DiskReading
}

// diskRows is every member and friend whose beat carries a fresh disk reading,
// in row order: the machines first, then the friends, each by name. The
// friends' beats are under their names in r.Beats (fleetBeats keeps them
// there).
func diskRows(s *Snapshot, r TickReq) []diskRow {
	var out []diskRow
	for _, m := range s.Members() {
		if d, ok := DiskAt(r.Beats[m], s.Now); ok {
			out = append(out, diskRow{Name: m, Reading: d})
		}
	}
	for _, f := range s.FriendNamesSorted() {
		if d, ok := DiskAt(r.Beats[f], s.Now); ok {
			out = append(out, diskRow{Name: f, Reading: d})
		}
	}
	return out
}

// FriendNamesSorted is every friend row of the fleet, in name order.
func (s *Snapshot) FriendNamesSorted() []string {
	if s == nil || s.Fleet == nil {
		return nil
	}
	var out []string
	for _, row := range s.Fleet.Rows() {
		if f, ok := FriendOfRow(row); ok {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

// diskVolume is one volume over its warn line: its name, the rows on it, and
// the largest directories the bounded scan found.
type diskVolume struct {
	Volume string
	Rows   []string
	Top    []DiskDir
	Use    float64
	Free   uint64
	Inode  float64
}

// diskVolumes groups the rows' readings by volume, keeping one entry per
// volume over the warn line and the rows on it, in name order. Two rows on the
// same volume get one judgment naming both.
func diskVolumes(rows []diskRow, warn int) []diskVolume {
	byVolume := map[string]*diskVolume{}
	var order []string
	for _, row := range rows {
		v := diskVolumeName(row.Reading)
		if row.Reading.Use() < float64(warn) {
			continue
		}
		vol, ok := byVolume[v]
		if !ok {
			vol = &diskVolume{Volume: v, Use: row.Reading.Use(), Free: row.Reading.Free, Inode: row.Reading.InodeUse()}
			byVolume[v] = vol
			order = append(order, v)
		}
		vol.Rows = append(vol.Rows, row.Name)
		vol.Use = max(vol.Use, row.Reading.Use())
		if vol.Free == 0 || row.Reading.Free < vol.Free {
			vol.Free = row.Reading.Free
		}
		vol.Inode = max(vol.Inode, row.Reading.InodeUse())
		if len(row.Reading.Top) > 0 {
			vol.Top = row.Reading.Top
		}
	}
	slices.Sort(order)
	out := make([]diskVolume, 0, len(order))
	for _, v := range order {
		vol := byVolume[v]
		slices.Sort(vol.Rows)
		out = append(out, *vol)
	}
	return out
}

// diskWhat is the judgment's line: the volume, its use, the free space and
// inode headroom, the machines on it, and the largest directories the bounded
// scan found.
func diskWhat(vol diskVolume) string {
	what := fmt.Sprintf("volume %s is %.0f%% full (%s free, %.0f%% inodes), on %s", vol.Volume, vol.Use, diskBytes(vol.Free), vol.Inode, strings.Join(vol.Rows, ", "))
	if len(vol.Top) > 0 {
		var dirs []string
		for _, d := range vol.Top {
			dirs = append(dirs, fmt.Sprintf("%s (%s)", d.Path, diskBytes(d.Bytes)))
		}
		what += "; the largest directories under the AI root: " + strings.Join(dirs, ", ")
	} else {
		what += "; its largest directories were not scanned"
	}
	what += "; run: nova-sprint gc"
	return what
}

// diskDecisions are the judgment's: hold the machine and deal its cards
// elsewhere, acknowledge it (seen: quiet for this episode), or wait 30m (quiet
// for that running time, raised again if the volume is still over). The
// reclamation itself is named in the judgment's line (nova-sprint gc).
func diskDecisions(member string) []string {
	return []string{"fleet down " + member, "ack", "wait 30m"}
}

// diskQuietValue is the disk-quiet property of member m on volume vol at now:
// a Quiet that lapses Reraise after the tick, so the gate stands while the
// beats that name it go on and clears itself if the tick stops.
func diskQuietValue(m string, vol diskVolume, now time.Time, stop int, reraise time.Duration) string {
	bucket := now.Truncate(reraise)
	q := Quiet{Member: m, Until: bucket.Add(reraise).UTC().Truncate(time.Second),
		Reason: fmt.Sprintf("volume %s is %.0f%% full (the stop line is %d%%); no new lane starts until it clears", vol.Volume, vol.Use, stop),
		By:     MachineActor, At: bucket.UTC().Truncate(time.Second)}
	return q.value()
}

// withDiskQuiets is s with the disk quiets of every machine whose volume is
// over the stop line added to its fleet table's properties, in memory only:
// the deal and the reads of this tick skip those machines at once, before the
// tick's own plan writes the quiet (tickDisk). A snapshot with no fleet, or
// none over the line, is returned as it is.
func withDiskQuiets(s *Snapshot, r TickReq) *Snapshot {
	if s == nil || s.Fleet == nil {
		return s
	}
	stop, reraise := s.DiskStop(), s.DiskReraise()
	if stop <= 0 {
		return s
	}
	vols := diskVolumes(diskRows(s, r), s.DiskWarn())
	added := map[string]string{}
	for _, vol := range vols {
		if vol.Use < float64(stop) {
			continue
		}
		for _, m := range vol.Rows {
			if s.MemberCtl(m) == nil || IsFriendRow(m) {
				continue
			}
			added[DiskQuietPropPrefix+m] = diskQuietValue(m, vol, s.Now, stop, reraise)
		}
	}
	if len(added) == 0 {
		return s
	}
	props := s.Fleet.Props()
	for k, v := range added {
		props[k] = v
	}
	fleet := *s.Fleet
	fleet.SetProps(props)
	out := *s
	out.Fleet = &fleet
	return &out
}

// tickDisk is the tick's disk watermarks: one judgment per volume over its warn
// line, naming the rows on it and the largest directories the beat's bounded
// scan found, re-raised every DiskReraise while it holds, and closed once when
// the volume falls under the line or its readings go stale. Above the stop line
// each machine on the volume gets the disk's own quiet (DiskQuietPropPrefix), so
// the deal gives it no new lane and the readiness headline shows why; the quiet
// is refreshed every tick while it holds and cleared when the volume clears. It
// writes notes and the fleet table's disk-quiet properties, no card.
func tickDisk(s *Snapshot, r TickReq) (Plan, int) {
	if s == nil || s.Fleet == nil {
		return Plan{}, 0
	}
	var p Plan
	vols := diskVolumes(diskRows(s, r), s.DiskWarn())
	stands := map[string]bool{}
	open := map[string]Open{}
	for _, o := range s.Open {
		if o.Note.Kind == Judgment && o.Note.Type == NDiskWatermark {
			open[o.Subject()] = o
		}
	}
	reraise := s.DiskReraise()
	stop := s.DiskStop()
	held := map[string]bool{}
	for _, vol := range vols {
		subj := DiskSubject(vol.Volume)
		stands[subj] = true
		what := diskWhat(vol)
		o, isOpen := open[subj]
		if isOpen && s.Now.Sub(o.Note.At) < reraise {
			if o.Note.What != what {
				n := o.Note
				n.What = what
				p.Updates = append(p.Updates, n)
			}
		} else {
			if isOpen {
				p.Closes = append(p.Closes, o) // the episode's push is spent: raise it again
			}
			p.Notes = append(p.Notes, Note{Kind: Judgment, Type: NDiskWatermark,
				Primaries: []string{subj}, Count: 1, What: what, Who: r.who(), At: s.Now, Marked: true,
				Decisions: diskDecisions(vol.Rows[0])})
		}
		if vol.Use < float64(stop) {
			continue
		}
		for _, m := range vol.Rows {
			if s.MemberCtl(m) == nil || IsFriendRow(m) {
				continue // a friend's lane is her own daemon's; only a machine's deal is gated here
			}
			held[m] = true
			name := DiskQuietPropPrefix + m
			was, had := s.Fleet.Prop(name)
			value := diskQuietValue(m, vol, s.Now, stop, reraise)
			if !had || was != value {
				p.Props = append(p.Props, PropWrite{Table: Fleet, Name: name, Value: value, Was: was, WasAbsent: !had})
			}
		}
	}
	for subj, o := range open {
		if !stands[subj] {
			p.Closes = append(p.Closes, o)
		}
	}
	// a machine that was disk-quiet and is not now: its quiet is cleared, so the
	// deal gives it lanes again
	for _, q := range DiskQuiets(s.Fleet.Props()) {
		if held[q.Member] {
			continue
		}
		name := DiskQuietPropPrefix + q.Member
		was, had := s.Fleet.Prop(name)
		if had && was != "" {
			p.Props = append(p.Props, PropWrite{Table: Fleet, Name: name, Value: "", Was: was})
		}
	}
	return p, 0
}

// MeasureDisk reads the volume of path and, when root is a directory, the
// largest directories of a bounded scan under it. It is the producer's one
// call (fleet beat, friend beat): path is the beat's working directory, root
// its AI root. A reading that cannot be taken for path is the error.
func MeasureDisk(path, root string, now time.Time) (DiskReading, error) {
	free, total, ifree, itotal, volume, err := statDisk(path)
	if err != nil {
		return DiskReading{}, err
	}
	d := DiskReading{At: now.UTC(), Path: path, Volume: volume, Free: free, Total: total, InodesFree: ifree, InodesTotal: itotal}
	if root != "" {
		d.Top = LargestDirs(root, DiskScanDepth, DiskScanTop)
	}
	return d, nil
}

// LargestDirs is the n largest directories under root, by the total size of
// the regular files directly inside them, at most depth levels below root:
// a bounded scan, one walk, no symlink followed and no directory read twice.
// An unreadable root or entry is skipped, never an error: the scan is
// best-effort evidence for a judgment, not a gate.
func LargestDirs(root string, depth, n int) []DiskDir {
	if depth <= 0 || n <= 0 {
		return nil
	}
	var dirs []DiskDir
	var walk func(dir string, level int)
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
				if level < depth {
					walk(filepath.Join(dir, e.Name()), level+1)
				}
				continue
			}
			if info, err := e.Info(); err == nil && info.Mode().IsRegular() {
				total += uint64(info.Size())
			}
		}
		dirs = append(dirs, DiskDir{Path: dir, Bytes: total})
	}
	walk(root, 1)
	sort.Slice(dirs, func(i, j int) bool {
		if dirs[i].Bytes != dirs[j].Bytes {
			return dirs[i].Bytes > dirs[j].Bytes
		}
		return dirs[i].Path < dirs[j].Path
	})
	if len(dirs) > n {
		dirs = dirs[:n]
	}
	return dirs
}
