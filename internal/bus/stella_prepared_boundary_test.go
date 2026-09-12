package bus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stellaIndependentPrepared(t *testing.T) (string, string, Prepared, PreparedArtifact) {
	t.Helper()
	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)
	tab := loadBus(t, clone)
	p, e := PrepareDraft(tab, "From: Ada\nTo: Bo\nSubject: Boundary fixture\n\nSynthetic note.\n", at("2026-09-12T17:00:00Z"), "boundary", "Ada")
	if e != nil {
		t.Fatal(e)
	}
	a, e := MakePreparedArtifact(p)
	if e != nil {
		t.Fatal(e)
	}
	return bare, clone, p, a
}
func TestStellaIndependentPreparedRequiresCompleteRemoteIndex(t *testing.T) {
	_, clone, p, a := stellaIndependentPrepared(t)
	if _, e := SendPreparedArtifact(clone, "origin", "main", p, a, 1); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(clone, IndexPath(p.Sender.Lane))
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	expected := IndexLine(p.Index)
	fields := strings.Split(expected, "\t")
	fields[len(fields)-1] = "SYNTHETIC_WRONG_INDEX_SUBJECT"
	changed := strings.Replace(string(b), expected, strings.Join(fields, "\t"), 1)
	if changed == string(b) {
		t.Fatal("did not mutate index")
	}
	os.WriteFile(path, []byte(changed), 0644)
	id := Identity{Name: p.Sender.GitName, Email: p.Sender.GitEmail}
	if _, e := stageAndCommit(clone, id, []string{IndexPath(p.Sender.Lane)}, "mutate synthetic index"); e != nil {
		t.Fatal(e)
	}
	if _, e := git(clone, "push", "origin", "HEAD:main"); e != nil {
		t.Fatal(e)
	}
	if r, e := SendPreparedArtifact(clone, "origin", "main", p, a, 1); e == nil && r.Pushed {
		t.Fatal("claimed already-published with a different INDEX record")
	}
}
func TestStellaIndependentPreparedCannotConfirmCommitWithoutIndex(t *testing.T) {
	bare, clone, p, a := stellaIndependentPrepared(t)
	if e := p.Save(clone); e != nil {
		t.Fatal(e)
	}
	id := Identity{Name: p.Sender.GitName, Email: p.Sender.GitEmail}
	if _, e := stageAndCommit(clone, id, []string{p.Path}, WithTrailer(p.Message, TrailerSend+" "+a.ID)); e != nil {
		t.Fatal(e)
	}
	r, e := SendPreparedArtifact(clone, "origin", "main", p, a, 1)
	if e != nil {
		return
	}
	index, e := git(bare, "show", "main:"+IndexPath(p.Sender.Lane))
	if r.Pushed && (e != nil || !strings.Contains(index, IndexLine(p.Index))) {
		t.Fatal("claimed success after publishing note-only commit without INDEX entry")
	}
}
func TestStellaIndependentPreparedPreservesUnrelatedAttributeEdit(t *testing.T) {
	bare, clone, p, a := stellaIndependentPrepared(t)
	path := filepath.Join(clone, AttributesName)
	old, _ := os.ReadFile(path)
	sentinel := "# synthetic_private_unrelated_attribute_edit\n"
	want := append(old, []byte(sentinel)...)
	os.WriteFile(path, want, 0644)
	r, e := SendPreparedArtifact(clone, "origin", "main", p, a, 1)
	if e == nil && r.Pushed {
		remote, _ := git(bare, "show", "main:"+AttributesName)
		if strings.Contains(remote, sentinel) {
			t.Fatal("published unrelated dirty attribute content during prepared delivery")
		}
	}
	now, _ := os.ReadFile(path)
	if string(now) != string(want) {
		t.Fatal("refusal changed unrelated dirty attribute content")
	}
}
func TestStellaIndependentPreparedRefusesUnknownArtifactField(t *testing.T) {
	_, clone, p, a := stellaIndependentPrepared(t)
	b, _ := json.Marshal(a)
	b = append(b[:len(b)-1], []byte(`,"unsupported":"synthetic"}`)...)
	if _, _, e := ValidatePreparedArtifact(b, clone, loadBus(t, clone).Config, p.Sender.Name); e == nil {
		t.Fatal("accepted unknown artifact field")
	}
}

