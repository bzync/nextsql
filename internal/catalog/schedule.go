package catalog

import (
	"bytes"
	"encoding/binary"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/ast"
)

const (
	scheduleMagic        = "NSSC"
	scheduleVersion      = 1
	KeySchedule     byte = 'Q'
	KeyScheduleDue  byte = 'R'

	MaxScheduleArgs       = MaxWorkflowParams
	MaxScheduleDescriptor = security.MaxSQLBytes
)

// Schedule is a durable invocation definition. SpecNS is an interval for
// ScheduleEvery and an absolute Unix nanosecond timestamp for ScheduleAt.
// WorkflowID is the stable dependency identity; Workflow is diagnostic.
type Schedule struct {
	ID         uint32
	Name       string
	Owner      string
	Tenant     string
	Kind       ast.ScheduleKind
	SpecNS     int64
	WorkflowID uint32
	Workflow   string
	Args       []ast.Expr
	CreatedNS  int64
	NextFireNS int64
	LastFireNS int64
	Enabled    bool
}

func (s *Schedule) Clone() *Schedule {
	if s == nil {
		return nil
	}
	raw, err := EncodeSchedule(s)
	if err != nil {
		return nil
	}
	out, err := DecodeSchedule(raw)
	if err != nil {
		return nil
	}
	return out
}

func ScheduleKey(name string) []byte {
	k := make([]byte, 1+len(name))
	k[0] = KeySchedule
	copy(k[1:], name)
	return k
}

func ScheduleDueKey(nextNS int64, id uint32) []byte {
	k := make([]byte, 13)
	k[0] = KeyScheduleDue
	binary.BigEndian.PutUint64(k[1:9], uint64(nextNS))
	binary.BigEndian.PutUint32(k[9:13], id)
	return k
}

func ScheduleDueRangeEnd(nowNS int64) []byte {
	if nowNS == int64(^uint64(0)>>1) {
		return []byte{KeyScheduleDue + 1}
	}
	return ScheduleDueKey(nowNS+1, 0)
}

func ParseScheduleDueKey(key []byte) (int64, uint32, error) {
	if len(key) != 13 || key[0] != KeyScheduleDue {
		return 0, 0, nerr.New(nerr.InvalidFormat, "catalog.ParseScheduleDueKey", "invalid schedule due key")
	}
	next := binary.BigEndian.Uint64(key[1:9])
	id := binary.BigEndian.Uint32(key[9:13])
	if next > uint64(^uint64(0)>>1) || id == 0 {
		return 0, 0, nerr.New(nerr.InvalidFormat, "catalog.ParseScheduleDueKey", "invalid schedule due key")
	}
	return int64(next), id, nil
}

func EncodeSchedule(s *Schedule) ([]byte, error) {
	if err := validateSchedule(s); err != nil {
		return nil, err
	}
	buf := append([]byte(nil), scheduleMagic...)
	buf = appendU16(buf, scheduleVersion)
	buf = appendU32(buf, s.ID)
	buf = appendString(buf, s.Name)
	buf = appendString(buf, s.Owner)
	buf = appendString(buf, s.Tenant)
	buf = append(buf, byte(s.Kind))
	buf = appendU64(buf, uint64(s.SpecNS))
	buf = appendU32(buf, s.WorkflowID)
	buf = appendString(buf, s.Workflow)
	buf = appendU16(buf, uint16(len(s.Args)))
	for _, arg := range s.Args {
		var err error
		buf, err = appendExpr(buf, arg)
		if err != nil {
			return nil, err
		}
		if len(buf) > MaxScheduleDescriptor {
			return nil, nerr.New(nerr.InvalidArgument, "catalog.EncodeSchedule", "schedule descriptor exceeds size limit")
		}
	}
	for _, value := range []int64{s.CreatedNS, s.NextFireNS, s.LastFireNS} {
		buf = appendU64(buf, uint64(value))
	}
	if s.Enabled {
		buf = append(buf, 1)
	} else {
		buf = append(buf, 0)
	}
	return buf, nil
}

