package roadmap

import (
	"fmt"
	"strconv"
	"strings"

	roadmapdoc "github.com/mas-bandwidth/nova-tools/pkg/roadmap"
)

// A move across the two files carries every field of the record. A field the
// target shape has a key for is carried under that key; one it has no key for
// is kept under :kept as <from>-<key> (item-why on a fix, fix-status on an
// item), with an empty value recording a field that was absent; and a move
// back restores what :kept holds for the target shape and takes it off
// :kept. Nothing is dropped: the move roadmap -> fixes -> roadmap gives back
// every field of the item (TestARoundTripIsLossless).

// kept is a record's :kept pairs, in order, with set, take and has.
type kept []roadmapdoc.Pair

func (k *kept) set(key, v string) {
	for i := range *k {
		if (*k)[i].Key == key {
			(*k)[i].Value = v
			return
		}
	}
	*k = append(*k, roadmapdoc.Pair{Key: key, Value: v})
}

// take returns the value under key and removes it.
func (k *kept) take(key string) (string, bool) {
	for i, p := range *k {
		if p.Key == key {
			*k = append((*k)[:i:i], (*k)[i+1:]...)
			return p.Value, true
		}
	}
	return "", false
}

func (k *kept) setIf(key, v string) {
	if v != "" {
		k.set(key, v)
	}
}

// itemToFix is the fix an item becomes in release: planned, with the item's
// title, text and origin, and every other field of the item under :kept.
func itemToFix(it roadmapdoc.Item, release string) roadmapdoc.Fix {
	k := append(kept(nil), it.Kept...)
	k.set("item-group", it.Group)
	k.set("item-date", it.Date)
	k.setIf("item-why", it.Why)
	k.setIf("item-area", it.Area)
	k.setIf("item-exists", it.Exists)
	if it.Cards > 0 {
		k.set("item-cards", strconv.Itoa(it.Cards))
	}
	k.setIf("item-release", it.Release)
	origin := it.Origin
	if origin == "" { // a fix needs an origin; the item had none, and :kept says so
		origin = "roadmap item " + it.ID
		k.set("item-origin", "")
	}
	text := it.Text
	if v, ok := k.take("fix-text"); ok && v == "" && it.Text == it.Title {
		text = "" // the fix had no text; the item's was its title
	}
	if v, ok := k.take("fix-release"); ok && v != release {
		k.set("fix-release", v) // an earlier release, not this one: still kept
	}
	return roadmapdoc.Fix{ID: it.ID, Release: release, Title: it.Title, Text: text, Origin: origin, Status: Planned, Kept: k}
}

// fixToItem is the item a fix becomes under group: the fix's title, text and
// origin, its status and release under :kept, and every item field :kept
// holds from an earlier move restored.
func fixToItem(fx roadmapdoc.Fix, group, today string) roadmapdoc.Item {
	k := append(kept(nil), fx.Kept...)
	k.set("fix-status", fx.Status)
	k.set("fix-release", fx.Release)
	text := fx.Text
	if strings.TrimSpace(text) == "" { // an item needs text; the fix had none
		text = fx.Title
		k.set("fix-text", "")
	}
	it := roadmapdoc.Item{ID: fx.ID, Group: group, Title: fx.Title, Text: text, Origin: fx.Origin, Date: today}
	if v, ok := k.take("item-origin"); ok {
		if v == "" && fx.Origin == "roadmap item "+fx.ID {
			it.Origin = ""
		} else if v != "" {
			k.set("item-origin", v)
		}
	}
	if v, ok := k.take("item-date"); ok {
		it.Date = v
	}
	if v, ok := k.take("item-group"); ok && v != group {
		k.set("item-group", v) // an earlier group, not this one: still kept
	}
	it.Why, _ = k.take("item-why")
	it.Area, _ = k.take("item-area")
	it.Exists, _ = k.take("item-exists")
	it.Release, _ = k.take("item-release")
	if v, ok := k.take("item-cards"); ok {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			k.set("item-cards", v) // not a count: kept as it was
		} else {
			it.Cards = n
		}
	}
	it.Kept = k
	return it
}

// itemToDone is the :done note an item becomes: its title, its text with the
// evidence, dated today, and every other field of the item under :kept.
func itemToDone(it roadmapdoc.Item, note, today string) roadmapdoc.Note {
	k := append(kept(nil), it.Kept...)
	k.set("item-group", it.Group)
	k.set("item-date", it.Date)
	k.setIf("item-why", it.Why)
	k.setIf("item-area", it.Area)
	k.setIf("item-exists", it.Exists)
	if it.Cards > 0 {
		k.set("item-cards", strconv.Itoa(it.Cards))
	}
	k.setIf("item-release", it.Release)
	k.setIf("item-origin", it.Origin)
	return roadmapdoc.Note{ID: it.ID, Title: it.Title, Text: strings.TrimRight(it.Text, " \n\t") + note, Date: today, Kept: k}
}

// The records below write a value as it was read (a text wrapped across lines
// keeps its line breaks), so a carried field is the same bytes inside its
// quotes.

func itemRecord(it roadmapdoc.Item) string {
	var b strings.Builder
	fmt.Fprintf(&b, "(item %s :group %s :date %s\n   :title %s\n   :text %s", quote(it.ID), quote(it.Group), quote(it.Date), quote(it.Title), quote(it.Text))
	for _, kv := range [][2]string{{"why", it.Why}, {"area", it.Area}, {"exists", it.Exists}, {"release", it.Release}, {"origin", it.Origin}} {
		if kv[1] != "" {
			fmt.Fprintf(&b, "\n   :%s %s", kv[0], quote(kv[1]))
		}
	}
	if it.Cards > 0 {
		fmt.Fprintf(&b, "\n   :cards %d", it.Cards)
	}
	return b.String() + keptRecord(it.Kept) + ")"
}

func fixRecord(fx roadmapdoc.Fix) string {
	var b strings.Builder
	fmt.Fprintf(&b, "(fix %s :release %s :status %s\n   :title %s", quote(fx.ID), quote(fx.Release), quote(fx.Status), quote(fx.Title))
	if strings.TrimSpace(fx.Text) != "" {
		fmt.Fprintf(&b, "\n   :text %s", quote(fx.Text))
	}
	fmt.Fprintf(&b, "\n   :origin %s", quote(fx.Origin))
	return b.String() + keptRecord(fx.Kept) + ")"
}

func doneRecord(n roadmapdoc.Note) string {
	return fmt.Sprintf("(done %s :title %s\n   :text %s\n   :date %s", quote(n.ID), quote(n.Title), quote(n.Text), quote(n.Date)) + keptRecord(n.Kept) + ")"
}

func keptRecord(k []roadmapdoc.Pair) string {
	if len(k) == 0 {
		return ""
	}
	var parts []string
	for _, p := range k {
		parts = append(parts, ":"+p.Key+" "+quote(p.Value))
	}
	return "\n   :kept (" + strings.Join(parts, "\n          ") + ")"
}
