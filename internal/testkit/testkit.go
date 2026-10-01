// Package testkit holds the test rigs that were copied from package to package:
// running a tool's entry point in process with both streams captured, and
// writing and reading the files a test sets up. Each helper fails the test
// through testify's require, so a caller's setup is one line.
//
// A tool's tests keep one adapter of their own, the entry point as a Main, and
// call its methods:
//
//	var cairn = testkit.Main(run)
//	out := cairn.OK(t, "open", "--store", dir).Stdout
//	r := cairn.Run("open") // r.Code, r.Stdout, r.Stderr
package testkit

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Main is a tool's entry point in process: the arguments after the tool's
// name, stdin, the two output streams, and the exit code it returns.
type Main func(args []string, stdin io.Reader, stdout, stderr io.Writer) int

// Result is one run of a Main: the exit code and everything each stream got.
type Result struct {
	Code           int
	Stdout, Stderr string
}

// Run calls the entry point with args and an empty stdin. It never fails the
// test: the caller asserts on the Result.
func (m Main) Run(args ...string) Result { return m.RunIn("", args...) }

// RunIn is Run with stdin holding the given text.
func (m Main) RunIn(stdin string, args ...string) Result {
	var out, errb bytes.Buffer
	code := m(args, strings.NewReader(stdin), &out, &errb)
	return Result{Code: code, Stdout: out.String(), Stderr: errb.String()}
}

// OK is Run that fails the test unless the exit code is 0, naming the
// arguments and both streams.
func (m Main) OK(t testing.TB, args ...string) Result {
	t.Helper()
	return m.OKIn(t, "", args...)
}

// OKIn is OK with stdin holding the given text.
func (m Main) OKIn(t testing.TB, stdin string, args ...string) Result {
	t.Helper()
	r := m.RunIn(stdin, args...)
	require.Equal(t, 0, r.Code, "run %q: stdout=%q stderr=%q", args, r.Stdout, r.Stderr)
	return r
}

// NoStdin is the entry point in the shape that reads no stdin, as
// testverbhelp.Run takes it.
func (m Main) NoStdin() func(args []string, stdout, stderr io.Writer) int {
	return func(args []string, stdout, stderr io.Writer) int {
		return m(args, strings.NewReader(""), stdout, stderr)
	}
}

// WriteFile writes body to path with mode 0o644, making the parent
// directories first, and fails the test on any error.
func WriteFile(t testing.TB, path, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
}

// ReadFile returns the file's contents, failing the test when it cannot be read.
func ReadFile(t testing.TB, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(raw)
}

// ReadJSON decodes the JSON file at path into a T, failing the test when the
// file cannot be read or does not decode.
func ReadJSON[T any](t testing.TB, path string) T {
	t.Helper()
	var v T
	require.NoError(t, json.Unmarshal([]byte(ReadFile(t, path)), &v), "decode %s", path)
	return v
}
