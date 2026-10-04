package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// coverBusDir writes a participants.json to a temp dir and returns the dir and the
// loaded config, for tests that need both a filesystem bus root and a roster.
func coverBusDir(t *testing.T) (string, *bus.Config) {
	t.Helper()
	dir := t.TempDir()
	raw := `{"participants":[` +
		`{"name":"Ada","lane":"from-ada","git_name":"Ada","git_email":"ada@example.com"},` +
		`{"name":"Bo","lane":"from-bo","git_name":"Bo","git_email":"bo@example.com"},` +
		`{"name":"Dana","lane":"from-dana","git_name":"Dana","git_email":"dana@example.com"},` +
		`{"name":"Rowan"}]}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, bus.ConfigName), []byte(raw), 0o644))
	c, err := bus.LoadConfig(dir)
	require.NoError(t, err)
	return dir, c
}

// replyBodyFile writes a body file with the given content into a temp path and
// returns the path. Each call gets its own t.TempDir so parallel tests do not
// collide.
func replyBodyFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "body.txt")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

// TestReplyCoverRefuseDraft pins refuseDraft: every problem prints one
// DRAFT REFUSED line with the remedy appended, and the function always
// returns exit 2 -- even when there are no problems, which is still a refusal.
func TestReplyCoverRefuseDraft(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		problems []error
		want     string
		wantCode int
	}{
		{
			name:     "one problem",
			problems: []error{errors.New("something is wrong")},
			want:     "DRAFT REFUSED: something is wrong; run: nova-bus draft -h\n",
			wantCode: 2,
		},
		{
			name:     "two problems print two lines",
			problems: []error{errors.New("first"), errors.New("second")},
			want:     "DRAFT REFUSED: first; run: nova-bus draft -h\nDRAFT REFUSED: second; run: nova-bus draft -h\n",
			wantCode: 2,
		},
		{
			name:     "no problems still returns 2 silently",
			problems: nil,
			want:     "",
			wantCode: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var stderr bytes.Buffer
			code := refuseDraft(&stderr, tc.problems)
			assert.Equal(t, tc.wantCode, code, "refuseDraft always returns exit 2")
			assert.Equal(t, tc.want, stderr.String())
		})
	}
}

// TestReplyCoverReplyResolve pins replyResolve: a known name resolves through the
// roster; an unknown name appends a problem and returns nil; a blank given
// value refuses; and a flag not given returns nil with no problem.
func TestReplyCoverReplyResolve(t *testing.T) {
	t.Parallel()
	c := coverRoster(t)
	cases := []struct {
		name         string
		value        string
		given        bool
		wantNames    []string
		wantProblems int
	}{
		{"resolves a known name", "Ada", true, []string{"Ada"}, 0},
		{"resolves multiple names", "Ada; Bo", true, []string{"Ada", "Bo"}, 0},
		{"not given returns nil", "Ada", false, nil, 0},
		{"blank given value refuses", "", true, nil, 1},
		{"unknown name refuses", "Nobody", true, nil, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var problems []error
			got := replyResolve(c, "--to", tc.value, tc.given, &problems)
			assert.Equal(t, tc.wantNames, got)
			assert.Len(t, problems, tc.wantProblems)
		})
	}
}

// TestReplyCoverReplyDraftDirProblems pins replyDraftDirProblems: a draft
// directory outside the checkout passes; one inside the checkout is refused;
// and a path that does not exist or is not a directory is refused.
func TestReplyCoverReplyDraftDirProblems(t *testing.T) {
	t.Parallel()
	t.Run("draft dir outside the bus is accepted", func(t *testing.T) {
		t.Parallel()
		busDir := t.TempDir()
		draftDir := t.TempDir()
		assert.Empty(t, replyDraftDirProblems(busDir, draftDir))
	})
	t.Run("draft dir inside the bus is refused", func(t *testing.T) {
		t.Parallel()
		busDir := t.TempDir()
		draftDir := filepath.Join(busDir, "drafts")
		require.NoError(t, os.MkdirAll(draftDir, 0o755))
		problems := replyDraftDirProblems(busDir, draftDir)
		require.Len(t, problems, 1)
		assert.Contains(t, problems[0].Error(), "is the bus checkout at")
	})
	t.Run("nonexistent draft dir is refused", func(t *testing.T) {
		t.Parallel()
		busDir := t.TempDir()
		problems := replyDraftDirProblems(busDir, filepath.Join(busDir, "nonexistent"))
		require.Len(t, problems, 1)
		assert.Contains(t, problems[0].Error(), "is not a directory")
	})
}

// TestReplyCoverResolveForCompare pins resolveForCompare: a real directory is
// absolutized and symlink-resolved; a nonexistent path falls back to the
// cleaned absolute form rather than skipping the check.
func TestReplyCoverResolveForCompare(t *testing.T) {
	t.Parallel()
	t.Run("real path resolves through EvalSymlinks", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		got := resolveForCompare(dir)
		abs, err := filepath.Abs(dir)
		require.NoError(t, err)
		want, err := filepath.EvalSymlinks(abs)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	})
	t.Run("nonexistent path falls back to cleaned absolute", func(t *testing.T) {
		t.Parallel()
		got := resolveForCompare("/nonexistent/path/here")
		assert.True(t, filepath.IsAbs(got), "a nonexistent path falls back to an absolute form")
	})
}

// TestReplyCoverReplyBodyFileProblems pins replyBodyFileProblems: a regular file
// passes; a nonexistent path is refused; and a directory is refused.
func TestReplyCoverReplyBodyFileProblems(t *testing.T) {
	t.Parallel()
	t.Run("regular file is accepted", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "body.txt")
		require.NoError(t, os.WriteFile(path, []byte("hello\n"), 0o644))
		assert.Empty(t, replyBodyFileProblems(path))
	})
	t.Run("nonexistent file is refused", func(t *testing.T) {
		t.Parallel()
		problems := replyBodyFileProblems(filepath.Join(t.TempDir(), "no-such-file"))
		require.Len(t, problems, 1)
		assert.Contains(t, problems[0].Error(), "cannot be read")
	})
	t.Run("directory is refused", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		problems := replyBodyFileProblems(dir)
		require.Len(t, problems, 1)
		assert.Contains(t, problems[0].Error(), "is not a file")
	})
}

// TestReplyCoverReplyBody pins replyBody: a valid body is read at the budget and
// one byte past it; a nonexistent file returns code 2; an empty body, an
// over-budget body, and the unedited placeholder body all return code 1.
func TestReplyCoverReplyBody(t *testing.T) {
	t.Parallel()
	t.Run("valid body returns content and zero", func(t *testing.T) {
		t.Parallel()
		path := replyBodyFile(t, "the body text\n")
		var stderr bytes.Buffer
		body, code := replyBody(path, 64, &stderr)
		assert.Equal(t, 0, code)
		assert.Equal(t, "the body text\n", string(body))
		assert.Empty(t, stderr.String())
	})
	t.Run("nonexistent file returns code 2", func(t *testing.T) {
		t.Parallel()
		var stderr bytes.Buffer
		body, code := replyBody("/nonexistent/body.txt", 64, &stderr)
		assert.Equal(t, 2, code)
		assert.Nil(t, body)
		assert.Contains(t, stderr.String(), "DRAFT REFUSED")
	})
	t.Run("empty body returns code 1", func(t *testing.T) {
		t.Parallel()
		path := replyBodyFile(t, "")
		var stderr bytes.Buffer
		body, code := replyBody(path, 64, &stderr)
		assert.Equal(t, 1, code)
		assert.Nil(t, body)
		assert.Contains(t, stderr.String(), "is empty")
	})
	t.Run("body at the budget is accepted", func(t *testing.T) {
		t.Parallel()
		path := replyBodyFile(t, strings.Repeat("a", 63)+"\n") // 64 bytes, budget 64
		var stderr bytes.Buffer
		body, code := replyBody(path, 64, &stderr)
		assert.Equal(t, 0, code)
		assert.Len(t, body, 64)
	})
	t.Run("body over the budget returns code 1", func(t *testing.T) {
		t.Parallel()
		path := replyBodyFile(t, strings.Repeat("a", 65)) // 65 bytes, budget 64
		var stderr bytes.Buffer
		body, code := replyBody(path, 64, &stderr)
		assert.Equal(t, 1, code)
		assert.Nil(t, body)
		assert.Contains(t, stderr.String(), "is over")
	})
	t.Run("placeholder body returns code 1", func(t *testing.T) {
		t.Parallel()
		path := replyBodyFile(t, bus.PlaceholderBody+"\n")
		var stderr bytes.Buffer
		body, code := replyBody(path, 64, &stderr)
		assert.Equal(t, 1, code)
		assert.Nil(t, body)
		assert.Contains(t, stderr.String(), "placeholder")
	})
}

// TestReplyCoverListingEntry pins listingEntry: a path on the listing is found
// and its entry returned; a path that is not there returns false.
func TestReplyCoverListingEntry(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		listing   []bus.OpenEntry
		path      string
		wantFound bool
		wantID    string
	}{
		{
			name: "finds entry by path",
			listing: []bus.OpenEntry{
				{ID: "bo-111111111111", Kind: bus.OpenNote, Path: "from-bo/one.md"},
				{ID: "bo-222222222222", Kind: bus.OpenNote, Path: "from-bo/two.md"},
			},
			path:      "from-bo/two.md",
			wantFound: true,
			wantID:    "bo-222222222222",
		},
		{
			name: "does not find missing path",
			listing: []bus.OpenEntry{
				{ID: "bo-111111111111", Kind: bus.OpenNote, Path: "from-bo/one.md"},
			},
			path:      "from-bo/missing.md",
			wantFound: false,
		},
		{
			name:      "empty listing finds nothing",
			listing:   nil,
			path:      "from-bo/any.md",
			wantFound: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e, found := listingEntry(tc.listing, tc.path)
			assert.Equal(t, tc.wantFound, found)
			if tc.wantFound {
				assert.Equal(t, tc.path, e.Path)
				assert.Equal(t, tc.wantID, e.ID)
			}
		})
	}
}

// TestReplyCoverReplyTargetName pins replyTargetName: a note with an ID returns
// that ID; a note without an ID returns its path, so a refusal can name what
// the tool would not.
func TestReplyCoverReplyTargetName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		note *bus.Note
		want string
	}{
		{
			name: "note with ID returns the ID",
			note: &bus.Note{
				Path:   "from-bo/note.md",
				Header: bus.Header{ID: "bo-111111111111"},
			},
			want: "bo-111111111111",
		},
		{
			name: "note without ID returns the path",
			note: &bus.Note{
				Path:   "from-bo/note.md",
				Header: bus.Header{},
			},
			want: "from-bo/note.md",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, replyTargetName(tc.note))
		})
	}
}

// TestReplyCoverCappedList pins cappedList: a list at or under the cap is fully
// quoted; an over-cap list is cut to the first eight names and the overflow is
// counted with `+<k>`.
func TestReplyCoverCappedList(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		names   []string
		wantSub string
		notWant string
	}{
		{
			name:    "two names fully quoted",
			names:   []string{"Ada", "Bo"},
			wantSub: "\"Ada\";\"Bo\"",
		},
		{
			name:    "empty list is the dash",
			names:   nil,
			wantSub: "-",
		},
		{
			name:    "exactly eight names are all printed",
			names:   []string{"A", "B", "C", "D", "E", "F", "G", "H"},
			wantSub: "\"H\"",
			notWant: "+",
		},
		{
			name:    "nine names are cut to eight plus +1",
			names:   []string{"A", "B", "C", "D", "E", "F", "G", "H", "I"},
			wantSub: ";+1",
			notWant: "\"I\"",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := cappedList(tc.names)
			assert.Contains(t, got, tc.wantSub)
			if tc.notWant != "" {
				assert.NotContains(t, got, tc.notWant)
			}
		})
	}
}

// TestReplyCoverOffTheListingReason pins offTheListingReason: each of the four
// reasons a target on the bus is not on the reader's listing produces its own
// sentence and door, and the !hasCursor case short-circuits before any of them.
func TestReplyCoverOffTheListingReason(t *testing.T) {
	t.Parallel()
	c := coverRoster(t)
	me, _ := c.Lookup("Ada")
	re := "bo-111111111111"

	// A note already answered by Ada: a note in Ada's lane carrying the target's id.
	answeredBus := &bus.Bus{
		Config: c,
		Notes: []bus.Note{{
			Path:   "from-ada/reply-to-target.md",
			Lane:   "from-ada",
			Header: bus.Header{From: "Ada", To: "Bo", Re: []string{"bo-111111111111"}},
		}},
	}

	// A note addressed to Ada but dated before the switch-day line.
	behind := &bus.Note{
		Path:   "from-bo/2025-09-09-behind.md",
		Lane:   "from-bo",
		Header: bus.Header{From: "Bo", To: "Ada", ID: "bo-111111111111"},
	}
	behindLegacy := bus.LegacyLine{
		Before: time.Date(2025, 10, 1, 0, 0, 0, 0, time.UTC),
		Text:   "2025-10-01",
	}

	// A note addressed to Ada, after the line, not answered.
	onBus := &bus.Note{
		Path:   "from-bo/2026-09-09-onbus.md",
		Lane:   "from-bo",
		Header: bus.Header{From: "Bo", To: "Ada", ID: "bo-222222222222"},
	}
	notBehindLegacy := bus.LegacyLine{
		Before: time.Date(2025, 10, 1, 0, 0, 0, 0, time.UTC),
		Text:   "2025-10-01",
	}

	// A note never addressed to Ada.
	notAddressed := &bus.Note{
		Path:   "from-bo/2026-09-09-bo-only.md",
		Lane:   "from-bo",
		Header: bus.Header{From: "Bo", To: "Bo", ID: "bo-333333333333"},
	}

	cases := []struct {
		name      string
		bus       *bus.Bus
		target    *bus.Note
		legacy    bus.LegacyLine
		hasCursor bool
		want      string
	}{
		{
			name:      "no cursor short-circuits",
			bus:       nil,
			target:    onBus,
			legacy:    notBehindLegacy,
			hasCursor: false,
			want:      "cannot be on a listing you have not got",
		},
		{
			name:      "already answered is its own reason",
			bus:       answeredBus,
			target:    behind,
			legacy:    notBehindLegacy,
			hasCursor: true,
			want:      "is a note you have already answered",
		},
		{
			name:      "never addressed is a reason",
			bus:       &bus.Bus{},
			target:    notAddressed,
			legacy:    notBehindLegacy,
			hasCursor: true,
			want:      "was never addressed to you",
		},
		{
			name:      "behind the switch-day line is a reason",
			bus:       &bus.Bus{},
			target:    behind,
			legacy:    behindLegacy,
			hasCursor: true,
			want:      "is behind your switch-day line",
		},
		{
			name:      "on the bus but not on the listing is the default reason",
			bus:       &bus.Bus{},
			target:    onBus,
			legacy:    notBehindLegacy,
			hasCursor: true,
			want:      "is on the bus and on no listing of yours",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := offTheListingReason(c, tc.bus, tc.target, me, tc.legacy, tc.hasCursor, re)
			assert.Contains(t, got, tc.want)
		})
	}
}

// TestReplyCoverReplyMovedNote pins replyMovedNote: the !moved case prints the
// resolved commit with no git call; the moved-with-error case prints the
// fallback sentence when the commit count cannot be computed.
func TestReplyCoverReplyMovedNote(t *testing.T) {
	t.Parallel()
	t.Run("nothing moved resolves against the current commit", func(t *testing.T) {
		t.Parallel()
		got := replyMovedNote("/nonexistent", "before", "deadbeefcafebabe", false)
		assert.Contains(t, got, "the bus had nothing new")
		assert.Contains(t, got, "deadbeefcafebabe")
	})
	t.Run("moved but CommitsBetween errors prints the fallback sentence", func(t *testing.T) {
		t.Parallel()
		// empty before is not a valid revision, so CommitsBetween returns an
		// error without running git.
		got := replyMovedNote("/nonexistent", "", "deadbeefcafebabe", true)
		assert.Contains(t, got, "the bus moved before this id was resolved")
		assert.Contains(t, got, "deadbeefcafebabe")
	})
}

// TestReplyCoverReplyListing pins replyListing: a cursor file that will not parse
// refuses with one DRAFT REFUSED line; a lane with no cursor returns code 0
// with an empty listing. The main path -- past the cursor check -- calls
// bus.IsAncestor and bus.ChangedSince, which are git operations; that path and
// the full listing walk are exercised by the functional tier, not here.
func TestReplyCoverReplyListing(t *testing.T) {
	t.Parallel()

	t.Run("unparseable cursor refuses with code 1", func(t *testing.T) {
		t.Parallel()
		busDir, c := coverBusDir(t)
		me, _ := c.Lookup("Ada")
		cursorDir := filepath.Join(busDir, "from-ada")
		require.NoError(t, os.MkdirAll(cursorDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(cursorDir, bus.CursorName), []byte("not-a-commit\n"), 0o644))
		var stderr bytes.Buffer
		listing, _, _, code := replyListing(busDir, c, me, &stderr)
		assert.Equal(t, 1, code)
		assert.Nil(t, listing)
		assert.Contains(t, stderr.String(), "DRAFT REFUSED")
	})

	t.Run("no cursor returns zero with an empty listing", func(t *testing.T) {
		t.Parallel()
		busDir, c := coverBusDir(t)
		me, _ := c.Lookup("Ada")
		var stderr bytes.Buffer
		listing, legacy, hasCursor, code := replyListing(busDir, c, me, &stderr)
		assert.Equal(t, 0, code)
		assert.Nil(t, listing)
		assert.False(t, hasCursor)
		assert.True(t, legacy.Before.IsZero())
	})
}

// TestReplyCoverCmdDraftReply pins cmdDraftReply's early refusal paths: every
// problem in one invocation is collected and printed as one DRAFT REFUSED line
// per problem, and refusals that need no git exit 2.
//
// The full success path -- after the problem collection -- calls
// lockCheckout, bus.HeadCommit, refreshCheckout, replyListing and bus.ReadBus,
// which are git operations; that path is exercised by the functional tier
// (reply_functional_test.go) and is not reachable here without a real checkout.
func TestReplyCoverCmdDraftReply(t *testing.T) {
	t.Parallel()

	// validSetup builds a replyOpts with all flags present and valid, pointing
	// at a real config so LoadConfig succeeds. Individual cases override fields
	// to trigger specific refusals.
	validSetup := func(t *testing.T) replyOpts {
		busDir, _ := coverBusDir(t)
		body := replyBodyFile(t, "Yes.\n")
		draftDir := t.TempDir()
		return replyOpts{
			busDir:       busDir,
			as:           "Ada",
			replyTo:      "bo-abcdef012345",
			bodyFile:     body,
			draftDir:     draftDir,
			remote:       "origin",
			branch:       "main",
			maxBodyBytes: 64,
			gitTimeout:   60,
		}
	}

	t.Run("missing required flags each refuse", func(t *testing.T) {
		t.Parallel()
		busDir, _ := coverBusDir(t)
		o := replyOpts{
			busDir:       busDir,
			as:           "Ada",
			replyTo:      "bo-abcdef012345",
			maxBodyBytes: 64,
			gitTimeout:   60,
		}
		var stdout, stderr bytes.Buffer
		code := cmdDraftReply(o, nil, &stdout, &stderr, now())
		assert.Equal(t, 2, code)
		for _, flag := range []string{"--body-file", "--draft-dir", "--remote", "--branch"} {
			assert.Contains(t, stderr.String(), flag+" is required")
		}
	})

	t.Run("reGiven and replyTo both name a thread", func(t *testing.T) {
		t.Parallel()
		o := validSetup(t)
		o.reGiven = true
		var stdout, stderr bytes.Buffer
		code := cmdDraftReply(o, nil, &stdout, &stderr, now())
		assert.Equal(t, 2, code)
		assert.Contains(t, stderr.String(), "both name a thread")
	})

	t.Run("remote beginning with a dash is refused", func(t *testing.T) {
		t.Parallel()
		o := validSetup(t)
		o.remote = "-bad"
		var stdout, stderr bytes.Buffer
		code := cmdDraftReply(o, nil, &stdout, &stderr, now())
		assert.Equal(t, 2, code)
		assert.Contains(t, stderr.String(), "begins with a dash")
	})

	t.Run("gitTimeout of zero is refused", func(t *testing.T) {
		t.Parallel()
		o := validSetup(t)
		o.gitTimeout = 0
		var stdout, stderr bytes.Buffer
		code := cmdDraftReply(o, nil, &stdout, &stderr, now())
		assert.Equal(t, 2, code)
		assert.Contains(t, stderr.String(), "--git-timeout")
	})

	t.Run("maxBodyBytes of zero is refused", func(t *testing.T) {
		t.Parallel()
		o := validSetup(t)
		o.maxBodyBytes = 0
		var stdout, stderr bytes.Buffer
		code := cmdDraftReply(o, nil, &stdout, &stderr, now())
		assert.Equal(t, 2, code)
		assert.Contains(t, stderr.String(), "--max-body-bytes")
	})

	t.Run("a newline in the subject is refused", func(t *testing.T) {
		t.Parallel()
		o := validSetup(t)
		o.subject = "one\ntwo"
		o.subjectGiven = true
		var stdout, stderr bytes.Buffer
		code := cmdDraftReply(o, nil, &stdout, &stderr, now())
		assert.Equal(t, 2, code)
		assert.Contains(t, stderr.String(), "a header line is one line")
	})

	t.Run("LoadConfig failure refuses", func(t *testing.T) {
		t.Parallel()
		o := validSetup(t)
		o.busDir = "/nonexistent/bus"
		var stdout, stderr bytes.Buffer
		code := cmdDraftReply(o, nil, &stdout, &stderr, now())
		assert.Equal(t, 2, code)
		assert.Contains(t, stderr.String(), "DRAFT REFUSED")
	})

	t.Run("unknown --as is refused", func(t *testing.T) {
		t.Parallel()
		o := validSetup(t)
		o.as = "Nobody"
		var stdout, stderr bytes.Buffer
		code := cmdDraftReply(o, nil, &stdout, &stderr, now())
		assert.Equal(t, 2, code)
		assert.Contains(t, stderr.String(), "names no one on this bus")
	})

	t.Run("--as with no lane is refused", func(t *testing.T) {
		t.Parallel()
		o := validSetup(t)
		o.as = "Rowan"
		var stdout, stderr bytes.Buffer
		code := cmdDraftReply(o, nil, &stdout, &stderr, now())
		assert.Equal(t, 2, code)
		assert.Contains(t, stderr.String(), "has no lane")
	})

	t.Run("draft dir inside the bus is refused", func(t *testing.T) {
		t.Parallel()
		o := validSetup(t)
		o.draftDir = filepath.Join(o.busDir, "scratch")
		require.NoError(t, os.MkdirAll(o.draftDir, 0o755))
		var stdout, stderr bytes.Buffer
		code := cmdDraftReply(o, nil, &stdout, &stderr, now())
		assert.Equal(t, 2, code)
		assert.Contains(t, stderr.String(), "is the bus checkout")
	})

	t.Run("nonexistent body file is refused", func(t *testing.T) {
		t.Parallel()
		o := validSetup(t)
		o.bodyFile = filepath.Join(t.TempDir(), "no-such-body")
		var stdout, stderr bytes.Buffer
		code := cmdDraftReply(o, nil, &stdout, &stderr, now())
		assert.Equal(t, 2, code)
		assert.Contains(t, stderr.String(), "--body-file")
		assert.Contains(t, stderr.String(), "cannot be read")
	})

	t.Run("empty body file returns code 1", func(t *testing.T) {
		t.Parallel()
		o := validSetup(t)
		o.bodyFile = replyBodyFile(t, "")
		var stdout, stderr bytes.Buffer
		code := cmdDraftReply(o, nil, &stdout, &stderr, now())
		assert.Equal(t, 1, code)
		assert.Contains(t, stderr.String(), "is empty")
	})

	t.Run("placeholder body file returns code 1", func(t *testing.T) {
		t.Parallel()
		o := validSetup(t)
		o.bodyFile = replyBodyFile(t, bus.PlaceholderBody+"\n")
		var stdout, stderr bytes.Buffer
		code := cmdDraftReply(o, nil, &stdout, &stderr, now())
		assert.Equal(t, 1, code)
		assert.Contains(t, stderr.String(), "placeholder")
	})
}
