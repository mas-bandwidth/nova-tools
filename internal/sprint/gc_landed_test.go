package sprint

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// landedTree is one friend's working directory under a temp AI root, on a fixed clock.
type landedTree struct {
	t           *testing.T
	home, ai, w string
	now         time.Time
}

func newLandedTree(t *testing.T) *landedTree {
	t.Helper()
	base := t.TempDir()
	g := &landedTree{
		t:    t,
		home: filepath.Join(base, "home"),
		ai:   filepath.Join(base, "ai"),
		now:  time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC),
	}
	g.w = filepath.Join(g.ai, "ada", "working")
	require.NoError(t, os.MkdirAll(g.w, 0o755))
	require.NoError(t, os.MkdirAll(g.home, 0o755))
	return g
}

// job is a friend's job directory, <name>, with a .git and an uncommitted file, aged.
func (g *landedTree) job(name string, age time.Duration) string {
	g.t.Helper()
	dir := filepath.Join(g.w, "jobs", name)
	require.NoError(g.t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
	require.NoError(g.t, os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/sprint/x\n"), 0o644))
	require.NoError(g.t, os.WriteFile(filepath.Join(dir, "dirty.txt"), []byte("uncommitted\n"), 0o644))
	at := g.now.Add(-age)
	require.NoError(g.t, filepath.WalkDir(dir, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(p, at, at)
	}))
	return dir
}

func (g *landedTree) cards(cs ...*Card) func(string) *Card {
	m := map[string]*Card{}
	for _, c := range cs {
		m[c.ID] = c
	}
	return func(id string) *Card { return m[id] }
}

func (g *landedTree) pass(dry bool, cards func(string) *Card) GCResult {
	return GCLanded(GCReq{
		Home: g.home, AIRoot: g.ai, Now: g.now, Dry: dry,
		Volume: func(string) (int, error) { return 0, errors.New("unread") },
	}, cards)
}

func landedAt(id string, at time.Time) *Card {
	c := &Card{ID: id, Col: Landed, Fields: map[string]string{}}
	if !at.IsZero() {
		c.Fields["landed"] = at.UTC().Format(time.RFC3339)
	}
	return c
}

func droppedAt(id string, at time.Time) *Card {
	return &Card{ID: id, Fields: map[string]string{
		"outcome": "dropped", "dropped_at": at.UTC().Format(time.RFC3339),
	}}
}

func openAt(id string) *Card {
	return &Card{ID: id, Col: Working}
}

// A landed card's job goes after the hour, git state included, and a dry run
// only says so. The card's own time is the grace: thirty minutes keeps a job
// whose directory is old, and exactly one hour removes it. No stamp uses the
// directory's time.
func TestLandedJobIsRemovedAfterTheGrace(t *testing.T) {
	t.Parallel()
	g := newLandedTree(t)
	old := g.job("c~15", 2*time.Hour)
	exact := g.job("hour~15", time.Hour)
	young := g.job("soon~15", 5*time.Hour)
	unstamped := g.job("nostamp~4", 2*time.Hour)
	dryDir := g.job("dry~15", 2*time.Hour)
	cards := g.cards(
		landedAt("c", g.now.Add(-2*time.Hour)),
		landedAt("hour", g.now.Add(-time.Hour)),
		landedAt("soon", g.now.Add(-30*time.Minute)),
		landedAt("nostamp", time.Time{}),
	)
	res := g.pass(false, cards)
	text := strings.Join(res.Detail, "\n")
	assert.Contains(t, text, "GC REMOVED class=landed path="+old)
	assert.Contains(t, text, "its card c is landed")
	assert.Contains(t, text, "GC REMOVED class=landed path="+exact)
	assert.Contains(t, text, "GC REMOVED class=landed path="+unstamped)
	assert.NotContains(t, text, "path="+young)
	assert.NoDirExists(t, old)
	assert.NoDirExists(t, exact)
	assert.NoDirExists(t, unstamped)
	assert.DirExists(t, young)
	assert.DirExists(t, dryDir)
	assert.Regexp(t, `(?m)^GC landed count=3 `, strings.Join(res.Lines(), "\n"))
	assert.ElementsMatch(t, []string{old, exact, unstamped}, res.Removed, "the pass names each job removed")

	dry := g.pass(true, g.cards(landedAt("dry", g.now.Add(-2*time.Hour))))
	assert.Contains(t, strings.Join(dry.Detail, "\n"), "GC WOULD-REMOVE class=landed path="+dryDir)
	assert.Contains(t, dry.Removed, dryDir)
	assert.DirExists(t, dryDir)
}

// An open card's job stays, and so does a job the store does not know. A work
// card that is still open stays even when its primary has landed.
func TestOpenJobIsKept(t *testing.T) {
	t.Parallel()
	g := newLandedTree(t)
	open := g.job("c~15", 5*time.Hour)
	unknown := g.job("gone~15", 5*time.Hour)
	work := g.job("p.w1~15", 5*time.Hour)
	res := g.pass(false, g.cards(openAt("c"), openAt("p.w1"), landedAt("p", g.now.Add(-5*time.Hour))))
	text := strings.Join(res.Detail, "\n")
	assert.NotContains(t, text, "GC REMOVED")
	assert.DirExists(t, open)
	assert.DirExists(t, unknown)
	assert.DirExists(t, work)
	assert.Regexp(t, `(?m)^GC landed count=0 `, strings.Join(res.Lines(), "\n"))
}

// A dropped card's job goes the same way, dirty git included. A work job whose
// own card has no record goes when its primary was dropped.
func TestDroppedJobIsRemoved(t *testing.T) {
	t.Parallel()
	g := newLandedTree(t)
	drop := g.job("c~15", 2*time.Hour)
	primary := g.job("p.w2~15.g2", 2*time.Hour)
	res := g.pass(false, g.cards(
		droppedAt("c", g.now.Add(-2*time.Hour)),
		droppedAt("p", g.now.Add(-2*time.Hour)),
	))
	text := strings.Join(res.Detail, "\n")
	assert.Contains(t, text, "GC REMOVED class=landed path="+drop)
	assert.Contains(t, text, "its card c is dropped")
	assert.Contains(t, text, "GC REMOVED class=landed path="+primary)
	assert.Contains(t, text, "its card p is dropped")
	assert.NoDirExists(t, drop)
	assert.NoDirExists(t, primary)
}

// Below the floor the pure rule names the landed removal to run and then the
// caches (RunLanded, ThenCaches), and the refusal names the free space; the
// command that runs them in that order is the disk guard's, tested through its
// seam in cmd/nova-swarm. Below the stop this machine's deals are held, never
// the server's, in the one judgment. A volume that could not be read is not
// treated as empty.
func TestGuardRunsLandedBelowTheFloorAndHoldsDealsBelowTheStop(t *testing.T) {
	t.Parallel()
	floor := GuardVolumes("studio", []GuardedVolume{{
		Name: "/Volumes/nova", Path: "/Volumes/nova", Free: 100 * GB, Floor: 200 * GB, Stop: 50 * GB,
	}})
	assert.True(t, floor.RunLanded)
	assert.True(t, floor.ThenCaches)
	assert.False(t, floor.HoldDeals)
	assert.False(t, floor.ServerHeld)
	assert.Contains(t, floor.Refused, "free=100GB")
	assert.Equal(t, "REFUSED /Volumes/nova free=100GB", floor.Refused)

	stop := GuardVolumes("studio", []GuardedVolume{{
		Name: "/Volumes/nova", Path: "/Volumes/nova", Free: 10 * GB,
	}})
	assert.True(t, stop.HoldDeals)
	assert.True(t, stop.RunLanded)
	assert.False(t, stop.ServerHeld)
	assert.Equal(t, "studio /Volumes/nova at 10GB: deals held", stop.Judgment)

	unread := GuardVolumes("studio", []GuardedVolume{{
		Name: "/Volumes/nova", Path: "/Volumes/nova", Err: errors.New("unread"),
	}})
	assert.False(t, unread.RunLanded)
	assert.False(t, unread.HoldDeals)
	assert.Empty(t, unread.Rows)
	assert.Empty(t, unread.Judgment)
}

// A row names the free figure. Under the floor it is red. At the floor it is not.
func TestVolumeRowShowsTheFreeFigure(t *testing.T) {
	t.Parallel()
	act := GuardVolumes("studio", []GuardedVolume{
		{Name: "/", Path: "/", Free: 300 * GB, Floor: 200 * GB, Stop: 50 * GB},
		{Name: "/Volumes/nova", Path: "/Volumes/nova", Free: 100 * GB, Floor: 200 * GB, Stop: 50 * GB},
	})
	assert.Equal(t, []string{
		"volume=/ free=300GB",
		"volume=/Volumes/nova free=100GB red",
	}, act.Rows)
	assert.NotContains(t, act.Rows[0], "red")
	assert.Contains(t, act.Rows[0], "free=300GB")
	assert.Contains(t, act.Rows[1], "free=100GB")

	level := GuardVolumes("studio", []GuardedVolume{{
		Name: "/", Path: "/", Free: 200 * GB, Floor: 200 * GB, Stop: 50 * GB,
	}})
	assert.Equal(t, []string{"volume=/ free=200GB"}, level.Rows)
	assert.False(t, level.RunLanded)
	assert.False(t, level.HoldDeals)

	vols, err := parseDiskVolumes("/Volumes/nova=200GB:50GB")
	require.NoError(t, err)
	require.Len(t, vols, 1)
	assert.Equal(t, "/Volumes/nova", vols[0].Name)
	assert.Equal(t, uint64(200*GB), vols[0].Floor)
	assert.Equal(t, uint64(50*GB), vols[0].Stop)
	defs := DefaultGuardVolumes([]string{"/Volumes/nova/ai/ada/working", "/Volumes/nova/ai/bud/working"})
	assert.Equal(t, []GuardedVolume{
		{Name: "/", Path: "/"},
		{Name: "/Volumes/nova", Path: "/Volumes/nova"},
	}, defs)
}
