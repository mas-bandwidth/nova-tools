package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/capture"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func cmdCapture(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("capture", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	repo := fs.String("repo", "", "")
	into := fs.String("into", "", "")
	maxBytes := fs.Int64("max-bytes", 10*1024*1024, "")
	maxBodyBytes := fs.Int64("max-body-bytes", 64*1024, "")
	ghTimeout := fs.Duration("gh-timeout", 60*time.Second, "")
	resume := fs.Bool("resume", false, "")
	issuesStr := fs.String("issues", "", "")

	if err := fs.Parse(args); err != nil {
		return refused(stderr, err.Error())
	}
	if *repo == "" {
		return refused(stderr, "--repo <owner/repo> is required")
	}
	if *into == "" {
		return refused(stderr, "--into <dir> is required")
	}

	var issues []int
	if *issuesStr != "" {
		for _, part := range strings.Split(*issuesStr, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			num := 0
			valid := true
			for _, ch := range part {
				if ch < '0' || ch > '9' {
					valid = false
					break
				}
				num = num*10 + int(ch-'0')
			}
			if !valid || num < 1 {
				return refused(stderr, fmt.Sprintf("--issues names invalid number %q", part))
			}
			issues = append(issues, num)
		}
	}

	adapter := capture.NewGitHubAdapter(*ghTimeout)
	ctx := context.Background()

	manifest, err := adapter.Capture(ctx, capture.Options{
		Repo:         *repo,
		StagingDir:   *into,
		MaxBytes:     *maxBytes,
		MaxBodyBytes: *maxBodyBytes,
		Timeout:      *ghTimeout,
		Resume:       *resume,
		Issues:       issues,
	})
	if err != nil {
		fmt.Fprintf(stderr, "CAPTURE REFUSED: %s\n", oneline.Escape(err.Error()))
		return 1
	}

	if _, err := adapter.ValidateBundle(*into); err != nil {
		fmt.Fprintf(stderr, "CAPTURE REFUSED: %s\n", oneline.Escape(err.Error()))
		return 1
	}

	fmt.Fprintf(stdout, "CAPTURE OK repo=%s issues=%d bytes=%d pages=%d manifest=%s\n",
		oneline.Field(manifest.Repository),
		manifest.TotalIssues,
		manifest.StagedBytes,
		manifest.PageCount,
		oneline.Field(manifest.Repository+"/manifest.json"),
	)
	return 0
}
