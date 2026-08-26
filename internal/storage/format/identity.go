package format

import (
	"crypto/rand"

	"github.com/bzync/nextsql/internal/nerr"
)

func NewUUID() ([16]byte, error) {
	var u [16]byte
	if _, err := rand.Read(u[:]); err != nil {
		return u, nerr.Wrap(nerr.Internal, "format.NewUUID", "rand", err)
	}
	u[6] = (u[6] & 0x0f) | 0x40
	u[8] = (u[8] & 0x3f) | 0x80
	return u, nil
}

func NewIdentity() (Identity, error) {
	var id Identity
	var err error
	if id.Database, err = NewUUID(); err != nil {
		return Identity{}, err
	}
	if id.File, err = NewUUID(); err != nil {
		return Identity{}, err
	}
	return id, nil
}
