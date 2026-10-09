//go:build !darwin && !linux

package secrets

import "fmt"

func readClaudeFleetRegularNoFollow(string, int64) ([]byte, error) {
	return nil, fmt.Errorf("Claude token manifest admission requires darwin or linux")
}
