package bus

import (
	"strings"
	"testing"
)

// The Host line is the one header field that says WHICH MACHINE posted. One name can post
// from two places -- the keeper on the Studio and the bud on the Air both post as Rowan --
// and until this line existed they were told apart by a `[bud air]` in the subject, which
// spent the subject on routing.
//
// Everything below is one claim: the line is written when it is asked for, parsed when it
// is there, refused when it is not a host, and ABSENT in every byte when nobody asked.

func TestSendWritesTheHostLineWhenHostIsGiven(t *testing.T) {
	tab := loadBus(t, writeBus(t, nil))
	p, err := PrepareWith(tab, "From: Ada\nTo: Bo\nSubject: the gate\n\nbody\n",
		at("2026-09-09T12:34:56Z"), SendOptions{Host: "air"})
	if err != nil {
		t.Fatalf("PrepareWith: %v", err)
	}
	if p.Note.Header.Host != "air" {
		t.Fatalf("Host = %q, want %q", p.Note.Header.Host, "air")
	}
	// Under From and above To, which is the order Render writes and a reader reads.
	rendered := p.Note.Render()
	if !strings.Contains(rendered, "From: Ada\nHost: air\nTo: Bo\n") {
		t.Fatalf("the Host line is not under From:\n%s", rendered)
	}
	// And send says what it did, because a tolerance nobody is told about is a tool
	// quietly rewriting what a person wrote.
	if len(p.Notices) == 0 || !strings.Contains(strings.Join(p.Notices, "\n"), "--host") {
		t.Fatalf("no notice named --host: %v", p.Notices)
	}
}

func TestADraftsOwnHostLineIsKept(t *testing.T) {
	tab := loadBus(t, writeBus(t, nil))
	p, err := Prepare(tab, "From: Ada\nHost: studio\nTo: Bo\nSubject: the gate\n\nbody\n",
		at("2026-09-09T12:34:56Z"), "")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if p.Note.Header.Host != "studio" {
		t.Fatalf("Host = %q, want %q", p.Note.Header.Host, "studio")
	}
	if len(p.Notices) != 0 {
		t.Fatalf("a draft that already says where it is from needs no notice: %v", p.Notices)
	}
}

func TestHostFlagAgainstADifferentHostLineIsARefusal(t *testing.T) {
	tab := loadBus(t, writeBus(t, nil))
	_, err := PrepareWith(tab, "From: Ada\nHost: studio\nTo: Bo\nSubject: the gate\n\nbody\n",
		at("2026-09-09T12:34:56Z"), SendOptions{Host: "air"})
	if err == nil {
		t.Fatal("send posted one machine's note as another")
	}
	for _, want := range []string{"--host", "air", "studio", "does not post one machine's note as another"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not say %q:\n%v", want, err)
		}
	}
}

// The flag that agrees with the line is neither a refusal nor a notice: the defaults file
// supplies --host on every send from a bench, and a draft written there that names its own
// host would otherwise be refused for agreeing.
func TestHostFlagAgreeingWithTheHostLineIsSilent(t *testing.T) {
	tab := loadBus(t, writeBus(t, nil))
	p, err := PrepareWith(tab, "From: Ada\nHost: air\nTo: Bo\nSubject: the gate\n\nbody\n",
		at("2026-09-09T12:34:56Z"), SendOptions{Host: "air"})
	if err != nil {
		t.Fatalf("PrepareWith: %v", err)
	}
	if len(p.Notices) != 0 {
		t.Fatalf("agreement is not a tolerance: %v", p.Notices)
	}
}

func TestAHostThatIsNotOneWordIsRefused(t *testing.T) {
	cases := []struct {
		name, host, want string
	}{
		{"a space", "the air", "one space-separated"},
		{"upper case", "Air", "lower-case"},
		{"a tab", "air\tbud", "one space-separated"},
		{"too long", strings.Repeat("a", HostMax+1), "longer than"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidHost(c.host)
			if err == nil {
				t.Fatalf("%q was accepted as a host", c.host)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("the refusal does not say %q:\n%v", c.want, err)
			}
		})
	}
	// And the ones a bench actually writes are accepted.
	for _, ok := range []string{"air", "studio", "hulk", "mini-2", "bench.west", "space_1"} {
		if err := ValidHost(ok); err != nil {
			t.Fatalf("%q is a host and was refused: %v", ok, err)
		}
	}
}

