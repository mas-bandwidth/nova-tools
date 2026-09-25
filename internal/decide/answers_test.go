package decide

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// The two answer edges a non-author found on #1327 (22 and 23): a choice answer
// was never checked against the question that asked it, so an answer naming
// something the criteria never held -- and an answer naming NOTHING AT ALL --
// both came back as decisions at exit 0, carrying the provider's confidence as
// if the machinery had agreed with them.

// helpQuestion is the offered set from the report: continue, ask all friends,
// ask Glenn -- and nothing else.
func helpQuestion() map[string]Question {
	return map[string]Question{"help": {
		Instructions: "continue, ask all friends, or ask Glenn?",
		Choice: map[string]string{
			HelpContinue:      "carry on",
			HelpAskAllFriends: "ask every friend",
			HelpAskGlenn:      "ask Glenn, and only after the friends",
		},
	}}
}

// Edge 22: an answer of "deepseek-flash" to a question offering
// continue|ask-all-friends|ask-glenn printed HELP help=deepseek-flash conf=0.94
// at exit 0. An answer the criteria never offered is a PROVIDER ERROR, and the
// refusal names the answer and the offered set so a reader can see the gap.
func TestChoiceAnswerOutsideTheCriteriaIsAProviderError(t *testing.T) {
	t.Setenv("CARD8331_JEV_KEY", "sekret")
	srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answers":{"help":{"type":"choice","choice":"deepseek-flash","confidence":0.94}}}`))
	})
	defer srv.Close()
	c, err := New(srv.URL, "CARD8331_JEV_KEY")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _, err = c.Decide(context.Background(), "some state", helpQuestion())
	if err == nil {
		t.Fatal("an answer the question never offered came back as a decision")
	}
	for _, want := range []string{"deepseek-flash", "help", HelpContinue, HelpAskAllFriends, HelpAskGlenn} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name %q: %v", want, err)
		}
	}
}

// Edge 23: {"type":"choice","confidence":0.99} with no choice at all printed
// ROUTE rung= conf=0.99 at exit 0 -- an empty answer ABOVE the floor. An answer
// that names nothing is the same provider error, and the floor never sees it.
func TestEmptyChoiceAboveTheFloorIsAProviderError(t *testing.T) {
	t.Setenv("CARD8331_JEV_KEY", "sekret")
	srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answers":{"help":{"type":"choice","confidence":0.99}}}`))
	})
	defer srv.Close()
	c, err := New(srv.URL, "CARD8331_JEV_KEY")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _, err = c.Decide(context.Background(), "some state", helpQuestion())
	if err == nil {
		t.Fatal("an empty choice above the floor came back as a decision")
	}
	for _, want := range []string{"help", HelpAskGlenn} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name %q: %v", want, err)
		}
	}
}

// The same two edges, socket-free, plus the answers that must still pass: a
// choice the criteria hold, a score, and a noul.
func TestValidateAnswers(t *testing.T) {
	qs := helpQuestion()
	qs["risk"] = Question{Instructions: "how risky?", Score: []string{"low", "mid", "high"}}
	qs["nullq"] = Question{Instructions: "nothing to decide", Noul: true}
	for name, tc := range map[string]struct {
		answers map[string]Answer
		wantErr []string
	}{
		"an offered choice stands": {
			answers: map[string]Answer{"help": {Type: "choice", Choice: HelpAskAllFriends, Confidence: 0.94}},
		},
		"a score stands": {
			answers: map[string]Answer{"risk": {Type: "score", Score: 1, Confidence: 0.94}},
		},
		"a noul stands": {
			answers: map[string]Answer{"nullq": {Type: "noul", Noul: 0.2, Confidence: 0.2}},
		},
		"a choice outside the criteria": {
			answers: map[string]Answer{"help": {Type: "choice", Choice: "deepseek-flash", Confidence: 0.94}},
			wantErr: []string{"deepseek-flash", HelpAskGlenn},
		},
		"no choice at all": {
			answers: map[string]Answer{"help": {Type: "choice", Confidence: 0.99}},
			wantErr: []string{"help", HelpContinue},
		},
		"a choice question answered with a score": {
			answers: map[string]Answer{"help": {Type: "score", Score: 2, Confidence: 0.99}},
			wantErr: []string{"help", "choice"},
		},
		"a question nobody asked": {
			answers: map[string]Answer{"vibes": {Type: "choice", Choice: "yes", Confidence: 0.99}},
			wantErr: []string{"vibes"},
		},
	} {
		err := ValidateAnswers(qs, tc.answers)
		if len(tc.wantErr) == 0 {
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: no refusal", name)
			continue
		}
		for _, want := range tc.wantErr {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: the refusal must name %q: %v", name, want, err)
			}
		}
	}
}
