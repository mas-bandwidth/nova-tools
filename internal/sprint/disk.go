package sprint

import (
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
)

// A volume's watermark (docs/SPEC-SPRINT.md section 8, "Disk watermark"). On 2026-10-06 at
// 09:50 ET the AI volume reached 100% with no warning from the machine: the first sign was
// the bus store refusing writes and lanes dying. The fleet and friend rows carried no disk
// figure, the tick raised no judgment of the volume, and nothing refused a new lane on a full
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
)

// Disk is one reading of the volume a working directory lives on, and DirSize one of the
// largest directories under the AI root a reading names (hostload.Disk: a worker measures
// it, and a worker never imports this package).
type (
	Disk      = hostload.Disk
	DirSize   = hostload.DirSize
	DiskStat  = hostload.DiskStat
	DiskMeter = hostload.DiskMeter
)

// The meter's bounds and the AI root's variable (hostload).
const (
	DiskTop       = hostload.DiskTop
	DiskScanEvery = hostload.DiskScanEvery
	DiskScanBound = hostload.DiskScanBound
	EnvAIRoot     = hostload.EnvAIRoot
)

// ParseDisk is a beat's --disk reading (hostload.ParseDisk).
func ParseDisk(text string) (*Disk, error) { return hostload.ParseDisk(text) }

// StatVolume is the figures of the volume dir lives on (hostload.StatVolume).
func StatVolume(dir string) (DiskStat, error) { return hostload.StatVolume(dir) }

// ScanTop is the n largest directories at the top of fsys (hostload.ScanTop).
func ScanTop(fsys fs.FS, bound, n int) ([]DirSize, error) { return hostload.ScanTop(fsys, bound, n) }

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
		d.Volume, d.Host, strings.Join(v.names, ", "), d.Used(), alarm, hostload.HumanBytes(d.Free), hostload.HumanBytes(d.Size))
	if d.Inodes > 0 {
		what += fmt.Sprintf(", %s of %s inodes free (%d%% used)", hostload.HumanCount(d.InodesFree), hostload.HumanCount(d.Inodes), d.InodesUsed())
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

// diskDecisions are the judgment's: act (free bytes on the volume; the next beat's reading
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
