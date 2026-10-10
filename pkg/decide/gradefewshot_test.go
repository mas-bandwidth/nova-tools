package decide

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureBackend fakes the backend: it records the state it was asked over and answers flash.
type captureBackend struct {
	mu    sync.Mutex
	state string
}

func (*captureBackend) Name() string { return "capture" }

func (c *captureBackend) Ask(_ context.Context, _ Schema, state string) (map[string]Answer, Usage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = state
	return map[string]Answer{GradeQuestion: {Type: Choice, Value: GradeFlash, P: map[string]float64{GradeFlash: 1}}}, Usage{}, nil
}

func fewShotPool(per int) []Example {
	var pool []Example
	for i := range per {
		for _, label := range GradeExampleClasses {
			pool = append(pool, Example{Card: fmt.Sprintf("%s-card-%02d", label, i), Heading: fmt.Sprintf("heading %s %02d", label, i),
				Paths: fmt.Sprintf("internal/%s/f%02d.go", label, i), Kind: "fix-red", Label: label})
		}
	}
	return pool
}

// The grade prompt carries ten examples per class drawn from the record
// (SPEC-NOVA-DECIDE section 11, the few-shot calibration): the same ten in the same order for
// the same seed and pool, none of them a held-out card, and never the brief's whole text.
func TestTheGradePromptCarriesTenExamplesPerClassFromTheRecord(t *testing.T) {
	t.Parallel()
	pool := fewShotPool(14)
	held := map[string]bool{"flash-card-00": true, "flash-card-01": true, "pro-card-03": true, "heavy-card-13": true}

	shots, err := PickExamples(pool, held, "seed-1", GradeExamplesPerClass)
	require.NoError(t, err)
	require.Len(t, shots, 10*len(GradeExampleClasses))
	for _, label := range GradeExampleClasses {
		n := 0
		for _, s := range shots {
			if s.Label == label {
				n++
			}
		}
		assert.Equal(t, 10, n, label)
	}
	for _, s := range shots {
		assert.False(t, held[s.Card], "held-out card %s is in the prompt", s.Card)
	}

	again, err := PickExamples(slices.Clone(pool), held, "seed-1", GradeExamplesPerClass)
	require.NoError(t, err)
	assert.Equal(t, shots, again, "same seed, same pool: same examples, same order")
	reversed := slices.Clone(pool)
	slices.Reverse(reversed)
	again, err = PickExamples(reversed, held, "seed-1", GradeExamplesPerClass)
	require.NoError(t, err)
	assert.Equal(t, shots, again, "the pool's order does not move the pick")
	other, err := PickExamples(pool, held, "seed-2", GradeExamplesPerClass)
	require.NoError(t, err)
	assert.NotEqual(t, shots, other, "another seed picks other examples")

	brief := "c1: rename one test\nPATHS: a.go\nthe whole brief body that must not be an example\n"
	state := GradeStateWith(brief, shots)
	fake := &captureBackend{}
	_, existing, err := Make(context.Background(), fake, GradeSchema(), state, t.TempDir()+"/g.jsonl", GradeOp("c1", state), nil, time.Date(2026, 10, 4, 18, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	assert.False(t, existing)
	assert.Equal(t, 30, strings.Count(fake.state, "\nEXAMPLE "), "thirty examples in the prompt")
	last := -1
	for _, s := range shots {
		at := strings.Index(fake.state, "\nEXAMPLE "+s.Heading+" | PATHS: "+s.Paths+" | KIND: "+s.Kind+" | LABEL: "+s.Label+"\n")
		require.Greater(t, at, last, "example %s is missing or out of order", s.Card)
		last = at
	}
	for _, label := range GradeExampleClasses {
		assert.NotContains(t, fake.state, "held-out "+label)
	}
	assert.True(t, strings.HasSuffix(fake.state, GradeState(brief)), "the card under grade comes last, whole")

	_, err = PickExamples(fewShotPool(10), map[string]bool{"pro-card-00": true}, "seed-1", GradeExamplesPerClass)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pro")
}
