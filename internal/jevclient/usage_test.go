package jevclient

import "testing"

// Stella's two witnesses on #1327 at df8cedaf, R5.

// (1) A successful answer is not evidence of reported usage. A 200 that carries
// a valid answer and no usage object at all reports NOTHING about what it
// spent, and nothing is not zero -- while an explicitly reported zero IS a
// measurement and must survive as one.
func TestUsagePresenceIsPerField(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		body      string
		wantIn    bool
		wantOut   bool
		wantInN   int
		wantOutN  int
		wantKnown bool
	}{
		"no usage object": {
			body: `{"answers": {"rung": {"type":"choice","choice":"rung-1","confidence":0.95}}}`,
		},
		"empty usage object": {
			body: `{"answers": {"rung": {"type":"choice","choice":"rung-1","confidence":0.95}}, "usage": {}}`,
		},
		"input only": {
			body:      `{"answers": {"rung": {"type":"choice","choice":"rung-1","confidence":0.95}}, "usage": {"input_tokens": 937}}`,
			wantIn:    true,
			wantInN:   937,
			wantKnown: true,
		},
		"an explicit zero is a measurement": {
			body:      `{"answers": {"rung": {"type":"choice","choice":"rung-1","confidence":0.95}}, "usage": {"input_tokens": 0, "output_tokens": 0}}`,
			wantIn:    true,
			wantOut:   true,
			wantKnown: true,
		},
		"both reported": {
			body:      `{"answers": {"rung": {"type":"choice","choice":"rung-1","confidence":0.95}}, "usage": {"input_tokens": 937, "output_tokens": 12}}`,
			wantIn:    true,
			wantOut:   true,
			wantInN:   937,
			wantOutN:  12,
			wantKnown: true,
		},
	} {
		_, usage, err := decodeResponse([]byte(tc.body))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if usage.HasInput != tc.wantIn || usage.HasOutput != tc.wantOut {
			t.Errorf("%s: presence = in:%v out:%v, want in:%v out:%v", name, usage.HasInput, usage.HasOutput, tc.wantIn, tc.wantOut)
		}
		if usage.InputTokens != tc.wantInN || usage.OutputTokens != tc.wantOutN {
			t.Errorf("%s: tokens = %d/%d, want %d/%d", name, usage.InputTokens, usage.OutputTokens, tc.wantInN, tc.wantOutN)
		}
		if usage.Known() != tc.wantKnown {
			t.Errorf("%s: known = %v, want %v", name, usage.Known(), tc.wantKnown)
		}
	}
}
