package bus

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ArchivePlan calculates which notes are to be archived before a given instant.
type ArchivePlan struct {
	BusDir        string
	Before        time.Time
	Target        string
	IsTarball     bool
	ArchivedNotes []Note
	KeptNotes     []Note
	ArchivedCount int
	KeptCount     int
	TouchedLanes  []string
	config        *Config
}

// ArchiveExecutionResult contains the outcome of executing an ArchivePlan.
type ArchiveExecutionResult struct {
	Archived     int
	Kept         int
	TargetPath   string
	TouchedPaths []string
}

// PlanArchive inspects the bus and identifies notes older than the specified RFC 3339 instant.
func PlanArchive(t *Bus, before time.Time, target string) (*ArchivePlan, error) {
	plan := &ArchivePlan{
		BusDir:  t.Root,
		Before:  before,
		Target:  target,
		config:  t.Config,
	}

	lower := strings.ToLower(target)
	if strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz") {
		plan.IsTarball = true
	}

	laneSet := make(map[string]bool)
	for _, n := range t.Notes {
		when := n.When()
		if when.IsZero() {
			when = n.legacyDay()
		}
		// A note that cannot say when it was written cannot claim to predate anything.
		if !when.IsZero() && when.Before(before) {
			plan.ArchivedNotes = append(plan.ArchivedNotes, n)
			laneSet[n.Lane] = true
		} else {
			plan.KeptNotes = append(plan.KeptNotes, n)
		}
	}

	plan.ArchivedCount = len(plan.ArchivedNotes)
	plan.KeptCount = len(plan.KeptNotes)

	for lane := range laneSet {
		plan.TouchedLanes = append(plan.TouchedLanes, lane)
	}
	sort.Strings(plan.TouchedLanes)

	return plan, nil
}

// Execute performs the archiving: moving or packaging notes, updating lane indexes,
// updating OPEN files and receipts tracking, and maintaining archive/INDEX.
func (plan *ArchivePlan) Execute(busDir string) (*ArchiveExecutionResult, error) {
	res := &ArchiveExecutionResult{
		Archived:   plan.ArchivedCount,
		Kept:       plan.KeptCount,
		TargetPath: plan.Target,
	}

	if plan.ArchivedCount == 0 {
		return res, nil
	}

	touched := make(map[string]bool)

	// Step 1: Write archived notes to destination (tarball or directory)
	if plan.IsTarball {
		if err := writeTarball(busDir, plan.Target, plan.ArchivedNotes); err != nil {
			return nil, fmt.Errorf("archive tarball failed: %w", err)
		}
		if rel, err := filepath.Rel(busDir, plan.Target); err == nil && !strings.HasPrefix(rel, "..") {
			touched[filepath.ToSlash(rel)] = true
		}
	} else {
		if err := copyNotesToDir(busDir, plan.Target, plan.ArchivedNotes); err != nil {
			return nil, fmt.Errorf("archive directory copy failed: %w", err)
		}
		for _, n := range plan.ArchivedNotes {
			destFile := filepath.Join(plan.Target, filepath.FromSlash(n.Path))
			if rel, err := filepath.Rel(busDir, destFile); err == nil && !strings.HasPrefix(rel, "..") {
				touched[filepath.ToSlash(rel)] = true
			}
		}
	}

	// Step 2: Update/create archive/INDEX at <busDir>/archive/INDEX
	archiveDir := filepath.Join(busDir, "archive")
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		return nil, fmt.Errorf("creating archive directory: %w", err)
	}

	if err := updateArchiveIndex(archiveDir, plan.config, plan.ArchivedNotes); err != nil {
		return nil, fmt.Errorf("updating archive index: %w", err)
	}
	touched["archive/"+IndexName] = true

	// Step 3: Remove archived note files from active lanes
	for _, n := range plan.ArchivedNotes {
		src := filepath.Join(busDir, filepath.FromSlash(n.Path))
		if err := os.Remove(src); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("removing archived note %s: %w", n.Path, err)
		}
		touched[n.Path] = true
	}

	// Step 4: Update lane INDEX files (remove archived notes from active lane INDEX)
	archivedIDSet := make(map[string]bool)
	archivedPathSet := make(map[string]bool)
	for _, n := range plan.ArchivedNotes {
		if n.Header.ID != "" {
			archivedIDSet[n.Header.ID] = true
		}
		archivedPathSet[n.Path] = true
	}

	for _, lane := range plan.TouchedLanes {
		entries, err := ReadLaneIndex(busDir, lane)
		if err != nil {
			return nil, fmt.Errorf("reading lane index %s: %w", lane, err)
		}
		var keptEntries []IndexEntry
		for _, e := range entries {
			if (e.ID != "" && archivedIDSet[e.ID]) || archivedPathSet[e.Path] {
				continue
			}
			keptEntries = append(keptEntries, e)
		}
		var b strings.Builder
		for _, e := range keptEntries {
			b.WriteString(IndexLine(e) + "\n")
		}
		if err := replaceLaneFile(busDir, IndexPath(lane), b.String()); err != nil {
			return nil, fmt.Errorf("replacing lane index %s: %w", lane, err)
		}
		touched[IndexPath(lane)] = true
	}

	// Step 5: Update reader OPEN files and preserve CURSOR positions
	dirEntries, _ := os.ReadDir(busDir)
	for _, d := range dirEntries {
		if !d.IsDir() || !strings.HasPrefix(d.Name(), "from-") {
			continue
		}
		lane := d.Name()
		openEntries, err := ReadOpen(busDir, lane)
		if err != nil || len(openEntries) == 0 {
			continue
		}
		modified := false
		for i := range openEntries {
			e := &openEntries[i]
			if (e.ID != "" && archivedIDSet[e.ID]) || archivedPathSet[e.Path] {
				// The note is archived. If archived in directory target inside bus,
				// track its archived path so cursor/open does not point to deleted file.
				if !plan.IsTarball {
					if !strings.HasPrefix(e.Path, "archive/") {
						e.Path = "archive/" + e.Path
						modified = true
					}
				}
			}
		}
		if modified {
			if err := WriteOpen(busDir, lane, openEntries); err != nil {
				return nil, fmt.Errorf("updating open list for %s: %w", lane, err)
			}
			touched[OpenPath(lane)] = true
		}
	}

	for p := range touched {
		res.TouchedPaths = append(res.TouchedPaths, p)
	}
	sort.Strings(res.TouchedPaths)

	return res, nil
}

