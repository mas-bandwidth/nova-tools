// clidoc generates CLI.md reference blocks from tool help.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

func main() {
	binDir := flag.String("bin", "", "directory containing built tool binaries")
	flag.Parse()

	if *binDir == "" {
		fmt.Fprintln(os.Stderr, "clidoc: --bin flag required")
		os.Exit(2)
	}

	tools, err := findTools(*binDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "clidoc: %v\n", err)
		os.Exit(2)
	}

	cliPath := "docs/CLI.md"
	cli, err := os.ReadFile(cliPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "clidoc: reading %s: %v\n", cliPath, err)
		os.Exit(2)
	}

	content := string(cli)
	for _, tool := range tools {
		ref := generateRef(tool, *binDir)
		content = replaceSection(content, tool, ref)
	}

	err = os.WriteFile(cliPath, []byte(content), 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "clidoc: writing %s: %v\n", cliPath, err)
		os.Exit(2)
	}
}

func findTools(binDir string) ([]string, error) {
	entries, err := os.ReadDir(binDir)
	if err != nil {
		return nil, err
	}

	var tools []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		tools = append(tools, e.Name())
	}
	return tools, nil
}

func generateRef(tool, binDir string) string {
	cmd := exec.Command(filepath.Join(binDir, tool), "help")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Sprintf("<!-- clidoc error: %v -->", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var buf strings.Builder
	buf.WriteString("```\n")
	for _, line := range lines {
		if line != "" {
			buf.WriteString(line)
		}
		buf.WriteString("\n")
	}
	buf.WriteString("```\n")
	return buf.String()
}

func replaceSection(content, tool, ref string) string {
	re := regexp.MustCompile(`(?s)(<!-- clidoc:begin ` + regexp.QuoteMeta(tool) + ` -->).*?(<!-- clidoc:end ` + regexp.QuoteMeta(tool) + ` -->)`)
	return re.ReplaceAllString(content, "$1"+ref+"$2")
}
