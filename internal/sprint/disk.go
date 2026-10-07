package sprint

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
)

// A volume watermark (docs/SPEC-SPRINT.md section 8, "Disk watermark"). On
// 2026-10-06 the AI volume filled with no judgment and lanes died. The beat
// records the volume of a working directory on that machine's or friend's
// control card; the tick reads the card and does not stat the host. One
// judgment per volume while use is at the warn line or past it, raised again
// every DiskReraise while it holds. Above the full line the tick holds the
// machine (status held, held_reason) before the deal, so the deal and the
// read-card placement, which both take UpMembers, start no new lane there
// until a fresh reading falls to the full line or under. A friend's seat
// carries the same reading and friendCanRead skips her.

const (
	// DiskWarnDefault is the use, block or inode, at which the tick raises
	// the volume judgment. DiskFullDefault is the line above which it holds
	// the machine. Both are percent. The work table's disk_warn and disk_full
	// override them.
	DiskWarnDefault = 80
	DiskFullDefault = 95
	// DiskReraise is how long a volume judgment stands before the tick raises
	// it again.
	DiskReraise = 30 * time.Minute
	// DiskScanBound is how many directory entries one scan of an AI root reads.
	// DiskScanKeep is how many of the largest directories a judgment names.
	DiskScanBound = 4096
	DiskScanKeep  = 8

	PropDiskWarn = "disk_warn"
	PropDiskFull = "disk_full"

	FieldDiskVolume    = "disk_volume"
	FieldDiskUse       = "disk_use"
	FieldDiskInode     = "disk_inode_use"
	FieldDiskFree      = "disk_free"
	FieldDiskInodeFree = "disk_inode_free"
	FieldDiskDirs      = "disk_dirs"
	FieldDiskAt        = "disk_at"

	// NVolumeWatermark is the one judgment of a volume past its warn line.
	// It is not in TickDecisions: that map is steps_tick.go, and the glob
	// tick*.go does not name that file, so ack does not keep it.
	NVolumeWatermark = "a volume is past its watermark"

	// PartDisk is the work-table part inserted before the deal.
	PartDisk = "disk"
)

// VolumeSubject is the open-judgment subject of one volume.
func VolumeSubject(volume string) string { return "volume:" + volume }

// DiskHoldReason is the held_reason the tick writes for a volume above the
// full line. diskHoldVolume reads it back, and a hold with any other reason
// is left alone.
func DiskHoldReason(volume string) string { return "volume full: " + volume }

func diskHoldVolume(reason string) (string, bool) {
	v, ok := strings.CutPrefix(reason, "volume full: ")
	return v, ok && v != ""
}

// VolumeReading is one beat's measure of a volume. Use and Inode are percent
// full, 0 to 100. Free and InodeFree are the beat's own words for the headroom.
type VolumeReading struct {
	Volume    string
	Use       int
	HasUse    bool
	Inode     int
	HasInode  bool
	Free      string
	InodeFree string
	Dirs      []string
	At        time.Time
}

// Level is the higher of the block use and the inode use.
func (rd VolumeReading) Level() int {
	n := 0
	if rd.HasUse {
		n = rd.Use
	}
	if rd.HasInode && rd.Inode > n {
		n = rd.Inode
	}
	return n
}

// Fields is what a beat writes on the control card. The tick reads them back
// with DiskOf.
func (rd VolumeReading) Fields() map[string]string {
	out := map[string]string{FieldDiskVolume: rd.Volume}
	if rd.HasUse {
		out[FieldDiskUse] = strconv.Itoa(rd.Use)
	}
	if rd.HasInode {
		out[FieldDiskInode] = strconv.Itoa(rd.Inode)
	}
	if rd.Free != "" {
		out[FieldDiskFree] = rd.Free
	}
	if rd.InodeFree != "" {
		out[FieldDiskInodeFree] = rd.InodeFree
	}
	if len(rd.Dirs) > 0 {
		out[FieldDiskDirs] = strings.Join(rd.Dirs, ",")
	}
	if !rd.At.IsZero() {
		out[FieldDiskAt] = stamp(rd.At)
	}
	return out
}

