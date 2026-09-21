package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestCmdPipeline(t *testing.T) {
	qDir := t.TempDir()
	fixed, _ := time.Parse(time.RFC3339, "2026-09-21T14:00:00Z")

	headSHA := "1122334455667788990011223344556677889900"

	// 1. Test nova-pulse pipeline --open
	var outBuf, errBuf bytes.Buffer
	code := cmdPipeline([]string{
		"--queue", qDir,
		"--open",
		"--repo", "mas-bandwidth/nova-tools",
		"--pr", "500",
		"--head", headSHA,
		"--base", "dev",
		"--title", "CLI pipeline test",
		"--files", "internal/pulse/pipeline.go,docs/SPEC-DECIDE.md",
		"--checks", "pass",
	}, &outBuf, &errBuf, fixed)

	if code != 0 {
		t.Fatalf("cmdPipeline --open failed with code %d: %s", code, errBuf.String())
	}

	outStr := outBuf.String()
	if !strings.Contains(outStr, "PIPELINE OPEN") || !strings.Contains(outStr, "assigned=stella") {
		t.Errorf("unexpected open output: %s", outStr)
	}

	// 2. Test nova-pulse pipeline --disposition (Stella APPROVE score=9/10)
	outBuf.Reset()
	errBuf.Reset()
	code = cmdPipeline([]string{
		"--queue", qDir,
		"--disposition", "DISPOSITION who=stella head=" + headSHA + " verdict=APPROVE score=9/10 pr=500",
	}, &outBuf, &errBuf, fixed)

	if code != 0 {
		t.Fatalf("cmdPipeline --disposition failed with code %d: %s", code, errBuf.String())
	}
	if !strings.Contains(outBuf.String(), "PIPELINE LANDABLE") || !strings.Contains(outBuf.String(), "who=stella") {
		t.Errorf("unexpected disposition output: %s", outBuf.String())
	}

	// 3. Test nova-pulse pipeline --landable
	outBuf.Reset()
	errBuf.Reset()
	code = cmdPipeline([]string{
		"--queue", qDir,
		"--landable",
	}, &outBuf, &errBuf, fixed)

	if code != 0 {
		t.Fatalf("cmdPipeline --landable failed with code %d: %s", code, errBuf.String())
	}
	if !strings.Contains(outBuf.String(), "LANDABLE repo=mas-bandwidth/nova-tools pr=500") {
		t.Errorf("unexpected landable list output: %s", outBuf.String())
	}

	// 4. Test nova-pulse pipeline --pop-landable
	outBuf.Reset()
	errBuf.Reset()
	code = cmdPipeline([]string{
		"--queue", qDir,
		"--pop-landable",
	}, &outBuf, &errBuf, fixed)

	if code != 0 {
		t.Fatalf("cmdPipeline --pop-landable failed with code %d: %s", code, errBuf.String())
	}
	if !strings.Contains(outBuf.String(), "POPPED repo=mas-bandwidth/nova-tools pr=500") {
		t.Errorf("unexpected pop-landable output: %s", outBuf.String())
	}

	// 5. Test nova-pulse pipeline --backpressure
	outBuf.Reset()
	errBuf.Reset()
	code = cmdPipeline([]string{
		"--queue", qDir,
		"--backpressure",
		"--cap", "5",
	}, &outBuf, &errBuf, fixed)

	if code != 0 {
		t.Fatalf("cmdPipeline --backpressure failed with code %d: %s", code, errBuf.String())
	}
	if !strings.Contains(outBuf.String(), "BACKPRESSURE pending=0 cap=5 status=clear") {
		t.Errorf("unexpected backpressure output: %s", outBuf.String())
	}
}
