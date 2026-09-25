package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoRetiredPRRecordRead(t *testing.T) {
	dirs := []string{".", "../../internal/nsprint/land"}
	for _, dir := range dirs {
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(content), "s:%s:pr:") {
				t.Errorf("file %s contains retired PR record prefix s:%%s:pr:", path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
}
