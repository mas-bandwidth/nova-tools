package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
	"github.com/mas-bandwidth/nova-tools/internal/secrets"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

var (
	memberMu             sync.RWMutex
	memberLoginLoaders   = map[string]func(seatName string) (secrets.Login, bool, error){}
	memberSecretsReaders = map[string]func(secrets.Login, ...string) (map[string]secrets.Secret, error){}
)

func registerMemberLoginLoader(seatName string, loader func(string) (secrets.Login, bool, error)) func() {
	memberMu.Lock()
	defer memberMu.Unlock()
	memberLoginLoaders[seatName] = loader
	return func() {
		memberMu.Lock()
		defer memberMu.Unlock()
		delete(memberLoginLoaders, seatName)
	}
}

func registerMemberSecretsReader(as string, reader func(secrets.Login, ...string) (map[string]secrets.Secret, error)) func() {
	memberMu.Lock()
	defer memberMu.Unlock()
	memberSecretsReaders[as] = reader
	return func() {
		memberMu.Lock()
		defer memberMu.Unlock()
		delete(memberSecretsReaders, as)
	}
}

func findMemberLoginPath() string {
	for _, tool := range []string{"nova-swarm", "nova-sprint"} {
		if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
			p := filepath.Join(x, tool, "login.json")
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
		if home, err := os.UserHomeDir(); err == nil {
			p := filepath.Join(home, ".config", tool, "login.json")
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

func defaultMemberLoginLoader(seatName string) (secrets.Login, bool, error) {
	path := findMemberLoginPath()
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return secrets.Login{}, false, fmt.Errorf("the seat login %s is unreadable: %w; run: nova-sprint seat login", path, err)
		}
		var raw struct {
			Store   string   `json:"store"`
			As      string   `json:"as"`
			Key     string   `json:"key"`
			Sops    string   `json:"sops"`
			Secret  string   `json:"secret"`
			Secrets []string `json:"secrets"`
		}
		if err := json.Unmarshal(b, &raw); err != nil {
			return secrets.Login{}, false, fmt.Errorf("the seat login %s is not a login: %w; run: nova-sprint seat login", path, err)
		}
		l := secrets.Login{
			Store:   raw.Store,
			As:      raw.As,
			Key:     raw.Key,
			Sops:    raw.Sops,
			Name:    raw.Secret,
			Secrets: raw.Secrets,
		}
		return l, true, nil
	}
	if seatName == "" {
		seatName = seatcred.Process().Selected()
	}
	if seatName != "" {
		paths, err := seatcred.PathsFor(seatName, os.Getenv)
		if err != nil {
			return secrets.Login{}, false, err
		}
		return secrets.Login{
			Store: paths.Store,
			As:    seatName,
			Key:   paths.Key,
			Sops:  paths.Sops,
		}, true, nil
	}
	return secrets.Login{}, false, nil
}

func loadAndCheckMemberSecrets(seatName string) (map[string]secrets.Secret, error) {
	memberMu.RLock()
	loader := memberLoginLoaders[seatName]
	memberMu.RUnlock()

	if loader == nil {
		loader = defaultMemberLoginLoader
	}
	login, ok, err := loader(seatName)
	if err != nil {
		return nil, err
	}
	if !ok || len(login.Secrets) == 0 {
		return nil, nil
	}

	memberMu.RLock()
	reader := memberSecretsReaders[login.As]
	memberMu.RUnlock()

	if reader == nil {
		reader = secrets.ReadLoginSecrets
	}
	return reader(login, login.Secrets...)
}

func routeKeyFor(model, workerSecret string) string {
	if workerSecret != "" {
		return workerSecret
	}
	if model == "" {
		return ""
	}
	provider, _, _ := strings.Cut(model, "/")
	provider = strings.TrimSpace(strings.ToLower(provider))
	if localProviders[provider] {
		return ""
	}
	clean := strings.ReplaceAll(provider, "-", "_")
	return strings.ToUpper(clean) + "_API_KEY"
}

func (r *nativeRunner) getWorkerSecret() string {
	if r.worker != "" {
		if w, problems := swarm.LoadWorker(r.worker); len(problems) == 0 {
			if w.Secret != "" {
				return w.Secret
			}
			if w.EnvVar != "" {
				return w.EnvVar
			}
		}
	}
	return ""
}

func (r *nativeRunner) childEnvFor(model string) []string {
	workerSec := r.getWorkerSecret()
	keyName := routeKeyFor(model, workerSec)
	var childPass []string
	var childExtra []string
	if keyName != "" {
		if r.secrets != nil {
			if sec, ok := r.secrets[keyName]; ok {
				var val string
				// ignored: the closure returns nil so Use never returns an error
				_ = sec.Use(func(v string) error { val = v; return nil })
				if val != "" {
					childExtra = append(childExtra, keyName+"="+val)
				}
				childPass = []string{keyName}
			}
		}
		if len(childPass) == 0 {
			for _, p := range r.pass {
				if p == keyName {
					childPass = []string{keyName}
					break
				}
			}
		}
	}
	if len(childPass) == 0 && r.secrets == nil && len(r.pass) > 0 {
		for _, p := range r.pass {
			if p != decide.JevSecret {
				childPass = append(childPass, p)
			}
		}
	}
	base := append(os.Environ(), r.env...)
	base = append(base, childExtra...)
	return childEnviron(base, childPass)
}
