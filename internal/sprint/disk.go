package sprint

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
)

// A volume's watermark (docs/SPEC-SPRINT.md section 8, "Disk watermark"). On 2026-10-06 at
// 09:50 ET the AI volume reached 100% with no warning from the machine: the first sign was
// the bus store refusing writes and lanes dying. The fleet and friend rows carried no disk
// figure, the tick raised no judgment about space, and nothing refused a new lane on a full
// disk. The owner, 2026-10-03: "alarms are effects on cards, every alarm is a pushed
// judgment"; 2026-10-06: "Don't shit in your own bed".
//
// Every beat (a machine's fleet beat, a friend's daemon's friend beat) measures the volume
// its working directory lives on (MeasureDisk: its size, its free bytes, its inodes and
// their headroom, and the largest directories under the AI root by a bounded scan) and
// carries the reading. A volume's use is the larger of its bytes used and its inodes used,
// as a percent: a volume out of inodes is as full as one out of bytes. Two thresholds, each
// a setting on the work table (set --disk-alarm, --disk-hold) with its default:
//   - above the alarm (DiskAlarmDefault, 80%) the tick writes one judgment per volume (a
//     machine and a mount point: two friends on one volume share it), naming the volume,
//     the machine, who works on it, its figures and the largest directories, its line kept
//     up to date in place; while it holds, the judgment is raised again every DiskReraise
//     (30 minutes of running time), so the alarm is pushed, never a line that went quiet;
//     under the alarm again it closes with one cleared note to the coordinator;
//   - above the hold (DiskHoldDefault, 95%) no new lane is started there: the deal and the
//     read skip that machine or friend (DiskFull), and a ready card the rest of the fleet
//     has no room for is held by the full volume, which its holder says (held.go), until
//     the watermark clears. A lane already running is left to finish.

// NDiskAlarm is the judgment of a volume whose use is above the alarm.
const NDiskAlarm = "a volume above its watermark"

const (
	// PropDiskAlarm and PropDiskHold are the thresholds as the work table holds them, a
	// percent of the volume used from 1 to 100; absent is the default.
	PropDiskAlarm = "disk_alarm"
	PropDiskHold  = "disk_hold"
	// DiskAlarmDefault and DiskHoldDefault are the thresholds while no setting is made.
	DiskAlarmDefault = 80
	DiskHoldDefault  = 95
	// DiskReraise is the running time after which a judgment of a volume still above its
	// alarm is raised again.
	DiskReraise = 30 * time.Minute
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
		out += ", " + humanCount(d.InodesFree) + " inodes free"
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

// humanCount is a count with a decimal unit: 950, 3.1K, 12M.
func humanCount(n uint64) string {
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

// DiskOf is the volume reading a beat carries, a machine's or a friend's, while the beat is
// fresh (Beat.Fresh) and the reading was taken within BeatDeadline of now: an older reading
// is no reading, as a stale beat's load is none.
func DiskOf(b Beat, now time.Time) (Disk, bool) {
	d := b.Disk
	if d == nil && b.Friend != nil {
		d = b.Friend.Disk
	}
	if d == nil || !d.Valid() || !b.Fresh(now) || now.Sub(d.At) > BeatDeadline {
		return Disk{}, false
	}
	return *d, true
}

// DisksOf is each name's fresh volume reading from the beats the tick reads, by the
// member's or the friend's name (a friend's row, friend.<name>, carries no beat of its
// own); nil when none has one. The binding sets it on the snapshot (Snapshot.Disks).
func DisksOf(beats map[string]Beat, now time.Time) map[string]Disk {
	var out map[string]Disk
	for name, b := range beats {
		if IsFriendRow(name) {
			continue
		}
		if d, ok := DiskOf(b, now); ok {
			if out == nil {
				out = map[string]Disk{}
			}
			out[name] = d
		}
	}
	return out
}

// DiskCell is a row's disk figure as the dashboard shows it (where --json --cards, disks):
// the reading's text while its beat carries a fresh one, else empty.
func DiskCell(b Beat, now time.Time) string {
	if d, ok := DiskOf(b, now); ok {
		return d.Text()
	}
	return ""
}

// DiskCells is each machine's and friend's disk figure from the beats, by name, the ones
// with none left out; nil when none has one.
func DiskCells(beats map[string]Beat, now time.Time) map[string]string {
	var out map[string]string
	for name, d := range DisksOf(beats, now) {
		if out == nil {
			out = map[string]string{}
		}
		out[name] = d.Text()
	}
	return out
}

// DiskBoundValid says v is a value a threshold takes: a whole percent from 1 to 100.
func DiskBoundValid(v string) bool {
	n, err := strconv.Atoi(v)
	return err == nil && n >= 1 && n <= 100
}

// DiskBounds is the alarm and the hold as the work table's settings hold them, each its
// default while unset or unreadable.
func (s *Snapshot) DiskBounds() (alarm, hold int) {
	alarm, hold = DiskAlarmDefault, DiskHoldDefault
	if s.Work == nil {
		return alarm, hold
	}
	if v, ok := s.Work.Prop(PropDiskAlarm); ok && DiskBoundValid(v) {
		alarm, _ = strconv.Atoi(v)
	}
	if v, ok := s.Work.Prop(PropDiskHold); ok && DiskBoundValid(v) {
		hold, _ = strconv.Atoi(v)
	}
	return alarm, hold
}

// DiskFull is why no new lane starts on the machine or friend name, "" while its volume is
// at or under the hold, or it carries no fresh reading (no reading is no evidence of a full
// volume: the beat that carries one says so).
func (s *Snapshot) DiskFull(name string) string {
	d, ok := s.Disks[name]
	if !ok {
		return ""
	}
	_, hold := s.DiskBounds()
	if d.Used() <= hold {
		return ""
	}
	return fmt.Sprintf("%s's volume %s on %s is %d%% used, above the hold of %d%%: no new lane starts there until it clears", name, d.Volume, d.Host, d.Used(), hold)
}

// withRoomOnDisk is names without the ones whose volume is above the hold.
func (s *Snapshot) withRoomOnDisk(names []string) []string {
	var out []string
	for _, n := range names {
		if s.DiskFull(n) == "" {
			out = append(out, n)
		}
	}
	return out
}

// fullOf is the names of the list whose volume is above the hold, each with why.
func (s *Snapshot) fullOf(names []string) []string {
	var out []string
	for _, n := range names {
		if why := s.DiskFull(n); why != "" {
			out = append(out, why)
		}
	}
	return out
}

// VolumeSubject is the stream a volume's judgment is keyed by: the machine and the mount
// point, its slashes and other characters outside an id's made underscores.
func VolumeSubject(host, volume string) string {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		}
		return '_'
	}, strings.Trim(volume, "/"))
	if clean == "" {
		clean = "root"
	}
	return "volume:" + host + ":" + clean
}