// VolumeStat is a volume as statfs reports it. Blocks and Bavail are the
// block totals a writer sees; Files and Ffree are the inode totals.
type VolumeStat struct {
	Bsize  uint64
	Blocks uint64
	Bavail uint64
	Files  uint64
	Ffree  uint64
}

// VolumeStatOf reads the volume of path. Tests replace it. The tick never
// calls it: a test that stats the host would hold every machine on a full
// volume. The default fails closed until disk_stat_unix.go installs the
// statfs reader.
var VolumeStatOf = func(string) (VolumeStat, error) {
	return VolumeStat{}, fmt.Errorf("volume stat is not available")
}

// MeasureVolume reads the volume of path through VolumeStatOf. Volume is the
// path; the beat may write another name on the card.
func MeasureVolume(path string) (VolumeReading, error) {
	st, err := VolumeStatOf(path)
	if err != nil {
		return VolumeReading{}, err
	}
	rd := VolumeReading{Volume: path}
	if st.Blocks > 0 {
		free := st.Bavail
		if free > st.Blocks {
			free = st.Blocks
		}
		rd.Use = int((st.Blocks - free) * 100 / st.Blocks)
		rd.HasUse = true
		if st.Bsize > 0 {
			rd.Free = strconv.FormatUint(free*st.Bsize, 10)
		}
	}
	if st.Files > 0 {
		free := st.Ffree
		if free > st.Files {
			free = st.Files
		}
		rd.Inode = int((st.Files - free) * 100 / st.Files)
		rd.HasInode = true
		rd.InodeFree = strconv.FormatUint(free, 10)
	}
	return rd, nil
}