func DecodeSchedule(raw []byte) (*Schedule, error) {
	if len(raw) > MaxScheduleDescriptor || len(raw) < len(scheduleMagic) || !bytes.Equal(raw[:len(scheduleMagic)], []byte(scheduleMagic)) {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeSchedule", "invalid schedule descriptor")
	}
	off := len(scheduleMagic)
	version, off, err := takeU16(raw, off)
	if err != nil || version != scheduleVersion {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeSchedule", "unsupported schedule version")
	}
	s := &Schedule{}
	s.ID, off, err = takeU32(raw, off)
	if err != nil {
		return nil, err
	}
	s.Name, off, err = takeString(raw, off)
	if err != nil {
		return nil, err
	}
	s.Owner, off, err = takeString(raw, off)
	if err != nil {
		return nil, err
	}
	s.Tenant, off, err = takeString(raw, off)
	if err != nil {
		return nil, err
	}
	if off >= len(raw) {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeSchedule", "truncated schedule kind")
	}
	s.Kind = ast.ScheduleKind(raw[off])
	off++
	var spec uint64
	spec, off, err = takeU64(raw, off)
	if err != nil {
		return nil, err
	}
	s.SpecNS = int64(spec)
	s.WorkflowID, off, err = takeU32(raw, off)
	if err != nil {
		return nil, err
	}
	s.Workflow, off, err = takeString(raw, off)
	if err != nil {
		return nil, err
	}
	var n uint16
	n, off, err = takeU16(raw, off)
	if err != nil || n > MaxScheduleArgs {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeSchedule", "invalid schedule argument count")
	}
	for i := 0; i < int(n); i++ {
		arg, next, err := takeExpr(raw, off)
		if err != nil {
			return nil, err
		}
		s.Args = append(s.Args, arg)
		off = next
	}
	fields := []*int64{&s.CreatedNS, &s.NextFireNS, &s.LastFireNS}
	for _, field := range fields {
		var value uint64
		value, off, err = takeU64(raw, off)
		if err != nil {
			return nil, err
		}
		*field = int64(value)
	}
	if off >= len(raw) || raw[off] > 1 {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeSchedule", "invalid schedule enabled flag")
	}
	s.Enabled = raw[off] == 1
	off++
	if off != len(raw) {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeSchedule", "trailing schedule bytes")
	}
	if err := validateSchedule(s); err != nil {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeSchedule", err.Error())
	}
	return s, nil
}

func validateSchedule(s *Schedule) error {
	if s == nil || s.ID == 0 || s.Name == "" || s.Owner == "" || s.WorkflowID == 0 || s.Workflow == "" || s.SpecNS <= 0 {
		return nerr.New(nerr.InvalidArgument, "catalog.EncodeSchedule", "invalid schedule identity")
	}
	for _, value := range []string{s.Name, s.Owner, s.Tenant, s.Workflow} {
		if len(value) > MaxWorkflowNameBytes {
			return nerr.New(nerr.InvalidArgument, "catalog.EncodeSchedule", "schedule text exceeds limit")
		}
	}
	if s.Kind != ast.ScheduleEvery && s.Kind != ast.ScheduleAt {
		return nerr.New(nerr.InvalidArgument, "catalog.EncodeSchedule", "invalid schedule kind")
	}
	if len(s.Args) > MaxScheduleArgs {
		return nerr.New(nerr.InvalidArgument, "catalog.EncodeSchedule", "schedule argument count exceeds limit")
	}
	if s.CreatedNS <= 0 || s.LastFireNS < 0 || (s.LastFireNS > 0 && s.LastFireNS < s.CreatedNS) {
		return nerr.New(nerr.InvalidArgument, "catalog.EncodeSchedule", "invalid schedule timestamps")
	}
	if s.Enabled {
		if s.NextFireNS <= 0 || s.NextFireNS < s.CreatedNS {
			return nerr.New(nerr.InvalidArgument, "catalog.EncodeSchedule", "invalid enabled schedule cursor")
		}
	} else if s.NextFireNS != 0 {
		return nerr.New(nerr.InvalidArgument, "catalog.EncodeSchedule", "disabled schedule has next fire time")
	}
	for _, arg := range s.Args {
		if !catalogScheduleLiteral(arg) {
			return nerr.New(nerr.InvalidArgument, "catalog.EncodeSchedule", "schedule arguments must be literals")
		}
	}
	return nil
}

func catalogScheduleLiteral(expr ast.Expr) bool {
	switch x := expr.(type) {
	case ast.Literal:
		return true
	case ast.Unary:
		return catalogScheduleLiteral(x.Right)
	default:
		return false
	}
}
