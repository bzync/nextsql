package crypto

import (
	"sync"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
)

// KeyProvider supplies DEKs by version. Implementations must not log key material.
type KeyProvider interface {
	Current() (*DEK, error)
	Key(version format.KeyVersion) (*DEK, error)
}

// MemoryKeyProvider is an in-process keyring used by tests and the Phase 1 CLI key file.
type MemoryKeyProvider struct {
	mu      sync.RWMutex
	current format.KeyVersion
	keys    map[format.KeyVersion]*DEK
}

func NewMemoryKeyProvider(deks ...*DEK) (*MemoryKeyProvider, error) {
	if len(deks) == 0 {
		return nil, nerr.New(nerr.InvalidArgument, "crypto.NewMemoryKeyProvider", "at least one DEK is required")
	}
	p := &MemoryKeyProvider{keys: make(map[format.KeyVersion]*DEK, len(deks))}
	for i, d := range deks {
		if d == nil {
			return nil, nerr.New(nerr.InvalidArgument, "crypto.NewMemoryKeyProvider", "nil DEK")
		}
		if _, exists := p.keys[d.Version]; exists {
			return nil, nerr.New(nerr.InvalidArgument, "crypto.NewMemoryKeyProvider", "duplicate key version")
		}
		p.keys[d.Version] = d
		if i == len(deks)-1 {
			p.current = d.Version
		}
	}
	return p, nil
}

func (p *MemoryKeyProvider) Current() (*DEK, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	d, ok := p.keys[p.current]
	if !ok {
		return nil, nerr.New(nerr.NotFound, "crypto.MemoryKeyProvider.Current", "current key missing")
	}
	return d, nil
}

func (p *MemoryKeyProvider) Key(version format.KeyVersion) (*DEK, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	d, ok := p.keys[version]
	if !ok {
		return nil, nerr.New(nerr.Crypto, "crypto.MemoryKeyProvider.Key", "unknown key version")
	}
	return d, nil
}

func (p *MemoryKeyProvider) Add(d *DEK) error {
	if d == nil {
		return nerr.New(nerr.InvalidArgument, "crypto.MemoryKeyProvider.Add", "nil DEK")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.keys[d.Version]; exists {
		return nerr.New(nerr.AlreadyExists, "crypto.MemoryKeyProvider.Add", "key version exists")
	}
	p.keys[d.Version] = d
	p.current = d.Version
	return nil
}
