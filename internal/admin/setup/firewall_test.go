package setup

import (
	"context"
	"runtime"
	"testing"
)

func TestParsePort(t *testing.T) {
	cases := []struct {
		addr string
		want int
	}{
		{"127.0.0.1:7210", 7210},
		{"0.0.0.0:8000", 8000},
		{":9090", 9090},
		{"7210", 7210},
		{"[::1]:7210", 7210},
		{"localhost:7210", 7210},
		{"", 7210},
		{"invalid", 7210},
		{"127.0.0.1:99999", 7210}, // out of range
		{"127.0.0.1:0", 7210},     // 0 out of range
	}
	for _, c := range cases {
		got := ParsePort(c.addr, 7210)
		if got != c.want {
			t.Errorf("ParsePort(%q, 7210) = %d, want %d", c.addr, got, c.want)
		}
	}
}

func TestDetectFirewallStructure(t *testing.T) {
	ctx := context.Background()
	st := DetectFirewall(ctx, 7210)
	if runtime.GOOS == "linux" {
		if !st.Supported {
			t.Errorf("expected Supported=true on Linux")
		}
		if st.Port != 7210 {
			t.Errorf("got Port=%d, want 7210", st.Port)
		}
		if st.Detected != "none" {
			if st.RuleCommand == "" || st.SudoCommand == "" {
				t.Errorf("expected non-empty RuleCommand and SudoCommand for detected=%s", st.Detected)
			}
		}
	} else {
		if st.Supported {
			t.Errorf("expected Supported=false on non-Linux")
		}
	}
}

func TestApplyFirewallNonElevatedFails(t *testing.T) {
	// Running as non-root should fail with permission denied (or not supported on non-linux)
	ctx := context.Background()
	_, err := ApplyFirewall(ctx, "ufw", 7210)
	if runtime.GOOS == "linux" {
		// Non-root in this environment
		if err == nil {
			t.Errorf("expected non-nil error when applying firewall as non-root")
		}
	}
}
