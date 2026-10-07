package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	binDir := flag.String("bin", "", "directory containing built tool binaries")
	outFile := flag.String("out", "docs/CLI.md", "output file")
	flag.Parse()

	if *binDir == "" {
		fmt.Fprintln(os.Stderr, "usage: clidoc -bin <binary-directory> [-out <output-file>]")
		os.Exit(2)
	}

	tools, err := discoverTools(*binDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "discoverTools: %v\n", err)
		os.Exit(2)
	}

	// Read existing CLI.md to preserve non-generated content
	existing, err := os.ReadFile(*outFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read %s: %v\n", *outFile, err)
		os.Exit(2)
	}

	output := generateCLI(tools, string(existing))

	err = os.WriteFile(*outFile, output.Bytes(), 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", *outFile, err)
		os.Exit(2)
	}
}

func discoverTools(binDir string) ([]string, error) {
	entries, err := os.ReadDir(binDir)
	if err != nil {
		return nil, err
	}

	var tools []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, "nova-") {
			tools = append(tools, strings.TrimPrefix(name, "nova-"))
		}
	}
	sort.Strings(tools)
	return tools, nil
}

func generateCLI(tools []string, existing string) *bytes.Buffer {
	var output bytes.Buffer

	// Header
	output.WriteString("# Command reference\n\n")
	output.WriteString("[Back to Nova Tools](../README.md)\n\n")
	output.WriteString("Command reference and worked examples. Run shell examples from the repository root unless a section says otherwise. `-h` or `--help` after any verb prints that verb's help (its usage lines and every flag it takes) on stdout at exit 0 and runs nothing, so `<tool> <verb> -h` is always a safe first question; `<tool> help` is the whole banner. nova-fuse alone refuses `-h` after a verb, because its exit 0 means CLEAR. The first-run transcripts also live in [TESTS.md](TESTS.md), where the tests execute them line by line, so what is shown here is what the tool does today.\n\n")

	for _, tool := range tools {
		// Get help output
		helpOut, err := getHelpOutput(tool)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: %s help: %v\n", tool, err)
			continue
		}

		output.WriteString("## ")
		output.WriteString(tool)
		output.WriteString("\n\n")
		output.WriteString("<!-- clidoc:begin ")
		output.WriteString(tool)
		output.WriteString(" -->\n")
		output.WriteString("```\n")
		output.WriteString(helpOut)
		output.WriteString("<!-- clidoc:end ")
		output.WriteString(tool)
		output.WriteString(" -->\n\n")
	}

	return &output
}

func getHelpOutput(tool string) (string, error) {
	// Try to find and run the binary
	binPath := filepath.Join("bin", "nova-"+tool)
	if stat, err := os.Stat(binPath); err != nil || stat.IsDir() {
		// Try PATH
		cmd := exec.Command("bash", "-c", "command -v nova-"+tool)
		if out, err := cmd.Output(); err != nil {
			return "", fmt.Errorf("binary not found")
		} else {
			binPath = strings.TrimSpace(string(out))
		}
	}

	cmd := exec.Command(binPath, "help")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
