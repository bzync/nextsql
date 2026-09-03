package executor

import (
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/txn"
)

func (s *Session) applyDefault(tab *catalog.Table, i int, v types.Value) (types.Value, error) {
	if tab == nil || i < 0 || i >= len(tab.Columns) {
		return types.Value{}, nerr.New(nerr.Internal, "executor.applyDefault", "column out of range")
	}
	if tab.Columns[i].Default.Kind != catalog.DefAI {
		return tab.ApplyDefault(i, v)
	}
	if !v.Null {
		if err := s.bumpAI(tab, i, v); err != nil {
			return types.Value{}, err
		}
		return v, nil
	}
	return s.nextAI(tab, i)
}

func (s *Session) evalInsertValue(ex ast.Expr, tab *catalog.Table, col int, row []types.Value) (types.Value, error) {
	if c, ok := ex.(ast.Call); ok && c.Name == "ai" {
		if col < 0 || col >= len(tab.Columns) || tab.Columns[col].Default.Kind != catalog.DefAI {
			return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.Insert", "AI() is only valid on a DEFAULT AI() column")
		}
		return types.Null(tab.Columns[col].Type), nil
	}
	return s.eval(ex, tab, row)
}

func (s *Session) nextAI(tab *catalog.Table, col int) (types.Value, error) {
	key, typ, err := aiCol(tab, col)
	if err != nil {
		return types.Value{}, err
	}
	if err := s.lockAI(tab.Name, key); err != nil {
		return types.Value{}, err
	}
	probe, err := s.aiProbe()
	if err != nil {
		return types.Value{}, err
	}
	next, err := s.readAINext(tab, col, probe)
	if err != nil {
		return types.Value{}, err
	}
	out, err := coerceAI(next, typ)
	if err != nil {
		return types.Value{}, err
	}
	adv := types.AddDec(next, types.DecimalFromInt64(1))
	if err := s.writeAINext(key, adv, probe); err != nil {
		return types.Value{}, err
	}
	return out, nil
}

func (s *Session) bumpAI(tab *catalog.Table, col int, v types.Value) error {
	if v.Null || v.Typ.Kind != types.KindDecimal || v.Dec.Coef == nil || v.Dec.Coef.Sign() <= 0 {
		return nil
	}
	key, _, err := aiCol(tab, col)
	if err != nil {
		return err
	}
	if err := s.lockAI(tab.Name, key); err != nil {
		return err
	}
	probe, err := s.aiProbe()
	if err != nil {
		return err
	}
	cur, err := s.readAINext(tab, col, probe)
	if err != nil {
		return err
	}
	need := types.AddDec(v.Dec, types.DecimalFromInt64(1))
	if need.Cmp(cur) <= 0 {
		return nil
	}
	return s.writeAINext(key, need, probe)
}

func (s *Session) lockAI(tag string, key []byte) error {
	h, tm, err := s.fkTM()
	if err != nil {
		return err
	}
	return tm.LockKey(h, key, txn.Exclusive, tag)
}

func (s *Session) aiProbe() (txn.Snapshot, error) {
	h, tm, err := s.fkTM()
	if err != nil {
		return txn.Snapshot{}, err
	}
	return tm.Capture(h.ID), nil
}

func (s *Session) readAINext(tab *catalog.Table, col int, probe txn.Snapshot) (types.Decimal, error) {
	one := types.DecimalFromInt64(1)
	if tab == nil || col < 0 || col >= len(tab.Columns) {
		return one, nerr.New(nerr.Internal, "executor.AI", "column out of range")
	}
	ctx := s.x.use(s.db.CatTree)
	raw, err := ctx.LookupAt(catalog.AIKey(tab.ID, tab.Columns[col].Name), probe)
	if err != nil {
		if nerr.HasCode(err, nerr.NotFound) {
			return one, nil
		}
		return types.Decimal{}, err
	}
	seq, err := aiSeqType()
	if err != nil {
		return types.Decimal{}, err
	}
	vals, err := types.DecodeRow(raw, []types.Type{seq})
	if err != nil {
		return types.Decimal{}, err
	}
	if len(vals) != 1 || vals[0].Null || vals[0].Typ.Kind != types.KindDecimal {
		return types.Decimal{}, nerr.New(nerr.InvalidFormat, "executor.AI", "bad sequence value")
	}
	return vals[0].Dec, nil
}

