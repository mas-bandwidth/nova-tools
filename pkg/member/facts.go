package member

import (
	"sort"
	"strings"
)

type FactSources struct {
	GOOS        string
	GOARCH      string
	CPUInfo     string
	HWCap       uint
	HWCap2      uint
	Sysctl      func(name string) (int64, bool)
	Stat        func(path string) bool
	Provisioned func() bool
}

func DetectFacts(src FactSources) []string {
	set := map[string]bool{}
	add := func(s string) {
		if s != "" {
			set[s] = true
		}
	}
	add(src.GOOS)
	add(src.GOARCH)
	if src.GOARCH == "amd64" || src.GOARCH == "386" {
		for _, f := range x86Facts(src.CPUInfo) {
			add(f)
		}
	}
	if src.GOARCH == "arm64" {
		if src.GOOS == "linux" {
			for _, f := range armLinuxFacts(src.HWCap, src.HWCap2) {
				add(f)
			}
		}
		if src.GOOS == "darwin" {
			for _, f := range armDarwinFacts(src.Sysctl) {
				add(f)
			}
		}
	}
	if src.Stat != nil && src.Stat("/opt/tla/tla2tools.jar") {
		add("tla")
	}
	if src.Provisioned != nil && src.Provisioned() {
		add("xdp")
	}
	var out []string
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func RealSources() FactSources {
	hw, hw2 := realHWCap()
	return FactSources{
		GOOS:        realGOOS(),
		GOARCH:      realGOARCH(),
		CPUInfo:     realCPUInfo(),
		HWCap:       hw,
		HWCap2:      hw2,
		Sysctl:      realSysctl,
		Stat:        realStat,
		Provisioned: realProvisioned,
	}
}

func x86Facts(cpuinfo string) []string {
	allow := map[string]string{
		"sse4_1":       "sse4_1",
		"sse4_2":       "sse4_2",
		"avx":          "avx",
		"avx2":         "avx2",
		"fma":          "fma",
		"bmi1":         "bmi1",
		"bmi2":         "bmi2",
		"adx":          "adx",
		"avx512f":      "avx512f",
		"avx512dq":     "avx512dq",
		"avx512bw":     "avx512bw",
		"avx512vl":     "avx512vl",
		"avx512cd":     "avx512cd",
		"avx512ifma":   "avx512ifma",
		"avx512vbmi":   "avx512vbmi",
		"avx512vbmi2":  "avx512vbmi2",
		"avx512_vnni":  "avx512vnni",
		"avx512_bf16":  "avx512bf16",
		"vaes":         "vaes",
		"vpclmulqdq":   "vpclmulqdq",
		"gfni":         "gfni",
		"sha_ni":       "sha",
	}
	flags := firstFlags(cpuinfo)
	var res []string
	for _, fl := range flags {
		if to, ok := allow[fl]; ok {
			res = append(res, to)
		}
	}
	return res
}

func firstFlags(cpuinfo string) []string {
	lines := strings.Split(cpuinfo, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "flags") && strings.Contains(line, ":") {
			after := strings.SplitN(line, ":", 2)[1]
			parts := strings.Fields(after)
			return parts
		}
	}
	return nil
}

func armLinuxFacts(hwcap, hwcap2 uint) []string {
	var res []string
	if (hwcap & (1 << 1)) != 0 {
		res = append(res, "neon")
	}
	if (hwcap & (1 << 22)) != 0 {
		res = append(res, "sve")
	}
	if (hwcap2 & (1 << 1)) != 0 {
		res = append(res, "sve2")
	}
	if (hwcap2 & (1 << 23)) != 0 {
		res = append(res, "sme")
	}
	if (hwcap2 & (1 << 24)) != 0 {
		res = append(res, "sme2")
	}
	if (hwcap & (1 << 20)) != 0 {
		res = append(res, "dotprod")
	}
	if (hwcap & (1 << 29)) != 0 {
		res = append(res, "i8mm")
	}
	if (hwcap2 & (1 << 5)) != 0 {
		res = append(res, "bf16")
	}
	return res
}

func armDarwinFacts(sysctl func(string) (int64, bool)) []string {
	var res []string
	get := func(n string) bool {
		if sysctl == nil {
			return false
		}
		v, ok := sysctl(n)
		return ok && v != 0
	}
	if get("hw.optional.neon") {
		res = append(res, "neon")
	}
	if get("hw.optional.arm.FEAT_SME") {
		res = append(res, "sme")
	}
	if get("hw.optional.arm.FEAT_SME2") {
		res = append(res, "sme2")
	}
	if get("hw.optional.arm.FEAT_I8MM") {
		res = append(res, "i8mm")
	}
	if get("hw.optional.arm.FEAT_BF16") {
		res = append(res, "bf16")
	}
	if get("hw.optional.arm.FEAT_DotProd") {
		res = append(res, "dotprod")
	}
	return res
}
