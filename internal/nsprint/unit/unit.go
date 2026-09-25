package unit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"text/template"
	"time"
	"embed"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

//go:embed templates/*
var Templates embed.FS

var VersionLine = func() string {
	return buildinfo.Line("nova-sprint", "v1.0.0")
}

var UnitLoader = func(osName, unitName, content string) error {
	return nil
}

type PlanOptions struct {
	Host    string
	Role    string // "coordinator" or "bench"
	Friends map[string]int
	Store   string
	OS      string
}

type UnitDef struct {
	Name             string
	Verb             []string
	User             string
	Secret           string
	Guard            string
	ProgramArguments []string
	ExecStart        string
}

type Lease struct {
	st       *store.Store
	key      string
	instance string
}

func (l *Lease) Instance() string { return l.instance }
func (l *Lease) Renew(ctx context.Context) error {
	res, err := l.st.Client().Expire(ctx, l.key, 6*time.Second).Result()
	if err != nil || !res {
		return errors.New("fenced or expired lease")
	}
	return nil
}
func (l *Lease) Release(ctx context.Context) error {
	_, _ = l.st.Client().Del(ctx, l.key).Result()
	return nil
}

func AcquireLease(ctx context.Context, st *store.Store, key string, ttl time.Duration) (*Lease, error) {
	instance := fmt.Sprintf("inst-%d", time.Now().UnixNano())
	ok, err := st.Client().SetNX(ctx, key, instance, ttl).Result()
	if err != nil || !ok {
		return nil, fmt.Errorf("lease %s is held", key)
	}
	return &Lease{st: st, key: key, instance: instance}, nil
}

func GenerateUnits(opts PlanOptions) []UnitDef {
	var units []UnitDef
	addr := opts.Store
	if addr == "" {
		addr = "127.0.0.1:6379"
	}
	if opts.Role == "coordinator" {
		units = append(units, UnitDef{
			Name:   "ns-reconciler",
			Verb:   []string{"reconcile", "--redis", addr},
			User:   "coordinator",
			Secret: "NOVA_REDIS_COORDINATOR_PASSWORD",
			Guard:  "lease:reconciler",
		})
		units = append(units, UnitDef{
			Name:   "ns-consume-ok-to-friend",
			Verb:   []string{"consume", "ok-to-friend", "--redis", addr},
			User:   "coordinator",
			Secret: "NOVA_REDIS_COORDINATOR_PASSWORD",
			Guard:  "lease:consume:ok-to-friend",
		})
		units = append(units, UnitDef{
			Name:   "ns-table",
			Verb:   []string{"table", "--redis", addr, "--loop"},
			User:   "coordinator",
			Secret: "NOVA_REDIS_COORDINATOR_PASSWORD",
			Guard:  "lease:table",
		})
		var friendNames []string
		for f := range opts.Friends {
			friendNames = append(friendNames, f)
		}
		sort.Strings(friendNames)
		for _, f := range friendNames {
			slots := opts.Friends[f]
			units = append(units, UnitDef{
				Name:   fmt.Sprintf("ns-friend-hello-%s", f),
				Verb:   []string{"friend", "hello", "--as", f, "--slots", fmt.Sprintf("%d", slots), "--redis", addr, "--loop"},
				User:   "bench",
				Secret: "NOVA_REDIS_BENCH_PASSWORD",
				Guard:  fmt.Sprintf("lease:friend-hello:%s", f),
			})
		}
	} else if opts.Role == "bench" {
		host := opts.Host
		if host == "" {
			host = "bench-1"
		}
		units = append(units, UnitDef{
			Name:   "ns-bench-beat",
			Verb:   []string{"bench", "beat", "--redis", addr},
			User:   "bench",
			Secret: "NOVA_REDIS_BENCH_PASSWORD",
			Guard:  fmt.Sprintf("bench:%s:beat", host),
		})
		units = append(units, UnitDef{
			Name:   "ns-harvest",
			Verb:   []string{"card", "harvest", "--bench", host, "--redis", addr},
			User:   "bench",
			Secret: "NOVA_REDIS_BENCH_PASSWORD",
			Guard:  fmt.Sprintf("lease:harvest:%s", host),
		})
	}
	return units
}

