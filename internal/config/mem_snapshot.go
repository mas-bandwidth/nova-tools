package config

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// A Mem is saved to one JSON document and loaded from it again: the file the
// command's `--pg file:<path>` twin keeps between one verb and the next (for
// learning and tests, never a fleet's store). Snapshot and Restore are the
// whole of it: every row and history row of the store's state, so a restored
// store answers every call as the store it was taken from did.

// SnapshotVersion names the document's shape; a document of another version
// is refused, never guessed at.
const SnapshotVersion = 1

type memSnapshot struct {
	Version int                       `json:"version"`
	Schema  int                       `json:"schema"`
	Rows    map[string]map[string]Row `json:"rows"`
	History []Change                  `json:"history,omitempty"`
}

// Snapshot returns the store's state and schema version as an indented JSON document.
func (m *Mem) Snapshot(schema int) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := memSnapshot{
		Version: SnapshotVersion,
		Schema:  schema,
		Rows:    map[string]map[string]Row{},
		History: append([]Change(nil), m.history...),
	}
	for kind, rows := range m.rows {
		s.Rows[kind] = map[string]Row{}
		for name, r := range rows {
			s.Rows[kind][name] = r.Clone()
		}
	}
	return json.MarshalIndent(s, "", "  ")
}

// Restore replaces the store's state with a snapshot's. A document that is
// not a snapshot of this version is refused and the store is left as it was.
func (m *Mem) Restore(doc []byte) (int, error) {
	var s memSnapshot
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return 0, fmt.Errorf("not a twin snapshot: %w", err)
	}
	if s.Version != SnapshotVersion {
		return 0, fmt.Errorf("a twin snapshot of version %d; this build reads version %d", s.Version, SnapshotVersion)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows = map[string]map[string]Row{}
	for kind, rows := range s.Rows {
		m.rows[kind] = map[string]Row{}
		for name, r := range rows {
			m.rows[kind][name] = r.Clone()
		}
	}
	// Ensure singleton kinds have their rows initialized if not present in snapshot.
	for _, k := range Kinds {
		if !k.Singleton {
			continue
		}
		if _, ok := m.rows[k.Name][k.Name]; !ok {
			if m.rows[k.Name] == nil {
				m.rows[k.Name] = map[string]Row{}
			}
			row := Row{Name: k.Name, Fields: map[string]string{}, CreatedAt: m.stamp(), UpdatedAt: m.stamp()}
			for _, f := range k.Fields {
				row.Fields[f.Name] = ""
			}
			m.rows[k.Name][k.Name] = row
		}
	}
	m.history = append([]Change(nil), s.History...)
	return s.Schema, nil
}
