package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// A reader needs no --width: it runs the width of its machine's fleet row
// (reader-<m> is the reader on m), read with its queue every tick as a member
// reads its own (the owner, 2026-10-02: "why not just have as many readers as
// workers per-machine"). Nothing here names width as missing; the run fails
// only at the server no test has, and the start line says width=row.
func TestAReaderNeedsNoWidth(t *testing.T) {
	t.Parallel()
	var args []string
	full := memberFull(t.TempDir())
	for i := 0; i < len(full); i++ {
		if full[i] == "--width" {
			i++ // and its value
			continue
		}
		args = append(args, full[i])
	}
	args = append(args, "--reader", "--as", "reader-m1")
	var out, errb bytes.Buffer
	run(args, strings.NewReader(""), &out, &errb, time.Now())
	assert.NotContains(t, errb.String(), "--width", "a reader without --width is refused:\n%s", errb.String())
	assert.Contains(t, out.String(), "MEMBER reader as=reader-m1 width=row ", "the start line:\n%s", out.String())
}