func RenderUnit(u UnitDef, osName string) (string, error) {
	argv := []string{
		"nova-secrets", "exec", "--only", u.Secret, "--require", u.Secret,
		"--env", fmt.Sprintf("NOVA_SPRINT_REDIS_USER=%s", u.User),
		"--env", fmt.Sprintf("NOVA_SPRINT_REDIS_PASSWORD_ENV=%s", u.Secret),
		"nova-sprint",
	}
	argv = append(argv, u.Verb...)

	for _, arg := range argv {
		if strings.Contains(strings.ToUpper(arg), "PASSWORD") && arg != "NOVA_REDIS_COORDINATOR_PASSWORD" && arg != "NOVA_REDIS_BENCH_PASSWORD" && !strings.HasPrefix(arg, "NOVA_SPRINT_REDIS_PASSWORD_ENV=") {
			return "", errors.New("password in argv forbidden")
		}
	}

	var tmplName string
	if osName == "darwin" {
		tmplName = "templates/darwin.plist.tmpl"
	} else if osName == "linux" {
		tmplName = "templates/linux.service.tmpl"
	} else {
		return "", fmt.Errorf("unsupported os %s", osName)
	}

	data, err := fs.ReadFile(Templates, tmplName)
	if err != nil {
		return "", err
	}

	execStart := strings.Join(argv, " ")
	t, err := template.New(tmplName).Parse(string(data))
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	err = t.Execute(&buf, struct {
		Name             string
		ProgramArguments []string
		ExecStart        string
	}{
		Name:             u.Name,
		ProgramArguments: argv,
		ExecStart:        execStart,
	})
	if err != nil {
		return "", err
	}

	rendered := buf.String()
	if osName == "darwin" {
		if !strings.Contains(rendered, "AbandonProcessGroup") || !strings.Contains(rendered, "KeepAlive") {
			return "", errors.New("darwin template missing AbandonProcessGroup or KeepAlive")
		}
	} else {
		if !strings.Contains(rendered, "KillMode=process") || !strings.Contains(rendered, "Restart=always") {
			return "", errors.New("linux template missing KillMode=process or Restart=always")
		}
	}
	return rendered, nil
}

func TemplatesSha256() string {
	var names []string
	fs.WalkDir(Templates, ".", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			names = append(names, path)
		}
		return nil
	})
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		data, _ := fs.ReadFile(Templates, name)
		h.Write([]byte(name))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func Plan(opts PlanOptions) (map[string]string, map[string]string, string, error) {
	if opts.OS == "" {
		opts.OS = "darwin"
	}
	units := GenerateUnits(opts)
	digests := make(map[string]string)
	files := make(map[string]string)
	var unitNames []string
	for _, u := range units {
		unitNames = append(unitNames, u.Name)
	}
	sort.Strings(unitNames)

	nameMap := make(map[string]UnitDef)
	for _, u := range units {
		nameMap[u.Name] = u
	}

	var lines []string
	for _, name := range unitNames {
		u := nameMap[name]
		content, err := RenderUnit(u, opts.OS)
		if err != nil {
			return nil, nil, "", err
		}
		files[name] = content
		h := sha256.Sum256([]byte(content))
		digest := hex.EncodeToString(h[:])[:8]
		digests[name] = digest
		lines = append(lines, fmt.Sprintf("%s=%s", name, digest))
	}
	sort.Strings(lines)
	pHash := sha256.Sum256([]byte(strings.Join(lines, "\n") + "\n"))
	planSha := hex.EncodeToString(pHash[:])[:8]

	return digests, files, planSha, nil
}

