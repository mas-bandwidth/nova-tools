package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleet"
	"github.com/redis/go-redis/v9"
)

func TestFleetRollHelp(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := runFleet(context.Background(), []string{"roll", "-h"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d err=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "fleet roll") {
		t.Errorf("stdout does not contain 'fleet roll':\n%s", stdout.String())
	}
}

func TestFleetRollRefusesPositionalArgs(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := runFleet(context.Background(), []string{"roll", "extra"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("code=%d; want 2", code)
	}
	if !strings.Contains(stderr.String(), "takes flags, not positional arguments") {
		t.Errorf("stderr does not mention positional arguments refusal:\n%s", stderr.String())
	}
}

func TestFleetRollDevTipReleaseAndVerify(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	mr.SAdd("benches", "space", "hulk")

	const testSha = "c8178673f5e1495991b7852b85526e8509172153"
	const wantVersion = "v0.16.0-dev.c8178673"

	mr.HSet("bench:hulk:beat", "build", "nova-sprint "+wantVersion+" linux/amd64 go1.26.6")
	mr.HSet("bench:space:beat", "sha", testSha)

	playRan := false
	deps := RollDeps{
		Play: func(ctx context.Context, client *redis.Client) error {
			playRan = true
			return nil
		},
		DevTip: func(ctx context.Context) (string, error) {
			return testSha, nil
		},
	}

	var stdout, stderr bytes.Buffer
	code := runFleetRollWith(context.Background(), []string{"--redis", mr.Addr()}, &stdout, &stderr, deps)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !playRan {
		t.Errorf("play runner seam was not called")
	}

	// Verify fleet:release and fleet:release:sha
	if v := mr.HGet("fleet:release", "version"); v != wantVersion {
		t.Errorf("fleet:release version = %q; want %q", v, wantVersion)
	}
	if c := mr.HGet("fleet:release", "commit"); c != testSha {
		t.Errorf("fleet:release commit = %q; want %q", c, testSha)
	}
	s, err := mr.Get("fleet:release:sha")
	if err != nil || s != testSha {
		t.Errorf("fleet:release:sha = %q; want %q", s, testSha)
	}

	outStr := stdout.String()
	wantHulkRow := "hulk\t" + wantVersion + "\t" + wantVersion + "\tok\n"
	wantSpaceRow := "space\t" + wantVersion + "\t" + testSha + "\tok\n"
	wantFinal := "FLEET ROLL OK version=" + wantVersion + " benches=2\n"

	if !strings.Contains(outStr, wantHulkRow) {
		t.Errorf("missing hulk row %q in stdout:\n%s", wantHulkRow, outStr)
	}
	if !strings.Contains(outStr, wantSpaceRow) {
		t.Errorf("missing space row %q in stdout:\n%s", wantSpaceRow, outStr)
	}
	if !strings.Contains(outStr, wantFinal) {
		t.Errorf("missing final receipt %q in stdout:\n%s", wantFinal, outStr)
	}
}

func TestFleetRollBehind(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	mr.SAdd("benches", "space", "hulk", "batman")

	const testSha = "c8178673f5e1495991b7852b85526e8509172153"
	const wantVersion = "v0.16.0-dev.c8178673"

	// hulk is up to date
	mr.HSet("bench:hulk:beat", "build", wantVersion)
	// space has old version
	mr.HSet("bench:space:beat", "build", "v0.16.0-dev.oldbuild")
	// batman has no beat at all

	deps := RollDeps{
		Play: fleet.Play,
	}

	var stdout, stderr bytes.Buffer
	code := runFleetRollWith(context.Background(), []string{"--to", testSha, "--redis", mr.Addr()}, &stdout, &stderr, deps)
	if code != 1 {
		t.Fatalf("code=%d; want 1 for behind benches", code)
	}

	outStr := stdout.String()
	wantBatmanRow := "batman\t" + wantVersion + "\tnone\tbehind\n"
	wantHulkRow := "hulk\t" + wantVersion + "\t" + wantVersion + "\tok\n"
	wantSpaceRow := "space\t" + wantVersion + "\tv0.16.0-dev.oldbuild\tbehind\n"
	wantFinal := "FLEET ROLL BEHIND version=" + wantVersion + " behind=batman,space\n"

	if !strings.Contains(outStr, wantBatmanRow) {
		t.Errorf("missing batman row in stdout:\n%s", outStr)
	}
	if !strings.Contains(outStr, wantHulkRow) {
		t.Errorf("missing hulk row in stdout:\n%s", outStr)
	}
	if !strings.Contains(outStr, wantSpaceRow) {
		t.Errorf("missing space row in stdout:\n%s", outStr)
	}
	if !strings.Contains(outStr, wantFinal) {
		t.Errorf("missing final receipt %q in stdout:\n%s", wantFinal, outStr)
	}
}

func TestFleetRollPlayRefused(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	const testSha = "c8178673f5e1495991b7852b85526e8509172153"

	deps := RollDeps{
		Play: func(ctx context.Context, client *redis.Client) error {
			return errors.New("ansible convergence failed")
		},
	}

	var stdout, stderr bytes.Buffer
	code := runFleetRollWith(context.Background(), []string{"--to", testSha, "--redis", mr.Addr()}, &stdout, &stderr, deps)
	if code != 1 {
		t.Fatalf("code=%d; want 1 on play failure", code)
	}
	if !strings.Contains(stderr.String(), "FLEET ROLL REFUSED: play: ansible convergence failed") {
		t.Errorf("stderr does not contain refusal message:\n%s", stderr.String())
	}
}
