package pulse

import (
	"os"
	"path/filepath"
	"strings"
)

const PlanFile = "plan.txt"
const PlanOKLine = "PLAN-OK"

func PlanStepReady(jobDir string, asks []string) (bool, string) {
	planPath := filepath.Join(jobDir, PlanFile)
	data, err := os.ReadFile(planPath)
	if err != nil {
		return false, "plan file missing"
	}
	content := strings.TrimSpace(string(data))
	if !strings.Contains(content, PlanOKLine) {
		return false, "waiting for PLAN-OK"
	}
	if !boundedAsks(asks) {
		return false, "asks not bounded multiple choice (each ask must list options in parentheses like (A / B / C))"
	}
	return true, ""
}

func boundedAsks(asks []string) bool {
	if len(asks) == 0 {
		return false
	}
	for _, a := range asks {
		if !strings.Contains(a, "(") || !strings.Contains(a, ")") {
			return false
		}
	}
	return true
}
