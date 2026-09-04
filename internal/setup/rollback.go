package setup

// This file is the decision logic for transactional rollback of a failed
// `nextsql setup` run (P28 "Transactional rollback of safe installer
// changes" / "Never delete existing user data/keys on failed install"). Like
// the rest of the package it performs no I/O: the command layer stats each
// path before touching it and records the observation here, tracks each path
// it creates, and on failure asks for the ordered removal list.

// InstallRollback records what a setup run may need to undo. A path observed
// to already exist before the run started is never scheduled for removal —
// that is the "never delete existing user data/keys" guarantee. Removal is
// offered in reverse creation order so directory contents come out before
// the directory.
type InstallRollback struct {
	preexisting map[string]bool
	order       []string
	tracked     map[string]bool
}

// NewInstallRollback returns an empty tracker.
func NewInstallRollback() *InstallRollback {
	return &InstallRollback{
		preexisting: make(map[string]bool),
		tracked:     make(map[string]bool),
	}
}

// Observe records whether path already existed on disk before the run began.
// Call it for every path the run might create, before creating anything.
// Observing the same path more than once keeps the first (earliest) reading.
func (r *InstallRollback) Observe(path string, existed bool) {
	if path == "" {
		return
	}
	if _, seen := r.preexisting[path]; !seen {
		r.preexisting[path] = existed
	}
}

// Preexisting reports whether path was observed to exist before the run.
func (r *InstallRollback) Preexisting(path string) bool { return r.preexisting[path] }

// Track schedules path for removal on rollback. It is a no-op if the path was
// observed to already exist, or is already tracked. A path that was never
// Observe'd is treated as new (not preexisting) and is tracked.
func (r *InstallRollback) Track(path string) {
	if path == "" || r.preexisting[path] || r.tracked[path] {
		return
	}
	r.tracked[path] = true
	r.order = append(r.order, path)
}

// Plan returns the paths to remove, in reverse of the order they were
// tracked (last created is removed first). Preexisting paths are already
// excluded by Track. The command layer executes this list.
func (r *InstallRollback) Plan() []string {
	out := make([]string, 0, len(r.order))
	for i := len(r.order) - 1; i >= 0; i-- {
		out = append(out, r.order[i])
	}
	return out
}

// Empty reports whether there is nothing to roll back.
func (r *InstallRollback) Empty() bool { return len(r.order) == 0 }
