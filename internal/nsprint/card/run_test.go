package card_test

// run_test.go: the Go card harness (#3681, run.go), against a throwaway
// redis-server with a card hash and its body, and a fake nova-swarm (this
// test binary re-executed, fakeRunner) that records the argv it got and the
// NAMES of the provider keys in its environment.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
)

const (
	fakeRunnerEnv    = "CARDRUN_FAKE_RUNNER" // set: this binary is nova-swarm
	fakeRunnerSeen   = "CARDRUN_SEEN"        // the dir the runner records into
	fakeRunnerResult = "CARDRUN_RESULT"      // 1: leave a RESULT.md under --results-root
	fakeRunnerRC     = "CARDRUN_RC"          // the exit code
)

// fakeRunner is nova-swarm native: it records argv, the provider key names
// it can see, the card env it was given, and the card bytes; leaves a
// RESULT.md when asked; prints native's state line; exits as told.
func fakeRunner() int {
	seen := os.Getenv(fakeRunnerSeen)
	if err := os.MkdirAll(seen, 0o755); err != nil {
		return 9
	}
	args := os.Args[1:]
	if err := os.WriteFile(filepath.Join(seen, "argv"), []byte(strings.Join(args, "\n")+"\n"), 0o644); err != nil {
		return 9
	}
	var keys []string
	for _, k := range card.ProviderKeys {
		if os.Getenv(k) != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	_ = os.WriteFile(filepath.Join(seen, "keys"), []byte(strings.Join(keys, "\n")), 0o644)
	env := fmt.Sprintf("card=%s out=%s job=%s pw=%t auth=%t\n", os.Getenv("NOVA_CARD"), os.Getenv("NOVA_CARD_OUT"), os.Getenv("NOVA_CARD_JOB"),
		os.Getenv("NOVA_REDIS_BENCH_PASSWORD") != "", os.Getenv("REDISCLI_AUTH") != "")
	_ = os.WriteFile(filepath.Join(seen, "env"), []byte(env), 0o644)
	cardPath, results := "", ""
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "--card":
			cardPath = args[i+1]
		case "--results-root":
			results = args[i+1]
		}
	}
	if data, err := os.ReadFile(cardPath); err == nil {
		_ = os.WriteFile(filepath.Join(seen, "card.md"), data, 0o644)
	}
	if os.Getenv(fakeRunnerResult) == "1" {
		job := filepath.Join(results, "job")
		if err := os.MkdirAll(job, 0o755); err != nil {
			return 9
		}
		if err := os.WriteFile(filepath.Join(job, "RESULT.md"), []byte("RESULT: "+os.Getenv("NOVA_CARD")+" sha=000000000000\n"), 0o644); err != nil {
			return 9
		}
		fmt.Println("NATIVE OK rc=0 harness=ok")
	} else {
		fmt.Println("NATIVE INCOMPLETE")
	}
	rc := 0
	fmt.Sscanf(os.Getenv(fakeRunnerRC), "%d", &rc)
	return rc
}

func TestCardRunRefusesAnIncompleteConfig(t *testing.T) {
	t.Parallel()

	rep := card.Run(context.Background(), nil, card.RunConfig{Sprint: "s", Label: "l", Attempt: 1})
	if rep.Code != card.RunExitRefused || !strings.HasPrefix(rep.Why, "missing ") {
		t.Fatalf("%s", rep.Line())
	}
	for _, want := range []string{"bench", "out dir", "job dir", "home", "harness bin", "deadline", "tokens"} {
		if !strings.Contains(rep.Why, want) {
			t.Fatalf("why %q lacks %s", rep.Why, want)
		}
	}
	cfg, missing := card.RunConfigFromEnv(func(string) string { return "" })
	if cfg.Home != "" || strings.Join(missing, " ") != "HOME NOVA_CARD_BENCH NOVA_CARD_DEADLINE NOVA_CARD_HARNESS_BIN NOVA_CARD_TOKENS" {
		t.Fatalf("missing = %v", missing)
	}
}

func TestProviderKey(t *testing.T) {
	t.Parallel()

	for launch, want := range map[string]string{
		"openrouter/deepseek/deepseek-v4-flash": "OPENROUTER_API_KEY",
		"opencode/glm-5.3-flash":                "OPENCODE_API_KEY",
		"deepseek/deepseek-v4-flash":            "DEEPSEEK_API_KEY",
		"inception/mercury-2.5":                 "INCEPTION_API_KEY",
	} {
		if got, err := card.ProviderKey(launch); err != nil || got != want {
			t.Fatalf("%s: %s %v, want %s", launch, got, err, want)
		}
	}
	if _, err := card.ProviderKey("datacenter/inception/mercury-2.5"); err == nil || !strings.Contains(err.Error(), "provider datacenter") {
		t.Fatalf("undeclared provider: %v", err)
	}
}

// TestWrapperConfigWantsOneHarness: a program or in-process, never both,
// and in-process needs the store.
func TestWrapperConfigWantsOneHarness(t *testing.T) {
	t.Parallel()

	id := card.Identity{Sprint: "s", Label: "l", BaseSHA: "0123abcd", Bench: "b", Attempt: 1}
	both := newHarnessRun(t, id, "/bin/true").cfg
	both.InProcess = &card.RunConfig{}
	if rep := card.RunWrapper(context.Background(), both, nil); rep.Code != card.WrapperExitUsage || !strings.Contains(rep.Why, "not both") {
		t.Fatalf("%s", rep.Line())
	}
	noStore := newHarnessRun(t, id, "").cfg
	noStore.InProcess = &card.RunConfig{}
	if rep := card.RunWrapper(context.Background(), noStore, nil); rep.Code != card.WrapperExitUsage || !strings.Contains(rep.Why, "store") {
		t.Fatalf("%s", rep.Line())
	}
	neither := newHarnessRun(t, id, "").cfg
	if rep := card.RunWrapper(context.Background(), neither, nil); rep.Code != card.WrapperExitUsage || !strings.Contains(rep.Why, "harness") {
		t.Fatalf("%s", rep.Line())
	}
}
