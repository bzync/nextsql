package catalog

import (
	"bytes"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/ast"
)

const (
	triggerMagic        = "NSTG"
	triggerVersion      = 1
	KeyTrigger     byte = 'G'

	MaxTriggerArgs       = MaxWorkflowParams
	MaxTriggerDescriptor = security.MaxSQLBytes
)

// Trigger is a durable row-trigger descriptor. TableID and WorkflowID are
// stable dependency identities; names are retained for diagnostics and lookup.
type Trigger struct {
	ID         uint32
	Name       string
	Owner      string
	Timing     ast.TriggerTiming
	Event      ast.TriggerEvent
	TableID    uint32
	Table      string
	WorkflowID uint32
	Workflow   string
	Args       []ast.Expr
}

func (t *Trigger) Clone() *Trigger {
	if t == nil {
		return nil
	}
	raw, err := EncodeTrigger(t)
	if err != nil {
		return nil
	}
	out, err := DecodeTrigger(raw)
	if err != nil {
		return nil
	}
	return out
}

func TriggerKey(name string) []byte {
	k := make([]byte, 1+len(name))
	k[0] = KeyTrigger
	copy(k[1:], name)
	return k
}

func EncodeTrigger(t *Trigger) ([]byte, error) {
	if err := validateTrigger(t); err != nil {
		return nil, err
	}
	buf := append([]byte(nil), triggerMagic...)
	buf = appendU16(buf, triggerVersion)
	buf = appendU32(buf, t.ID)
	buf = appendString(buf, t.Name)
	buf = appendString(buf, t.Owner)
	buf = append(buf, byte(t.Timing), byte(t.Event))
	buf = appendU32(buf, t.TableID)
	buf = appendString(buf, t.Table)
	buf = appendU32(buf, t.WorkflowID)
	buf = appendString(buf, t.Workflow)
	buf = appendU16(buf, uint16(len(t.Args)))
	for _, arg := range t.Args {
		var err error
		buf, err = appendExpr(buf, arg)
		if err != nil {
			return nil, err
		}
		if len(buf) > MaxTriggerDescriptor {
			return nil, nerr.New(nerr.InvalidArgument, "catalog.EncodeTrigger", "trigger descriptor exceeds size limit")
		}
	}
	return buf, nil
}

func DecodeTrigger(raw []byte) (*Trigger, error) {
	if len(raw) > MaxTriggerDescriptor || len(raw) < len(triggerMagic) || !bytes.Equal(raw[:len(triggerMagic)], []byte(triggerMagic)) {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTrigger", "invalid trigger descriptor")
	}
	off := len(triggerMagic)
	version, off, err := takeU16(raw, off)
	if err != nil {
		return nil, err
	}
	if version != triggerVersion {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTrigger", "unsupported trigger version")
	}
	t := &Trigger{}
	t.ID, off, err = takeU32(raw, off)
	if err != nil {
		return nil, err
	}
	t.Name, off, err = takeString(raw, off)
	if err != nil {
		return nil, err
	}
	t.Owner, off, err = takeString(raw, off)
	if err != nil {
		return nil, err
	}
	if off+2 > len(raw) {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTrigger", "truncated trigger event")
	}
	t.Timing, t.Event = ast.TriggerTiming(raw[off]), ast.TriggerEvent(raw[off+1])
	off += 2
	t.TableID, off, err = takeU32(raw, off)
	if err != nil {
		return nil, err
	}
	t.Table, off, err = takeString(raw, off)
	if err != nil {
		return nil, err
	}
	t.WorkflowID, off, err = takeU32(raw, off)
	if err != nil {
		return nil, err
	}
	t.Workflow, off, err = takeString(raw, off)
	if err != nil {
		return nil, err
	}
	n, off, err := takeU16(raw, off)
	if err != nil {
		return nil, err
	}
	if n > MaxTriggerArgs {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTrigger", "trigger argument count exceeds limit")
	}
	for i := 0; i < int(n); i++ {
		arg, next, err := takeExpr(raw, off)
		if err != nil {
			return nil, err
		}
		t.Args = append(t.Args, arg)
		off = next
	}
	if off != len(raw) {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTrigger", "trailing trigger bytes")
	}
	if err := validateTrigger(t); err != nil {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTrigger", err.Error())
	}
	return t, nil
}

func validateTrigger(t *Trigger) error {
	if t == nil || t.ID == 0 || t.TableID == 0 || t.WorkflowID == 0 || t.Name == "" || t.Owner == "" || t.Table == "" || t.Workflow == "" {
		return nerr.New(nerr.InvalidArgument, "catalog.EncodeTrigger", "invalid trigger identity")
	}
	for _, name := range []string{t.Name, t.Owner, t.Table, t.Workflow} {
		if len(name) > MaxWorkflowNameBytes {
			return nerr.New(nerr.InvalidArgument, "catalog.EncodeTrigger", "trigger name exceeds limit")
		}
	}
	if t.Timing != ast.TriggerBefore && t.Timing != ast.TriggerAfter {
		return nerr.New(nerr.InvalidArgument, "catalog.EncodeTrigger", "invalid trigger timing")
	}
	if t.Event != ast.TriggerInsert && t.Event != ast.TriggerUpdate && t.Event != ast.TriggerDelete {
		return nerr.New(nerr.InvalidArgument, "catalog.EncodeTrigger", "invalid trigger event")
	}
	if len(t.Args) > MaxTriggerArgs {
		return nerr.New(nerr.InvalidArgument, "catalog.EncodeTrigger", "trigger argument count exceeds limit")
	}
	for _, arg := range t.Args {
		if arg == nil {
			return nerr.New(nerr.InvalidArgument, "catalog.EncodeTrigger", "nil trigger argument")
		}
		if err := validateCatalogTriggerExpr(arg, t.Event); err != nil {
			return err
		}
	}
	return nil
}

func validateCatalogTriggerExpr(expr ast.Expr, event ast.TriggerEvent) error {
	if expr == nil {
		return nil
	}
	switch x := expr.(type) {
	case ast.Literal:
		return nil
	case ast.Path:
		if len(x.Parts) != 2 || (x.Parts[0] != "old" && x.Parts[0] != "new") || (event == ast.TriggerInsert && x.Parts[0] == "old") || (event == ast.TriggerDelete && x.Parts[0] == "new") {
			return nerr.New(nerr.InvalidArgument, "catalog.EncodeTrigger", "invalid trigger row reference")
		}
		return nil
	case ast.Unary:
		return validateCatalogTriggerExpr(x.Right, event)
	case ast.Binary:
		if err := validateCatalogTriggerExpr(x.Left, event); err != nil {
			return err
		}
		return validateCatalogTriggerExpr(x.Right, event)
	case ast.Between:
		for _, item := range []ast.Expr{x.Expr, x.Low, x.High} {
			if err := validateCatalogTriggerExpr(item, event); err != nil {
				return err
			}
		}
		return nil
	case ast.IsNull:
		return validateCatalogTriggerExpr(x.Expr, event)
	case ast.Case:
		if err := validateCatalogTriggerExpr(x.Operand, event); err != nil {
			return err
		}
		for _, arm := range x.Whens {
			if err := validateCatalogTriggerExpr(arm.When, event); err != nil {
				return err
			}
			if err := validateCatalogTriggerExpr(arm.Then, event); err != nil {
				return err
			}
		}
		return validateCatalogTriggerExpr(x.Else, event)
	default:
		return nerr.New(nerr.InvalidArgument, "catalog.EncodeTrigger", "unsupported trigger expression")
	}
}
