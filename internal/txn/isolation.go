package txn

// Isolation is a transaction isolation level.
type Isolation uint8

const (
	ReadCommitted Isolation = iota + 1
	SnapshotIsolation
	Serializable
)

func (i Isolation) String() string {
	switch i {
	case ReadCommitted:
		return "read committed"
	case SnapshotIsolation:
		return "snapshot"
	case Serializable:
		return "serializable"
	default:
		return "invalid"
	}
}

// Status is the lifecycle of a transaction id.
type Status uint8

const (
	StatusUnknown Status = iota
	StatusInProgress
	StatusCommitted
	StatusAborted
)