// A note whose header carries a Host line the parser cannot accept is a FINDING on the
// note, not a crash and not a silently-dropped field.
func TestAnUnusableHostLineIsAHeaderProblem(t *testing.T) {
	c, err := LoadConfig(writeBus(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	n, perr := ParseNote("from-ada/x.md", "From: Ada\nHost: The Air\nTo: Bo\nSubject: s\n\nbody\n")
	if perr != nil {
		t.Fatalf("the note did not parse; the Host line is a header VALUE problem: %v", perr)
	}
	found := false
	for _, p := range n.Header.Problems(c) {
		if strings.Contains(p.Error(), "Host") {
			found = true
		}
	}
	if !found {
		t.Fatal("a Host line that is not a host is not reported")
	}
}

// THE COMPATIBILITY CLAIM, in one test: nothing about a note sent without a host changed.
// The rendered bytes carry no Host line, and the id is the id it would have been before
// this field existed -- the host is not in the `nova-bus id v1` preimage, so every id on
// every bus is still the id it was.
func TestANoteWithNoHostIsUnchanged(t *testing.T) {
	draft := "From: Ada\nTo: Bo\nSubject: the gate\n\nbody\n"
	tab := loadBus(t, writeBus(t, nil))
	plain, err := Prepare(tab, draft, at("2026-09-09T12:34:56Z"), "")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if strings.Contains(plain.Note.Render(), "Host:") {
		t.Fatalf("a note nobody gave a host carries a Host line:\n%s", plain.Note.Render())
	}
	hosted, err := PrepareWith(tab, draft, at("2026-09-09T12:34:56Z"), SendOptions{Host: "air"})
	if err != nil {
		t.Fatalf("PrepareWith: %v", err)
	}
	if hosted.Note.Header.ID != plain.Note.Header.ID {
		t.Fatalf("the host changed the id: %q with a host, %q without; the host is not in the preimage",
			hosted.Note.Header.ID, plain.Note.Header.ID)
	}
}

// The OPEN cache carries the host so a later run lists it without opening the note, and it
// carries it as a NINTH field that is written only when there is one -- so a bus whose
// notes have no host has the eight-field file it has always had, and needs no migration.
func TestTheOpenLineCarriesTheHostOnlyWhenThereIsOne(t *testing.T) {
	plain := OpenEntry{ID: "ada-3f9a1c2b8d40", Kind: OpenNote, From: "Ada", Addr: "to", Date: "2026-09-09T12:34:56Z", Path: "from-ada/x.md", Subject: "s"}
	if got := strings.Count(OpenLine(plain), "\t"); got != openFieldsV2-1 {
		t.Fatalf("an entry with no host writes %d tabs, want %d: %q", got, openFieldsV2-1, OpenLine(plain))
	}
	hosted := plain
	hosted.Host = "air"
	line := OpenLine(hosted)
	if got := strings.Count(line, "\t"); got != openFields-1 {
		t.Fatalf("an entry with a host writes %d tabs, want %d: %q", got, openFields-1, line)
	}
	if !strings.HasSuffix(line, "\tair") {
		t.Fatalf("the host is not the last field: %q", line)
	}
}

func TestTheOpenListReadsBothWidths(t *testing.T) {
	hosted := OpenEntry{ID: "ada-3f9a1c2b8d40", Kind: OpenNote, From: "Ada", Addr: "to", Date: "2026-09-09T12:34:56Z", Path: "from-ada/x.md", Subject: "s", Host: "air"}
	plain := hosted
	plain.Host = ""
	root := writeBus(t, nil)
	if err := WriteOpen(root, "from-ada", []OpenEntry{plain, hosted}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadOpen(root, "from-ada")
	if err != nil {
		t.Fatalf("ReadOpen over a file holding both widths: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("read %d entries, want 2", len(got))
	}
	if got[0].Host != "" {
		t.Fatalf("the eight-field row came back with host %q", got[0].Host)
	}
	if got[1].Host != "air" {
		t.Fatalf("the nine-field row came back with host %q, want %q", got[1].Host, "air")
	}
}

// A row of any OTHER width is still the refusal it was, and it still names both widths so
// a reader knows which one they have.
func TestAnOpenRowOfTheWrongWidthIsStillRefused(t *testing.T) {
	root := writeBus(t, nil)
	write(t, root, OpenPath("from-ada"), OpenHeader+"\nada-3f9a1c2b8d40\tnote\t-\tAda\n")
	_, err := ReadOpen(root, "from-ada")
	if err == nil {
		t.Fatal("a four-field row was accepted")
	}
	for _, want := range []string{"9 tab-separated fields", "or 8 without the host", "got 4"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not say %q:\n%v", want, err)
		}
	}
}

// A reply posts from a machine too, and a reply with no host writes the reply this tool
// has always written.
func TestAReplyCarriesTheHostItIsGiven(t *testing.T) {
	root := writeBus(t, map[string]string{
		"from-bo/note.md": "From: Bo\nTo: Ada\nDate: Wed Sep  9 12:00:00 UTC 2026\nId: bo-abcdef012345\nSubject: the gate\n\nA question.\n",
	})
	tab := loadBus(t, root)
	me := mustParticipant(t, tab.Config, "Ada")
	original, ok := tab.Resolve("bo-abcdef012345")
	if !ok {
		t.Fatal("the fixture note did not resolve")
	}
	hosted, err := PrepareReplyFrom(tab, me, original, "Yes.\n", at("2026-09-09T12:34:56Z"), "air")
	if err != nil {
		t.Fatalf("PrepareReplyFrom: %v", err)
	}
	if !strings.Contains(hosted.Note.Render(), "From: Ada\nHost: air\nTo: Bo\n") {
		t.Fatalf("the reply's Host line is not under From:\n%s", hosted.Note.Render())
	}
	plain, err := PrepareReply(tab, me, original, "Yes.\n", at("2026-09-09T12:34:56Z"))
	if err != nil {
		t.Fatalf("PrepareReply: %v", err)
	}
	if strings.Contains(plain.Note.Render(), "Host:") {
		t.Fatalf("a reply nobody gave a host carries a Host line:\n%s", plain.Note.Render())
	}
	if err := ValidHost("The Air"); err == nil {
		t.Fatal("ValidHost is what PrepareReplyFrom refuses on and it accepted a sentence")
	}
	if _, err := PrepareReplyFrom(tab, me, original, "Yes.\n", at("2026-09-09T12:34:56Z"), "The Air"); err == nil {
		t.Fatal("a reply posted from a host that is not one word")
	}
}
