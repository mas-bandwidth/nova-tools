package ci

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// data_lifecycle_test.go holds docs/DATA.md, the page that names the source
// of truth for each class of data, and the specs that name a store to one
// split:
//
//   - DATA.md's source table gives configuration to PostgreSQL; messages,
//     receipts and runtime state to Redis; code, playbooks and records to Git;
//     each class once;
//   - it has the backups, the acceptable loss, the restore time and the owner
//     of every class, the restore order, the thresholds, and the retention
//     after what retention must keep;
//   - each threshold is a setting of fleet/group_vars/all.yml whose default is
//     the one the page gives, and every nova_data_* setting there is named on
//     the page and read by fleet/backup.yml, the play that converges them;
//   - SPEC-BUS.md and SPEC-REDIS.md link the page and keep none of the
//     sentences it replaced (Redis never the record, trimming a later decision);
//   - every class with a backup is scheduled by the play: the configuration
//     by the loop record data-backup-config running nova-config backup, and
//     no backup is OWED;
//   - the Redis restore step loads the RDB with the AOF off first (a store
//     started AOF-on beside only an RDB loads nothing), and the acceptance
//     names the drill that proves the order, which is in cmd/nova-config.

// dataSourceOf is the store each class must name, by the class's cell.
var dataSourceOf = map[string]string{
	"configuration":               "PostgreSQL",
	"messages":                    "Redis",
	"receipts":                    "Redis",
	"runtime state":               "Redis",
	"code, playbooks and records": "Git",
}

// dataSections are DATA.md's headings in the order the page must keep:
// retention is defined only after what it must keep.
var dataSections = []string{
	"## The source of truth",
	"## Backups",
	"## Acceptable loss, restore time and ownership",
	"## Restore order onto a replacement host",
	"## Thresholds",
	"## What retention must keep",
	"## Retention",
	"## Acceptance",
}

// dataThresholds are the measures the thresholds table must give a setting.
var dataThresholds = []string{
	"nova_data_bus_messages_per_hour",
	"nova_data_oldest_pending_seconds",
	"nova_data_unreceipted_age_seconds",
	"nova_data_disk_free_percent",
	"nova_data_redis_memory_percent",
}

// dataStale are sentences a spec may no longer hold: DATA.md replaced them.
var dataStale = []string{
	"never the record",
	"nothing in Redis is the only copy",
	"Trimming is a later decision",
}

var reDataSetting = regexp.MustCompile(`\bnova_data_[a-z0-9_]+\b`)

// dataDrill is the acceptance drill DATA.md must name, and the file it is in.
const (
	dataDrill     = "TestRestoreDrillOntoAFreshHost"
	dataDrillFile = "cmd/nova-config/restore_drill_functional_test.go"
)

// dataTexts is what the rule reads, by role.
type dataTexts struct {
	data, bus, redis, groupVars, play, drill string
}

// mdTable is the first table after heading in md, its header and separator
// dropped, each cell trimmed and unquoted from backticks.
func mdTable(md, heading string) [][]string {
	i := strings.Index(md, "\n"+heading+"\n")
	if i < 0 {
		return nil
	}
	var rows [][]string
	in := false
	for _, l := range strings.Split(md[i+1:], "\n")[1:] {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "## ") {
			break
		}
		if !strings.HasPrefix(l, "|") {
			if in {
				break
			}
			continue
		}
		in = true
		var cells []string
		for _, c := range strings.Split(strings.Trim(l, "|"), "|") {
			cells = append(cells, strings.Trim(strings.TrimSpace(c), "`"))
		}
		rows = append(rows, cells)
	}
	if len(rows) < 2 {
		return nil
	}
	return rows[2:]
}

