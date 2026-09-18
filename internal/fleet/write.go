package fleet

// Writing the machines registry.
//
// The registry was edited by hand three times on 2026-09-18 -- twice with `sed`, once with
// a python one-liner -- to add a machine and to correct two rows. Each edit was a control
// file changed by a tool that knows nothing about it: nothing held the new row to the
// roles set, nothing noticed a name written twice, and nothing would have caught a
// `bench,runner` line whose dated exception was missing until a card landed on a CI runner
// host. A control file must be written by a verb that validates it.
//
// So: Add and Set render the WHOLE file and hand it back to parseRegistry before a byte
// reaches the disk. A row a verb writes is held to exactly the rules a row a person writes
// is held to, by the same reader, and a refusal leaves the file on disk untouched. The
// file's own header, comments, blank lines and untouched rows are carried through
// verbatim, because the header is where the lock is written down and a writer that eats it
// costs more than it saves.
//
// Save is temp-and-rename in the file's own directory, so a reader either sees the whole
// old file or the whole new one and never half of either.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Change is what `registry set` may change about one machine. A nil field is a field the
// command line did not name, which is left exactly as the file has it -- so `set --notes`
// is a note and not a rewritten row.
type Change struct {
	SSH      *string
	OSArch   *string
	Roles    *string
	Seat     *string
	Cores    *int
	Notes    *string
	Provider *string
	MAC      *string
}

// Any says whether the change asks for anything at all. A `set` that names no column is a
// refusal, not a no-op write: rewriting a file to say the same thing loses the difference
// between "nothing to do" and "the flag was misspelled".
func (c Change) Any() bool {
	return c.SSH != nil || c.OSArch != nil || c.Roles != nil || c.Seat != nil ||
		c.Cores != nil || c.Notes != nil || c.Provider != nil || c.MAC != nil
}

// Fields is the columns the change names, in file order, as a line prints them.
func (c Change) Fields() []string {
	var out []string
	for _, f := range []struct {
		name string
		set  bool
	}{
		{"ssh", c.SSH != nil}, {"os", c.OSArch != nil}, {"roles", c.Roles != nil},
		{"seat", c.Seat != nil}, {"cores", c.Cores != nil}, {"notes", c.Notes != nil},
		{"provider", c.Provider != nil}, {"mac", c.MAC != nil},
	} {
		if f.set {
			out = append(out, f.name)
		}
	}
	return out
}

// Draft is one machine as the command line gave it, before the reader has seen it. Every
// field is the text of a flag: it is rendered into a row and read back, so the reader --
// and only the reader -- decides whether it is a machine.
type Draft struct {
	Name     string
	SSH      string
	OSArch   string
	Roles    string
	Seat     string
	Cores    int
	Notes    string
	Provider string
	MAC      string
}

// row renders a draft as the nine tab-separated columns, with `-` for the columns it does
// not carry. Nothing here validates: the reader does that, once, for every writer.
func (d Draft) row() string {
	provider := strings.TrimSpace(d.Provider)
	if provider == "" {
		provider = ProviderSelf
	}
	return strings.Join([]string{
		strings.TrimSpace(d.Name), strings.TrimSpace(d.SSH), strings.TrimSpace(d.OSArch),
		strings.TrimSpace(d.Roles), dash(d.Seat), strconv.Itoa(d.Cores), dash(d.Notes),
		provider, dash(d.MAC),
	}, "\t")
}

// Add appends one machine to the registry and returns the registry as it now reads. The
// file on disk is not touched: Save writes it.
//
// A duplicate name is refused here, by the reader, and so is every other rule -- an unknown
// role, a bench+runner row with no dated exception, a provider outside the set, a wake
// address that is not six bytes, a lan-bench this file does not name.
func (r *Registry) Add(d Draft) (*Registry, Machine, error) {
	name := strings.TrimSpace(d.Name)
	if name == "" {
		return nil, Machine{}, fmt.Errorf("a machine needs a name; run: nova-pulse fleet registry add --machines %s --name <n> --ssh <user@host> --os <goos/goarch> --roles <a,b> --seat <s|-> --cores <n> --notes <text>", r.path)
	}
	if m, ok := r.Lookup(name); ok {
		return nil, Machine{}, fmt.Errorf("%s already names %s, on line %d; one line per machine (run: nova-pulse fleet registry set --machines %s --name %s … to change it)",
			r.path, name, m.Line, r.path, name)
	}
	next, err := parseRegistry(r.path, render(append(append([]string{}, r.lines...), d.row())))
	if err != nil {
		return nil, Machine{}, err
	}
	m, _ := next.Lookup(name)
	return next, m, nil
}

