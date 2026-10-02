package bus

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The Host line is the one header field that says WHICH MACHINE posted. One name can post
// from two places -- the keeper on the Studio and the bud on the Air both post as Rowan --
// and until this line existed they were told apart by a `[bud air]` in the subject, which
// spent the subject on routing.
//
// Everything below is one claim: the line is written when it is asked for, parsed when it
// is there, refused when it is not a host, and ABSENT in every byte when nobody asked.

func TestSendWritesTheHostLineWhenHostIsGiven(t *testing.T) {
	t.Parallel()

	tab := loadBus(t, writeBus(t, nil))
	p, err := PrepareWith(tab, "From: Ada\nTo: Bo\nSubject: the gate\n\nbody\n",
		at("2026-09-09T12:34:56Z"), SendOptions{Host: "air"})
	require.NoError(t, err, "PrepareWith: %v", err)
	if p.Note.Header.Host != "air" {
		require.Equal(t, "air", p.Note.Header.Host, "Host = %q, want %q", p.Note.Header.Host, "air")
	}
	// Under From and above To, which is the order Render writes and a reader reads.
	rendered := p.Note.Render()
	require.Contains(t, rendered, "From: Ada\nHost: air\nTo: Bo\n", "the Host line is not under From:\n%s", rendered)
	// And send says what it did, because a tolerance nobody is told about is a tool
	// quietly rewriting what a person wrote.
	if len(p.Notices) == 0 || !strings.Contains(strings.Join(p.Notices, "\n"), "--host") {
		require.False(t, len(p.Notices) == 0 || !strings.Contains(strings.Join(p.Notices, "\n"), "--host"), "no notice named --host: %v", p.Notices)
	}
}

func TestADraftsOwnHostLineIsKept(t *testing.T) {
	t.Parallel()

	tab := loadBus(t, writeBus(t, nil))
	p, err := Prepare(tab, "From: Ada\nHost: studio\nTo: Bo\nSubject: the gate\n\nbody\n",
		at("2026-09-09T12:34:56Z"), "")
	require.NoError(t, err, "Prepare: %v", err)
	if p.Note.Header.Host != "studio" {
		require.Equal(t, "studio", p.Note.Header.Host, "Host = %q, want %q", p.Note.Header.Host, "studio")
	}
	if len(p.Notices) != 0 {
		require.Equal(t, 0, len(p.Notices), "a draft that already says where it is from needs no notice: %v", p.Notices)
	}
}

func TestHostFlagAgainstADifferentHostLineIsARefusal(t *testing.T) {
	t.Parallel()

	tab := loadBus(t, writeBus(t, nil))
	_, err := PrepareWith(tab, "From: Ada\nHost: studio\nTo: Bo\nSubject: the gate\n\nbody\n",
		at("2026-09-09T12:34:56Z"), SendOptions{Host: "air"})
	require.Error(t, err, "send posted one machine's note as another")
	for _, want := range []string{"--host", "air", "studio", "does not post one machine's note as another"} {
		require.Contains(t, err.Error(), want, "the refusal does not say %q:\n%v", want, err)
	}
}

// The flag that agrees with the line is neither a refusal nor a notice: the defaults file
// supplies --host on every send from a bench, and a draft written there that names its own
// host would otherwise be refused for agreeing.
func TestHostFlagAgreeingWithTheHostLineIsSilent(t *testing.T) {
	t.Parallel()

	tab := loadBus(t, writeBus(t, nil))
	p, err := PrepareWith(tab, "From: Ada\nHost: air\nTo: Bo\nSubject: the gate\n\nbody\n",
		at("2026-09-09T12:34:56Z"), SendOptions{Host: "air"})
	require.NoError(t, err, "PrepareWith: %v", err)
	if len(p.Notices) != 0 {
		require.Equal(t, 0, len(p.Notices), "agreement is not a tolerance: %v", p.Notices)
	}
}

