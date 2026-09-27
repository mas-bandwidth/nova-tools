package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The typed lines nova-config prints (docs/SPEC-CONFIG.md, "Lines"). Every
// line is one event, first token the record, then key=value fields whose
// values go through oneline.Field, so a whitespace-splitting scanner sees
// exactly the fields the tool wrote; an empty value prints as "-".

// Value renders one field value.
func Value(s string) string {
	if s == "" {
		return "-"
	}
	return oneline.Field(s)
}

// RowLine is a row: `FRIEND name=<n> <field>=<v> ...`, every field of the
// kind in declaration order.
func RowLine(k *Kind, row Row) string {
	var b strings.Builder
	b.WriteString(strings.ToUpper(k.Name))
	b.WriteString(" name=" + Value(row.Name))
	for _, f := range k.Fields {
		b.WriteString(" " + f.Name + "=" + Value(row.Fields[f.Name]))
	}
	return b.String()
}

// ShowLine is RowLine with the row's stamps.
func ShowLine(k *Kind, row Row) string {
	return RowLine(k, row) + " created=" + Value(row.CreatedAt) + " updated=" + Value(row.UpdatedAt)
}

// HistoryLine is one change: `HISTORY id=<n> kind=<k> name=<n> op=<add|set|remove>
// actor=<a> at=<rfc3339>` then, for a set, each changed field as
// `<field>=<before>><after>`; for an add every field's value; for a remove
// every field's last value.
func HistoryLine(c Change) string {
	var b strings.Builder
	fmt.Fprintf(&b, "HISTORY id=%d kind=%s name=%s op=%s actor=%s at=%s", c.ID, Value(c.Kind), Value(c.Name), Value(c.Op), Value(c.Actor), Value(c.At))
	switch c.Op {
	case OpAdd:
		for _, f := range sortedKeys(c.After) {
			b.WriteString(" " + f + "=" + Value(c.After[f]))
		}
	case OpRemove:
		for _, f := range sortedKeys(c.Before) {
			b.WriteString(" " + f + "=" + Value(c.Before[f]))
		}
	default:
		for _, f := range sortedKeys(c.After) {
			if c.Before[f] != c.After[f] {
				b.WriteString(" " + f + "=" + Value(c.Before[f]) + ">" + Value(c.After[f]))
			}
		}
	}
	return b.String()
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// OpLine is one line of an apply or a check: `APPLY ADD kind=<k> name=<n>`,
// `APPLY SET kind=<k> name=<n> changed=<f,g>`, `APPLY REMOVE kind=<k>
// name=<n>`; the first word is CHECK when nothing is written.
func OpLine(word string, kind string, op Op) string {
	line := word + " " + strings.ToUpper(op.Op) + " kind=" + Value(kind) + " name=" + Value(op.Name)
	if op.Op == OpSet {
		line += " changed=" + Value(strings.Join(op.Changed, ","))
	}
	return line
}

// KindLine is one line of `nova-config kinds`: `CONFIG KIND name=<k>
// table=config.<t> fields=<f,g,...> required=<f,...> rows=many|one` (one:
// a singleton kind, whose row the migration creates).
func KindLine(k *Kind) string {
	var required []string
	for _, f := range k.Fields {
		if f.Required {
			required = append(required, f.Name)
		}
	}
	rows := "many"
	if k.Singleton {
		rows = "one"
	}
	return "CONFIG KIND name=" + k.Name + " table=config." + k.Table + " fields=" + Value(strings.Join(k.FieldNames(), ",")) + " required=" + Value(strings.Join(required, ",")) + " rows=" + rows
}

// LiveLine is the measured facts a machine's list and show lines carry
// after the declared fields when Redis is at hand (docs/SPEC-CONFIG.md,
// "Declared and measured"): ` os=<v> arch=<v> cores=<n> memory_gb=<n>
// beat=<rfc3339>`, each `-` when the beat does not carry it, and
// `beat=none` alone when the machine has no beat. Nothing here is stored or
// typed: it is what the machine reported last.
func LiveLine(b *Beat) string {
	if b == nil {
		return " beat=none"
	}
	return " os=" + Value(b.OS) + " arch=" + Value(b.Arch) + " cores=" + Value(b.Cores) + " memory_gb=" + Value(b.MemoryGB) + " beat=" + Value(b.At)
}