func TestStellaIndependentPreparedPreservesUnrelatedAheadAttributeEdit(t *testing.T) {
	bare, clone, p, a := stellaIndependentPrepared(t)
	if e := p.Save(clone); e != nil {
		t.Fatal(e)
	}
	if e := p.AppendIndex(clone); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(clone, AttributesName)
	old, _ := os.ReadFile(path)
	sentinel := "# synthetic_unrelated_ahead_attribute_edit\n"
	if e := os.WriteFile(path, append(old, []byte(sentinel)...), 0644); e != nil {
		t.Fatal(e)
	}
	id := Identity{Name: p.Sender.GitName, Email: p.Sender.GitEmail}
	if _, e := stageAndCommit(clone, id, []string{p.Path, IndexPath(p.Sender.Lane), AttributesName}, WithTrailer(p.Message, TrailerSend+" "+a.ID)); e != nil {
		t.Fatal(e)
	}
	r, e := SendPreparedArtifact(clone, "origin", "main", p, a, 1)
	if e == nil && r.Pushed {
		remote, _ := git(bare, "show", "main:"+AttributesName)
		if strings.Contains(remote, sentinel) {
			t.Fatal("published unrelated committed attribute content with an allowed trailer")
		}
	}
}

func TestStellaIndependentPreparedRefusesAheadAttributeDeletion(t *testing.T) {
	bare, clone, p, a := stellaIndependentPrepared(t)
	if e := p.Save(clone); e != nil {
		t.Fatal(e)
	}
	if e := p.AppendIndex(clone); e != nil {
		t.Fatal(e)
	}
	// Seed a tracked attribute file on remote before creating the send contribution.
	id := Identity{Name: p.Sender.GitName, Email: p.Sender.GitEmail}
	if e := os.WriteFile(filepath.Join(clone, AttributesName), []byte("# synthetic original attribute\n"), 0644); e != nil {
		t.Fatal(e)
	}
	if _, e := stageAndCommit(clone, id, []string{AttributesName}, "synthetic base attrs"); e != nil {
		t.Fatal(e)
	}
	if _, e := git(clone, "push", "origin", "HEAD:main"); e != nil {
		t.Fatal(e)
	}
	if e := os.Remove(filepath.Join(clone, AttributesName)); e != nil {
		t.Fatal(e)
	}
	if _, e := stageAndCommit(clone, id, []string{p.Path, IndexPath(p.Sender.Lane), AttributesName}, WithTrailer(p.Message, TrailerSend+" "+a.ID)); e != nil {
		t.Fatal(e)
	}
	if r, e := SendPreparedArtifact(clone, "origin", "main", p, a, 1); e == nil && r.Pushed {
		if _, e := git(bare, "show", "main:"+AttributesName); e != nil {
			t.Fatal("published unrelated attribute deletion")
		}
	}
}
func TestStellaIndependentPreparedRefusesIntermediateIndexLeak(t *testing.T) {
	bare, clone, p, a := stellaIndependentPrepared(t)
	if e := p.Save(clone); e != nil {
		t.Fatal(e)
	}
	if e := p.AppendIndex(clone); e != nil {
		t.Fatal(e)
	}
	indexPath := filepath.Join(clone, IndexPath(p.Sender.Lane))
	good, e := os.ReadFile(indexPath)
	if e != nil {
		t.Fatal(e)
	}
	sentinel := "SYNTHETIC_PRIVATE_HISTORY_SENTINEL\n"
	if e := os.WriteFile(indexPath, append(good, []byte(sentinel)...), 0644); e != nil {
		t.Fatal(e)
	}
	id := Identity{Name: p.Sender.GitName, Email: p.Sender.GitEmail}
	msg := WithTrailer(p.Message, TrailerSend+" "+a.ID)
	if _, e := stageAndCommit(clone, id, []string{p.Path, IndexPath(p.Sender.Lane)}, msg); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(indexPath, good, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e := stageAndCommit(clone, id, []string{IndexPath(p.Sender.Lane)}, msg); e != nil {
		t.Fatal(e)
	}
	if r, e := SendPreparedArtifact(clone, "origin", "main", p, a, 1); e == nil && r.Pushed {
		history, _ := git(bare, "log", "-p", "main", "--", IndexPath(p.Sender.Lane))
		if strings.Contains(history, sentinel) {
			t.Fatal("published unrelated content in intermediate commit history")
		}
	}
}
