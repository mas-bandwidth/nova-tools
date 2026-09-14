package main

import (
	"encoding/base64"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func writeChunks(t *testing.T, w interface{ Write([]byte) (int, error) }, raw []byte, step int) {
	t.Helper()
	for len(raw) != 0 {
		n := step
		if n > len(raw) {
			n = len(raw)
		}
		if got, err := w.Write(raw[:n]); err != nil || got != n {
			t.Fatalf("Write(%d) got=%d err=%v", n, got, err)
		}
		raw = raw[n:]
	}
}

func TestTrimmedTextMatchesCurrentTrimSpaceAcrossChunks(t *testing.T) {
	invalid := append([]byte(" \u2003title\n\n"), 0xff)
	invalid = append(invalid, []byte(" body\u2002 ")...)
	cases := [][]byte{
		[]byte(" \t\u2003title\nbody\u2002 "),
		invalid,
		[]byte("\u2003\u2002\t\n"),
	}
	for n, raw := range cases {
		for _, step := range []int{1, 2, 3, 7} {
			t.Run(fmt.Sprintf("case-%d/step-%d", n, step), func(t *testing.T) {
				got := newTrimmedText(4096)
				writeChunks(t, got, raw, step)
				got.Finish()
				want := strings.TrimSpace(string(raw))
				if got.String() != want || got.tooLarge {
					t.Fatalf("got=%q tooLarge=%t want=%q", got.String(), got.tooLarge, want)
				}
			})
		}
	}
}

func TestTrimmedTextBudgetMatchesTrimSpaceAcrossChunks(t *testing.T) {
	invalid := append([]byte(" \u2003alpha\t"), 0xff)
	invalid = append(invalid, []byte(" \u2002beta\n ")...)
	incomplete := append([]byte("\tleft "), 0xe2, 0x80)
	incomplete = append(incomplete, []byte(" right\u2003 ")...)
	cases := [][]byte{
		[]byte(" \u2003alpha\t beta\u2002 "),
		invalid,
		incomplete,
		[]byte(" \t\u2002\n"),
	}
	for n, raw := range cases {
		want := strings.TrimSpace(string(raw))
		limits := []int{len(want), len(want) + 1}
		if len(want) > 0 {
			limits = append([]int{len(want) - 1}, limits...)
		}
		for _, limit := range limits {
			for _, step := range []int{1, 2, 3, 4, 7, 19} {
				t.Run(fmt.Sprintf("case-%d/limit-%d/step-%d", n, limit, step), func(t *testing.T) {
					got := newTrimmedText(limit)
					writeChunks(t, got, raw, step)
					got.Finish()
					if got.tooLarge != (len(want) > limit) {
						t.Fatalf("tooLarge=%t want length=%d limit=%d", got.tooLarge, len(want), limit)
					}
					if !got.tooLarge && got.String() != want {
						t.Fatalf("got=%q want=%q", got.String(), want)
					}
				})
			}
		}
	}
}

func TestTrimmedTextDefersTerminalWhitespaceWithoutRetainingIt(t *testing.T) {
	terminal := strings.Repeat("\u2003", 48*1024)
	for _, raw := range [][]byte{
		[]byte(terminal + " title \t"),
		[]byte("body" + terminal),
	} {
		text := newTrimmedText(64)
		writeChunks(t, text, raw, 1)
		text.Finish()
		if text.tooLarge || text.String() != strings.TrimSpace(string(raw)) {
			t.Fatalf("terminal whitespace got=%q tooLarge=%t", text.String(), text.tooLarge)
		}
	}

	internal := newTrimmedText(64)
	writeChunks(t, internal, []byte("x"+strings.Repeat("\u2003", 64)+"y"), 1)
	internal.Finish()
	if !internal.tooLarge {
		t.Fatal("internal over-budget whitespace was treated as terminal")
	}
}

func TestTrimmedTextRetainsOnlyBudgetedWhitespace(t *testing.T) {
	const limit = 4096
	chunk := []byte(strings.Repeat(" ", 32*1024))

	leading := newTrimmedText(limit)
	for range 256 {
		_, _ = leading.Write(chunk)
	}
	if len(leading.out) != 0 || len(leading.pending) != 0 || cap(leading.out) != 0 || cap(leading.pending) != 0 {
		t.Fatalf("leading whitespace retained out=%d/%d pending=%d/%d", len(leading.out), cap(leading.out), len(leading.pending), cap(leading.pending))
	}
	_, _ = leading.Write([]byte("x"))
	leading.Finish()
	if leading.String() != "x" || leading.tooLarge {
		t.Fatalf("leading result=%q tooLarge=%t", leading.String(), leading.tooLarge)
	}

	trailing := newTrimmedText(limit)
	_, _ = trailing.Write([]byte("x"))
	for range 256 {
		_, _ = trailing.Write(chunk)
	}
	if len(trailing.out) > limit || len(trailing.pending) > limit || len(trailing.partial) >= utf8.UTFMax {
		t.Fatalf("retained lengths out=%d pending=%d partial=%d", len(trailing.out), len(trailing.pending), len(trailing.partial))
	}
	// append growth is implementation-defined, but retained capacity remains a
	// small multiple of the caller's packet budget, never of the 8 MiB stream.
	if cap(trailing.out) > 2*limit || cap(trailing.pending) > 2*limit {
		t.Fatalf("retained capacity out=%d pending=%d limit=%d", cap(trailing.out), cap(trailing.pending), limit)
	}
	if !trailing.pendingOverLimit {
		t.Fatal("large deferred terminal whitespace did not saturate the bound")
	}
	trailing.Finish()
	if trailing.String() != "x" || trailing.tooLarge {
		t.Fatalf("terminal stream result=%q tooLarge=%t", trailing.String(), trailing.tooLarge)
	}
}

func TestGitAuthorStreamMatchesCurrentCutTrimAndFormat(t *testing.T) {
	title := append([]byte(" \u2003subject "), 0xff)
	body := append([]byte(" \tbody\nmore \u2002"), 0xfe)
	raw := append(append(append([]byte{}, title...), '\n', '\n'), body...)
	wantTitle, wantBody, _ := strings.Cut(string(raw), "\n\n")
	wantTitle, wantBody = strings.TrimSpace(wantTitle), strings.TrimSpace(wantBody)
	for _, step := range []int{1, 2, 3, 11} {
		t.Run(fmt.Sprintf("step-%d", step), func(t *testing.T) {
			stream := newGitAuthorStream(4096)
			writeChunks(t, stream, raw, step)
			stream.finish()
			got := materializeAuthor(stream.title, stream.body, 4096)
			if got.err != nil || got.title != wantTitle || got.body != wantBody {
				t.Fatalf("got=%#v want title=%q body=%q", got, wantTitle, wantBody)
			}
			if formatThisHead(got.title, got.body) != formatThisHead(wantTitle, wantBody) {
				t.Fatal("stream changed quoted author section")
			}
		})
	}
}

func TestGitAuthorStreamRandomizedDifferential(t *testing.T) {
	random := rand.New(rand.NewSource(20260914))
	for n := 0; n < 128; n++ {
		raw := make([]byte, random.Intn(512))
		for i := range raw {
			raw[i] = byte(random.Intn(256))
		}
		// Include both UTF-8 fragments and complete whitespace runes in the
		// deterministic corpus; random bytes independently cover invalid UTF-8.
		raw = append([]byte("\xe2\x80"), raw...)
		raw = append(raw, []byte("\u2003\n\n")...)
		wantTitle, wantBody, _ := strings.Cut(string(raw), "\n\n")
		wantTitle, wantBody = strings.TrimSpace(wantTitle), strings.TrimSpace(wantBody)
		for _, step := range []int{1, 2, 3, 7, 19} {
			stream := newGitAuthorStream(8192)
			writeChunks(t, stream, raw, step)
			stream.finish()
			got := materializeAuthor(stream.title, stream.body, 8192)
			if got.err != nil || got.title != wantTitle || got.body != wantBody {
				t.Fatalf("case=%d step=%d got=%#v want title=%q body=%q", n, step, got, wantTitle, wantBody)
			}
		}
	}
}

func TestAuthorBudgetUsesExactQuotedSection(t *testing.T) {
	title := "a\nb"
	body := "c\nd"
	limit := len(formatThisHead(title, body))
	for _, tc := range []struct {
		name string
		max  int
		want bool
	}{
		{"exact", limit, true},
		{"one-byte-short", limit - 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotTitle, gotBody := newTrimmedText(tc.max), newTrimmedText(tc.max)
			_, _ = gotTitle.Write([]byte(title))
			_, _ = gotBody.Write([]byte(body))
			gotTitle.Finish()
			gotBody.Finish()
			result := materializeAuthor(gotTitle, gotBody, tc.max)
			if (result.err == nil) != tc.want {
				t.Fatalf("result=%#v", result)
			}
		})
	}
}

