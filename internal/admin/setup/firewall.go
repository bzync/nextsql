package setup

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/bzync/nextsql/internal/nerr"
)

// FirewallStatus is what Setup mode displays when a non-loopback listen
// address is configured, providing explicit assistance and optional rule
// creation for the host firewall (PROJECT.md §46, TODO.md Phase 28).
type FirewallStatus struct {
	Supported   bool   `json:"supported"`
	Elevated    bool   `json:"elevated"`
	Detected    string `json:"detected"`    // "ufw", "firewalld", "nftables", "iptables", "none"
	Active      bool   `json:"active"`      // true if detected firewall is running
	Port        int    `json:"port"`        // target port
	RuleCommand string `json:"ruleCommand"` // command to run when elevated
	SudoCommand string `json:"sudoCommand"` // command to run with sudo when unprivileged
}

// FirewallOutcome reports the outcome of applying a firewall rule post-install.
type FirewallOutcome struct {
	Applied bool   `json:"applied"`
	Tool    string `json:"tool,omitempty"`
	Command string `json:"command,omitempty"`
	Error   string `json:"error,omitempty"`
}

// ParsePort extracts a TCP port number from a host:port or :port listen address.
// If addr is empty or the port cannot be parsed, defaultPort is returned.
func ParsePort(addr string, defaultPort int) int {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return defaultPort
	}
	// Try net.SplitHostPort first
	if _, portStr, err := net.SplitHostPort(addr); err == nil {
		if p, err := strconv.Atoi(portStr); err == nil && p > 0 && p <= 65535 {
			return p
		}
	}
	// Try parsing as bare port integer
	if p, err := strconv.Atoi(addr); err == nil && p > 0 && p <= 65535 {
		return p
	}
	// Try trailing :<port>
	if idx := strings.LastIndexByte(addr, ':'); idx >= 0 {
		if p, err := strconv.Atoi(addr[idx+1:]); err == nil && p > 0 && p <= 65535 {
			return p
		}
	}
	return defaultPort
}

// DetectFirewall inspects the host to determine the available firewall management
// tool (ufw, firewalld, nftables, iptables) on Linux and whether it is active.
func DetectFirewall(ctx context.Context, port int) FirewallStatus {
	if port <= 0 || port > 65535 {
		port = 7210
	}
	st := FirewallStatus{
		Port:     port,
		Detected: "none",
	}
	if runtime.GOOS != "linux" {
		return st
	}
	st.Supported = true
	st.Elevated = (os.Geteuid() == 0)

	// Check ufw first (standard on Debian/Ubuntu)
	if ufwPath, err := exec.LookPath("ufw"); err == nil && ufwPath != "" {
		st.Detected = "ufw"
		st.RuleCommand = fmt.Sprintf("ufw allow %d/tcp comment 'NextSQL database'", port)
		st.SudoCommand = fmt.Sprintf("sudo ufw allow %d/tcp comment 'NextSQL database'", port)

		out, err := exec.CommandContext(ctx, "ufw", "status").CombinedOutput()
		if err == nil && strings.Contains(strings.ToLower(string(out)), "status: active") {
			st.Active = true
		}
		return st
	}

	// Check firewall-cmd next (standard on RHEL/Fedora/CentOS)
	if fwcPath, err := exec.LookPath("firewall-cmd"); err == nil && fwcPath != "" {
		st.Detected = "firewalld"
		st.RuleCommand = fmt.Sprintf("firewall-cmd --permanent --add-port=%d/tcp && firewall-cmd --reload", port)
		st.SudoCommand = fmt.Sprintf("sudo firewall-cmd --permanent --add-port=%d/tcp && sudo firewall-cmd --reload", port)

		out, err := exec.CommandContext(ctx, "firewall-cmd", "--state").CombinedOutput()
		if err == nil && strings.TrimSpace(string(out)) == "running" {
			st.Active = true
		}
		return st
	}

	// Check nftables
	if nftPath, err := exec.LookPath("nft"); err == nil && nftPath != "" {
		st.Detected = "nftables"
		st.RuleCommand = fmt.Sprintf("nft add rule inet filter input tcp dport %d accept", port)
		st.SudoCommand = fmt.Sprintf("sudo nft add rule inet filter input tcp dport %d accept", port)
		st.Active = true
		return st
	}

	// Check iptables
	if ipPath, err := exec.LookPath("iptables"); err == nil && ipPath != "" {
		st.Detected = "iptables"
		st.RuleCommand = fmt.Sprintf("iptables -A INPUT -p tcp --dport %d -j ACCEPT", port)
		st.SudoCommand = fmt.Sprintf("sudo iptables -A INPUT -p tcp --dport %d -j ACCEPT", port)
		st.Active = true
		return st
	}

	return st
}

