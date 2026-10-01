package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// commandLine renders a step's argv as a line a reader can paste back into the shell.
func commandLine(argv []string) string {
	return commandLineFor(argv, runtime.GOOS == "windows")
}

// commandLineFor renders argv for the given platform.
func commandLineFor(argv []string, windows bool) string {
	parts := make([]string, 0, len(argv))
	for _, a := range argv {
		parts = append(parts, shellArg(a, windows))
	}
	return strings.Join(parts, " ")
}

// shellArg renders one argument for that platform's shell.
func shellArg(s string, windows bool) string {
	esc := oneline.Escape(s)
	if esc != "" && !needsQuoting(esc, windows) {
		return esc
	}
	if windows {
		return `"` + strings.ReplaceAll(esc, `"`, `""`) + `"`
	}
	return "'" + strings.ReplaceAll(esc, "'", `'\''`) + "'"
}

// needsQuoting reports whether an argument needs shell quotes.
func needsQuoting(s string, windows bool) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune(`-_./=+:,@`, r):
		case windows && strings.ContainsRune(`\~`, r):
		default:
			return true
		}
	}
	return false
}

// step echoes one command line and then runs it through the same dispatch a
// caller reaches from a shell.
func step(argv []string, stdin io.Reader, stdout, stderr io.Writer, now time.Time) int {
	fmt.Fprintf(stdout, "$ nova-bus %s\n", commandLine(argv))
	return run(argv, stdin, stdout, stderr, now)
}

// stepFailed reports a step that could not run. quickstart exits 0 only when all four ran.
func stepFailed(verb string, code int, stderr io.Writer) int {
	return refuse(stderr, " quickstart", fmt.Sprintf("the %s step could not run (exit %d); nothing further was attempted", oneline.Field(verb), code))
}

const quickstartRoster = `{"participants":[{"name":"Ada","lane":"from-ada","git_name":"Ada","git_email":"ada@example.com"},{"name":"Bo","lane":"from-bo","git_name":"Bo","git_email":"bo@example.com"}]}` + "\n"

func initQuickstartBus(dir string) error {
	ctx := context.Background()
	env := append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat", "GIT_OPTIONAL_LOCKS=0",
		"GIT_EDITOR=true", "GIT_SEQUENCE_EDITOR=true",
	)
	opts := gitrun.Options{
		C:       dir,
		Env:     env,
		Timeout: bus.DefaultGitTimeout,
	}

	// 1. git init -q -b main
	if _, err := gitrun.Run(ctx, opts, "init", "-q", "-b", "main"); err != nil {
		if _, err := gitrun.Run(ctx, opts, "init", "-q"); err != nil {
			return fmt.Errorf("git init: %w", err)
		}
		if _, err := gitrun.Run(ctx, opts, "checkout", "-q", "-B", "main"); err != nil {
			return fmt.Errorf("git checkout: %w", err)
		}
	}

	// Disable GPG signing for this demo repo so it never prompts for a key.
	if _, err := gitrun.Run(ctx, opts, "config", "commit.gpgsign", "false"); err != nil {
		return fmt.Errorf("git config commit.gpgsign: %w", err)
	}

	// 2. participants.json
	if err := os.WriteFile(filepath.Join(dir, bus.ConfigName), []byte(quickstartRoster), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", bus.ConfigName, err)
	}

	// 3. git add participants.json
	if _, err := gitrun.Run(ctx, opts, "add", bus.ConfigName); err != nil {
		return fmt.Errorf("git add %s: %w", bus.ConfigName, err)
	}

	// 4. git commit -m roster
	if _, err := gitrun.Run(ctx, opts, "-c", "user.name=Ada", "-c", "user.email=ada@example.com", "commit", "-q", "-m", "roster"); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}

	// 5. bare origin in .git/origin.git
	bareDir := filepath.Join(dir, ".git", "origin.git")
	if err := os.MkdirAll(bareDir, 0o755); err != nil {
		return fmt.Errorf("mkdir origin.git: %w", err)
	}
	bareOpts := gitrun.Options{
		C:       bareDir,
		Env:     env,
		Timeout: bus.DefaultGitTimeout,
	}
	if _, err := gitrun.Run(ctx, bareOpts, "init", "--bare", "-q", "-b", "main"); err != nil {
		if _, err := gitrun.Run(ctx, bareOpts, "init", "--bare", "-q"); err != nil {
			return fmt.Errorf("git init bare: %w", err)
		}
	}
	if _, err := gitrun.Run(ctx, bareOpts, "config", "commit.gpgsign", "false"); err != nil {
		return fmt.Errorf("git config bare commit.gpgsign: %w", err)
	}

	// 6. remote add origin <absBareDir>
	absDir, err := filepath.Abs(dir)
	if err != nil {
		absDir = dir
	}
	absBareDir := filepath.Join(absDir, ".git", "origin.git")
	if _, err := gitrun.Run(ctx, opts, "remote", "add", "origin", absBareDir); err != nil {
		return fmt.Errorf("git remote add: %w", err)
	}

	// 7. git push -q -u origin main
	if _, err := gitrun.Run(ctx, opts, "push", "-q", "-u", "origin", "main"); err != nil {
		return fmt.Errorf("git push origin main: %w", err)
	}

	// 8. exclude .nova-bus/ and make directory
	excludePath := filepath.Join(dir, ".git", "info", "exclude")
	if err := os.WriteFile(excludePath, []byte(".nova-bus/\n"), 0o644); err != nil {
		return fmt.Errorf("write exclude: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, bus.BusStateDir), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", bus.BusStateDir, err)
	}

	return nil
}