// Set changes the named machine's columns and returns the registry as it now reads. The
// row is rebuilt from the machine the reader already read, so a column the change does not
// name comes through exactly as the file has it, and the row is then read back.
func (r *Registry) Set(name string, c Change) (*Registry, Machine, error) {
	name = strings.TrimSpace(name)
	m, ok := r.Lookup(name)
	if !ok {
		return nil, Machine{}, fmt.Errorf("%s does not name %s; the machines are %s (run: nova-pulse fleet registry add --machines %s --name %s … to add it)",
			r.path, dash(name), strings.Join(r.names(), ", "), r.path, dash(name))
	}
	if !c.Any() {
		return nil, Machine{}, fmt.Errorf("set %s names no column to change; give at least one of --ssh, --os, --roles, --seat, --cores, --notes, --provider, --mac", name)
	}
	d := Draft{
		Name: m.Name, SSH: m.SSH, OSArch: m.OS + "/" + m.Arch, Roles: m.RoleList(),
		Seat: m.Seat, Cores: m.Cores, Notes: m.Notes, Provider: m.Provider, MAC: m.MACField(),
	}
	if c.SSH != nil {
		d.SSH = *c.SSH
	}
	if c.OSArch != nil {
		d.OSArch = *c.OSArch
	}
	if c.Roles != nil {
		d.Roles = *c.Roles
	}
	if c.Seat != nil {
		d.Seat = *c.Seat
	}
	if c.Cores != nil {
		d.Cores = *c.Cores
	}
	if c.Notes != nil {
		d.Notes = *c.Notes
	}
	if c.Provider != nil {
		d.Provider = *c.Provider
	}
	if c.MAC != nil {
		d.MAC = *c.MAC
	}

	lines := append([]string{}, r.lines...)
	if m.Line < 1 || m.Line > len(lines) {
		return nil, Machine{}, fmt.Errorf("%s: %s was read from line %d, which %s no longer has; read the file again",
			r.path, name, m.Line, r.path)
	}
	lines[m.Line-1] = d.row()
	next, err := parseRegistry(r.path, render(lines))
	if err != nil {
		return nil, Machine{}, err
	}
	out, _ := next.Lookup(name)
	return next, out, nil
}

// render is the file's lines as the file's bytes: one trailing newline, never two and
// never none.
func render(lines []string) string {
	return strings.Join(lines, "\n") + "\n"
}

// Bytes is the whole file as this registry would write it.
func (r *Registry) Bytes() []byte { return []byte(render(r.lines)) }

// Save writes the registry to its own path, atomically: a temp file in the same directory,
// synced, then renamed over the original. A reader racing the write sees the whole old
// file or the whole new one, and a write that dies half way leaves the original alone.
func (r *Registry) Save() error {
	dir := filepath.Dir(r.path)
	tmp, err := os.CreateTemp(dir, ".machines-*.tsv")
	if err != nil {
		return fmt.Errorf("cannot write beside %s: %w (check the directory exists and is writable)", r.path, err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(r.Bytes()); err != nil {
		tmp.Close()
		return fmt.Errorf("cannot write %s: %w", name, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("cannot flush %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("cannot close %s: %w", name, err)
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return fmt.Errorf("cannot set the mode of %s: %w", name, err)
	}
	if err := os.Rename(name, r.path); err != nil {
		return fmt.Errorf("cannot move %s over %s: %w", name, r.path, err)
	}
	return nil
}