func writeTarball(busDir, tarballPath string, notes []Note) error {
	if err := os.MkdirAll(filepath.Dir(tarballPath), 0755); err != nil {
		return err
	}
	f, err := os.Create(tarballPath)
	if err != nil {
		return err
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	for _, n := range notes {
		src := filepath.Join(busDir, filepath.FromSlash(n.Path))
		info, err := os.Stat(src)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		hdr := &tar.Header{
			Name:    n.Path,
			Mode:    0644,
			Size:    int64(len(data)),
			ModTime: info.ModTime(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := tw.Write(data); err != nil {
			return err
		}
	}
	return nil
}

func copyNotesToDir(busDir, targetDir string, notes []Note) error {
	for _, n := range notes {
		src := filepath.Join(busDir, filepath.FromSlash(n.Path))
		dest := filepath.Join(targetDir, filepath.FromSlash(n.Path))
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dest, data, 0644); err != nil {
			return err
		}
	}
	return nil
}

func updateArchiveIndex(archiveDir string, c *Config, notes []Note) error {
	indexPath := filepath.Join(archiveDir, IndexName)
	existing := make(map[string]IndexEntry)
	if raw, err := os.ReadFile(indexPath); err == nil {
		for _, r := range records(string(raw)) {
			fields := strings.Split(r.text, "\t")
			if len(fields) >= indexFields {
				e := IndexEntry{
					ID:   fields[0],
					Path: fields[1],
					Date: undash(fields[2]),
					To:   splitList(fields[3]),
					Re:   splitList(fields[4]),
					Lane: "archive",
					Line: r.line,
				}
				existing[e.Path] = e
			}
		}
	}

	for _, n := range notes {
		entry := IndexEntryFor(c, n)
		existing[entry.Path] = entry
	}

	var allEntries []IndexEntry
	for _, e := range existing {
		allEntries = append(allEntries, e)
	}
	sort.Slice(allEntries, func(i, j int) bool {
		if allEntries[i].Path != allEntries[j].Path {
			return allEntries[i].Path < allEntries[j].Path
		}
		return allEntries[i].ID < allEntries[j].ID
	})

	var b strings.Builder
	for _, e := range allEntries {
		b.WriteString(IndexLine(e) + "\n")
	}

	return replaceLaneFile(filepath.Dir(archiveDir), "archive/"+IndexName, b.String())
}