func Declare(ctx context.Context, st *store.Store, opts PlanOptions, seat string, remove bool) (string, error) {
	host := opts.Host
	if host == "" {
		if opts.Role == "coordinator" {
			host = "coordinator"
		} else {
			host = "bench-1"
		}
	}
	if remove {
		pipe := st.Client().Pipeline()
		pipe.Del(ctx, fmt.Sprintf("units:want:%s", host))
		pipe.Del(ctx, fmt.Sprintf("units:want:%s:meta", host))
		pipe.SRem(ctx, "units:hosts", host)
		pipe.Del(ctx, "units:registry:coordinator")
		if _, err := pipe.Exec(ctx); err != nil {
			return "", err
		}
		return fmt.Sprintf("REMOVED %s", host), nil
	}

	digests, _, planSha, err := Plan(opts)
	if err != nil {
		return "", err
	}

	vLine := VersionLine()
	tSha := TemplatesSha256()

	client := st.Client()
	meta, err := client.HGetAll(ctx, fmt.Sprintf("units:want:%s:meta", host)).Result()
	if err == nil && len(meta) > 0 {
		if meta["plan_sha256"] == planSha && meta["verb_version"] == vLine {
			return "UNCHANGED", nil
		}
	}

	pipe := client.Pipeline()
	pipe.Del(ctx, fmt.Sprintf("units:want:%s", host))
	if len(digests) > 0 {
		args := make([]any, 0, len(digests)*2)
		for k, v := range digests {
			args = append(args, k, v)
		}
		pipe.HSet(ctx, fmt.Sprintf("units:want:%s", host), args...)
	}

	at := fmt.Sprintf("%d", time.Now().UnixMilli())
	metaMap := map[string]any{
		"at":               at,
		"verb_version":     vLine,
		"templates_sha256": tSha,
		"plan_sha256":      planSha,
		"role":             opts.Role,
		"os":               opts.OS,
		"by":               seat,
	}
	pipe.HSet(ctx, fmt.Sprintf("units:want:%s:meta", host), metaMap)
	pipe.SAdd(ctx, "units:hosts", host)
	if opts.Role == "coordinator" {
		pipe.Set(ctx, "units:registry:coordinator", host, 0)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return "", err
	}

	return fmt.Sprintf("DECLARED %s units=%d plan=%s", host, len(digests), planSha), nil
}

func Apply(ctx context.Context, st *store.Store, opts PlanOptions, dir string) (int, error) {
	host := opts.Host
	if host == "" {
		if opts.Role == "coordinator" {
			host = "coordinator"
		} else {
			host = "bench-1"
		}
	}
	client := st.Client()
	want, err := client.HGetAll(ctx, fmt.Sprintf("units:want:%s", host)).Result()
	if err != nil {
		return 2, err
	}

	_, files, _, err := Plan(opts)
	if err != nil {
		return 2, err
	}

	actualSet, err := client.SMembers(ctx, fmt.Sprintf("units:%s", host)).Result()
	if err != nil {
		actualSet = nil
	}
	actualMap := make(map[string]bool)
	for _, u := range actualSet {
		actualMap[u] = true
	}

	vLine := VersionLine()
	pipe := client.Pipeline()

	for name, digest := range want {
		content := files[name]
		actHash, err := client.HGet(ctx, fmt.Sprintf("unit:%s:%s", host, name), "digest").Result()
		if err != nil || actHash != digest {
			if err := UnitLoader(opts.OS, name, content); err != nil {
				return 1, err
			}
			pipe.HSet(ctx, fmt.Sprintf("unit:%s:%s", host, name), map[string]any{
				"at":           fmt.Sprintf("%d", time.Now().UnixMilli()),
				"digest":       digest,
				"verb_version": vLine,
				"loaded":       1,
			})
		}
		pipe.SAdd(ctx, fmt.Sprintf("units:%s", host), name)
	}

	for _, act := range actualSet {
		if _, ok := want[act]; !ok {
			pipe.SRem(ctx, fmt.Sprintf("units:%s", host), act)
			pipe.Del(ctx, fmt.Sprintf("unit:%s:%s", host, act))
		}
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return 2, err
	}
	return 0, nil
}

