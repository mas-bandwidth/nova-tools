package config

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestBuildInventoryStructure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := NewMem()
	machine, _ := Lookup(KindMachine)
	for _, r := range []struct {
		n   string
		raw map[string]string
	}{
		{"bench-alpha", map[string]string{"user": "user-a", "seat": "seat-alpha", "slots": "64", "runners": "1"}},
		{"bench-beta", map[string]string{"user": "user-b", "seat": "seat-beta", "slots": "64"}},
	} {
		row, err := machine.NewRow(r.n, r.raw)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.Insert(ctx, KindMachine, row, "operator"); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := st.Update(ctx, KindFleet, KindFleet, map[string]string{"coordinator": "bench-alpha", "store": "bench-beta"}, "operator"); err != nil {
		t.Fatal(err)
	}

	inv, err := BuildInventory(ctx, st, "bench-alpha")
	if err != nil {
		t.Fatalf("BuildInventory: %v", err)
	}

	// Verify groups
	if len(inv.All.Hosts) != 2 || inv.All.Hosts[0] != "bench-alpha" || inv.All.Hosts[1] != "bench-beta" {
		t.Errorf("all hosts: got %v, want [bench-alpha bench-beta]", inv.All.Hosts)
	}
	if len(inv.Benches.Hosts) != 2 || inv.Benches.Hosts[0] != "bench-alpha" || inv.Benches.Hosts[1] != "bench-beta" {
		t.Errorf("benches hosts: got %v, want [bench-alpha bench-beta]", inv.Benches.Hosts)
	}
	if len(inv.Coordinator.Hosts) != 1 || inv.Coordinator.Hosts[0] != "bench-alpha" {
		t.Errorf("coordinator hosts: got %v, want [bench-alpha]", inv.Coordinator.Hosts)
	}
	if len(inv.Store.Hosts) != 1 || inv.Store.Hosts[0] != "bench-beta" {
		t.Errorf("store hosts: got %v, want [bench-beta]", inv.Store.Hosts)
	}
	if len(inv.Runners.Hosts) != 1 || inv.Runners.Hosts[0] != "bench-alpha" {
		t.Errorf("runners hosts: got %v, want [bench-alpha]", inv.Runners.Hosts)
	}

	// Verify hostvars for bench-alpha (local host)
	alphaHV, ok := inv.Meta.Hostvars["bench-alpha"]
	if !ok {
		t.Fatal("hostvars missing bench-alpha")
	}
	if alphaHV["ansible_host"] != "bench-alpha" {
		t.Errorf("bench-alpha ansible_host: got %v, want bench-alpha", alphaHV["ansible_host"])
	}
	if alphaHV["ansible_user"] != "user-a" || alphaHV["user"] != "user-a" {
		t.Errorf("bench-alpha user: got %v / %v, want user-a", alphaHV["ansible_user"], alphaHV["user"])
	}
	if alphaHV["seat"] != "seat-alpha" || alphaHV["registry_seat"] != "seat-alpha" {
		t.Errorf("bench-alpha seat: got %v / %v, want seat-alpha", alphaHV["seat"], alphaHV["registry_seat"])
	}
	if alphaHV["slots"] != 64 {
		t.Errorf("bench-alpha slots: got %v, want 64", alphaHV["slots"])
	}
	if alphaHV["runners"] != 1 {
		t.Errorf("bench-alpha runners: got %v, want 1", alphaHV["runners"])
	}
	if alphaHV["kind"] != "machine" {
		t.Errorf("bench-alpha kind: got %v, want machine", alphaHV["kind"])
	}
	if alphaHV["ansible_connection"] != "local" {
		t.Errorf("bench-alpha ansible_connection: got %v, want local", alphaHV["ansible_connection"])
	}

	// Verify hostvars for bench-beta (remote host)
	betaHV, ok := inv.Meta.Hostvars["bench-beta"]
	if !ok {
		t.Fatal("hostvars missing bench-beta")
	}
	if betaHV["ansible_host"] != "bench-beta" {
		t.Errorf("bench-beta ansible_host: got %v, want bench-beta", betaHV["ansible_host"])
	}
	if betaHV["ansible_user"] != "user-b" || betaHV["user"] != "user-b" {
		t.Errorf("bench-beta user: got %v, want user-b", betaHV["user"])
	}
	if betaHV["slots"] != 64 {
		t.Errorf("bench-beta slots: got %v, want 64", betaHV["slots"])
	}
	if betaHV["runners"] != 0 {
		t.Errorf("bench-beta runners: got %v, want 0", betaHV["runners"])
	}
	if _, hasConn := betaHV["ansible_connection"]; hasConn {
		t.Errorf("bench-beta should not have ansible_connection, got %v", betaHV["ansible_connection"])
	}

	// Verify JSON output parses into standard map
	rawJSON, err := inv.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(rawJSON, &parsed); err != nil {
		t.Fatalf("Unmarshal inventory JSON: %v", err)
	}
	if _, hasMeta := parsed["_meta"]; !hasMeta {
		t.Error("JSON missing _meta key")
	}

	// Verify HostJSON
	hostRaw, err := inv.HostJSON("bench-alpha")
	if err != nil {
		t.Fatalf("HostJSON: %v", err)
	}
	var parsedHost map[string]any
	if err := json.Unmarshal(hostRaw, &parsedHost); err != nil {
		t.Fatalf("Unmarshal HostJSON: %v", err)
	}
	if parsedHost["ansible_host"] != "bench-alpha" {
		t.Errorf("HostJSON ansible_host: got %v", parsedHost["ansible_host"])
	}

	// HostJSON for a name the inventory does not hold is a typed refusal
	// that carries the known names, never an empty object.
	unknownRaw, err := inv.HostJSON("unknown")
	var unknown *UnknownHostError
	if !errors.As(err, &unknown) || unknownRaw != nil {
		t.Fatalf("HostJSON unknown: got %q, %v; want *UnknownHostError", unknownRaw, err)
	}
	if unknown.Name != "unknown" || len(unknown.Known) != 2 || unknown.Known[0] != "bench-alpha" || unknown.Known[1] != "bench-beta" {
		t.Errorf("HostJSON unknown: %+v", unknown)
	}
}

func TestBuildInventoryEmptyStore(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := NewMem()

	inv, err := BuildInventory(ctx, st, "")
	if err != nil {
		t.Fatalf("BuildInventory on empty store: %v", err)
	}
	if len(inv.All.Hosts) != 0 || len(inv.Benches.Hosts) != 0 {
		t.Errorf("empty store should have empty hosts, got all=%v benches=%v", inv.All.Hosts, inv.Benches.Hosts)
	}
	if len(inv.Meta.Hostvars) != 0 {
		t.Errorf("empty store should have empty hostvars, got %v", inv.Meta.Hostvars)
	}
	raw, err := inv.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
}