func TestAHostThatIsNotOneWordIsRefused(t *testing.T) {
	t.Parallel()

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
				require.Error(t, err, "%q was accepted as a host", c.host)
			}
			if !strings.Contains(err.Error(), c.want) {
				require.Contains(t, err.Error(), c.want, "the refusal does not say %q:\n%v", c.want, err)
			}
		})
	}
	// And the ones a bench actually writes are accepted.
	for _, ok := range []string{"air", "studio", "hulk", "mini-2", "bench.west", "space_1"} {
		{
			err := ValidHost(ok)
			require.NoError(t, err, "%q is a host and was refused: %v", ok, err)
		}
	}
}

// A note whose header carries a Host line the parser cannot accept is a FINDING on the
// note, not a crash and not a silently-dropped field.
func TestAnUnusableHostLineIsAHeaderProblem(t *testing.T) {
	t.Parallel()

	c, err := LoadConfig(writeBus(t, nil))
	require.NoError(t, err)
	n, perr := ParseNote("from-ada/x.md", "From: Ada\nHost: The Air\nTo: Bo\nSubject: s\n\nbody\n")
	require.Equal(t, nil, perr, "the note did not parse; the Host line is a header VALUE problem: %v", perr)
	found := false
	for _, p := range n.Header.Problems(c) {
		if strings.Contains(p.Error(), "Host") {
			found = true
		}
	}
	require.True(t, found, "a Host line that is not a host is not reported")
}

// THE COMPATIBILITY CLAIM, in one test: nothing about a note sent without a host changed.
// The rendered bytes carry no Host line, and the id is the id it would have been before
// this field existed -- the host is not in the `nova-bus id v1` preimage, so every id on
// every bus is still the id it was.
func TestANoteWithNoHostIsUnchanged(t *testing.T) {
	t.Parallel()

	draft := "From: Ada\nTo: Bo\nSubject: the gate\n\nbody\n"
	tab := loadBus(t, writeBus(t, nil))
	plain, err := Prepare(tab, draft, at("2026-09-09T12:34:56Z"), "")
	require.NoError(t, err, "Prepare: %v", err)
	if strings.Contains(plain.Note.Render(), "Host:") {
		require.NotContains(t, plain.Note.Render(), "Host:", "a note nobody gave a host carries a Host line:\n%s", plain.Note.Render())
	}
	hosted, err := PrepareWith(tab, draft, at("2026-09-09T12:34:56Z"), SendOptions{Host: "air"})
	require.NoError(t, err, "PrepareWith: %v", err)
	if hosted.Note.Header.ID != plain.Note.Header.ID {
		require.FailNowf(t, "assertion failed", "the host changed the id: %q with a host, %q without; the host is not in the preimage",
			hosted.Note.Header.ID, plain.Note.Header.ID)
	}
}

// The OPEN cache carries the host so a later run lists it without opening the note, and it
// carries it as a NINTH field that is written only when there is one -- so a bus whose
// notes have no host has the eight-field file it has always had, and needs no migration.
func TestTheOpenLineCarriesTheHostOnlyWhenThereIsOne(t *testing.T) {
	t.Parallel()

	plain := OpenEntry{ID: "ada-3f9a1c2b8d40", Kind: OpenNote, From: "Ada", Addr: "to", Date: "2026-09-09T12:34:56Z", Path: "from-ada/x.md", Subject: "s"}
	{
		got := strings.Count(OpenLine(plain), "\t")
		if got != openFieldsV2-1 {
			require.False(t, got != openFieldsV2-1, "an entry with no host writes %d tabs, want %d: %q", got, openFieldsV2-1, OpenLine(plain))
		}
	}
	hosted := plain
	hosted.Host = "air"
	line := OpenLine(hosted)
	{
		got := strings.Count(line, "\t")
		require.False(t, got != openFields-1, "an entry with a host writes %d tabs, want %d: %q", got, openFields-1, line)
	}
	require.False(t, !strings.HasSuffix(line, "\tair"), "the host is not the last field: %q", line)
}

