package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFirstLoadReadsTheOneMinuteFigure(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]float64{"{ 1.50 2.00 2.50 }\n": 1.5, "0.42 0.30 0.10 1/200 99\n": 0.42, "": 0, "garbage": 0} {
		assert.Equal(t, want, firstLoad(in), in)
	}
}
