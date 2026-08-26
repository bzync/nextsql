package security

import "sync"

// Registry tracks live sessions so credential or key revocation can disconnect them.
type Registry struct {
	mu   sync.Mutex
	next uint64
	subs map[string]map[uint64]func()
}

func NewRegistry() *Registry {
	return &Registry{subs: make(map[string]map[uint64]func())}
}

// Register adds cancel under user. The returned function unregisters it.
func (r *Registry) Register(user string, cancel func()) (unregister func()) {
	if r == nil || cancel == nil {
		return func() {}
	}
	r.mu.Lock()
	r.next++
	id := r.next
	if r.subs[user] == nil {
		r.subs[user] = make(map[uint64]func())
	}
	r.subs[user][id] = cancel
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if hooks := r.subs[user]; hooks != nil {
			delete(hooks, id)
			if len(hooks) == 0 {
				delete(r.subs, user)
			}
		}
	}
}

// Terminate invokes every cancel registered for user.
func (r *Registry) Terminate(user string) int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	hooks := r.subs[user]
	delete(r.subs, user)
	r.mu.Unlock()
	n := 0
	for _, fn := range hooks {
		fn()
		n++
	}
	return n
}

// TerminateAll disconnects every tracked session (key-version revocation).
func (r *Registry) TerminateAll() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	var all []func()
	for _, hooks := range r.subs {
		for _, fn := range hooks {
			all = append(all, fn)
		}
	}
	r.subs = make(map[string]map[uint64]func())
	r.mu.Unlock()
	for _, fn := range all {
		fn()
	}
	return len(all)
}