// dataLifecycleProblems is every way the texts break the rule, one line each.
func dataLifecycleProblems(src dataTexts) []string {
	var out []string
	at := map[string]int{}
	for i, h := range dataSections {
		at[h] = strings.Index(src.data, "\n"+h+"\n")
		if at[h] < 0 {
			out = append(out, fmt.Sprintf("DATA.md has no section %q", h))
			continue
		}
		if i > 0 && at[dataSections[i-1]] > at[h] {
			out = append(out, fmt.Sprintf("DATA.md has %q before %q", h, dataSections[i-1]))
		}
	}

	seen := map[string]int{}
	for _, r := range mdTable(src.data, "## The source of truth") {
		want, ok := dataSourceOf[r[0]]
		if !ok {
			continue
		}
		seen[r[0]]++
		if len(r) < 2 || r[1] != want {
			out = append(out, fmt.Sprintf("DATA.md gives %s to %q; its source of truth is %s", r[0], strings.Join(r[1:2], ""), want))
		}
	}
	loss := map[string][]string{}
	for _, r := range mdTable(src.data, "## Acceptable loss, restore time and ownership") {
		loss[r[0]] = r
	}
	for _, class := range fleetKeys(dataSourceOf) {
		if seen[class] != 1 {
			out = append(out, fmt.Sprintf("DATA.md's source table names %s %d times, not once", class, seen[class]))
		}
		r := loss[class]
		if len(r) < 4 || r[1] == "" || r[2] == "" || r[3] == "" {
			out = append(out, fmt.Sprintf("DATA.md gives %s no acceptable loss, restore time and owner", class))
		}
	}

	var vars map[string]any
	if err := yaml.Unmarshal([]byte(src.groupVars), &vars); err != nil {
		return append(out, fmt.Sprintf("fleet/group_vars/all.yml: not YAML: %v", err))
	}
	given := map[string]string{}
	for _, r := range mdTable(src.data, "## Thresholds") {
		if len(r) >= 2 {
			given[r[0]] = r[1]
		}
	}
	for _, s := range dataThresholds {
		def, named := given[s]
		v, set := vars[s]
		switch {
		case !named:
			out = append(out, fmt.Sprintf("DATA.md's thresholds table has no %s", s))
		case !set:
			out = append(out, fmt.Sprintf("%s is a threshold with no default in fleet/group_vars/all.yml", s))
		case fmt.Sprint(v) != def:
			out = append(out, fmt.Sprintf("%s defaults to %v in fleet/group_vars/all.yml and %s in DATA.md", s, v, def))
		}
	}
	for _, s := range fleetKeys(vars) {
		if !strings.HasPrefix(s, "nova_data_") {
			continue
		}
		if !strings.Contains(src.data, s) {
			out = append(out, fmt.Sprintf("%s is a setting DATA.md does not name", s))
		}
		if !strings.Contains(src.play, s) {
			out = append(out, fmt.Sprintf("%s is a setting fleet/backup.yml does not read", s))
		}
	}
	for _, s := range reDataSetting.FindAllString(src.data, -1) {
		if _, ok := vars[s]; !ok && s != "nova_data_backup_dir" && s != "nova_data_bus_addr" {
			out = append(out, fmt.Sprintf("DATA.md names %s, which fleet/group_vars/all.yml does not set", s))
		}
	}

	for name, text := range map[string]string{"SPEC-BUS.md": src.bus, "SPEC-REDIS.md": src.redis} {
		if !strings.Contains(text, "(DATA.md)") {
			out = append(out, fmt.Sprintf("%s does not link DATA.md, the source of truth per class", name))
		}
		for _, s := range dataStale {
			if strings.Contains(strings.ReplaceAll(text, "\n", " "), s) {
				out = append(out, fmt.Sprintf("%s still says %q, which DATA.md replaced", name, s))
			}
		}
	}
	if !strings.Contains(src.play, "data-backup-config: \"{{ data_pg_login + ['nova-config', 'backup',") {
		out = append(out, "fleet/backup.yml holds no data-backup-config loop running nova-config backup: the configuration has no scheduled backup")
	}
	if strings.Contains(src.play, "OWED") || strings.Contains(src.data, "BACKUP config OWED") {
		out = append(out, "a backup is still OWED in fleet/backup.yml or DATA.md")
	}
	restore := dataSection(src.data, "## Restore order onto a replacement host")
	if !strings.Contains(restore, "--appendonly no") || !strings.Contains(restore, "CONFIG SET appendonly yes") {
		out = append(out, "DATA.md's Redis restore step does not load the RDB with the AOF off first; a store started AOF-on beside only an RDB loads nothing")
	}
	if !strings.Contains(dataSection(src.data, "## Acceptance"), dataDrill) {
		out = append(out, "DATA.md's acceptance names no drill ("+dataDrill+")")
	}
	if !strings.Contains(src.drill, "func "+dataDrill+"(t *testing.T)") {
		out = append(out, "the drill DATA.md names is not in "+dataDrillFile)
	}
	sort.Strings(out)
	return out
}

// dataSection is md's text from heading to the next "## ".
func dataSection(md, heading string) string {
	i := strings.Index(md, "\n"+heading+"\n")
	if i < 0 {
		return ""
	}
	rest := md[i+len(heading)+2:]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		return rest[:j]
	}
	return rest
}