// ApplyFirewall executes the firewall configuration command for the specified
// firewall tool when running with elevated privileges (root).
func ApplyFirewall(ctx context.Context, tool string, port int) (string, error) {
	if port <= 0 || port > 65535 {
		return "", nerr.New(nerr.InvalidArgument, "setup.ApplyFirewall", fmt.Sprintf("invalid port %d", port))
	}
	if runtime.GOOS != "linux" {
		return "", nerr.New(nerr.InvalidArgument, "setup.ApplyFirewall", "firewall management is supported on Linux only")
	}
	if os.Geteuid() != 0 {
		return "", nerr.New(nerr.Forbidden, "setup.ApplyFirewall", "root privileges required to apply firewall rules")
	}

	switch tool {
	case "ufw":
		portSpec := fmt.Sprintf("%d/tcp", port)
		out, err := exec.CommandContext(ctx, "ufw", "allow", portSpec, "comment", "NextSQL database").CombinedOutput()
		cmdStr := fmt.Sprintf("ufw allow %s comment 'NextSQL database'", portSpec)
		if err != nil {
			msg := strings.TrimSpace(string(out))
			if msg == "" {
				msg = err.Error()
			}
			return cmdStr, nerr.Wrap(nerr.IO, "setup.ApplyFirewall", cmdStr, errors.New(msg))
		}
		return cmdStr, nil

	case "firewalld":
		portSpec := fmt.Sprintf("--add-port=%d/tcp", port)
		out, err := exec.CommandContext(ctx, "firewall-cmd", "--permanent", portSpec).CombinedOutput()
		cmdStr := fmt.Sprintf("firewall-cmd --permanent %s && firewall-cmd --reload", portSpec)
		if err != nil {
			msg := strings.TrimSpace(string(out))
			if msg == "" {
				msg = err.Error()
			}
			return cmdStr, nerr.Wrap(nerr.IO, "setup.ApplyFirewall", cmdStr, errors.New(msg))
		}
		if outReload, err := exec.CommandContext(ctx, "firewall-cmd", "--reload").CombinedOutput(); err != nil {
			msg := strings.TrimSpace(string(outReload))
			if msg == "" {
				msg = err.Error()
			}
			return cmdStr, nerr.Wrap(nerr.IO, "setup.ApplyFirewall", cmdStr, errors.New(msg))
		}
		return cmdStr, nil

	case "nftables":
		rule := fmt.Sprintf("add rule inet filter input tcp dport %d accept", port)
		out, err := exec.CommandContext(ctx, "nft", strings.Fields(rule)...).CombinedOutput()
		cmdStr := fmt.Sprintf("nft %s", rule)
		if err != nil {
			msg := strings.TrimSpace(string(out))
			if msg == "" {
				msg = err.Error()
			}
			return cmdStr, nerr.Wrap(nerr.IO, "setup.ApplyFirewall", cmdStr, errors.New(msg))
		}
		return cmdStr, nil

	case "iptables":
		portStr := strconv.Itoa(port)
		out, err := exec.CommandContext(ctx, "iptables", "-A", "INPUT", "-p", "tcp", "--dport", portStr, "-j", "ACCEPT").CombinedOutput()
		cmdStr := fmt.Sprintf("iptables -A INPUT -p tcp --dport %s -j ACCEPT", portStr)
		if err != nil {
			msg := strings.TrimSpace(string(out))
			if msg == "" {
				msg = err.Error()
			}
			return cmdStr, nerr.Wrap(nerr.IO, "setup.ApplyFirewall", cmdStr, errors.New(msg))
		}
		return cmdStr, nil

	default:
		return "", nerr.New(nerr.InvalidArgument, "setup.ApplyFirewall", fmt.Sprintf("unknown firewall tool %q", tool))
	}
}