// ScanLargest names up to DiskScanKeep directories directly under root, the
// ones whose walked bytes are largest. It reads at most bound entries and
// does not follow a link. bound 0 is DiskScanBound.
func ScanLargest(root string, bound int) ([]string, error) {
	if bound <= 0 {
		bound = DiskScanBound
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	type weighed struct {
		name string
		n    int64
	}
	var dirs []weighed
	left := bound
	for _, e := range entries {
		if left <= 0 {
			break
		}
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		n, walked := weighDir(filepath.Join(root, e.Name()), left)
		left -= walked
		dirs = append(dirs, weighed{e.Name(), n})
	}
	slices.SortFunc(dirs, func(a, b weighed) int {
		if c := cmp.Compare(b.n, a.n); c != 0 {
			return c
		}
		return cmp.Compare(a.name, b.name)
	})
	if len(dirs) > DiskScanKeep {
		dirs = dirs[:DiskScanKeep]
	}
	out := make([]string, len(dirs))
	for i, d := range dirs {
		out[i] = d.name
	}
	return out, nil
}

// weighDir sums file bytes under path and returns how many entries it read,
// stopping at left.
func weighDir(path string, left int) (int64, int) {
	if left <= 0 {
		return 0, 0
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return 0, 1
	}
	var n int64
	walked := 1
	for _, e := range entries {
		if walked >= left {
			break
		}
		walked++
		info, err := e.Info()
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if e.IsDir() {
			sub, w := weighDir(filepath.Join(path, e.Name()), left-walked)
			n += sub
			walked += w
			continue
		}
		n += info.Size()
	}
	return n, walked
}

func fieldPct(ctl *Card, name string) (int, bool) {
	if ctl == nil || !ctl.Has(name) {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(ctl.F(name)))
	if err != nil || n < 0 || n > 100 {
		return 0, false
	}
	return n, true
}

// DiskOf is the control card's volume reading. A card with no volume, or with
// neither use field, is no reading. disk_at absent means the fields are the
// beat's current word. disk_at set and older than a beat window, or unreadable,
// is no reading: a stale beat does not clear a hold and does not raise one.
func DiskOf(ctl *Card, now time.Time) (VolumeReading, bool) {
	if ctl == nil {
		return VolumeReading{}, false
	}
	vol := strings.TrimSpace(ctl.F(FieldDiskVolume))
	if vol == "" {
		return VolumeReading{}, false
	}
	use, hasUse := fieldPct(ctl, FieldDiskUse)
	ino, hasIno := fieldPct(ctl, FieldDiskInode)
	if !hasUse && !hasIno {
		return VolumeReading{}, false
	}
	if ctl.Has(FieldDiskAt) {
		t, err := time.Parse(time.RFC3339, ctl.F(FieldDiskAt))
		if err != nil || now.Sub(t) > BeatDeadline || t.After(now.Add(time.Second)) {
			return VolumeReading{}, false
		}
	}
	return VolumeReading{
		Volume: vol, Use: use, HasUse: hasUse, Inode: ino, HasInode: hasIno,
		Free: ctl.F(FieldDiskFree), InodeFree: ctl.F(FieldDiskInodeFree),
		Dirs: Split(ctl.F(FieldDiskDirs)),
	}, true
}

// diskLines is the warn line and the full line for this snapshot: the work
// table's disk_warn and disk_full when they are percents, else the defaults.
// The full line is never under the warn line.
func diskLines(s *Snapshot) (warn, full int) {
	warn, full = DiskWarnDefault, DiskFullDefault
	if s == nil || s.Work == nil {
		return warn, full
	}
	if n, ok := propPct(s.Work, PropDiskWarn); ok {
		warn = n
	}
	if n, ok := propPct(s.Work, PropDiskFull); ok {
		full = n
	}
	if full < warn {
		full = warn
	}
	return warn, full
}

func propPct(t *Table, name string) (int, bool) {
	v, ok := t.Prop(name)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 || n > 100 {
		return 0, false
	}
	return n, true
}

// pastWarn is use at the warn line or past it. pastFull is use above the full
// line: the full line itself warns and does not yet hold.
func pastWarn(level, warn int) bool { return level >= warn }
func pastFull(level, full int) bool { return level > full }

// DiskFigure is the row's disk words: the beat's headroom text, the inode
// headroom, and the held reason when the hold is a volume's.
func DiskFigure(ctl *Card) string {
	if ctl == nil {
		return ""
	}
	var parts []string
	if v := ctl.F(FieldDiskVolume); v != "" {
		parts = append(parts, v)
	}
	if v := ctl.F(FieldDiskFree); v != "" {
		parts = append(parts, v)
	}
	if v := ctl.F(FieldDiskInodeFree); v != "" {
		parts = append(parts, "inodes "+v)
	}
	if _, ok := diskHoldVolume(ctl.F(FieldHeldReason)); ok {
		parts = append(parts, ctl.F(FieldHeldReason))
	}
	return strings.Join(parts, " ")
}

// DiskHeadline is the readiness line: one clause per volume that has a figure
// or a disk hold, the held reason included when the tick holds it.
func DiskHeadline(s *Snapshot) string {
	if s == nil || s.Fleet == nil {
		return ""
	}
	type clause struct {
		vol, free, inodes, reason string
		held                      bool
	}
	got := map[string]*clause{}
	var order []string
	add := func(ctl *Card) {
		if ctl == nil {
			return
		}
		vol := strings.TrimSpace(ctl.F(FieldDiskVolume))
		reason := ctl.F(FieldHeldReason)
		hv, held := diskHoldVolume(reason)
		if vol == "" {
			vol = hv
		}
		if vol == "" {
			return
		}
		if ctl.F(FieldDiskFree) == "" && ctl.F(FieldDiskInodeFree) == "" && !held && ctl.F(FieldDiskUse) == "" && ctl.F(FieldDiskInode) == "" {
			return
		}
		c := got[vol]
		if c == nil {
			c = &clause{vol: vol}
			got[vol] = c
			order = append(order, vol)
		}
		if c.free == "" {
			c.free = ctl.F(FieldDiskFree)
		}
		if c.inodes == "" {
			c.inodes = ctl.F(FieldDiskInodeFree)
		}
		if held {
			c.held, c.reason = true, reason
		}
	}
	for _, m := range s.Members() {
		add(s.MemberCtl(m))
	}
	for _, row := range s.Fleet.Rows() {
		if _, ok := FriendOfRow(row); ok {
			add(s.MemberCtl(row))
		}
	}
	slices.Sort(order)
	var lines []string
	for _, vol := range order {
		c := got[vol]
		line := "disk " + c.vol
		if c.free != "" {
			line += " " + c.free
		}
		if c.inodes != "" {
			line += " inodes " + c.inodes
		}
		if c.held {
			line += " held (" + c.reason + ")"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "; ")
}

type diskWho struct {
	name  string
	ctl   *Card
	level int
	dirs  []string
}

// TickDisk is the tick's volume part. It returns an empty plan when no card
// and no friend seat carries a reading and no disk judgment or disk hold is
// open, so a tick that never measured a volume changes nothing.
func TickDisk(s *Snapshot, r TickReq) (Plan, int) {
	if s == nil || s.Fleet == nil {
		return Plan{}, 0
	}
	if !diskCares(s, r) {
		return Plan{}, 0
	}
	warn, full := diskLines(s)
	fresh := map[string][]diskWho{}
	add := func(vol string, w diskWho) {
		if vol == "" {
			return
		}
		for i, e := range fresh[vol] {
			if e.name != w.name {
				continue
			}
			if w.level > e.level {
				e.level = w.level
			}
			e.dirs = append(e.dirs, w.dirs...)
			if e.ctl == nil {
				e.ctl = w.ctl
			}
			fresh[vol][i] = e
			return
		}
		fresh[vol] = append(fresh[vol], w)
	}
	for _, m := range s.Members() {
		ctl := s.MemberCtl(m)
		rd, ok := DiskOf(ctl, s.Now)
		if !ok {
			continue
		}
		add(rd.Volume, diskWho{name: "machine " + m, ctl: ctl, level: rd.Level(), dirs: rd.Dirs})
	}
	for _, f := range r.Friends {
		if f.DiskVolume == "" {
			continue
		}
		level := f.DiskUse
		if f.DiskInode > level {
			level = f.DiskInode
		}
		var ctl *Card
		if s.Fleet != nil {
			ctl = s.MemberCtl(FriendRow(f.Name))
		}
		add(f.DiskVolume, diskWho{name: "friend " + f.Name, ctl: ctl, level: level, dirs: Split(f.DiskDirs)})
	}
	for _, c := range s.Fleet.Cards() {
		row, ok := strings.CutPrefix(c.ID, "ctl-")
		if !ok {
			continue
		}
		if _, friend := FriendOfRow(row); !friend {
			continue
		}
		rd, ok := DiskOf(c, s.Now)
		if !ok {
			continue
		}
		name, _ := FriendOfRow(row)
		add(rd.Volume, diskWho{name: "friend " + name, ctl: c, level: rd.Level(), dirs: rd.Dirs})
	}

	vols := map[string]bool{}
	for v := range fresh {
		vols[v] = true
	}
	for _, o := range append(append([]Open{}, s.Open...), s.Acked...) {
		if v, ok := diskSubject(o); ok {
			vols[v] = true
		}
	}
	for _, m := range s.Members() {
		if v, ok := diskHoldVolume(s.MemberCtl(m).F(FieldHeldReason)); ok {
			vols[v] = true
		}
	}
	names := make([]string, 0, len(vols))
	for v := range vols {
		names = append(names, v)
	}
	slices.Sort(names)

	var p Plan
	for _, vol := range names {
		whos := fresh[vol]
		slices.SortFunc(whos, func(a, b diskWho) int { return strings.Compare(a.name, b.name) })
		have := len(whos) > 0
		level := 0
		for _, w := range whos {
			if w.level > level {
				level = w.level
			}
		}
		what := ""
		if have {
			what = diskWhat(vol, whos, pastFull(level, full))
		}
		if have && !pastWarn(level, warn) {
			diskClose(&p, s, vol)
			diskRelease(&p, s, vol)
			continue
		}
		if have && pastWarn(level, warn) {
			if o, ok := diskFind(s.Open, vol); ok {
				diskKeep(&p, s, r, o, what)
			} else if _, acked := diskFind(s.Acked, vol); !acked {
				p.Notes = append(p.Notes, Note{Kind: Judgment, Type: NVolumeWatermark, Primaries: []string{VolumeSubject(vol)}, Count: 1, What: what, Who: r.who(), At: s.Now, Marked: true})
			}
		} else if o, ok := diskFind(s.Open, vol); ok {
			diskKeep(&p, s, r, o, "")
		}
		if have && pastFull(level, full) {
			for _, w := range whos {
				if u, ok := diskHoldUnit(s, w.ctl, vol); ok {
					p.Units = append(p.Units, u)
				}
			}
		} else if have {
			diskRelease(&p, s, vol)
		}
	}
	return p, 0
}

func diskCares(s *Snapshot, r TickReq) bool {
	for _, m := range s.Members() {
		ctl := s.MemberCtl(m)
		if ctl == nil {
			continue
		}
		if ctl.F(FieldDiskVolume) != "" {
			return true
		}
		if _, ok := diskHoldVolume(ctl.F(FieldHeldReason)); ok {
			return true
		}
	}
	for _, f := range r.Friends {
		if f.DiskVolume != "" {
			return true
		}
	}
	for _, c := range s.Fleet.Cards() {
		row, ok := strings.CutPrefix(c.ID, "ctl-")
		if !ok {
			continue
		}
		if _, friend := FriendOfRow(row); friend && c.F(FieldDiskVolume) != "" {
			return true
		}
	}
	for _, o := range s.Open {
		if o.Note.Type == NVolumeWatermark {
			return true
		}
	}
	for _, o := range s.Acked {
		if o.Note.Type == NVolumeWatermark {
			return true
		}
	}
	return false
}

func diskSubject(o Open) (string, bool) {
	if o.Note.Type != NVolumeWatermark {
		return "", false
	}
	v, ok := strings.CutPrefix(o.Subject(), "volume:")
	return v, ok && v != ""
}

func diskFind(open []Open, vol string) (Open, bool) {
	sub := VolumeSubject(vol)
	for _, o := range open {
		if o.Note.Type == NVolumeWatermark && o.Subject() == sub {
			return o, true
		}
	}
	return Open{}, false
}

func diskWhat(vol string, whos []diskWho, full bool) string {
	line := "the warn line"
	tail := ""
	if full {
		line = "the full line"
		tail = "; no new lane until it clears"
	}
	seen := map[string]bool{}
	var who, dirs []string
	for _, w := range whos {
		if w.name != "" && !seen[w.name] {
			seen[w.name] = true
			who = append(who, w.name)
		}
		for _, d := range w.dirs {
			if d != "" && !seen["dir:"+d] {
				seen["dir:"+d] = true
				dirs = append(dirs, d)
			}
		}
	}
	slices.Sort(who)
	slices.Sort(dirs)
	where := "nowhere named"
	if len(who) > 0 {
		where = strings.Join(who, ", ")
	}
	largest := "(none named)"
	if len(dirs) > 0 {
		largest = strings.Join(dirs, ", ")
	}
	return fmt.Sprintf("volume %s on %s is past %s; largest under the AI root: %s%s", vol, where, line, largest, tail)
}

func diskKeep(p *Plan, s *Snapshot, r TickReq, o Open, what string) {
	n := o.Note
	base := n.ReviewSet
	if base.IsZero() {
		base = n.At
	}
	quiet := !n.Review.IsZero() && !DueNow(s.Now, n.Review, base, r.Stopped)
	d, ok := r.running(s.Now, stamp(n.At))
	k := 0
	if ok {
		k = int(d / DiskReraise)
	}
	if what == "" {
		what = n.What
	}
	if !quiet && ok && k > n.Before {
		n.Before, n.What = k, what
		p.Updates = append(p.Updates, n)
		to := s.Coordinator
		if to == "" {
			to = "coordinator"
		}
		p.Notes = append(p.Notes, Note{Kind: Happened, Type: NRaisedAgain, Stream: n.Stream, Primaries: n.Primaries, Count: n.Count, Who: r.who(), To: to, At: s.Now,
			What: fmt.Sprintf("%s (%s) still holds, open since %s: %s", n.ID, n.Type, stamp(n.At), what)})
		return
	}
	if n.What != what {
		n.What = what
		p.Updates = append(p.Updates, n)
	}
}

func diskClose(p *Plan, s *Snapshot, vol string) {
	for _, set := range [][]Open{s.Open, s.Acked} {
		for _, o := range set {
			if v, ok := diskSubject(o); ok && v == vol {
				p.Closes = append(p.Closes, o)
			}
		}
	}
}

func diskHoldUnit(s *Snapshot, ctl *Card, vol string) (Unit, bool) {
	if ctl == nil || s == nil {
		return Unit{}, false
	}
	reason := DiskHoldReason(vol)
	if cur := ctl.F(FieldHeldReason); cur != "" && cur != reason {
		return Unit{}, false
	}
	if ctl.F("held") != "" && ctl.F(FieldHeldReason) == "" {
		return Unit{}, false
	}
	set := map[string]string{}
	if ctl.F("held") == "" {
		set["held"] = stamp(s.Now)
	}
	if ctl.F(FieldHeldReason) != reason {
		set[FieldHeldReason] = reason
	}
	if ctl.F(FieldHeldFinish) == "" {
		set[FieldHeldFinish] = stamp(s.Now)
	}
	if ctl.F("status") == Up {
		set["status"] = Held
		set["since"] = stamp(s.Now)
	}
	if len(set) == 0 {
		return Unit{}, false
	}
	return Unit{Key: ctl.ID, Changes: []Change{change(Fleet, setEntry(ctl, set))}, Moved: ctl.ID + " held: " + reason}, true
}

func diskRelease(p *Plan, s *Snapshot, vol string) {
	seen := map[string]bool{}
	release := func(ctl *Card) {
		if ctl == nil || seen[ctl.ID] {
			return
		}
		got, ok := diskHoldVolume(ctl.F(FieldHeldReason))
		if !ok || got != vol {
			return
		}
		seen[ctl.ID] = true
		set := map[string]string{}
		if ctl.F("status") == Held {
			set["status"] = Up
			set["since"] = stamp(s.Now)
		}
		p.Units = append(p.Units, Unit{Key: ctl.ID, Changes: []Change{change(Fleet, setEntry(ctl, set, "held", FieldHeldReason, FieldHeldFinish))},
			Moved: ctl.ID + " released: " + DiskHoldReason(vol) + " cleared"})
	}
	for _, m := range s.Members() {
		release(s.MemberCtl(m))
	}
	for _, row := range s.Fleet.Rows() {
		if _, ok := FriendOfRow(row); ok {
			release(s.MemberCtl(row))
		}
	}
}

// friendVolumeFull says the friend's seat, or her control card, is above the
// full line. friendCanRead skips her, so the deal and her reads start no lane.
func friendVolumeFull(s *Snapshot, f FriendSeat) bool {
	_, full := diskLines(s)
	if f.DiskVolume != "" {
		level := f.DiskUse
		if f.DiskInode > level {
			level = f.DiskInode
		}
		if pastFull(level, full) {
			return true
		}
	}
	if s == nil {
		return false
	}
	rd, ok := DiskOf(s.MemberCtl(FriendRow(f.Name)), s.Now)
	return ok && pastFull(rd.Level(), full)
}

// installDiskPart puts TickDisk in front of the deal. The deal's member list
// is UpMembers in steps_tick.go, which tick*.go does not match, so this part
// holds the machine first and the deal reads the hold. init runs after
// TickParts is frozen, so both the live tables and the frozen list are patched.
func init() { installDiskPart() }

func installDiskPart() {
	insert := func(parts []TickPartDef) []TickPartDef {
		for _, p := range parts {
			if p.Name == PartDisk {
				return parts
			}
		}
		for i, p := range parts {
			if p.Name == "deal" {
				out := make([]TickPartDef, 0, len(parts)+1)
				out = append(out, parts[:i]...)
				out = append(out, TickPartDef{Name: PartDisk, Fn: TickDisk})
				return append(out, parts[i:]...)
			}
		}
		return parts
	}
	for i := range TickTables {
		if TickTables[i].Table == Work {
			TickTables[i].Parts = insert(TickTables[i].Parts)
		}
	}
	TickParts = insert(TickParts)
	diskPtr := reflect.ValueOf(TickDisk).Pointer()
	dealPtr := reflect.ValueOf(TickDeal).Pointer()
	for _, fn := range heldParts {
		if fn != nil && reflect.ValueOf(fn).Pointer() == diskPtr {
			return
		}
	}
	for i, fn := range heldParts {
		if fn != nil && reflect.ValueOf(fn).Pointer() == dealPtr {
			heldParts = append(heldParts[:i], append([]TickPartFn{TickDisk}, heldParts[i:]...)...)
			break
		}
	}
}