func readDataTexts(t *testing.T) dataTexts {
	t.Helper()
	root := repoRoot(t)
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		require.NoError(t, err)
		return string(b)
	}
	return dataTexts{
		data:      read("docs/DATA.md"),
		bus:       read("docs/SPEC-BUS.md"),
		redis:     read("docs/SPEC-REDIS.md"),
		groupVars: read("fleet/group_vars/all.yml"),
		play:      read("fleet/backup.yml"),
		drill:     read(dataDrillFile),
	}
}

// TestTheSpecsAgreeOnTheSourceOfTruth holds the real texts to the rule, then
// plants each defect in a copy and sees the rule name it.
func TestTheSpecsAgreeOnTheSourceOfTruth(t *testing.T) {
	t.Parallel()
	base := readDataTexts(t)
	require.Empty(t, dataLifecycleProblems(base))
	assert.Contains(t, fleetPlays, "backup.yml", "the fleet-plays rule holds the backup play")

	cases := []struct {
		name string
		edit func(*dataTexts)
		want string
	}{
		{"configuration in Redis", func(s *dataTexts) {
			s.data = strings.Replace(s.data, "| configuration | PostgreSQL |", "| configuration | Redis |", 1)
		}, `DATA.md gives configuration to "Redis"; its source of truth is PostgreSQL`},
		{"a class missing", func(s *dataTexts) {
			s.data = strings.Replace(s.data, "| receipts | Redis |", "| delivery | Redis |", 1)
		}, "names receipts 0 times"},
		{"retention before what it must keep", func(s *dataTexts) {
			s.data = strings.Replace(s.data, "\n## What retention must keep\n", "\n## Retention notes\n", 1)
			s.data += "\n## What retention must keep\n"
		}, `DATA.md has "## Retention" before "## What retention must keep"`},
		{"no owner", func(s *dataTexts) {
			s.data = strings.Replace(s.data, "| runtime state | as messages | 30 minutes | the coordinator seat |", "| runtime state | as messages | 30 minutes | |", 1)
		}, "gives runtime state no acceptable loss, restore time and owner"},
		{"a threshold with no default", func(s *dataTexts) {
			s.groupVars = strings.Replace(s.groupVars, "nova_data_disk_free_percent: 20\n", "", 1)
		}, "nova_data_disk_free_percent is a threshold with no default"},
		{"a default the page does not give", func(s *dataTexts) {
			s.groupVars = strings.Replace(s.groupVars, "nova_data_oldest_pending_seconds: 3600", "nova_data_oldest_pending_seconds: 900", 1)
		}, "nova_data_oldest_pending_seconds defaults to 900 in fleet/group_vars/all.yml and 3600 in DATA.md"},
		{"a setting the play does not read", func(s *dataTexts) {
			s.groupVars += "nova_data_unread: 1\n"
		}, "nova_data_unread is a setting fleet/backup.yml does not read"},
		{"the old sentence", func(s *dataTexts) {
			s.redis += "\nRedis is never the record.\n"
		}, `SPEC-REDIS.md still says "never the record"`},
		{"trimming deferred", func(s *dataTexts) {
			s.bus += "\n- Nothing is ever deleted by the tool. Trimming is a later decision.\n"
		}, `SPEC-BUS.md still says "Trimming is a later decision"`},
		{"no link", func(s *dataTexts) {
			s.bus = strings.ReplaceAll(s.bus, "(DATA.md)", "(OTHER.md)")
		}, "SPEC-BUS.md does not link DATA.md"},
		{"the configuration unscheduled", func(s *dataTexts) {
			s.play = strings.Replace(s.play, "data-backup-config: ", "data-backup-other: ", 1)
		}, "fleet/backup.yml holds no data-backup-config loop running nova-config backup"},
		{"a backup owed", func(s *dataTexts) {
			s.play += "\n# BACKUP config OWED\n"
		}, "a backup is still OWED"},
		{"serve straight onto the RDB", func(s *dataTexts) {
			s.data = strings.Replace(s.data, "CONFIG SET appendonly yes", "nova-redis serve", 1)
		}, "DATA.md's Redis restore step does not load the RDB with the AOF off first"},
		{"no drill named", func(s *dataTexts) {
			s.data = strings.ReplaceAll(s.data, dataDrill, "TestSomethingElse")
		}, "DATA.md's acceptance names no drill"},
		{"the drill gone", func(s *dataTexts) {
			s.drill = ""
		}, "the drill DATA.md names is not in cmd/nova-config/restore_drill_functional_test.go"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := base
			tc.edit(&s)
			assert.Contains(t, strings.Join(dataLifecycleProblems(s), "\n"), tc.want)
		})
	}
}
