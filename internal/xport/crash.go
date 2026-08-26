package xport

import (
	"errors"
	"sync"

	"github.com/bzync/nextsql/internal/nerr"
)

// Point is a crash-injection site on the export path.
type Point int

const (
	PointNone Point = iota
	PointBeforeWrite
	PointDuringWrite
	PointBeforeVerify
)

// ErrCrash is returned when an armed crash point fires. Tests treat it as
// process death; the destination must not be published.
var ErrCrash = nerr.New(nerr.Unavailable, "xport.crash", "injected crash")

func IsCrash(err error) bool {
	return errors.Is(err, ErrCrash)
}

// Injector fires at most once at an armed point.
type Injector struct {
	mu    sync.Mutex
	point Point
	fired bool
}

func NewInjector() *Injector { return &Injector{} }

func (i *Injector) Arm(p Point) {
	if i == nil {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.point = p
	i.fired = false
}

func (i *Injector) Hit(p Point) error {
	if i == nil || p == PointNone {
		return nil
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.point == p && !i.fired {
		i.fired = true
		return ErrCrash
	}
	return nil
}

func hit(inj *Injector, p Point) error {
	if inj == nil {
		return nil
	}
	return inj.Hit(p)
}
