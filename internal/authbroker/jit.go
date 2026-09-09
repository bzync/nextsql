package authbroker

import (
	"sort"
	"strings"
)

// UserProvisionerFunc creates a native user principal and assigns initial roles
// when JIT provisioning is enabled.
type UserProvisionerFunc func(realm, principal string, roles []string) error

// ProhibitedJITRoles contains administrative roles that can never be JIT-granted
// unless explicitly included in AllowedRoleBoundary.
var defaultProhibitedJITRoles = map[string]bool{
	"admin":         true,
	"cluster_admin": true,
	"superuser":     true,
	"operator":      true,
}

// applyRoleBoundary filters candidate mapped roles against AllowedRoleBoundary
// and default administrative role safeguards to prevent privilege escalation.
func (b *Broker) applyRoleBoundary(mapped []string) []string {
	if len(mapped) == 0 {
		return nil
	}

	boundary := make(map[string]bool, len(b.cfg.AllowedRoleBoundary))
	for _, r := range b.cfg.AllowedRoleBoundary {
		boundary[strings.ToLower(strings.TrimSpace(r))] = true
	}

	hasExplicitBoundary := len(boundary) > 0
	var allowed []string
	seen := make(map[string]bool)

	for _, raw := range mapped {
		r := strings.ToLower(strings.TrimSpace(raw))
		if r == "" || seen[r] {
			continue
		}
		if hasExplicitBoundary {
			if boundary[r] {
				allowed = append(allowed, r)
				seen[r] = true
			}
		} else {
			// No explicit boundary: deny administrative roles by default to prevent escalation.
			if !defaultProhibitedJITRoles[r] {
				allowed = append(allowed, r)
				seen[r] = true
			}
		}
	}

	sort.Strings(allowed)
	return allowed
}