func ghAuthorScalar(title, body string) string {
	return base64.StdEncoding.EncodeToString([]byte(title)) + "\n" + base64.StdEncoding.EncodeToString([]byte(body)) + "\n"
}

func TestGetAuthorIntentStreamsGHSemanticsAndFallbacks(t *testing.T) {
	sha := strings.Repeat("a", 40)
	t.Run("scalar title body controls and query", func(t *testing.T) {
		title, body := " \tTitle\n", "\u2003body\x01\n"
		fake := packetFakeCommand(t, "[ \"$1\" = pr ] && [ \"$2\" = view ] && [ \"$6\" = --json ] && [ \"$7\" = title,body ] && [ \"$8\" = --jq ] && [ \"$9\" = '"+ghAuthorJQ+"' ] || exit 9; printf '"+shellOctal(ghAuthorScalar(title, body))+"'")
		withPacketSourceBinaries(t, packetGitBinary, fake)
		gotTitle, gotBody, err := getAuthorIntent(time.Second, t.TempDir(), "owner/repo", 7, "", sha, 4096)
		if err != nil || gotTitle != strings.TrimSpace(title) || gotBody != strings.TrimSpace(body) {
			t.Fatalf("title=%q body=%q err=%v", gotTitle, gotBody, err)
		}
	})
	t.Run("empty scalar fields", func(t *testing.T) {
		fake := packetFakeStdout(t, "\n\n")
		withPacketSourceBinaries(t, packetGitBinary, fake)
		gotTitle, gotBody, err := getAuthorIntent(time.Second, t.TempDir(), "owner/repo", 7, "", sha, 4096)
		if err != nil || gotTitle != "" || gotBody != "" {
			t.Fatalf("title=%q body=%q err=%v", gotTitle, gotBody, err)
		}
	})
	t.Run("malformed scalar falls back to git", func(t *testing.T) {
		gh := packetFakeStdout(t, "not-base64\n\n")
		git := packetFakeStdout(t, "fallback\n\nGit body\n")
		withPacketSourceBinaries(t, git, gh)
		gotTitle, gotBody, err := getAuthorIntent(time.Second, t.TempDir(), "owner/repo", 7, "", sha, 4096)
		if err != nil || gotTitle != "fallback" || gotBody != "Git body" {
			t.Fatalf("title=%q body=%q err=%v", gotTitle, gotBody, err)
		}
	})
	t.Run("malformed framing after oversized prefix falls back to git", func(t *testing.T) {
		gh := packetFakeStdout(t, ghAuthorScalar("", strings.Repeat("x", 256))+"not-a-new-field")
		git := packetFakeStdout(t, "fallback\n\nGit body\n")
		withPacketSourceBinaries(t, git, gh)
		gotTitle, gotBody, err := getAuthorIntent(time.Second, t.TempDir(), "owner/repo", 7, "", sha, 64)
		if err != nil || gotTitle != "fallback" || gotBody != "Git body" {
			t.Fatalf("title=%q body=%q err=%v", gotTitle, gotBody, err)
		}
	})
	t.Run("failed oversized gh falls back to git", func(t *testing.T) {
		large := ghAuthorScalar("", strings.Repeat("x", 256))
		gh := packetFakeCommand(t, "printf '"+shellOctal(large)+"'; exit 7")
		git := packetFakeStdout(t, "fallback\n\nGit body\n")
		withPacketSourceBinaries(t, git, gh)
		gotTitle, gotBody, err := getAuthorIntent(time.Second, t.TempDir(), "owner/repo", 7, "", sha, 64)
		if err != nil || gotTitle != "fallback" || gotBody != "Git body" {
			t.Fatalf("title=%q body=%q err=%v", gotTitle, gotBody, err)
		}
	})
	t.Run("timed-out oversized gh falls back to git", func(t *testing.T) {
		large := ghAuthorScalar("", strings.Repeat("x", 256))
		gh := packetFakeCommand(t, "printf '"+shellOctal(large)+"'; exec sleep 2")
		git := packetFakeStdout(t, "fallback\n\nGit body\n")
		withPacketSourceBinaries(t, git, gh)
		gotTitle, gotBody, err := getAuthorIntent(time.Second, t.TempDir(), "owner/repo", 7, "", sha, 64)
		if err != nil || gotTitle != "fallback" || gotBody != "Git body" {
			t.Fatalf("title=%q body=%q err=%v", gotTitle, gotBody, err)
		}
	})
	t.Run("successful oversized gh refuses", func(t *testing.T) {
		gh := packetFakeStdout(t, ghAuthorScalar("", strings.Repeat("x", 256)))
		withPacketSourceBinaries(t, packetGitBinary, gh)
		_, _, err := getAuthorIntent(time.Second, t.TempDir(), "owner/repo", 7, "", sha, 64)
		if err == nil || !strings.Contains(err.Error(), "cannot fit --max-bytes 64") {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("successful oversized git refuses", func(t *testing.T) {
		git := packetFakeStdout(t, "subject\n\n"+strings.Repeat("x", 256))
		withPacketSourceBinaries(t, git, packetGHBinary)
		_, _, err := getAuthorIntent(time.Second, t.TempDir(), "", 0, "", sha, 64)
		if err == nil || !strings.Contains(err.Error(), "cannot fit --max-bytes 64") {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("failed git keeps unknown fallback", func(t *testing.T) {
		git := packetFakeCommand(t, "exit 7")
		withPacketSourceBinaries(t, git, packetGHBinary)
		gotTitle, gotBody, err := getAuthorIntent(time.Second, t.TempDir(), "", 0, "", sha, 64)
		if err != nil || gotTitle != "" || !strings.Contains(gotBody, "no intent is guessed") {
			t.Fatalf("title=%q body=%q err=%v", gotTitle, gotBody, err)
		}
	})
}

func TestGetAuthorIntentRejectsNonCanonicalGHScalars(t *testing.T) {
	sha := strings.Repeat("a", 40)
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"carriage-return-only-quartet", "\r\r\r\r\n\n"},
		{"carriage-return-inside-quartet", "YQ\r==\n\n"},
		{"noncanonical-padding-bits", "Zh==\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gh := packetFakeStdout(t, tc.raw)
			git := packetFakeStdout(t, "fallback\n\nGit body\n")
			withPacketSourceBinaries(t, git, gh)
			gotTitle, gotBody, err := getAuthorIntent(time.Second, t.TempDir(), "owner/repo", 7, "", sha, 4096)
			if err != nil || gotTitle != "fallback" || gotBody != "Git body" {
				t.Fatalf("title=%q body=%q err=%v", gotTitle, gotBody, err)
			}
		})
	}
}

func shellOctal(raw string) string {
	var out strings.Builder
	for _, b := range []byte(raw) {
		fmt.Fprintf(&out, "\\%03o", b)
	}
	return out.String()
}

func TestPacketRefusesSuccessfulOversizedAuthorWithoutArtifact(t *testing.T) {
	lane, _ := packetLab(t)
	repo := filepath.Join(lane, "repo")
	message := filepath.Join(t.TempDir(), "message")
	if err := os.WriteFile(message, []byte("subject\n\n"+strings.Repeat("x", 8192)), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "commit", "--amend", "-q", "-F", message)
	cmd.Dir = repo
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("amend: %v %s", err, b)
	}
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(lane); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	if code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "emma", "--out", "too-large-author.md", "--max-bytes", "4096"}, &out, &errb); code != 2 || !strings.Contains(errb.String(), "author title and body cannot fit") {
		t.Fatalf("code=%d stderr=%s", code, errb.String())
	}
	if _, err := os.Stat("too-large-author.md"); !os.IsNotExist(err) {
		t.Fatalf("oversized author published artifact: %v", err)
	}
}
