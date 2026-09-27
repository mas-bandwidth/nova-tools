package testpg

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// recorder is a testing.TB that writes down a failure instead of ending the
// test that provoked it, so a test reads what Start says when it refuses.
// Everything else (TempDir, Cleanup, Helper, Logf) is the real test's.
type recorder struct {
	testing.TB
	fatal string
}

func (r *recorder) Fatal(args ...any) {
	r.fatal = fmt.Sprint(args...)
	runtime.Goexit()
}

func (r *recorder) Fatalf(format string, args ...any) {
	r.fatal = fmt.Sprintf(format, args...)
	runtime.Goexit()
}

// provoke runs f the way the testing package runs a test: on a goroutine of
// its own, which a failure ends. It returns what was written down.
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

// install is a directory that holds the named programs, as empty files:
// Binaries looks for them and runs none.
func install(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// env is an environment of its own, so no test touches the process's.
func env(pairs ...string) func(string) string {
	return func(key string) string {
		for i := 0; i+1 < len(pairs); i += 2 {
			if pairs[i] == key {
				return pairs[i+1]
			}
		}
		return ""
	}
}