func (s *Session) writeAINext(key []byte, next types.Decimal, probe txn.Snapshot) error {
	seq, err := aiSeqType()
	if err != nil {
		return err
	}
	v, err := types.Coerce(types.DecimalValue(next, types.Type{Kind: types.KindDecimal}), seq)
	if err != nil {
		return nerr.New(nerr.InvalidArgument, "executor.AI", "AI() exceeds DECIMAL precision")
	}
	raw, err := types.EncodeRow([]types.Value{v})
	if err != nil {
		return err
	}
	ctx := s.x.use(s.db.CatTree)
	_, err = ctx.LookupAt(key, probe)
	if err != nil {
		if nerr.HasCode(err, nerr.NotFound) {
			return ctx.InsertAt(key, raw, probe)
		}
		return err
	}
	return ctx.UpdateAt(key, raw, probe)
}

func (s *Session) deleteAIKey(tableID uint32, col string) error {
	if s == nil || s.x == nil {
		return nerr.New(nerr.Internal, "executor.AI", "no active transaction")
	}
	ctx := s.x.use(s.db.CatTree)
	err := ctx.Delete(catalog.AIKey(tableID, col))
	if err != nil && !nerr.HasCode(err, nerr.NotFound) {
		return err
	}
	return nil
}

func (s *Session) dropAIKeys(tab *catalog.Table) error {
	if tab == nil {
		return nil
	}
	for _, c := range tab.Columns {
		if c.Default.Kind != catalog.DefAI {
			continue
		}
		if err := s.deleteAIKey(tab.ID, c.Name); err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) renameAIKey(tab *catalog.Table, oldName, newName string) error {
	if tab == nil || oldName == newName || oldName == "" || newName == "" {
		return nil
	}
	idx, ok := tab.ColIndex(oldName)
	if !ok || tab.Columns[idx].Default.Kind != catalog.DefAI {
		return nil
	}
	oldKey := catalog.AIKey(tab.ID, oldName)
	newKey := catalog.AIKey(tab.ID, newName)
	ctx := s.x.use(s.db.CatTree)
	raw, err := ctx.Lookup(oldKey)
	if err != nil {
		if nerr.HasCode(err, nerr.NotFound) {
			return nil
		}
		return err
	}
	if err := ctx.Delete(oldKey); err != nil {
		return err
	}
	return ctx.Insert(newKey, raw)
}

func aiCol(tab *catalog.Table, col int) ([]byte, types.Type, error) {
	if tab == nil || col < 0 || col >= len(tab.Columns) {
		return nil, types.Type{}, nerr.New(nerr.Internal, "executor.AI", "column out of range")
	}
	c := tab.Columns[col]
	if c.Default.Kind != catalog.DefAI {
		return nil, types.Type{}, nerr.New(nerr.InvalidArgument, "executor.AI", "column is not DEFAULT AI()")
	}
	if c.Type.Kind != types.KindDecimal || c.Type.Scale != 0 {
		return nil, types.Type{}, nerr.New(nerr.InvalidArgument, "executor.AI", "AI() requires DECIMAL(p,0)")
	}
	return catalog.AIKey(tab.ID, c.Name), c.Type, nil
}

func coerceAI(d types.Decimal, typ types.Type) (types.Value, error) {
	v, err := types.Coerce(types.DecimalValue(d, types.Type{Kind: types.KindDecimal}), typ)
	if err != nil {
		return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.AI", "AI() exceeds DECIMAL precision")
	}
	return v, nil
}

func aiSeqType() (types.Type, error) {
	return types.DecimalType(38, 0)
}
