package member

import (
	"regexp"
	"sort"
	"strings"
)

// FactSources are the inputs DetectFacts reads. Tests pass fakes; the member
// start path passes RealSources (docs/SPEC-SWARM.md, the member's beat).
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

// factToken is the shape a gate fact may have: lowercase, no spaces.
var factToken = regexp.MustCompile(`^[a-z0-9_.-]+$`)

// cpuFact is one row of the CPU fact table. A new flag is one row.
// X86 is the cpuinfo `flags` word (renamed where the published token differs:
// avx512_vnni as avx512vnni, avx512_bf16 as avx512bf16, sha_ni as sha).
// ArmBit is the bit in the kernel's uapi arch/arm64/include/uapi/asm/hwcap.h
// (Linux 6.12); golang.org/x/sys/unix does not export those constants.
// ArmHW2 selects HWCAP2 (AT_HWCAP2) over HWCAP. Darwin is the sysctl name;
// a name absent or 0 is no fact. ArmBit < 0 means the row is not an arm linux fact.
type cpuFact struct {
	Token  string
	X86    string
	ArmBit int
	ArmHW2 bool
	Darwin string
}

// cpuFacts is the one allow-list a SIMD card may need (docs/SPEC-SWARM.md, the member).
var cpuFacts = []cpuFact{
	{Token: "sse4_1", X86: "sse4_1", ArmBit: -1},
	{Token: "sse4_2", X86: "sse4_2", ArmBit: -1},
	{Token: "avx", X86: "avx", ArmBit: -1},
	{Token: "avx2", X86: "avx2", ArmBit: -1},
	{Token: "fma", X86: "fma", ArmBit: -1},
	{Token: "bmi1", X86: "bmi1", ArmBit: -1},
	{Token: "bmi2", X86: "bmi2", ArmBit: -1},
	{Token: "adx", X86: "adx", ArmBit: -1},
	{Token: "avx512f", X86: "avx512f", ArmBit: -1},
	{Token: "avx512dq", X86: "avx512dq", ArmBit: -1},
	{Token: "avx512bw", X86: "avx512bw", ArmBit: -1},
	{Token: "avx512vl", X86: "avx512vl", ArmBit: -1},
	{Token: "avx512cd", X86: "avx512cd", ArmBit: -1},
	{Token: "avx512ifma", X86: "avx512ifma", ArmBit: -1},
	{Token: "avx512vbmi", X86: "avx512vbmi", ArmBit: -1},
	{Token: "avx512vbmi2", X86: "avx512vbmi2", ArmBit: -1},
	{Token: "avx512vnni", X86: "avx512_vnni", ArmBit: -1},
	{Token: "avx512bf16", X86: "avx512_bf16", ArmBit: -1},
	{Token: "vaes", X86: "vaes", ArmBit: -1},
	{Token: "vpclmulqdq", X86: "vpclmulqdq", ArmBit: -1},
	{Token: "gfni", X86: "gfni", ArmBit: -1},
	{Token: "sha", X86: "sha_ni", ArmBit: -1},
	// HWCAP_ASIMD (1<<1), published as neon.
	{Token: "neon", ArmBit: 1, Darwin: "hw.optional.neon"},
	// HWCAP_SVE (1<<22).
	{Token: "sve", ArmBit: 22},
	// HWCAP2_SVE2 (1<<1).
	{Token: "sve2", ArmBit: 1, ArmHW2: true},
	// HWCAP2_SME (1<<23); hw.optional.arm.FEAT_SME.
	{Token: "sme", ArmBit: 23, ArmHW2: true, Darwin: "hw.optional.arm.FEAT_SME"},
	// HWCAP2_SME2 (1<<37); hw.optional.arm.FEAT_SME2.
	{Token: "sme2", ArmBit: 37, ArmHW2: true, Darwin: "hw.optional.arm.FEAT_SME2"},
	// HWCAP2_I8MM (1<<13); hw.optional.arm.FEAT_I8MM.
	{Token: "i8mm", ArmBit: 13, ArmHW2: true, Darwin: "hw.optional.arm.FEAT_I8MM"},
	// HWCAP2_BF16 (1<<14); hw.optional.arm.FEAT_BF16.
	{Token: "bf16", ArmBit: 14, ArmHW2: true, Darwin: "hw.optional.arm.FEAT_BF16"},
	// HWCAP_ASIMDDP (1<<20), published as dotprod; hw.optional.arm.FEAT_DotProd.
	{Token: "dotprod", ArmBit: 20, Darwin: "hw.optional.arm.FEAT_DotProd"},
}

// DetectFacts returns the machine's gate facts: sorted, de-duplicated, lowercase
// tokens matching [a-z0-9_.-]+ (docs/SPEC-SWARM.md, the member's beat).
func DetectFacts(src FactSources) []string {
	set := map[string]bool{}
	add := func(s string) {
		s = strings.ToLower(s)
		if factToken.MatchString(s) {
			set[s] = true
		}
	}
	add(src.GOOS)
	add(src.GOARCH)
	if src.GOARCH == "amd64" || src.GOARCH == "386" {
		flags := map[string]bool{}
		for _, fl := range firstFlags(src.CPUInfo) {
			flags[fl] = true
		}
		for _, row := range cpuFacts {
			if row.X86 != "" && flags[row.X86] {
				add(row.Token)
			}
		}
	}
	if src.GOARCH == "arm64" && src.GOOS == "linux" {
		for _, row := range cpuFacts {
			if row.ArmBit < 0 {
				continue
			}
			word := src.HWCap
			if row.ArmHW2 {
				word = src.HWCap2
			}
			if bitSet(word, row.ArmBit) {
				add(row.Token)
			}
		}
	}
	if src.GOARCH == "arm64" && src.GOOS == "darwin" {
		for _, row := range cpuFacts {
			if row.Darwin != "" && sysctlOn(src.Sysctl, row.Darwin) {
				add(row.Token)
			}
		}
	}
	if src.Stat != nil && src.Stat("/opt/tla/tla2tools.jar") {
		add("tla")
	}
	if src.Provisioned != nil && src.Provisioned() {
		add("xdp")
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func bitSet(word uint, bit int) bool {
	if bit < 0 || bit >= 64 {
		return false
	}
	return word&(uint(1)<<uint(bit)) != 0
}

func sysctlOn(sysctl func(string) (int64, bool), name string) bool {
	if sysctl == nil {
		return false
	}
	v, ok := sysctl(name)
	return ok && v != 0
}

func firstFlags(cpuinfo string) []string {
	for _, line := range strings.Split(cpuinfo, "\n") {
		name, rest, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(name) != "flags" {
			continue
		}
		return strings.Fields(rest)
	}
	return nil
}

// RealSources reads this process's own machine. Called once at member start,
// never per beat (docs/SPEC-SWARM.md, the member's beat).
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
