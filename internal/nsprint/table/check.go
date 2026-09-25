package table

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// CheckSprints returns whether the sprints set exists and is non-empty, and
// if so, the name of the first non-control sprint it finds. A control sprint
// is one whose name starts with "control-".
func CheckSprints(ctx context.Context, client *redis.Client) (hasSprints bool, nonControl string, err error) {
	members, err := client.SMembers(ctx, "sprints").Result()
	if err != nil {
		return false, "", fmt.Errorf("SMEMBERS sprints: %w", err)
	}
	if len(members) == 0 {
		return false, "", nil
	}
	for _, name := range members {
		if !strings.HasPrefix(name, "control-") {
			return true, name, nil
		}
	}
	return true, "", nil
}

// CheckLive reads the --out file, asserts it is younger than 2 s, renders
// the table from Redis, and compares every cell. It returns exit 0 when the
// file is fresh and every cell matches, or exit 1 naming the first mismatched
// cell.
func CheckLive(ctx context.Context, client *redis.Client, out string, stdout, stderr io.Writer) int {
	info, err := os.Stat(out)
	if err != nil {
		fmt.Fprintf(stderr, "nova-sprint table --check --live: %s\n", err.Error())
		return 1
	}
	age := time.Since(info.ModTime())
	if age > 2*time.Second {
		fmt.Fprintf(stderr, "nova-sprint table --check --live: --out file is %s old (must be <2s)\n", age.Round(time.Millisecond))
		return 1
	}
	fileData, err := os.ReadFile(out)
	if err != nil {
		fmt.Fprintf(stderr, "nova-sprint table --check --live: %s\n", err.Error())
		return 1
	}
	snap, err := Read(ctx, client)
	if err != nil {
		fmt.Fprintf(stderr, "nova-sprint table --check --live: %s\n", err.Error())
		return 1
	}
	rendered := snap.Render()
	if len(snap.Errors) > 0 {
		fmt.Fprintf(stderr, "nova-sprint table --check --live: %s\n", strings.Join(snap.Errors, "; "))
		return 1
	}
	mismatch := compareCells(string(fileData), rendered)
	if mismatch != "" {
		fmt.Fprintf(stderr, "nova-sprint table --check --live: cell mismatch: %s\n", mismatch)
		return 1
	}
	fmt.Fprintln(stdout, "CHECK LIVE OK file="+out+" age="+age.Round(time.Millisecond).String())
	return 0
}

// compareCells compares two table outputs cell by cell and returns the first
// mismatch description, or "" when they are identical.
func compareCells(a, b string) string {
	aLines := strings.Split(strings.TrimSuffix(a, "\n"), "\n")
	bLines := strings.Split(strings.TrimSuffix(b, "\n"), "\n")
	if len(aLines) != len(bLines) {
		return fmt.Sprintf("line count: file=%d rendered=%d", len(aLines), len(bLines))
	}
	for i := range aLines {
		aCells := strings.Split(aLines[i], " | ")
		bCells := strings.Split(bLines[i], " | ")
		if len(aCells) != len(bCells) {
			return fmt.Sprintf("line %d cell count: file=%d rendered=%d", i+1, len(aCells), len(bCells))
		}
		for j := range aCells {
			if strings.TrimSpace(aCells[j]) != strings.TrimSpace(bCells[j]) {
				return fmt.Sprintf("line %d cell %d: file=%q rendered=%q", i+1, j+1, aCells[j], bCells[j])
			}
		}
	}
	return ""
}
