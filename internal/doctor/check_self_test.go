package doctor_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/doctor"
)

func versionLine(tool, v string) string { return tool + " " + v + " linux/amd64 go1.26.6\n" }

func selfEnv(path string, dirs map[string][]string, execs map[string]string) fakeEnv {
	return fakeEnv{env: map[string]string{"PATH": path}, dirs: dirs, execs: execs, now: time.Unix(0, 0)}
}

func selfCheck(t *testing.T) doctor.Check {
	t.Helper()
	c, ok := doctor.Default.Lookup("self")
	assert.True(t, ok, "check_self.go registers the self check from its own init")
	return c
}

func TestSelfChecksTheToolsOnPathAreOneRelease(t *testing.T) {
	t.Parallel()
	c := selfCheck(t)
	ctx := context.Background()

	t.Run("one release is ok", func(t *testing.T) {
		t.Parallel()
		env := selfEnv("/b", map[string][]string{"/b": {"nova-bus", "nova-redis", "other"}}, map[string]string{
			"/b/nova-bus":   versionLine("nova-bus", "v1.2.0"),
			"/b/nova-redis": versionLine("nova-redis", "v1.2.0"),
		})
		r := c.Run(ctx, env)
		assert.Equal(t, doctor.OK, r.Status)
		assert.Contains(t, r.Evidence, "2 tools")
		assert.Contains(t, r.Evidence, "v1.2.0")
		assert.Empty(t, r.Fix)
	})
	t.Run("a skew is a fail naming the odd one", func(t *testing.T) {
		t.Parallel()
		env := selfEnv("/b", map[string][]string{"/b": {"nova-bus", "nova-redis", "nova-table"}}, map[string]string{
			"/b/nova-bus":   versionLine("nova-bus", "v1.2.0"),
			"/b/nova-redis": versionLine("nova-redis", "v1.1.0"),
			"/b/nova-table": versionLine("nova-table", "v1.2.0"),
		})
		r := c.Run(ctx, env)
		assert.Equal(t, doctor.Fail, r.Status)
		assert.Contains(t, r.Evidence, "nova-redis v1.1.0")
		assert.NotContains(t, r.Evidence, "nova-bus v")
		assert.Equal(t, "nova-update release install --from <release dir> --version v1.2.0 --bin /b", r.Fix)
	})
	t.Run("the first copy on PATH is the one that runs", func(t *testing.T) {
		t.Parallel()
		env := selfEnv("/a:/b", map[string][]string{"/a": {"nova-bus"}, "/b": {"nova-bus", "nova-redis"}}, map[string]string{
			"/a/nova-bus":   versionLine("nova-bus", "v1.2.0"),
			"/b/nova-bus":   versionLine("nova-bus", "v0.9.0"),
			"/b/nova-redis": versionLine("nova-redis", "v1.2.0"),
		})
		assert.Equal(t, doctor.OK, c.Run(ctx, env).Status)
	})
	t.Run("a tie has no majority and names every tool", func(t *testing.T) {
		t.Parallel()
		env := selfEnv("/b", map[string][]string{"/b": {"nova-bus", "nova-redis"}}, map[string]string{
			"/b/nova-bus":   versionLine("nova-bus", "v1.2.0"),
			"/b/nova-redis": versionLine("nova-redis", "v1.1.0"),
		})
		r := c.Run(ctx, env)
		assert.Equal(t, doctor.Fail, r.Status)
		assert.Contains(t, r.Evidence, "nova-bus v1.2.0")
		assert.Contains(t, r.Evidence, "nova-redis v1.1.0")
	})
	t.Run("a tool that answers no version line is a fail naming it", func(t *testing.T) {
		t.Parallel()
		env := selfEnv("/b", map[string][]string{"/b": {"nova-bus", "nova-redis"}}, map[string]string{
			"/b/nova-bus":   versionLine("nova-bus", "v1.2.0"),
			"/b/nova-redis": "usage: nova-redis\n",
		})
		r := c.Run(ctx, env)
		assert.Equal(t, doctor.Fail, r.Status)
		assert.Contains(t, r.Evidence, "nova-redis")
		assert.NotEmpty(t, r.Fix)
	})
	t.Run("no nova tool on PATH is a fail with the install verb", func(t *testing.T) {
		t.Parallel()
		r := c.Run(ctx, selfEnv("/b", map[string][]string{"/b": {"ls"}}, nil))
		assert.Equal(t, doctor.Fail, r.Status)
		assert.Contains(t, r.Evidence, "no nova-* tool on PATH")
		assert.Contains(t, r.Fix, "nova-update release install")
	})
}
