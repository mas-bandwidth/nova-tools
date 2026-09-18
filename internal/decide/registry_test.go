package decide

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The embedded default is the ladder of minds Glenn named: Flash and Pro at the
// bottom on the DeepSeek lineage, the child rungs Opus and Sol at ONE height in
// two lineages, the friends above them, Fable and Astra at the top pair, then
// all friends at once, then Glenn.
func TestDefaultRegistryCarriesTheLadder(t *testing.T) {
	reg, err := DefaultRegistry()
	if err != nil {
		t.Fatalf("the embedded registry does not parse: %v", err)
	}
	flash, ok := reg.ByName("flash")
	if !ok {
		t.Fatal("the registry names no flash rung")
	}
	pro, ok := reg.ByName("pro")
	if !ok {
		t.Fatal("the registry names no pro rung")
	}
	if flash.Lineage != "deepseek" || pro.Lineage != "deepseek" {
		t.Errorf("flash and pro are the DeepSeek lineage, got %q and %q", flash.Lineage, pro.Lineage)
	}
	if flash.Height >= pro.Height {
		t.Errorf("flash sits below pro, got %d and %d", flash.Height, pro.Height)
	}
	opus, ok := reg.ByName("opus")
	if !ok {
		t.Fatal("the registry names no opus rung")
	}
	sol, ok := reg.ByName("sol")
	if !ok {
		t.Fatal("the registry names no sol rung")
	}
	if opus.Height != sol.Height {
		t.Errorf("opus and sol are the same height, got %d and %d", opus.Height, sol.Height)
	}
	if opus.Lineage == sol.Lineage {
		t.Errorf("opus and sol are different lineages, both %q", opus.Lineage)
	}
	astra, _ := reg.ByName("astra")
	fable, _ := reg.ByName("fable")
	all, _ := reg.ByName("all-friends")
	glenn, _ := reg.ByName("glenn")
	if astra.Height != fable.Height {
		t.Errorf("astra and fable are the top pair at one height, got %d and %d", astra.Height, fable.Height)
	}
	if !(fable.Height < all.Height && all.Height < glenn.Height) {
		t.Errorf("the top is fable/astra (%d) then all-friends (%d) then glenn (%d)", fable.Height, all.Height, glenn.Height)
	}
	johnny, ok := reg.ByName("johnny")
	if !ok {
		t.Fatal("the registry names no johnny")
	}
	for _, kind := range []string{KindGuard, DesignationFreshTake} {
		if !johnny.DesignatedFor(kind) {
			t.Errorf("johnny is designated for %q by kind, not by height; his kinds are %v", kind, johnny.Kinds)
		}
	}
	if len(johnny.Lanes) == 0 {
		t.Error("every friend is a mind with an owned lane; johnny owns none")
	}
}

// Every rung is reached one of three ways, and the row says which.
func TestDefaultRegistryNamesHowEachRungIsAsked(t *testing.T) {
	reg, err := DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"flash": AskCard, "pro": AskCard, "opus": AskChild, "sol": AskChild, "johnny": AskBus, "glenn": AskBus}
	for name, ask := range want {
		m, ok := reg.ByName(name)
		if !ok {
			t.Fatalf("no rung %q", name)
		}
		if m.Ask != ask {
			t.Errorf("%s is asked by %q, want %q", name, m.Ask, ask)
		}
	}
}

// A registry is data, and bad data is a refusal, never a guess.
func TestParseRegistryRefusesBadRows(t *testing.T) {
	for name, body := range map[string]string{
		"empty":            `{"minds": []}`,
		"duplicate name":   `{"minds":[{"name":"a","lineage":"x","height":0,"availability":"available"},{"name":"a","lineage":"y","height":1,"availability":"available"}]}`,
		"no lineage":       `{"minds":[{"name":"a","height":0,"availability":"available"}]}`,
		"bad availability": `{"minds":[{"name":"a","lineage":"x","height":0,"availability":"maybe"}]}`,
		"bad ask":          `{"minds":[{"name":"a","lineage":"x","height":0,"availability":"available","ask":"smoke-signal"}]}`,
		"negative height":  `{"minds":[{"name":"a","lineage":"x","height":-1,"availability":"available"}]}`,
		"not json":         `minds`,
	} {
		if _, err := ParseRegistry([]byte(body)); err == nil {
			t.Errorf("%s: parsed, want a refusal", name)
		}
	}
}

// The registry is a data file: a path loads one, an empty path is the embedded
// default, and an unreadable path is a refusal.
func TestLoadRegistry(t *testing.T) {
	reg, err := LoadRegistry(filepath.Join("testdata", "registry.json"))
	if err != nil {
		t.Fatalf("testdata registry: %v", err)
	}
	if _, ok := reg.ByName("flash"); !ok {
		t.Error("the testdata registry names no flash rung")
	}
	def, err := LoadRegistry("")
	if err != nil {
		t.Fatalf("empty path is the embedded default: %v", err)
	}
	if len(def.Minds) == 0 {
		t.Error("the embedded default is empty")
	}
	missing := filepath.Join(t.TempDir(), "nope.json")
	if _, err := LoadRegistry(missing); err == nil {
		t.Error("an unreadable registry parsed, want a refusal")
	} else if !strings.Contains(err.Error(), "nope.json") {
		t.Errorf("the refusal must name the path it could not read: %v", err)
	}
	if _, err := os.Stat(missing); err == nil {
		t.Error("LoadRegistry wrote the path it could not read")
	}
}
