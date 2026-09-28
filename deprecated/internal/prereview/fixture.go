package prereview

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
)

// A Jev call costs money, so the 122-cell pass is allowed to run ONCE and the
// tests replay what that run recorded. A fixture is the provider's own answer
// for one pull request, on disk, named by repository and number.

// Fixture is one recorded answer.
type Fixture struct {
	Repo  string  `json:"repo"`
	PR    int     `json:"pr"`
	Score float64 `json:"score"`
	Conf  float64 `json:"confidence"`
}

// FixtureName is the file one pull request's answer is recorded under.
func FixtureName(repo string, pr int) string {
	return fmt.Sprintf("%s-%d.json", strings.ReplaceAll(repo, "/", "-"), pr)
}

// RecordFixture writes one provider answer to dir, creating dir when needed.
func RecordFixture(dir, repo string, pr int, score, conf float64) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("prereview: fixture dir: %w", err)
	}
	row, err := json.MarshalIndent(Fixture{Repo: repo, PR: pr, Score: score, Conf: conf}, "", "  ")
	if err != nil {
		return fmt.Errorf("prereview: encode fixture: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, FixtureName(repo, pr)), append(row, '\n'), 0o644)
}

// GroupFixtureName is one file group's answer in tool-PR mode: group 0 (a
// pull request asked as one question) is FixtureName.
func GroupFixtureName(repo string, pr, group int) string {
	if group <= 0 {
		return FixtureName(repo, pr)
	}
	return fmt.Sprintf("%s-%d-g%d.json", strings.ReplaceAll(repo, "/", "-"), pr, group)
}

// RecordGroupFixture writes one file group's answer (group 0: the pull
// request's) to dir.
func RecordGroupFixture(dir, repo string, pr, group int, score, conf float64) error {
	if group <= 0 {
		return RecordFixture(dir, repo, pr, score, conf)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("prereview: fixture dir: %w", err)
	}
	row, err := json.MarshalIndent(Fixture{Repo: repo, PR: pr, Score: score, Conf: conf}, "", "  ")
	if err != nil {
		return fmt.Errorf("prereview: encode fixture: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, GroupFixtureName(repo, pr, group)), append(row, '\n'), 0o644)
}

// LoadGroupFixture reads one file group's recorded answer.
func LoadGroupFixture(dir, repo string, pr, group int) (Fixture, error) {
	if group <= 0 {
		return LoadFixture(dir, repo, pr)
	}
	raw, err := os.ReadFile(filepath.Join(dir, GroupFixtureName(repo, pr, group)))
	if err != nil {
		return Fixture{}, fmt.Errorf("prereview: no recorded answer for %s#%d file group %d: %w", repo, pr, group, err)
	}
	var f Fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		return Fixture{}, fmt.Errorf("prereview: decode fixture for %s#%d file group %d: %w", repo, pr, group, err)
	}
	return f, nil
}

// LoadFixture reads one recorded answer.
func LoadFixture(dir, repo string, pr int) (Fixture, error) {
	raw, err := os.ReadFile(filepath.Join(dir, FixtureName(repo, pr)))
	if err != nil {
		return Fixture{}, fmt.Errorf("prereview: no recorded answer for %s#%d: %w", repo, pr, err)
	}
	var f Fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		return Fixture{}, fmt.Errorf("prereview: decode fixture for %s#%d: %w", repo, pr, err)
	}
	return f, nil
}

// FixtureAsker replays a recorded run. It dials nothing: a test that wants the
// provider's behaviour gets the provider's own recorded answer, and a test that
// finds no fixture fails rather than inventing one.
type FixtureAsker struct {
	Dir  string
	Repo string
	PR   int
}

// Ask returns the recorded answer for the pull request the asker was pointed at.
func (a FixtureAsker) Ask(_ context.Context, state string, qs map[string]decide.Question) (map[string]decide.Answer, error) {
	pr := a.PR
	if pr == 0 {
		var err error
		if pr, err = prFromState(state); err != nil {
			return nil, err
		}
	}
	f, err := LoadGroupFixture(a.Dir, a.Repo, pr, groupFromState(state))
	if err != nil {
		return nil, err
	}
	// The recorded answer is still validated against the question that was
	// asked, exactly as a live one is: a fixture that no longer fits the
	// question is a fixture that must be re-recorded, not one to read past.
	answers := map[string]decide.Answer{"score": {Type: "score", Score: f.Score, Confidence: f.Conf}}
	if err := decide.ValidateAnswers(qs, answers); err != nil {
		return nil, err
	}
	return answers, nil
}

// prFromState reads the pull request number back out of the state's first line,
// which State writes as "pull request <repo>#<n> at head <sha>".
func prFromState(state string) (int, error) {
	first := state
	if i := strings.IndexByte(first, '\n'); i >= 0 {
		first = first[:i]
	}
	i := strings.LastIndexByte(first, '#')
	if i < 0 {
		return 0, fmt.Errorf("prereview: cannot tell which pull request this state is about: %q", first)
	}
	rest := first[i+1:]
	n := 0
	for j := 0; j < len(rest); j++ {
		if rest[j] < '0' || rest[j] > '9' {
			rest = rest[:j]
			break
		}
	}
	if _, err := fmt.Sscanf(rest, "%d", &n); err != nil || n <= 0 {
		return 0, fmt.Errorf("prereview: cannot tell which pull request this state is about: %q", first)
	}
	return n, nil
}
