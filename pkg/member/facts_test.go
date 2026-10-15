package member

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGateFacts(t *testing.T) {
	t.Parallel()
	dir := "testdata/facts"
	cases := []struct {
		name    string
		src     FactSources
		want    []string
	}{
		{
			name: "zen5",
			src: FactSources{
				GOOS:     "linux",
				GOARCH:   "amd64",
				CPUInfo:  mustRead(t, filepath.Join(dir, "cpuinfo-zen5-avx512.txt")),
				HWCap:    0,
				HWCap2:   0,
				Sysctl:   func(string) (int64, bool) { return 0, false },
				Stat:     func(string) bool { return false },
				Provisioned: func() bool { return false },
			},
			want: []string{"amd64", "avx2", "avx512f", "avx512ifma", "linux"},
		},
		{
			name: "zen1",
			src: FactSources{
				GOOS:     "linux",
				GOARCH:   "amd64",
				CPUInfo:  mustRead(t, filepath.Join(dir, "cpuinfo-zen1-avx2.txt")),
				HWCap:    0,
				HWCap2:   0,
				Sysctl:   func(string) (int64, bool) { return 0, false },
				Stat:     func(string) bool { return false },
				Provisioned: func() bool { return false },
			},
			want: []string{"amd64", "avx2", "linux"},
		},
		{
			name: "graviton-hwcap",
			src: FactSources{
				GOOS:   "linux",
				GOARCH: "arm64",
				CPUInfo: mustRead(t, filepath.Join(dir, "cpuinfo-graviton-arm64.txt")),
				// HWCAP with asimd (bit 1) and sve2
				HWCap:  (1 << 1),
				HWCap2: (1 << 1),
				Sysctl: func(string) (int64, bool) { return 0, false },
				Stat:   func(string) bool { return false },
				Provisioned: func() bool { return false },
			},
			want: []string{"arm64", "linux", "neon", "sve2"},
		},
		{
			name: "darwin-m5-sme2",
			src: FactSources{
				GOOS:   "darwin",
				GOARCH: "arm64",
				CPUInfo: "",
				HWCap:  0,
				HWCap2: 0,
				Sysctl: sysctlMap(t, filepath.Join(dir, "sysctl-m5-sme2.txt")),
				Stat:   func(string) bool { return false },
				Provisioned: func() bool { return false },
			},
			want: []string{"arm64", "bf16", "darwin", "dotprod", "i8mm", "neon", "sme", "sme2"},
		},
		{
			name: "darwin-m3-neon",
			src: FactSources{
				GOOS:   "darwin",
				GOARCH: "arm64",
				CPUInfo: "",
				HWCap:  0,
				HWCap2: 0,
				Sysctl: sysctlMap(t, filepath.Join(dir, "sysctl-m3-neon.txt")),
				Stat:   func(string) bool { return false },
				Provisioned: func() bool { return false },
			},
			want: []string{"arm64", "bf16", "darwin", "dotprod", "i8mm", "neon"},
		},
		{
			name: "tla",
			src: FactSources{
				GOOS:        "linux",
				GOARCH:      "amd64",
				CPUInfo:     "",
				Sysctl:      func(string) (int64, bool) { return 0, false },
				Stat:        func(p string) bool { return p == "/opt/tla/tla2tools.jar" },
				Provisioned: func() bool { return false },
			},
			want: []string{"amd64", "linux", "tla"},
		},
		{
			name: "xdp",
			src: FactSources{
				GOOS:        "linux",
				GOARCH:      "amd64",
				CPUInfo:     "",
				Sysctl:      func(string) (int64, bool) { return 0, false },
				Stat:        func(string) bool { return false },
				Provisioned: func() bool { return true },
			},
			want: []string{"amd64", "linux", "xdp"},
		},
		{
			name: "no-xdp",
			src: FactSources{
				GOOS:        "linux",
				GOARCH:      "amd64",
				CPUInfo:     "",
				Sysctl:      func(string) (int64, bool) { return 0, false },
				Stat:        func(string) bool { return false },
				Provisioned: func() bool { return false },
			},
			want: []string{"amd64", "linux"},
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := DetectFacts(c.src)
			require.Equal(t, c.want, got)
		})
	}
}

func mustRead(t *testing.T, p string) string {
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	return string(b)
}

func sysctlMap(t *testing.T, p string) func(string) (int64, bool) {
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	m := map[string]int64{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		var i int64
		// values are 0 or 1 mostly
		if val == "1" {
			i = 1
		}
		m[name] = i
	}
	return func(name string) (int64, bool) {
		v, ok := m[name]
		return v, ok
	}
}