func Check(ctx context.Context, st *store.Store, hostFilter string, all bool) ([]string, int, error) {
	client := st.Client()
	hosts, err := client.SMembers(ctx, "units:hosts").Result()
	if err != nil {
		hosts = nil
	}
	benches, err := client.SMembers(ctx, "benches").Result()
	if err != nil {
		benches = nil
	}

	coord, _ := client.Get(ctx, "units:registry:coordinator").Result()
	if coord == "" {
		for _, h := range hosts {
			meta, _ := client.HGetAll(ctx, fmt.Sprintf("units:want:%s:meta", h)).Result()
			if meta["role"] == "coordinator" {
				coord = h
				break
			}
		}
	}

	var defects []string
	hostsMap := make(map[string]bool)
	for _, h := range hosts {
		hostsMap[h] = true
	}

	if coord != "" {
		if !hostsMap[coord] {
			defects = append(defects, fmt.Sprintf("UNDECLARED %s", coord))
		} else {
			meta, _ := client.HGetAll(ctx, fmt.Sprintf("units:want:%s:meta", coord)).Result()
			if len(meta) == 0 {
				defects = append(defects, fmt.Sprintf("UNDECLARED %s", coord))
			}
		}
	}

	for _, b := range benches {
		if hostFilter != "" && hostFilter != b {
			continue
		}
		beatExist, _ := client.Exists(ctx, fmt.Sprintf("bench:%s:beat", b)).Result()
		isUp := false
		if beatExist > 0 {
			beatAtStr, _ := client.HGet(ctx, fmt.Sprintf("bench:%s:beat", b), "at").Result()
			if beatAtStr != "" {
				var atMs int64
				fmt.Sscanf(beatAtStr, "%d", &atMs)
				atTime := time.UnixMilli(atMs)
				if time.Since(atTime) <= 2*time.Second {
					isUp = true
				}
			} else {
				isUp = true
			}
		}
		if isUp {
			if !hostsMap[b] {
				defects = append(defects, fmt.Sprintf("UNDECLARED %s", b))
			} else {
				meta, _ := client.HGetAll(ctx, fmt.Sprintf("units:want:%s:meta", b)).Result()
				if len(meta) == 0 {
					defects = append(defects, fmt.Sprintf("UNDECLARED %s", b))
				}
			}
		}
	}

	allHosts := hosts
	if hostFilter != "" {
		allHosts = []string{hostFilter}
	}

	totalUnits := 0
	for _, h := range allHosts {
		want, _ := client.HGetAll(ctx, fmt.Sprintf("units:want:%s", h)).Result()
		actual, _ := client.SMembers(ctx, fmt.Sprintf("units:%s", h)).Result()
		actualMap := make(map[string]bool)
		for _, u := range actual {
			actualMap[u] = true
		}

		meta, _ := client.HGetAll(ctx, fmt.Sprintf("units:want:%s:meta", h)).Result()
		wantVersion := meta["verb_version"]

		for unit, wantDigest := range want {
			totalUnits++
			if !actualMap[unit] {
				defects = append(defects, fmt.Sprintf("MISSING %s %s", h, unit))
				continue
			}
			uData, _ := client.HGetAll(ctx, fmt.Sprintf("unit:%s:%s", h, unit)).Result()
			if len(uData) == 0 {
				defects = append(defects, fmt.Sprintf("MISSING %s %s", h, unit))
				continue
			}
			if uData["digest"] != wantDigest {
				defects = append(defects, fmt.Sprintf("STALE %s %s digest have=%s want=%s", h, unit, uData["digest"], wantDigest))
			}
			if wantVersion != "" && uData["verb_version"] != wantVersion {
				defects = append(defects, fmt.Sprintf("STALE %s %s verb_version have=%s want=%s", h, unit, uData["verb_version"], wantVersion))
			}
			if uData["loaded"] != "1" {
				defects = append(defects, fmt.Sprintf("NOTLOADED %s %s", h, unit))
			}
		}

		for _, act := range actual {
			if _, ok := want[act]; !ok {
				defects = append(defects, fmt.Sprintf("EXTRA %s %s", h, act))
			}
		}
	}

	if len(defects) > 0 {
		return defects, 1, nil
	}
	return []string{fmt.Sprintf("UNITS OK hosts=%d units=%d", len(allHosts), totalUnits)}, 0, nil
}
