package fn

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// The loader reaches its store paths at the unit tier with no redis-server
// (STANDARD section 8): fnStoreFake (loader_cover_test.go) answers FUNCTION
// LIST, FUNCTION LOAD and FCALL ns_ping from its fields, so the client dials
// nothing. This rig arms that fake and runs the loader's functions against it,
// so a test names a reply and checks one answer.

// rig is one fn test's fixture: a fake store behind a client no dial reaches,
// and the loader functions that run against it.
type rig struct {
	t       *testing.T
	src     string
	haveSrc bool
	store   *fnStoreFake
	client  *redis.Client
}

// newRig arms a fake store behind a client that answers every command.
func newRig(t *testing.T) *rig {
	t.Helper()
	store := &fnStoreFake{}
	return &rig{t: t, store: store, client: fakeClient(t, store)}
}

// source is the embedded library's assembled source, read once.
func (r *rig) source() string {
	r.t.Helper()
	if !r.haveSrc {
		src, err := Source()
		require.NoError(r.t, err, "assemble the embedded library")
		r.src, r.haveSrc = src, true
	}
	return r.src
}

// libs arms the FUNCTION LIST reply and clears any list error.
func (r *rig) libs(libs ...redis.Library) *rig {
	r.store.libs, r.store.listErr = libs, nil
	return r
}

// list is a FUNCTION LIST reply holding these libraries.
func (r *rig) list(libs ...redis.Library) []redis.Library { return libs }

// our is the nova_sprint library with the given code.
func (r *rig) our(code string) redis.Library { return redis.Library{Name: Library, Code: code} }

// other is a library that is not nova_sprint, with the given code.
func (r *rig) other(code string) redis.Library { return redis.Library{Name: "other", Code: code} }

// listErr arms a FUNCTION LIST error.
func (r *rig) listErr(err error) *rig { r.store.listErr = err; return r }

// loadErr arms a FUNCTION LOAD error.
func (r *rig) loadErr(err error) *rig { r.store.loadErr = err; return r }

// pingReply arms the FCALL ns_ping reply and clears any ping error.
func (r *rig) pingReply(reply any) *rig { r.store.ping, r.store.pingErr = reply, nil; return r }

// pingErr arms an FCALL ns_ping error.
func (r *rig) pingErr(err error) *rig { r.store.pingErr = err; return r }

// load runs Load against the armed store.
func (r *rig) load() error { return Load(context.Background(), r.client) }

// loadMissing runs LoadMissing against the armed store.
func (r *rig) loadMissing() error { return LoadMissing(context.Background(), r.client) }

// loaded runs Loaded against the armed store.
func (r *rig) loaded() (string, bool, error) { return Loaded(context.Background(), r.client) }

// loads is the code of every FUNCTION LOAD the loader sent.
func (r *rig) loads() []string { return r.store.loads }

// pings is the number of FCALL ns_ping sends.
func (r *rig) pings() int { return r.store.pings }

// pattern is the libraryname pattern of the last FUNCTION LIST.
func (r *rig) pattern() string { return r.store.pattern }

// wantErr requires err to be nil when want is "", else to print want exactly.
func (r *rig) wantErr(err error, want string) {
	r.t.Helper()
	if want == "" {
		require.NoError(r.t, err)
		return
	}
	require.EqualError(r.t, err, want)
}
