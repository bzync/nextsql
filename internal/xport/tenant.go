package xport

import (
	"bytes"
	"context"
	"sort"
	"strings"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

const (
	// LegacyTenantDestinationColumn preserves the historical value without
	// leaving the migrated table behind the legacy shared-tenancy admission
	// guard. It is ordinary application data in the isolated destination.
	LegacyTenantDestinationColumn = "legacy_tenant_id"
	defaultTenantMigrationBatch   = 256
	maxTenantMigrationBatch       = 4096
)

// LegacyTenantOptions bounds one offline tenant migration.
type LegacyTenantOptions struct {
	BatchRows int
}

// LegacyTenantResult reports verified logical work. It contains no keys or
// row values.
type LegacyTenantResult struct {
	Tables int
	Rows   uint64
}

type legacyTenantTable struct {
	source    *catalog.Table
	dest      *catalog.Table
	tenantCol int
	tenant    types.Value
	rows      uint64
}

// MigrateLegacyTenant copies one historical row tenant into an empty or
// exactly resumable destination database. The caller must hold exclusive
// offline locks for both deployments and must keep the destination registry
// non-ACTIVE until this function and its durable close/verification complete.
//
// Work is bounded to BatchRows per destination transaction. UPSERT makes a
// retry after a committed batch idempotent; final point verification detects
// missing, extra, or changed rows before activation.
func MigrateLegacyTenant(source, dest *executor.DB, tenant string, opt LegacyTenantOptions) (*LegacyTenantResult, error) {
	if source == nil || source.Eng == nil || dest == nil || dest.Eng == nil {
		return nil, nerr.New(nerr.InvalidArgument, "xport.MigrateLegacyTenant", "source and destination databases are required")
	}
	if source.Eng.Identity() == dest.Eng.Identity() {
		return nil, nerr.New(nerr.InvalidArgument, "xport.MigrateLegacyTenant", "source and destination database identities must differ")
	}
	if tenant == "" {
		return nil, nerr.New(nerr.InvalidArgument, "xport.MigrateLegacyTenant", "tenant is required")
	}
	batchRows := opt.BatchRows
	if batchRows == 0 {
		batchRows = defaultTenantMigrationBatch
	}
	if batchRows < 1 || batchRows > maxTenantMigrationBatch {
		return nil, nerr.New(nerr.InvalidArgument, "xport.MigrateLegacyTenant", "batch rows must be between 1 and 4096")
	}

	tables, admitted, err := prepareLegacyTenantTables(source, tenant)
	if err != nil {
		return nil, err
	}
	if len(tables) == 0 {
		return nil, nerr.New(nerr.NotFound, "xport.MigrateLegacyTenant", "source has no legacy tenant tables")
	}
	if err := validateLegacyTenantForeignKeys(tables); err != nil {
		return nil, err
	}
	if err := preflightLegacyTenantDestination(dest, tables); err != nil {
		return nil, err
	}

	var total uint64
	for i := range tables {
		count, err := countLegacyTenantRows(source, &tables[i])
		if err != nil {
			return nil, err
		}
		tables[i].rows = count
		total += count
	}
	if total == 0 && !admitted {
		return nil, nerr.New(nerr.NotFound, "xport.MigrateLegacyTenant", "tenant is not present in the source database")
	}

	ordered, err := orderLegacyTenantTables(tables)
	if err != nil {
		return nil, err
	}
	parents := make(map[string]*catalog.Table, len(tables))
	for i := range tables {
		parents[tables[i].dest.Name] = tables[i].dest
	}
	for _, table := range ordered {
		if err := ensureLegacyTenantTable(dest, table.dest, parents); err != nil {
			return nil, err
		}
	}

	for _, table := range ordered {
		if err := copyLegacyTenantRows(source, dest, table, batchRows); err != nil {
			return nil, err
		}
		if err := ensureLegacyTenantIndexes(dest, table.dest); err != nil {
			return nil, err
		}
	}
	for _, table := range ordered {
		if err := verifyLegacyTenantTable(source, dest, table); err != nil {
			return nil, err
		}
	}
	return &LegacyTenantResult{Tables: len(tables), Rows: total}, nil
}

// VerifyLegacyTenantMigration performs the activation proof without changing
// the destination. It is used for an exact retry after the registry is already
// ACTIVE, so a verification rerun cannot overwrite later user changes.
func VerifyLegacyTenantMigration(source, dest *executor.DB, tenant string) (*LegacyTenantResult, error) {
	if source == nil || source.Eng == nil || dest == nil || dest.Eng == nil {
		return nil, nerr.New(nerr.InvalidArgument, "xport.VerifyLegacyTenantMigration", "source and destination databases are required")
	}
	if source.Eng.Identity() == dest.Eng.Identity() || tenant == "" {
		return nil, nerr.New(nerr.InvalidArgument, "xport.VerifyLegacyTenantMigration", "invalid source, destination, or tenant")
	}
	tables, admitted, err := prepareLegacyTenantTables(source, tenant)
	if err != nil {
		return nil, err
	}
	if len(tables) == 0 {
		return nil, nerr.New(nerr.NotFound, "xport.VerifyLegacyTenantMigration", "source has no legacy tenant tables")
	}
	if err := validateLegacyTenantForeignKeys(tables); err != nil {
		return nil, err
	}
	if err := preflightLegacyTenantDestination(dest, tables); err != nil {
		return nil, err
	}
	var total uint64
	for i := range tables {
		count, err := countLegacyTenantRows(source, &tables[i])
		if err != nil {
			return nil, err
		}
		tables[i].rows = count
		total += count
		existing, ok := dest.Cat.Get(tables[i].dest.Name)
		if !ok {
			return nil, nerr.New(nerr.Corruption, "xport.VerifyLegacyTenantMigration", "destination table is missing")
		}
		if err := compareLogicalTables(existing, tables[i].dest, true); err != nil {
			return nil, err
		}
	}
	if total == 0 && !admitted {
		return nil, nerr.New(nerr.NotFound, "xport.VerifyLegacyTenantMigration", "tenant is not present in the source database")
	}
	for i := range tables {
		if err := verifyLegacyTenantTable(source, dest, tables[i]); err != nil {
			return nil, err
		}
	}
	return &LegacyTenantResult{Tables: len(tables), Rows: total}, nil
}

func prepareLegacyTenantTables(source *executor.DB, tenant string) ([]legacyTenantTable, bool, error) {
	sourceTables := source.Cat.List()
	sort.Slice(sourceTables, func(i, j int) bool { return sourceTables[i].Name < sourceTables[j].Name })
	out := make([]legacyTenantTable, 0, len(sourceTables))
	admitted := false
	for _, table := range sourceTables {
		ord, legacy := table.LegacyTenantCol()
		if !legacy {
			continue
		}
		if _, collision := table.ColIndex(LegacyTenantDestinationColumn); collision {
			return nil, false, nerr.New(nerr.Conflict, "xport.MigrateLegacyTenant", "legacy_tenant_id column already exists")
		}
		value, err := parseLegacyTenantValue(tenant, table.Columns[ord].Type)
		if err != nil {
			return nil, false, nerr.Wrap(nerr.InvalidArgument, "xport.MigrateLegacyTenant", "tenant does not match table "+table.Name, err)
		}
		dest := migratedLegacyTenantSchema(table, ord)
		out = append(out, legacyTenantTable{source: table, dest: dest, tenantCol: ord, tenant: value})
		if legacyTenantDescriptorAdmits(table, value) {
			admitted = true
		}
	}
	return out, admitted, nil
}

func parseLegacyTenantValue(raw string, typ types.Type) (types.Value, error) {
	switch typ.Kind {
	case types.KindUUID:
		return types.ParseUUID(raw)
	case types.KindString:
		return types.StringValue(raw), nil
	case types.KindText:
		return types.TextValue(raw), nil
	default:
		return types.Value{}, nerr.New(nerr.InvalidArgument, "xport.MigrateLegacyTenant", "unsupported tenant column type")
	}
}

func legacyTenantDescriptorAdmits(table *catalog.Table, tenant types.Value) bool {
	if table == nil || table.Partitioning == nil || table.Partitioning.Kind != catalog.PartitionLegacyTenant {
		return false
	}
	row := make([]types.Value, len(table.Columns))
	for i := range row {
		row[i] = types.Null(table.Columns[i].Type)
	}
	row[table.Partitioning.Columns[0]] = tenant
	_, err := table.PartitionForRow(row)
	return err == nil
}

func migratedLegacyTenantSchema(source *catalog.Table, tenantCol int) *catalog.Table {
	dest := source.Clone()
	oldName := dest.Columns[tenantCol].Name
	dest.Columns[tenantCol].Name = LegacyTenantDestinationColumn
	dest.Partitioning = nil
	dest.HeapMeta = 0
	dest.VecMeta = 0
	for i := range dest.Indexes {
		dest.Indexes[i] = dest.Indexes[i].RenameColumn(oldName, LegacyTenantDestinationColumn)
		dest.Indexes[i].Meta = 0
	}
	return dest
}

func validateLegacyTenantForeignKeys(tables []legacyTenantTable) error {
	owned := make(map[string]struct{}, len(tables))
	for i := range tables {
		owned[tables[i].source.Name] = struct{}{}
	}
	for i := range tables {
		for _, fk := range tables[i].source.ForeignKeys {
			if fk.RefTable == "" || fk.RefTable == tables[i].source.Name {
				continue
			}
			if _, ok := owned[fk.RefTable]; !ok {
				return nerr.New(nerr.Conflict, "xport.MigrateLegacyTenant", "legacy tenant table has a foreign key to an unmigrated table")
			}
		}
	}
	return nil
}

func preflightLegacyTenantDestination(dest *executor.DB, tables []legacyTenantTable) error {
	want := make(map[string]*catalog.Table, len(tables))
	for i := range tables {
		want[tables[i].dest.Name] = tables[i].dest
	}
	for _, existing := range dest.Cat.List() {
		desired, ok := want[existing.Name]
		if !ok {
			return nerr.New(nerr.Conflict, "xport.MigrateLegacyTenant", "destination contains a table outside this migration")
		}
		if err := compareLogicalTables(existing, desired, false); err != nil {
			return err
		}
	}
	return nil
}

func countLegacyTenantRows(source *executor.DB, table *legacyTenantTable) (uint64, error) {
	var count uint64
	err := source.Session().ForEachVisible(table.source.Name, func(row []types.Value) error {
		match, err := legacyTenantRowMatches(table, row)
		if err != nil {
			return err
		}
		if match {
			count++
		}
		return nil
	})
	return count, err
}

func legacyTenantRowMatches(table *legacyTenantTable, row []types.Value) (bool, error) {
	if table == nil || table.tenantCol < 0 || table.tenantCol >= len(row) {
		return false, nerr.New(nerr.Corruption, "xport.MigrateLegacyTenant", "legacy row is missing tenant column")
	}
	value := row[table.tenantCol]
	if value.Null {
		return false, nerr.New(nerr.Corruption, "xport.MigrateLegacyTenant", "legacy row has NULL tenant")
	}
	cmp, err := value.Cmp(table.tenant)
	if err != nil {
		return false, nerr.Wrap(nerr.Corruption, "xport.MigrateLegacyTenant", "legacy row tenant type", err)
	}
	return cmp == 0, nil
}

func orderLegacyTenantTables(tables []legacyTenantTable) ([]legacyTenantTable, error) {
	dumps := make([]tableDump, len(tables))
	byName := make(map[string]legacyTenantTable, len(tables))
	for i := range tables {
		dumps[i] = tableDump{Table: tables[i].dest}
		byName[tables[i].dest.Name] = tables[i]
	}
	ordered, err := orderTablesByFK(dumps)
	if err != nil {
		return nil, err
	}
	out := make([]legacyTenantTable, 0, len(ordered))
	for _, dump := range ordered {
		out = append(out, byName[dump.Table.Name])
	}
	return out, nil
}

func ensureLegacyTenantTable(dest *executor.DB, desired *catalog.Table, parents map[string]*catalog.Table) error {
	if existing, ok := dest.Cat.Get(desired.Name); ok {
		return compareLogicalTables(existing, desired, false)
	}
	ddl, err := createTableSQLWithParents(desired, parents)
	if err != nil {
		return err
	}
	if _, err := dest.Session().Exec(ddl); err != nil {
		return err
	}
	if desired.CDCImages == catalog.CDCImagesFull {
		_, err = dest.Session().Exec("ALTER TABLE " + quoteIdent(desired.Name) + " SET CDC IMAGES FULL")
		return err
	}
	return nil
}

func copyLegacyTenantRows(source, dest *executor.DB, table legacyTenantTable, batchRows int) error {
	upsert, err := upsertAllSQL(table.dest)
	if err != nil {
		return err
	}
	destSession := dest.Session()
	inTxn := false
	batch := 0
	begin := func() error {
		if inTxn {
			return nil
		}
		if _, err := destSession.Exec("BEGIN"); err != nil {
			return err
		}
		inTxn = true
		return nil
	}
	commit := func() error {
		if !inTxn {
			return nil
		}
		if _, err := destSession.Exec("COMMIT"); err != nil {
			_, _ = destSession.Exec("ROLLBACK")
			inTxn = false
			return err
		}
		inTxn = false
		batch = 0
		return nil
	}
	err = source.Session().ForEachVisible(table.source.Name, func(row []types.Value) error {
		match, err := legacyTenantRowMatches(&table, row)
		if err != nil || !match {
			return err
		}
		if err := begin(); err != nil {
			return err
		}
		params := make([]executor.Param, len(row))
		for i := range row {
			params[i] = executor.Param{Value: row[i].Clone()}
		}
		if _, err := destSession.ExecContext(context.Background(), upsert, params); err != nil {
			return err
		}
		batch++
		if batch == batchRows {
			return commit()
		}
		return nil
	})
	if err != nil {
		if inTxn {
			_, _ = destSession.Exec("ROLLBACK")
		}
		return err
	}
	return commit()
}

func upsertAllSQL(table *catalog.Table) (string, error) {
	insert, err := insertSQL(table)
	if err != nil {
		return "", err
	}
	return "UPSERT" + strings.TrimPrefix(insert, "INSERT"), nil
}

func ensureLegacyTenantIndexes(dest *executor.DB, desired *catalog.Table) error {
	existing, ok := dest.Cat.Get(desired.Name)
	if !ok {
		return nerr.New(nerr.Corruption, "xport.MigrateLegacyTenant", "destination table disappeared")
	}
	have := make(map[string]catalog.Index, len(existing.Indexes))
	for _, index := range existing.Indexes {
		have[index.Name] = index
	}
	for _, index := range desired.Indexes {
		if _, ok := have[index.Name]; ok {
			continue
		}
		ddl, err := createIndexSQL(desired, index)
		if err != nil {
			return err
		}
		if _, err := dest.Session().Exec(ddl); err != nil {
			return err
		}
	}
	existing, ok = dest.Cat.Get(desired.Name)
	if !ok {
		return nerr.New(nerr.Corruption, "xport.MigrateLegacyTenant", "destination table disappeared")
	}
	return compareLogicalTables(existing, desired, true)
}

func compareLogicalTables(existing, desired *catalog.Table, indexes bool) error {
	a, err := logicalTableBytes(existing, indexes)
	if err != nil {
		return err
	}
	b, err := logicalTableBytes(desired, indexes)
	if err != nil {
		return err
	}
	if !bytes.Equal(a, b) {
		return nerr.New(nerr.Conflict, "xport.MigrateLegacyTenant", "destination table does not match resumable migration schema")
	}
	return nil
}

func logicalTableBytes(table *catalog.Table, indexes bool) ([]byte, error) {
	logical := table.Clone()
	logical.ID = 0
	logical.HeapMeta = 0
	logical.VecMeta = 0
	logical.Partitioning = nil
	if !indexes {
		logical.Indexes = nil
	} else {
		for i := range logical.Indexes {
			logical.Indexes[i].Meta = 0
		}
	}
	for i := range logical.ForeignKeys {
		logical.ForeignKeys[i].RefTableID = 0
	}
	return catalog.EncodeTable(logical)
}

func verifyLegacyTenantTable(source, dest *executor.DB, table legacyTenantTable) error {
	var destRows uint64
	err := dest.Session().ForEachVisible(table.dest.Name, func(row []types.Value) error {
		if table.tenantCol < 0 || table.tenantCol >= len(row) {
			return nerr.New(nerr.Corruption, "xport.MigrateLegacyTenant", "destination row is missing legacy tenant value")
		}
		cmp, err := row[table.tenantCol].Cmp(table.tenant)
		if err != nil || cmp != 0 {
			return nerr.New(nerr.Corruption, "xport.MigrateLegacyTenant", "destination contains a row for another tenant")
		}
		destRows++
		return nil
	})
	if err != nil {
		return err
	}
	if destRows != table.rows {
		return nerr.New(nerr.Corruption, "xport.MigrateLegacyTenant", "destination row count does not match source tenant")
	}

	lookup, err := selectByPrimaryKeySQL(table.dest)
	if err != nil {
		return err
	}
	destSession := dest.Session()
	return source.Session().ForEachVisible(table.source.Name, func(row []types.Value) error {
		match, err := legacyTenantRowMatches(&table, row)
		if err != nil || !match {
			return err
		}
		params := make([]executor.Param, len(table.dest.PK))
		for i, ord := range table.dest.PK {
			params[i] = executor.Param{Value: row[ord].Clone()}
		}
		result, err := destSession.ExecContext(context.Background(), lookup, params)
		if err != nil {
			return err
		}
		if len(result.Rows) != 1 {
			return nerr.New(nerr.Corruption, "xport.MigrateLegacyTenant", "destination primary-key verification failed")
		}
		want, err := types.EncodeRow(inlineVectors(row))
		if err != nil {
			return err
		}
		got, err := types.EncodeRow(inlineVectors(result.Rows[0]))
		if err != nil {
			return err
		}
		if !bytes.Equal(want, got) {
			return nerr.New(nerr.Corruption, "xport.MigrateLegacyTenant", "destination row does not match source tenant")
		}
		return nil
	})
}

func selectByPrimaryKeySQL(table *catalog.Table) (string, error) {
	if table == nil || len(table.PK) == 0 {
		return "", nerr.New(nerr.InvalidFormat, "xport.MigrateLegacyTenant", "legacy table has no primary key")
	}
	var b strings.Builder
	b.WriteString("SELECT ")
	for i, column := range table.Columns {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(quoteIdent(column.Name))
	}
	b.WriteString(" FROM ")
	b.WriteString(quoteIdent(table.Name))
	b.WriteString(" WHERE ")
	for i, ord := range table.PK {
		if ord < 0 || ord >= len(table.Columns) {
			return "", nerr.New(nerr.InvalidFormat, "xport.MigrateLegacyTenant", "invalid primary key ordinal")
		}
		if i > 0 {
			b.WriteString(" AND ")
		}
		b.WriteString(quoteIdent(table.Columns[ord].Name))
		b.WriteString(" = $")
		b.WriteString(intString(i + 1))
	}
	return b.String(), nil
}

func intString(value int) string {
	// Migration parameter counts are bounded by the table column limit. This
	// avoids formatting through an interface in the row-verification hot path.
	if value < 10 {
		return string(rune('0' + value))
	}
	var digits [20]byte
	i := len(digits)
	for value > 0 {
		i--
		digits[i] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[i:])
}