func cmdQuickstart(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("quickstart")
	dirFlag := f.fs.String("dir", "", "the directory to initialize the bus in (default: a temporary directory)")
	if !f.parse(args, stderr, nil) {
		return 2
	}

	busDir := strings.TrimSpace(*dirFlag)
	if busDir == "" {
		tmp, err := os.MkdirTemp("", "nova-bus-quickstart-*")
		if err != nil {
			return refuse(stderr, " quickstart", oneline.Err(err))
		}
		busDir = tmp
	} else {
		busDir = filepath.Clean(busDir)
		if fi, err := os.Stat(busDir); err == nil {
			if !fi.IsDir() {
				return refuse(stderr, " quickstart", fmt.Sprintf("--dir %s is not a directory", oneline.Field(busDir)))
			}
			entries, err := os.ReadDir(busDir)
			if err != nil {
				return refuse(stderr, " quickstart", oneline.Err(err))
			}
			if len(entries) > 0 {
				return refuse(stderr, " quickstart", fmt.Sprintf("--dir %s is not empty; quickstart requires an empty or non-existent directory", oneline.Field(busDir)))
			}
		} else if os.IsNotExist(err) {
			if err := os.MkdirAll(busDir, 0o755); err != nil {
				return refuse(stderr, " quickstart", oneline.Err(err))
			}
		} else {
			return refuse(stderr, " quickstart", oneline.Err(err))
		}
	}

	if err := initQuickstartBus(busDir); err != nil {
		return refuse(stderr, " quickstart", fmt.Sprintf("initialization failed: %s", oneline.Err(err)))
	}

	// Step 1: send initial note from Ada to Bo.
	draftPath := filepath.Join(busDir, bus.BusStateDir, "draft.md")
	draftContent := "From: Ada\nTo: Bo\nSubject: Hello Bo\n\nHello Bo, this is a demonstration note from Ada.\n"
	if err := os.WriteFile(draftPath, []byte(draftContent), 0o644); err != nil {
		return refuse(stderr, " quickstart", fmt.Sprintf("write draft: %s", oneline.Err(err)))
	}
	sendArgs := []string{"send", "--bus", busDir, "--file", draftPath, "--as", "Ada", "--remote", "origin", "--branch", "main"}
	if code := step(sendArgs, strings.NewReader(""), stdout, stderr, now); code != 0 {
		return stepFailed("send", code, stderr)
	}

	// Step 2: inbox reads the note as Bo.
	inboxArgs := []string{"inbox", "--bus", busDir, "--as", "Bo", "--receipt-max-words", "40"}
	if code := step(inboxArgs, strings.NewReader(""), stdout, stderr, now); code != 0 {
		return stepFailed("inbox", code, stderr)
	}

	// Find the note ID sent by Ada so Bo can reply.
	cfg, err := bus.LoadConfig(busDir)
	if err != nil {
		return refuse(stderr, " quickstart", oneline.Err(err))
	}
	idx, err := bus.ReadIndex(busDir, cfg)
	if err != nil || len(idx.Entries) == 0 {
		return refuse(stderr, " quickstart", "cannot find sent note in bus index")
	}
	noteID := idx.Entries[0].ID

	// Step 3: reply from Bo to Ada.
	replyPath := filepath.Join(busDir, bus.BusStateDir, "reply.md")
	replyContent := "Hello Ada, this is a demonstration reply from Bo.\n"
	if err := os.WriteFile(replyPath, []byte(replyContent), 0o644); err != nil {
		return refuse(stderr, " quickstart", fmt.Sprintf("write reply: %s", oneline.Err(err)))
	}
	replyArgs := []string{"reply", "--bus", busDir, "--as", "Bo", "--re", noteID, "--file", replyPath, "--remote", "origin", "--branch", "main"}
	if code := step(replyArgs, strings.NewReader(""), stdout, stderr, now); code != 0 {
		return stepFailed("reply", code, stderr)
	}

	// Step 4: close note as Ada.
	closeArgs := []string{"close", "--bus", busDir, "--as", "Ada", "--before", now.Add(time.Hour).Format(time.RFC3339), "--remote", "origin", "--branch", "main"}
	if code := step(closeArgs, strings.NewReader(""), stdout, stderr, now); code != 0 {
		return stepFailed("close", code, stderr)
	}

	fmt.Fprintf(stdout, "BUS QUICKSTART OK dir=%s steps=4\n", oneline.Field(busDir))
	return 0
}
