package swarm

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"unicode"
)

// The key file, read as DATA.
//
// The leak that taught this rule was a key reaching a place a key must never be: an API key
// in an argument is in the process table, in a log, and in a transcript somebody pastes. So
// the key lives in one file the person writes -- one line, either the bare key or
// NAME=<key> -- and this is the only code that opens it.
//
// It is read and NEVER SOURCED. Sourcing a key file executes it, and a file that is one
// line today is a file somebody appends to tomorrow. That property is kept here by there
// being no code path that could do otherwise: this binary has no shell.
//
// Nothing in the key's lifetime reaches an event line. ReadKey returns the value and an
// error that names the FILE and never its contents, and the audit over every printed
// argument covers the rest.

// ReadKey reads the first line of the key file and returns the key.
//
// The strips, in order: a leading "export ", then a leading "<VAR>=" for this worker's own
// variable name, then every whitespace character. An empty result is a refusal carrying the
// command that writes the file, because a refusal that says only "empty" has moved the
// guessing onto the reader.
func ReadKey(path, varName string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("the worker description's key_file wants the path of a file holding one line, either the bare key or %s=<key>, mode 0600; refusing to guess", envOr(varName))
	}
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("the key file %s could not be read (%s); it wants one line, either the bare key or %s=<key>: printf '%%s\\n' \"$KEY\" > %s && chmod 600 %s", path, redactedReason(err), envOr(varName), path, path)
	}
	defer f.Close()
	// Bufio's Scanner over the FIRST line only: a second line is not read, so a file
	// somebody appended to cannot contribute anything to the value.
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	line := ""
	if sc.Scan() {
		line = sc.Text()
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("the key file %s could not be read (%s)", path, redactedReason(err))
	}
	key := strings.TrimPrefix(strings.TrimLeft(line, " \t"), "export ")
	if varName != "" {
		key = strings.TrimPrefix(key, varName+"=")
	}
	key = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, key)
	if key == "" {
		return "", fmt.Errorf("the key file %s is empty; it wants one line, either the bare key or %s=<key>: printf '%%s\\n' \"$KEY\" > %s && chmod 600 %s", path, envOr(varName), path, path)
	}
	return key, nil
}

// KeyFileMode reports whether the key file is readable by anybody but its owner. It is a
// NOTE rather than a refusal: a mode this tool refused over would be this tool deciding
// how somebody else's machine is administered, and a mode nobody mentions is how a key
// ends up world-readable.
func KeyFileMode(path string) (os.FileMode, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	mode := info.Mode().Perm()
	return mode, mode&0o077 != 0
}

func envOr(varName string) string {
	if strings.TrimSpace(varName) == "" {
		return "<VAR>"
	}
	return varName
}

// redactedReason is an error's text with the key file's own CONTENT impossible in it: an
// os error carries the path and the errno and never the bytes, and this function is where
// that claim is made once rather than assumed at four call sites.
func redactedReason(err error) string {
	if err == nil {
		return "<nil>"
	}
	if pe, ok := err.(*os.PathError); ok {
		return pe.Err.Error()
	}
	return err.Error()
}
