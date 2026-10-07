package testredis

import (
	"errors"
	"fmt"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recorder is a testing.TB that writes down a failure or a skip instead of
// ending the test that provoked it, so a test reads what Start says when it
// refuses. Everything else (TempDir, Cleanup, Helper) is the real test's.
type recorder struct {
	testing.TB
	fatal   string
	skipped string
}

func (r *recorder) Fatal(args ...any) {
	r.fatal = fmt.Sprint(args...)
	runtime.Goexit()
}

func (r *recorder) Fatalf(format string, args ...any) {
	r.fatal = fmt.Sprintf(format, args...)
	runtime.Goexit()
}

func (r *recorder) Errorf(format string, args ...any) {
	r.fatal = fmt.Sprintf(format, args...)
}

func (r *recorder) FailNow() {
	runtime.Goexit()
}

func (r *recorder) Skipf(format string, args ...any) {
	r.skipped = fmt.Sprintf(format, args...)
	runtime.Goexit()
}

// provoke runs f the way the testing package runs a test: on a goroutine of
// its own, which a failure or a skip ends. It returns what was written down.
func provoke(t *testing.T, f func(tb testing.TB)) *recorder {
	t.Helper()
	r := &recorder{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		f(r)
	}()
	<-done
	return r
}

// panics returns what f panicked with, or nil.
func panics(f func()) (said any) {
	defer func() { said = recover() }()
	f()
	return nil
}

// standing is a sentry that stands without a process: group 0, never gone.
func standing() *sentry {
	return &sentry{enlist: func() (*post, error) { return &post{gone: make(chan struct{})}, nil }}
}

func TestRecorderCapturesTestifyFailureAndEndsTheCallback(t *testing.T) {
	t.Parallel()

	continued := false
	r := provoke(t, func(tb testing.TB) {
		require.NoError(tb, errors.New("recorded cause"), "recorded failure")
		continued = true
	})
	assert.Contains(t, r.fatal, "recorded cause")
	assert.Contains(t, r.fatal, "recorded failure")
	assert.False(t, continued)
}
