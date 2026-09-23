package chat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Issue nova-tools#2315: build the strict allow-list parser, quickstart skeleton
// and leave removal in internal/chat/allow.go. SPEC-CHAT.md names the file in
// the work list (item 1) and demands the strict parser, the quickstart skeleton
// and the leave removal under "The allow-list, which is the line's person's":
// `#` comments and blank lines ignored, every field named, an unknown key is
// a refusal naming the key (never ignored), `class=own` checked against the
// pinned `own-server`, `dm *` accepted, every per-conversation number required
// where the spec says it is required, the ladder's steps read in the file's
// order, and quickstart creates a commented skeleton with no entries while
// leave removes one entry. The test exercises the parser, the skeleton and the
// removal on a fixture written by hand to be the spec's own example; it goes
// red on the base (allow.go does not exist) and green at the head where the
// file exists and is the parser the work-list item describes.
func TestIssue2315(t *testing.T) {
	dir := t.TempDir()

	allowPath := filepath.Join(dir, "allow")
	contents := strings.Join([]string{
		"# ---- the person, and their own server.",
		"person      214800000000000000",
		"own-server  1600000000000000000",
		"member      214800000000000000",
		"member      1601000000000000001 kind=line",
		"",
		"conversation 1600000000000000010 class=own    name=table   mode=resume session-idle=7d  session-max=30d wrap-file=./wrap-table.md reply-max=1600 min-gap=0s  replies-per-hour=60 context=40",
		"conversation 1600000000000000011 class=own    name=work    mode=resume session-idle=7d  session-max=30d wrap-file=./wrap-work.md  reply-max=1900 min-gap=0s  replies-per-hour=60 context=40",
		"conversation 1524795311036563549 class=public name=general mode=fresh  reply-max=600  min-gap=20m replies-per-hour=2  context=25",
		"dm           *                   class=dm     mode=resume session-idle=4h  session-max=48h wrap-file=./wrap-dm.md reply-max=1200 min-gap=1m  replies-per-hour=20 history-budget=8000",
		"",
		"backoff 5m 10m 20m 40m 80m 160m",
		"cooldown 30m",
		"burst-window 90s",
		"gap-messages 200",
		"flood-multiple 6",
		"",
	}, "\n")
	if err := os.WriteFile(allowPath, []byte(contents), 0o644); err != nil {
		t.Fatalf("seed allow-list: %v", err)
	}

	allow, err := ParseAllow(allowPath)
	if err != nil {
		t.Fatalf("ParseAllow on the spec's own example refused: %v", err)
	}

	if allow.Person != "214800000000000000" {
		t.Errorf("Person = %q, want %q", allow.Person, "214800000000000000")
	}
	if allow.OwnServer != "1600000000000000000" {
		t.Errorf("OwnServer = %q, want %q", allow.OwnServer, "1600000000000000000")
	}
	if len(allow.Members) != 2 {
		t.Fatalf("Members has %d entries, want 2", len(allow.Members))
	}
	if allow.Members[0].ID != "214800000000000000" {
		t.Errorf("Members[0].ID = %q, want %q", allow.Members[0].ID, "214800000000000000")
	}
	if allow.Members[1].ID != "1601000000000000001" {
		t.Errorf("Members[1].ID = %q, want %q", allow.Members[1].ID, "1601000000000000001")
	}
	if !allow.Members[1].KindLine {
		t.Error("Members[1].KindLine = false, want true (kind=line)")
	}
	if len(allow.Conversations) != 3 {
		t.Fatalf("Conversations has %d entries, want 3", len(allow.Conversations))
	}
	table := allow.Conversations[0]
	if table.ID != "1600000000000000010" || table.Class != ClassOwn || table.Name != "table" || table.Mode != ModeResume {
		t.Errorf("Conversations[0] = %+v, want id=1600000000000000010 class=own name=table mode=resume", table)
	}
	if !table.ReplyMaxSet || table.ReplyMax != 1600 {
		t.Errorf("Conversations[0].ReplyMax = %d (set=%v), want 1600", table.ReplyMax, table.ReplyMaxSet)
	}
	if len(allow.DMs) != 1 {
		t.Fatalf("DMs has %d entries, want 1", len(allow.DMs))
	}
	dm := allow.DMs[0]
	if dm.ID != "*" || dm.Class != ClassDM || dm.Mode != ModeResume {
		t.Errorf("DMs[0] = %+v, want id=* class=dm mode=resume", dm)
	}
	if !dm.ReplyMaxSet || dm.ReplyMax != 1200 {
		t.Errorf("DMs[0].ReplyMax = %d (set=%v), want 1200", dm.ReplyMax, dm.ReplyMaxSet)
	}
	if dm.HistoryBudget != 8000 {
		t.Errorf("DMs[0].HistoryBudget = %d, want 8000", dm.HistoryBudget)
	}
	if len(allow.Backoff) != 6 {
		t.Fatalf("Backoff has %d steps, want 6", len(allow.Backoff))
	}
	wantSteps := []string{"5m", "10m", "20m", "40m", "80m", "160m"}
	for i, want := range wantSteps {
		if allow.Backoff[i] != want {
			t.Errorf("Backoff[%d] = %q, want %q", i, allow.Backoff[i], want)
		}
	}
	if allow.Cooldown != "30m" {
		t.Errorf("Cooldown = %q, want %q", allow.Cooldown, "30m")
	}
	if allow.BurstWindow != "90s" {
		t.Errorf("BurstWindow = %q, want %q", allow.BurstWindow, "90s")
	}
	if allow.GapMessages != 200 {
		t.Errorf("GapMessages = %d, want 200", allow.GapMessages)
	}
	if allow.FloodMultiple != 6 {
		t.Errorf("FloodMultiple = %d, want 6", allow.FloodMultiple)
	}

	if bad := allow.ConversationByID("1600000000000000010"); bad == nil {
		t.Error("ConversationByID(1600000000000000010) = nil, want the table conversation")
	}
	if bad := allow.ConversationByID("9999"); bad != nil {
		t.Errorf("ConversationByID(9999) = %+v, want nil", bad)
	}

	badContents := strings.Join([]string{
		"person      214800000000000000",
		"own-server  1600000000000000000",
		"conversation 1600000000000000010 class=own name=table mode=resume session-idle=7d session-max=30d wrap-file=./wrap-table.md reply-max=1600 min-gap=0s replies-per-hour=60 context=40",
		"unknown-key value",
		"",
	}, "\n")
	badPath := filepath.Join(dir, "bad")
	if err := os.WriteFile(badPath, []byte(badContents), 0o644); err != nil {
		t.Fatalf("seed bad allow-list: %v", err)
	}
	if _, err := ParseAllow(badPath); err == nil {
		t.Error("ParseAllow on a line carrying an unknown key accepted it; an unknown key is a refusal (SPEC-CHAT.md, The allow-list)")
	}

	// Class must agree with the entry kind. Each fixture is a full, otherwise
	// valid file (person, own-server, the whole ladder), so the only reason
	// it can be refused is the class, and the error must name class.
	ladder := []string{
		"backoff 5m 10m 20m 40m 80m 160m",
		"cooldown 30m",
		"burst-window 90s",
		"gap-messages 200",
		"flood-multiple 6",
		"",
	}
	fullFile := func(entry string) string {
		return strings.Join(append([]string{
			"person      214800000000000000",
			"own-server  1600000000000000000",
			entry,
		}, ladder...), "\n")
	}
	validConversation := "conversation 1524795311036563549 class=public name=general mode=fresh reply-max=600 min-gap=20m replies-per-hour=2 context=25"
	validDM := "dm * class=dm mode=fresh reply-max=1200 min-gap=1m replies-per-hour=20 history-budget=8000"
	for _, tc := range []struct {
		name, entry string
		wantClass   Class
		wantErr     bool
	}{
		{"conversation-class-public", validConversation, ClassPublic, false},
		{"dm-class-dm", validDM, ClassDM, false},
		{"conversation-class-dm", strings.Replace(validConversation, "class=public", "class=dm", 1), "", true},
		{"conversation-no-class", strings.Replace(validConversation, "class=public ", "", 1), "", true},
		{"dm-class-public", strings.Replace(validDM, "class=dm", "class=public", 1), "", true},
		{"dm-class-own", strings.Replace(validDM, "class=dm", "class=own", 1), "", true},
		{"dm-no-class", strings.Replace(validDM, "class=dm ", "", 1), "", true},
	} {
		path := filepath.Join(dir, "class-"+tc.name)
		if err := os.WriteFile(path, []byte(fullFile(tc.entry)), 0o644); err != nil {
			t.Fatalf("seed %s: %v", tc.name, err)
		}
		got, err := ParseAllow(path)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s: ParseAllow accepted a class that contradicts the entry kind", tc.name)
			} else if !strings.Contains(err.Error(), "class") {
				t.Errorf("%s: refused for a reason other than class: %v", tc.name, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: ParseAllow refused a valid full file: %v", tc.name, err)
			continue
		}
		var entries []Conversation
		entries = append(entries, got.Conversations...)
		entries = append(entries, got.DMs...)
		if len(entries) != 1 || entries[0].Class != tc.wantClass {
			t.Errorf("%s: entries = %+v, want one entry with class=%s", tc.name, entries, tc.wantClass)
		}
	}

	// mode=resume requires session-idle, session-max and wrap-file (SPEC-CHAT.md,
	// the allow-list). Each fixture is a full, otherwise valid file that drops
	// exactly one of the three, so the only reason it can be refused is that
	// field, and the error must name it. The valid entry parses on the same
	// ladder, so a refusal is never a fixture defect.
	validResume := "conversation 1600000000000000010 class=own name=table mode=resume session-idle=7d session-max=30d wrap-file=./wrap-table.md reply-max=1600 min-gap=0s replies-per-hour=60 context=40"
	validResumeDM := "dm * class=dm mode=resume session-idle=4h session-max=48h wrap-file=./wrap-dm.md reply-max=1200 min-gap=1m replies-per-hour=20 history-budget=8000"
	for _, entry := range []string{validResume, validResumeDM} {
		path := filepath.Join(dir, "resume-valid")
		if err := os.WriteFile(path, []byte(fullFile(entry)), 0o644); err != nil {
			t.Fatalf("seed resume-valid: %v", err)
		}
		if _, err := ParseAllow(path); err != nil {
			t.Errorf("ParseAllow refused a complete mode=resume entry %q: %v", entry, err)
		}
		for _, drop := range []string{"session-idle", "session-max", "wrap-file"} {
			var kept []string
			for _, field := range strings.Fields(entry) {
				if !strings.HasPrefix(field, drop+"=") {
					kept = append(kept, field)
				}
			}
			path := filepath.Join(dir, "resume-no-"+drop)
			if err := os.WriteFile(path, []byte(fullFile(strings.Join(kept, " "))), 0o644); err != nil {
				t.Fatalf("seed resume-no-%s: %v", drop, err)
			}
			_, err := ParseAllow(path)
			if err == nil {
				t.Errorf("%s: ParseAllow accepted a mode=resume entry with no %s=; it is required on resume", kept[0], drop)
			} else if !strings.Contains(err.Error(), drop) {
				t.Errorf("%s: mode=resume without %s= refused for another reason: %v", kept[0], drop, err)
			}
		}
	}

	// The spec's own example, verbatim from SPEC-CHAT.md (the allow-list
	// section): trailing `#` comments on member lines, and history-budget on
	// the mode=fresh `general` conversation. Both must parse.
	specExample := strings.Join([]string{
		"# ---- the person, and their own server. Set by hand, out of band, never from a message.",
		"person      214800000000000000",
		"own-server  1600000000000000000",
		"member      214800000000000000     # glenn",
		"member      1601000000000000001 kind=line   # rowan",
		"member      1601000000000000002 kind=line   # stella",
		"member      1601000000000000003 kind=line   # freddy",
		"",
		"conversation 1600000000000000010 class=own    name=table   mode=resume session-idle=7d  session-max=30d wrap-file=./wrap-table.md   reply-max=1600 min-gap=0s  replies-per-hour=60 context=40",
		"conversation 1600000000000000011 class=own    name=work    mode=resume session-idle=7d  session-max=30d wrap-file=./wrap-work.md    reply-max=1900 min-gap=0s  replies-per-hour=60 context=40",
		"conversation 1524795311036563549 class=public name=general mode=fresh  history-budget=8000                                          reply-max=600  reply-ratio=2 reply-floor=240 min-gap=20m replies-per-hour=2  context=25",
		"conversation 1529471102441492681 class=public name=allies  mode=resume session-idle=2h  session-max=24h wrap-file=./wrap-allies.md  reply-max=1200 reply-ratio=3 reply-floor=240 min-gap=2m  replies-per-hour=12 context=40",
		"dm           *                   class=dm     mode=resume session-idle=4h  session-max=48h wrap-file=./wrap-dm.md  history-budget=8000 reply-max=1200 reply-ratio=2 reply-floor=240 min-gap=1m  replies-per-hour=20",
		"",
	}, "\n") + "\n" + strings.Join(ladder, "\n")
	specPath := filepath.Join(dir, "spec-example")
	if err := os.WriteFile(specPath, []byte(specExample), 0o644); err != nil {
		t.Fatalf("seed spec example: %v", err)
	}
	spec, err := ParseAllow(specPath)
	if err != nil {
		t.Fatalf("ParseAllow on SPEC-CHAT.md's example verbatim refused: %v", err)
	}
	if len(spec.Members) != 4 || len(spec.Conversations) != 4 || len(spec.DMs) != 1 {
		t.Errorf("spec example: %d members, %d conversations, %d dms; want 4, 4, 1", len(spec.Members), len(spec.Conversations), len(spec.DMs))
	}
	if g := spec.ConversationByID("1524795311036563549"); g == nil || g.HistoryBudget != 8000 || g.Mode != ModeFresh {
		t.Errorf("spec example general = %+v, want mode=fresh history-budget=8000", g)
	}

	missingRequired := strings.Join([]string{
		"person      214800000000000000",
		"own-server  1600000000000000000",
		"conversation 1600000000000000010 class=own name=table mode=resume session-idle=7d session-max=30d wrap-file=./wrap-table.md min-gap=0s replies-per-hour=60 context=40",
		"",
	}, "\n")
	missingPath := filepath.Join(dir, "missing-required")
	if err := os.WriteFile(missingPath, []byte(missingRequired), 0o644); err != nil {
		t.Fatalf("seed missing-required allow-list: %v", err)
	}
	if _, err := ParseAllow(missingPath); err == nil {
		t.Error("ParseAllow on a conversation missing reply-max accepted it; reply-max is required (SPEC-CHAT.md, allow-list)")
	}

	freshWithIdle := strings.Join([]string{
		"person      214800000000000000",
		"own-server  1600000000000000000",
		"conversation 1600000000000000010 class=own name=table mode=fresh reply-max=600 session-idle=7d",
		"",
	}, "\n")
	freshPath := filepath.Join(dir, "fresh-with-idle")
	if err := os.WriteFile(freshPath, []byte(freshWithIdle), 0o644); err != nil {
		t.Fatalf("seed fresh-with-idle allow-list: %v", err)
	}
	if _, err := ParseAllow(freshPath); err == nil {
		t.Error("ParseAllow on a mode=fresh entry carrying session-idle accepted it; session-idle is forbidden on mode=fresh (SPEC-CHAT.md, rule 3)")
	}

	if err := Quickstart(allowPath); err == nil {
		t.Error("Quickstart over an existing file accepted it; quickstart refuses a file that already exists (SPEC-CHAT.md, rule 13)")
	}

	freshDir := t.TempDir()
	skeletonPath := filepath.Join(freshDir, "allow")
	if err := Quickstart(skeletonPath); err != nil {
		t.Fatalf("Quickstart over a fresh path refused: %v", err)
	}
	if _, err := os.Stat(skeletonPath); err != nil {
		t.Fatalf("Quickstart did not create the allow-list: %v", err)
	}
	skeleton, err := os.ReadFile(skeletonPath)
	if err != nil {
		t.Fatalf("read skeleton: %v", err)
	}
	if !strings.Contains(string(skeleton), "person") || !strings.Contains(string(skeleton), "conversation") {
		t.Errorf("Quickstart's skeleton does not name the leading keys; got %q", string(skeleton))
	}
	if !strings.Contains(string(skeleton), "backoff") {
		t.Errorf("Quickstart's skeleton does not name the ladder; got %q", string(skeleton))
	}

	if err := Quickstart(skeletonPath); err == nil {
		t.Error("Quickstart over the skeleton it just wrote accepted the second write; quickstart is create-once")
	}

	leaveDir := t.TempDir()
	leavePath := filepath.Join(leaveDir, "allow")
	if err := os.WriteFile(leavePath, []byte(contents), 0o644); err != nil {
		t.Fatalf("seed leave allow-list: %v", err)
	}
	remaining, err := Leave(leavePath, "1524795311036563549")
	if err != nil {
		t.Fatalf("Leave on the public conversation refused: %v", err)
	}
	if remaining != 3 {
		t.Errorf("Leave remaining = %d, want 3 (2 conversations + 1 dm)", remaining)
	}
	after, err := ParseAllow(leavePath)
	if err != nil {
		t.Fatalf("ParseAllow after Leave refused: %v", err)
	}
	if after.ConversationByID("1524795311036563549") != nil {
		t.Error("Leave did not remove the public conversation")
	}
	if after.ConversationByID("1600000000000000010") == nil {
		t.Error("Leave removed the wrong entry; the own conversation must still be present")
	}

	if _, err := Leave(leavePath, "9999"); err == nil {
		t.Error("Leave accepted an id the file did not name")
	}

	if _, err := Leave(leavePath, ""); err == nil {
		t.Error("Leave accepted an empty conversation id")
	}
}
