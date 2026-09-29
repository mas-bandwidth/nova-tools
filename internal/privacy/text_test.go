package privacy_test

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/mas-bandwidth/nova-tools/internal/privacy"
)

func utf16Bytes(s string, bigEndian, bom bool) []byte {
	var out []byte
	put := func(u uint16) {
		if bigEndian {
			out = append(out, byte(u>>8), byte(u))
		} else {
			out = append(out, byte(u), byte(u>>8))
		}
	}
	if bom {
		put(0xFEFF)
	}
	for _, u := range utf16.Encode([]rune(s)) {
		put(u)
	}
	return out
}

// Text in UTF-16 with a byte-order mark is decoded, never measured as bytes.
func TestUTF16WithAByteOrderMarkIsDecoded(t *testing.T) {
	t.Parallel()
	const leak = "thinking about the zarquon flibberty wumpus again, naïvely"
	for name, b := range map[string][]byte{
		"little endian": utf16Bytes(leak, false, true),
		"big endian":    utf16Bytes(leak, true, true),
		"utf-8 bom":     append([]byte{0xEF, 0xBB, 0xBF}, leak...),
	} {
		got, err := privacy.DecodeText(b, "payload")
		if err != nil || got != leak {
			t.Errorf("%s: got %q err %v, want the text itself", name, got, err)
		}
	}
}

// Bytes that are not text are refused by name, never cleared as no words.
func TestBytesThatAreNotTextAreRefused(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		b    []byte
		want string
	}{
		"utf-16 without a mark": {utf16Bytes("the zarquon engine", false, false), "NUL"},
		"a NUL in utf-8":        {[]byte("the zarquon\x00 engine"), "NUL"},
		"invalid utf-8":         {[]byte("the zarquon \xff\xfe engine flibberty wumpus"), "not valid UTF-8"},
		"odd utf-16":            {append(utf16Bytes("zarquon", false, true), 'x'), "odd"},
		"lone surrogate":        {[]byte{0xFF, 0xFE, 0x00, 0xD8, 'a', 0x00}, "surrogate"},
		"utf-16 holding a NUL":  {utf16Bytes("zarquon\x00engine", false, true), "NUL"},
	} {
		_, err := privacy.DecodeText(c.b, "payload")
		if !errors.Is(err, privacy.ErrNotText) || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "payload") {
			t.Errorf("%s: err %v, want ErrNotText naming the input and %q", name, err, c.want)
		}
	}
}

// A payload with no word in it is its own could-not-verify outcome: it was
// read, and there was nothing to compare.
func TestAPayloadWithNoWordsIsNotCleared(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	c := privacy.Load(f.spec)
	for _, p := range []string{"\u200b\u200b\u200b", "!!! --- ... ??? ***", "\u00ad\u2060\ufeff", "12345 678 90"} {
		r := privacy.Judge(c, p)
		if r.Outcome != privacy.PayloadHasNoWords || r.Outcome.Cleared() || !r.Outcome.CouldNotVerify() {
			t.Errorf("payload %q: outcome %s, want %s", p, r.Outcome, privacy.PayloadHasNoWords)
		}
		if !strings.Contains(r.Reason, "no word") || r.Remedy == "" {
			t.Errorf("payload %q: reason %q remedy %q", p, r.Reason, r.Remedy)
		}
	}
	if r := privacy.Judge(c, "a short but real reply"); r.Outcome != privacy.UnprovenClean {
		t.Errorf("a payload of ordinary words: outcome %s", r.Outcome)
	}
}

// The corpus is judged on its own: no payload, and so no payload outcome.
func TestTheCorpusIsJudgedWithoutAPayload(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	r := privacy.JudgeCorpus(privacy.Load(f.spec))
	if r.Outcome != privacy.UnprovenClean || r.Checkable != 3 {
		t.Errorf("outcome %s checkable %d", r.Outcome, r.Checkable)
	}
}
