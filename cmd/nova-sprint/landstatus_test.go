package main

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLandPrintsEachPhaseAndStatusShowsIt(t *testing.T) {
	t.Parallel()
	l := &lander{
		a:   &app{},
		dry: true,
	}
	l.clearStatus()

	var buf1 bytes.Buffer
	var a app
	a.cmdLandStatus(nil, &buf1, io.Discard)
	assert.Contains(t, buf1.String(), "no land pass running")

	var out bytes.Buffer
	l.printPhase("s1", "fetch", 1, &out)
	l.printPhase("s1", "merge", 1, &out)
	l.printPhase("s1", "check", 1, &out)
	l.printPhase("s1", "queue", 1, &out)
	l.printPhase("s1", "push", 1, &out)
	l.printPhase("s1", "report", 1, &out)

	s := out.String()
	assert.Contains(t, s, "LANDING stream=s1 phase=fetch cards=1 at=")
	assert.Contains(t, s, "LANDING stream=s1 phase=merge cards=1 at=")
	assert.Contains(t, s, "LANDING stream=s1 phase=check cards=1 at=")
	assert.Contains(t, s, "LANDING stream=s1 phase=queue cards=1 at=")
	assert.Contains(t, s, "LANDING stream=s1 phase=push cards=1 at=")
	assert.Contains(t, s, "LANDING stream=s1 phase=report cards=1 at=")

	var buf2 bytes.Buffer
	a.cmdLandStatus(nil, &buf2, io.Discard)
	assert.Contains(t, buf2.String(), "LANDING PASS stream=s1 phase=report")

	l.clearStatus()
}
