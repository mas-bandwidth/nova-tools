package sprint

import (
	"net"
	"os"
	"testing"
)

func TestLocalOnlyMode(t *testing.T) {
	// Test default (no env)
	os.Unsetenv("NOVA_SPRINT_LOCAL")
	if LocalOnlyMode() {
		t.Error("LocalOnlyMode() should be false when NOVA_SPRINT_LOCAL is not set")
	}

	// Test with NOVA_SPRINT_LOCAL=1
	os.Setenv("NOVA_SPRINT_LOCAL", "1")
	if !LocalOnlyMode() {
		t.Error("LocalOnlyMode() should be true when NOVA_SPRINT_LOCAL=1")
	}

	// Test with NOVA_SPRINT_LOCAL=0
	os.Setenv("NOVA_SPRINT_LOCAL", "0")
	if LocalOnlyMode() {
		t.Error("LocalOnlyMode() should be false when NOVA_SPRINT_LOCAL=0")
	}

	// Test with NOVA_SPRINT_LOCAL= (empty)
	os.Setenv("NOVA_SPRINT_LOCAL", "")
	if LocalOnlyMode() {
		t.Error("LocalOnlyMode() should be false when NOVA_SPRINT_LOCAL is empty")
	}
}

func TestAddrOK(t *testing.T) {
	tests := []struct {
		name string
		addr string
		want string
	}{
		{"loopback", "127.0.0.1:7395", ""},
		{"loopback2", "127.0.0.1:0", ""},
		{"private", "10.0.0.1:7395", ""},
		{"private2", "192.168.1.1:7395", ""},
		{"private3", "172.16.0.1:7395", ""},
		{"tailnet", "100.64.0.1:7395", ""},
		{"tailnet2", "100.127.255.255:7395", ""},
		{"public", "8.8.8.8:7395", "address is neither loopback nor private nor tailnet: 8.8.8.8:7395"},
		{"link-local", "169.254.1.1:7395", "address is neither loopback nor private nor tailnet: 169.254.1.1:7395"},
		{"unspecified", "0.0.0.0:7395", "address is neither loopback nor private nor tailnet: 0.0.0.0:7395"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Unsetenv("NOVA_SPRINT_LOCAL")
			if got := AddrOK(tt.addr); got != tt.want {
				t.Errorf("AddrOK(%q) = %q, want %q", tt.addr, got, tt.want)
			}
		})
	}
}

func TestAddrOKLocalOnly(t *testing.T) {
	os.Setenv("NOVA_SPRINT_LOCAL", "1")
	defer os.Unsetenv("NOVA_SPRINT_LOCAL")

	tests := []struct {
		name string
		addr string
		want string
	}{
		{"loopback ok", "127.0.0.1:7395", ""},
		{"private refused", "10.0.0.1:7395", "local-only mode allows only loopback; 10.0.0.1:7395 is not loopback"},
		{"tailnet refused", "100.64.0.1:7395", "local-only mode allows only loopback; 100.64.0.1:7395 is not loopback"},
		{"public refused", "8.8.8.8:7395", "local-only mode allows only loopback; 8.8.8.8:7395 is not loopback"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AddrOK(tt.addr); got != tt.want {
				t.Errorf("AddrOK(%q) = %q, want %q", tt.addr, got, tt.want)
			}
		})
	}
}

func TestIsLoopback(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		want bool
	}{
		{"loopback", "127.0.0.1", true},
		{"not loopback", "10.0.0.1", false},
		{"nil", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if got := isLoopback(ip.String()); got != tt.want {
				t.Errorf("isLoopback(%q) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

func TestIsPrivateOrTailnet(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		want bool
	}{
		{"private", "10.0.0.1", true},
		{"private2", "192.168.1.1", true},
		{"tailnet", "100.64.0.1", true},
		{"loopback", "127.0.0.1", false},
		{"public", "8.8.8.8", false},
		{"nil", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if got := isPrivateOrTailnet(ip.String()); got != tt.want {
				t.Errorf("isPrivateOrTailnet(%q) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}
