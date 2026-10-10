package secrets

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// RunExec validates all preflight invariants, decrypts <store>/<as>.yaml,
// applies --only and --require filters, prints the OK line to stderr,
// sets RLIMIT_CORE to 0, and replaces the current process with cmdArgs. The OK
// line goes out before the exec call, so a failure of that call returns 125 with
// a FAIL line saying the command never started.
func RunExec(storeDir, asName, keyPath, sopsPath, onlyArg string, required []string, cmdArgs []string) (int, error) {
	// No core file: a crash after the decrypt must not write the values to disk.
	if err := setRlimitCoreZero(); err != nil {
		return 125, fmt.Errorf("failed to set RLIMIT_CORE to 0: %w", err)
	}

	// Every missing flag and the missing command, named in one refusal (ONBOARDING point
	// 2). The command is not a flag, so it is named after the flags, as what it is.
	var problems []string
	if err := preflight("", need{storeDir, "--store <dir>", false}, need{asName, "--as <name>", false}, need{keyPath, "--key <path>", false},
		need{sopsPath, "--sops <path>", false}, need{onlyArg, "--only <names|all>", false}); err != nil {
		problems = append(problems, err.Error())
	}
	if len(cmdArgs) == 0 {
		problems = append(problems, "no command after '--': exec runs one, as -- <cmd> [args...]")
	}
	if len(problems) > 0 {
		return 125, fmt.Errorf("%s; example: nova-secrets exec --store ./secrets --as worker --key ~/.config/nova-secrets/worker.key --sops \"$(command -v sops)\" --only GH_TOKEN -- gh api user", strings.Join(problems, "; "))
	}

	// The command must exist before the OK line says it will run.
	if _, err := exec.LookPath(cmdArgs[0]); err != nil {
		return 125, fmt.Errorf("command not found: %s; the command after '--' is a program on PATH or a path to one", cmdArgs[0])
	}
	// The store, the seat's file and its key, checked and decrypted by the one path
	// every in-process reader of a seat also takes (seatfile.go).
	sf, err := OpenSeatFile(storeDir, asName, keyPath, sopsPath)
	if err != nil {
		return 125, err
	}
	targetFile, headSHA, secretsMap := sf.Path, sf.HeadSHA, sf.Secrets

	// --only: the values the command is given.
	selectedSecrets := make(map[string]Secret)
	onlyWord := ""
	if onlyArg == "all" {
		selectedSecrets = secretsMap
		onlyWord = "all"
	} else {
		parts := strings.Split(onlyArg, ",")
		var missingOnly []string
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			sec, ok := secretsMap[p]
			if !ok {
				missingOnly = append(missingOnly, p)
				continue
			}
			selectedSecrets[p] = sec
		}
		if len(missingOnly) > 0 {
			sort.Strings(missingOnly)
			return 125, fmt.Errorf("--only names key(s) not in %s: %s; the names the seat holds: run: nova-secrets names --store %s --as %s", targetFile, strings.Join(missingOnly, ", "), storeDir, asName)
		}
		onlyWord = strconv.Itoa(len(selectedSecrets))
	}

	// --require: names that must be in the file and in --only.
	var missingRequired []string
	var excludedRequired []string
	for _, req := range required {
		req = strings.TrimSpace(req)
		if req == "" {
			continue
		}
		if _, ok := secretsMap[req]; !ok {
			missingRequired = append(missingRequired, req)
			continue
		}
		if _, ok := selectedSecrets[req]; !ok {
			excludedRequired = append(excludedRequired, req)
		}
	}
	if len(missingRequired) > 0 {
		sort.Strings(missingRequired)
		return 125, fmt.Errorf("required key(s) missing from %s: %s; run: sops %s",
			targetFile, strings.Join(missingRequired, ", "), targetFile)
	}
	if len(excludedRequired) > 0 {
		sort.Strings(excludedRequired)
		return 125, fmt.Errorf("--require %s is excluded by --only %q", strings.Join(excludedRequired, ", "), onlyArg)
	}

	// The OK line, on stderr, every field escaped: the command's own stdout stays its
	// own. It cannot move below the exec call: a successful exec never returns.
	fmt.Fprintf(os.Stderr, "SECRETS EXEC OK as=%s keys=%d only=%s required=%d file=%s head=%s cmd=%s\n",
		oneline.Field(asName), len(selectedSecrets), oneline.Field(onlyWord), len(required),
		oneline.Field(targetFile), oneline.Field(headSHA), oneline.Field(cmdArgs[0]))

	// The command's environment: every name the seat file holds is dropped from the
	// inherited one (an omitted value must not arrive by another route), as are
	// SOPS_AGE_KEY and SOPS_AGE_KEY_FILE; then the --only values are added.
	scrubKeys := make(map[string]bool)
	for k := range secretsMap {
		scrubKeys[k] = true
	}
	scrubKeys["SOPS_AGE_KEY"] = true
	scrubKeys["SOPS_AGE_KEY_FILE"] = true

	var cleanEnv []string
	for _, envEntry := range os.Environ() {
		idx := strings.IndexByte(envEntry, '=')
		if idx == -1 {
			continue
		}
		envKey := envEntry[:idx]
		collides := false
		for sKey := range scrubKeys {
			if envKey == sKey || (runtime.GOOS == "windows" && strings.EqualFold(envKey, sKey)) {
				collides = true
				break
			}
		}
		if !collides {
			cleanEnv = append(cleanEnv, envEntry)
		}
	}
	env := cleanEnv
	for k, sec := range selectedSecrets {
		// ignored: Use fails only when its function is nil or fails, and this one does neither
		_ = sec.Use(func(val string) error {
			env = append(env, k+"="+val)
			return nil
		})
	}

	// Become the command.
	if err := replaceProcess(cmdArgs, env); err != nil {
		// The OK line above is already out, and it cannot wait for the exec below:
		// a successful exec never returns. So this return is a FAIL after an OK, and
		// it must say exactly what that means: the command never started (security#64
		// finding 4).
		return 125, fmt.Errorf("exec failed: %w; the command never started", err)
	}

	return 0, nil
}