func TestTheOpenListReadsBothWidths(t *testing.T) {
	t.Parallel()

	hosted := OpenEntry{ID: "ada-3f9a1c2b8d40", Kind: OpenNote, From: "Ada", Addr: "to", Date: "2026-09-09T12:34:56Z", Path: "from-ada/x.md", Subject: "s", Host: "air"}
	plain := hosted
	plain.Host = ""
	root := writeBus(t, nil)
	require.NoError(t, WriteOpen(root, "from-ada", []OpenEntry{plain, hosted}))
	got, err := ReadOpen(root, "from-ada")
	require.NoError(t, err, "ReadOpen over a file holding both widths: %v", err)
	if len(got) != 2 {
		require.Equal(t, 2, len(got), "read %d entries, want 2", len(got))
	}
	if got[0].Host != "" {
		require.False(t, got[0].Host != "", "the eight-field row came back with host %q", got[0].Host)
	}
	if got[1].Host != "air" {
		require.False(t, got[1].Host != "air", "the nine-field row came back with host %q, want %q", got[1].Host, "air")
	}
}

// A row of any OTHER width is still the refusal it was, and it still names both widths so
// a reader knows which one they have.
func TestAnOpenRowOfTheWrongWidthIsStillRefused(t *testing.T) {
	t.Parallel()

	root := writeBus(t, nil)
	write(t, root, OpenPath("from-ada"), OpenHeader+"\nada-3f9a1c2b8d40\tnote\t-\tAda\n")
	_, err := ReadOpen(root, "from-ada")
	require.Error(t, err, "a four-field row was accepted")
	for _, want := range []string{"9 tab-separated fields", "or 8 without the host", "got 4"} {
		require.Contains(t, err.Error(), want, "the refusal does not say %q:\n%v", want, err)
	}
}

// A reply posts from a machine too, and a reply with no host writes the reply this tool
// has always written.
func TestAReplyCarriesTheHostItIsGiven(t *testing.T) {
	t.Parallel()

	root := writeBus(t, map[string]string{
		"from-bo/note.md": "From: Bo\nTo: Ada\nDate: Wed Sep  9 12:00:00 UTC 2026\nId: bo-abcdef012345\nSubject: the gate\n\nA question.\n",
	})
	tab := loadBus(t, root)
	me := mustParticipant(t, tab.Config, "Ada")
	original, ok := tab.Resolve("bo-abcdef012345")
	require.True(t, ok, "the fixture note did not resolve")
	hosted, err := PrepareReplyFrom(tab, me, original, "Yes.\n", at("2026-09-09T12:34:56Z"), "air")
	require.NoError(t, err, "PrepareReplyFrom: %v", err)
	if !strings.Contains(hosted.Note.Render(), "From: Ada\nHost: air\nTo: Bo\n") {
		require.Contains(t, hosted.Note.Render(), "From: Ada\nHost: air\nTo: Bo\n", "the reply's Host line is not under From:\n%s", hosted.Note.Render())
	}
	plain, err := PrepareReply(tab, me, original, "Yes.\n", at("2026-09-09T12:34:56Z"))
	require.NoError(t, err, "PrepareReply: %v", err)
	if strings.Contains(plain.Note.Render(), "Host:") {
		require.NotContains(t, plain.Note.Render(), "Host:", "a reply nobody gave a host carries a Host line:\n%s", plain.Note.Render())
	}
	{
		err := ValidHost("The Air")
		require.Error(t, err, "ValidHost is what PrepareReplyFrom refuses on and it accepted a sentence")
	}
	{
		_, err := PrepareReplyFrom(tab, me, original, "Yes.\n", at("2026-09-09T12:34:56Z"), "The Air")
		require.Error(t, err, "a reply posted from a host that is not one word")
	}
}