// volume is one volume above the alarm: its reading (the fullest of those naming it) and
// the names that work on it.
type volume struct {
	d     Disk
	names []string
}

// volumesOver is every volume whose use is above the alarm, keyed by VolumeSubject.
func volumesOver(s *Snapshot) map[string]*volume {
	alarm, _ := s.DiskBounds()
	out := map[string]*volume{}
	for _, name := range slices.Sorted(maps.Keys(s.Disks)) {
		d := s.Disks[name]
		if d.Used() <= alarm {
			continue
		}
		k := VolumeSubject(d.Host, d.Volume)
		v := out[k]
		if v == nil {
			v = &volume{d: d}
			out[k] = v
		} else if d.Used() > v.d.Used() || (d.Used() == v.d.Used() && len(d.Top) > len(v.d.Top)) {
			v.d = d
		}
		v.names = append(v.names, name)
	}
	return out
}

// diskWhat is the judgment's line: the volume, the machine, who works there, its use and
// headroom, the largest directories, and whether new lanes start there.
func diskWhat(s *Snapshot, v *volume) string {
	alarm, hold := s.DiskBounds()
	d := v.d
	what := fmt.Sprintf("volume %s on %s (used by %s) is %d%% used, above the alarm of %d%%: %s free of %s",
		d.Volume, d.Host, strings.Join(v.names, ", "), d.Used(), alarm, HumanBytes(d.Free), HumanBytes(d.Size))
	if d.Inodes > 0 {
		what += fmt.Sprintf(", %s of %s inodes free (%d%% used)", humanCount(d.InodesFree), humanCount(d.Inodes), d.InodesUsed())
	}
	root := d.Root
	if root == "" {
		root = "the AI root"
	}
	what += "; the largest under " + root + ": " + d.TopText()
	if d.Used() > hold {
		what += fmt.Sprintf("; above the hold of %d%%: no new lane starts on %s until it clears", hold, strings.Join(v.names, ", "))
	} else {
		what += fmt.Sprintf("; above the hold of %d%% no new lane starts there", hold)
	}
	return what
}

// diskDecisions are the judgment's: act (free space on the volume; the next beat's reading
// closes it), acknowledge it (quiet for the episode, the hold of new lanes standing), or
// wait 30m (quiet for that running time, raised again if it still holds).
var diskDecisions = []string{"act", "ack", "wait 30m"}

// diskConds is the tick's condition of every volume above its alarm, one per volume.
func diskConds(s *Snapshot) []cond {
	vs := volumesOver(s)
	var out []cond
	for _, k := range slices.Sorted(maps.Keys(vs)) {
		v := vs[k]
		out = append(out, cond{typ: NDiskAlarm, stream: k, streamLevel: true, what: diskWhat(s, v), decisions: diskDecisions})
	}
	return out
}

// tickDisk is the tick's volume watermarks, planned with the backlog alarms (tickAlarms): a
// judgment for each volume whose use passes its alarm; while it holds, its line updated in
// place and, every DiskReraise of running time since it was written, closed and written
// again, so it is pushed again; for each the tick closes because the use fell to the alarm
// or under (or the beat went stale), one cleared note to the coordinator.
func tickDisk(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	conds := diskConds(s)
	stands := map[string]bool{}
	for _, c := range conds {
		stands[c.stream] = true
	}
	// a judgment open DiskReraise or longer on a volume still above its alarm is closed and
	// written again in this plan: notify sees it gone and raises the condition fresh
	view := *s
	view.Open = nil
	for _, o := range s.Open {
		if o.Note.Type == NDiskAlarm && o.Note.Kind == Judgment && stands[o.Note.Stream] {
			if d, ok := r.running(s.Now, o.Note.At.UTC().Format(time.RFC3339)); ok && d >= DiskReraise {
				p.Closes = append(p.Closes, o)
				continue
			}
		}
		view.Open = append(view.Open, o)
	}
	reraised := len(p.Closes)
	due := notify(&p, &view, conds, []string{NDiskAlarm}, r)
	for _, o := range p.Closes[reraised:] {
		if stands[o.Note.Stream] {
			continue // a wait run out on a volume still over the alarm is raised again, not cleared
		}
		p.Notes = append(p.Notes, Note{Kind: Happened, Type: NAlarmCleared, Who: r.who(), To: s.Coordinator, At: s.Now,
			What: NDiskAlarm + ": " + strings.TrimPrefix(o.Note.Stream, "volume:") + " is at or under its alarm, or gives no fresh reading"})
	}
	return p, due
}
