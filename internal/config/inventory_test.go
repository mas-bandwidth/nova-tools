package config

import (
	"context"
	"encoding/json"
	"testing"
)

func TestBuildInventoryStructure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := seed(t)

	// In seed(t):
	// studio: user=glenn, seat=studio, slots=64, runners=1
	// hulk:   user=gaffer, seat=swarm-hulk, slots=64
	// fleet:  coordinator=studio
	// Let's also set fleet store = hulk
	if _, _, err := st.Update(ctx, KindFleet, KindFleet, map[string]string{"store": "hulk"}, "rowan"); err != nil {
		t.Fatal(err)
	}

	inv, err := BuildInventory(ctx, st, "studio")
	if err != nil {
		t.Fatalf("BuildInventory: %v", err)
	}

	// Verify groups
	if len(inv.All.Hosts) != 2 || inv.All.Hosts[0] != "hulk" || inv.All.Hosts[1] != "studio" {
		t.Errorf("all hosts: got %v, want [hulk studio]", inv.All.Hosts)
	}
	if len(inv.Benches.Hosts) != 2 || inv.Benches.Hosts[0] != "hulk" || inv.Benches.Hosts[1] != "studio" {
		t.Errorf("benches hosts: got %v, want [hulk studio]", inv.Benches.Hosts)
	}
	if len(inv.Coordinator.Hosts) != 1 || inv.Coordinator.Hosts[0] != "studio" {
		t.Errorf("coordinator hosts: got %v, want [studio]", inv.Coordinator.Hosts)
	}
	if len(inv.Store.Hosts) != 1 || inv.Store.Hosts[0] != "hulk" {
		t.Errorf("store hosts: got %v, want [hulk]", inv.Store.Hosts)
	}
	if len(inv.Runners.Hosts) != 1 || inv.Runners.Hosts[0] != "studio" {
		t.Errorf("runners hosts: got %v, want [studio]", inv.Runners.Hosts)
	}

	// Verify hostvars for studio (local host)
	studioHV, ok := inv.Meta.Hostvars["studio"]
	if !ok {
		t.Fatal("hostvars missing studio")
	}
	if studioHV["ansible_host"] != "studio" {
		t.Errorf("studio ansible_host: got %v, want studio", studioHV["ansible_host"])
	}
	if studioHV["ansible_user"] != "glenn" || studioHV["user"] != "glenn" {
		t.Errorf("studio user: got %v / %v, want glenn", studioHV["ansible_user"], studioHV["user"])
	}
	if studioHV["seat"] != "studio" || studioHV["registry_seat"] != "studio" {
		t.Errorf("studio seat: got %v / %v, want studio", studioHV["seat"], studioHV["registry_seat"])
	}
	if studioHV["slots"] != 64 {
		t.Errorf("studio slots: got %v, want 64", studioHV["slots"])
	}
	if studioHV["runners"] != 1 {
		t.Errorf("studio runners: got %v, want 1", studioHV["runners"])
	}
	if studioHV["kind"] != "machine" {
		t.Errorf("studio kind: got %v, want machine", studioHV["kind"])
	}
	if studioHV["ansible_connection"] != "local" {
		t.Errorf("studio ansible_connection: got %v, want local", studioHV["ansible_connection"])
	}

	// Verify hostvars for hulk (remote host)
	hulkHV, ok := inv.Meta.Hostvars["hulk"]
	if !ok {
		t.Fatal("hostvars missing hulk")
	}
	if hulkHV["ansible_host"] != "hulk" {
		t.Errorf("hulk ansible_host: got %v, want hulk", hulkHV["ansible_host"])
	}
	if hulkHV["ansible_user"] != "gaffer" || hulkHV["user"] != "gaffer" {
		t.Errorf("hulk user: got %v, want gaffer", hulkHV["user"])
	}
	if hulkHV["slots"] != 64 {
		t.Errorf("hulk slots: got %v, want 64", hulkHV["slots"])
	}
	if hulkHV["runners"] != 0 {
		t.Errorf("hulk runners: got %v, want 0", hulkHV["runners"])
	}
	if _, hasConn := hulkHV["ansible_connection"]; hasConn {
		t.Errorf("hulk should not have ansible_connection, got %v", hulkHV["ansible_connection"])
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
	hostRaw, err := inv.HostJSON("studio")
	if err != nil {
		t.Fatalf("HostJSON: %v", err)
	}
	var parsedHost map[string]any
	if err := json.Unmarshal(hostRaw, &parsedHost); err != nil {
		t.Fatalf("Unmarshal HostJSON: %v", err)
	}
	if parsedHost["ansible_host"] != "studio" {
		t.Errorf("HostJSON ansible_host: got %v", parsedHost["ansible_host"])
	}

	// Verify HostJSON for unknown host
	unknownRaw, err := inv.HostJSON("unknown")
	if err != nil {
		t.Fatalf("HostJSON unknown: %v", err)
	}
	var parsedUnknown map[string]any
	if err := json.Unmarshal(unknownRaw, &parsedUnknown); err != nil {
		t.Fatalf("Unmarshal HostJSON unknown: %v", err)
	}
	if len(parsedUnknown) != 0 {
		t.Errorf("HostJSON unknown: got %v, want empty map", parsedUnknown)
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
