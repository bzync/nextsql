package catalog

import (
	"bytes"

	"github.com/bzync/nextsql/internal/nerr"
)

const (
	resourceGroupMagic        = "NSRG"
	resourceGroupVersion      = 1
	KeyResourceGroup     byte = 'U'

	// MaxResourceGroupConcurrency/Workers/Priority bound the stored fields
	// against nonsense input; 0 means "unbounded"/"unset" for Concurrency
	// and MemoryBytes, matching the zero-means-uncapped convention used by
	// protocol.Limits.MaxSessionsPerUser and hosting storage caps.
	MaxResourceGroupConcurrency = 1_000_000
	MaxResourceGroupMemoryBytes = int64(1) << 48
	MaxResourceGroupWorkers     = 128
	MaxResourceGroupPriority    = 9
)

// ResourceGroup is the durable, versioned workload-governance descriptor
// (CREATE/ALTER/DROP RESOURCE GROUP). It is a pure config record: creating
// one does not yet change how any query is scheduled (see
// docs/sql.md "RESOURCE GROUP" for what is and is not wired up).
type ResourceGroup struct {
	ID             uint32
	Name           string
	Owner          string
	MaxConcurrency int32 // 0 = unbounded
	MemoryBytes    int64 // 0 = unbounded
	Workers        int32 // 0 = unset (session/process default applies)
	Priority       int32 // 0 = normal; higher = more favored (not yet enforced)
}

func (g *ResourceGroup) Clone() *ResourceGroup {
	if g == nil {
		return nil
	}
	raw, err := EncodeResourceGroup(g)
	if err != nil {
		return nil
	}
	out, err := DecodeResourceGroup(raw)
	if err != nil {
		return nil
	}
	return out
}

func ResourceGroupKey(name string) []byte {
	k := make([]byte, 1+len(name))
	k[0] = KeyResourceGroup
	copy(k[1:], name)
	return k
}

func EncodeResourceGroup(g *ResourceGroup) ([]byte, error) {
	if err := validateResourceGroup(g); err != nil {
		return nil, err
	}
	buf := append([]byte(nil), resourceGroupMagic...)
	buf = appendU16(buf, resourceGroupVersion)
	buf = appendU32(buf, g.ID)
	buf = appendString(buf, g.Name)
	buf = appendString(buf, g.Owner)
	buf = appendU32(buf, uint32(g.MaxConcurrency))
	buf = appendU64(buf, uint64(g.MemoryBytes))
	buf = appendU32(buf, uint32(g.Workers))
	buf = appendU32(buf, uint32(g.Priority))
	return buf, nil
}

func DecodeResourceGroup(raw []byte) (*ResourceGroup, error) {
	if len(raw) < len(resourceGroupMagic) || !bytes.Equal(raw[:len(resourceGroupMagic)], []byte(resourceGroupMagic)) {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeResourceGroup", "bad resource group magic")
	}
	off := len(resourceGroupMagic)
	ver, off, err := takeU16(raw, off)
	if err != nil {
		return nil, err
	}
	if ver != resourceGroupVersion {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeResourceGroup", "unsupported resource group version")
	}
	g := &ResourceGroup{}
	g.ID, off, err = takeU32(raw, off)
	if err != nil {
		return nil, err
	}
	g.Name, off, err = takeString(raw, off)
	if err != nil {
		return nil, err
	}
	g.Owner, off, err = takeString(raw, off)
	if err != nil {
		return nil, err
	}
	var u32 uint32
	u32, off, err = takeU32(raw, off)
	if err != nil {
		return nil, err
	}
	g.MaxConcurrency = int32(u32)
	var u64 uint64
	u64, off, err = takeU64(raw, off)
	if err != nil {
		return nil, err
	}
	g.MemoryBytes = int64(u64)
	u32, off, err = takeU32(raw, off)
	if err != nil {
		return nil, err
	}
	g.Workers = int32(u32)
	u32, off, err = takeU32(raw, off)
	if err != nil {
		return nil, err
	}
	g.Priority = int32(u32)
	if off != len(raw) {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeResourceGroup", "trailing resource group bytes")
	}
	if err := validateResourceGroup(g); err != nil {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeResourceGroup", err.Error())
	}
	return g, nil
}

func validateResourceGroup(g *ResourceGroup) error {
	if g == nil {
		return nerr.New(nerr.InvalidArgument, "catalog.EncodeResourceGroup", "nil resource group")
	}
	if g.ID == 0 || g.Name == "" || g.Owner == "" || len(g.Name) > MaxWorkflowNameBytes || len(g.Owner) > MaxWorkflowNameBytes {
		return nerr.New(nerr.InvalidArgument, "catalog.EncodeResourceGroup", "invalid resource group identity")
	}
	if g.MaxConcurrency < 0 || g.MaxConcurrency > MaxResourceGroupConcurrency {
		return nerr.New(nerr.InvalidArgument, "catalog.EncodeResourceGroup", "max_concurrency out of range")
	}
	if g.MemoryBytes < 0 || g.MemoryBytes > MaxResourceGroupMemoryBytes {
		return nerr.New(nerr.InvalidArgument, "catalog.EncodeResourceGroup", "memory out of range")
	}
	if g.Workers < 0 || g.Workers > MaxResourceGroupWorkers {
		return nerr.New(nerr.InvalidArgument, "catalog.EncodeResourceGroup", "workers out of range")
	}
	if g.Priority < 0 || g.Priority > MaxResourceGroupPriority {
		return nerr.New(nerr.InvalidArgument, "catalog.EncodeResourceGroup", "priority out of range")
	}
	return nil
}
