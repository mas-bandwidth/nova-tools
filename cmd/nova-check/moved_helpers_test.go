package main

import (
	"fmt"
	"testing"
)

var hygLabGoldenDir string

func writeCLI(t *testing.T, dir string) string                      { t.Helper(); return "" }
func dogfoodRun(t *testing.T, args ...string) (int, string, string) { t.Helper(); return 0, "", "" }
func hygLab(t *testing.T) string                                    { t.Helper(); return t.TempDir() }
func hygWrite(t *testing.T, lab, p, content string)                 { t.Helper() }
func hygGit(t *testing.T, lab string, args ...string)               { t.Helper() }
func fakeBin(t *testing.T, dir, name string, script ...string) {
	t.Helper()
	fmt.Println("fake bin stub", dir, name)
}

type convFixture struct{ dir string }

func newConvFixture(t *testing.T) *convFixture { t.Helper(); return &convFixture{} }
func (f *convFixture) run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	return 0, "", ""
}
func writeReceipt(t *testing.T, dir, name string, fields map[string]any) { t.Helper() }
func init()                                                              { hygLabGoldenDir = "" }
