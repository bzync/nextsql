package nextsql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

func init() {
	sql.Register("nextsql", &Driver{})
}

// Driver implements database/sql/driver.Driver and driver.DriverContext.
type Driver struct{}

// Open opens a new database connection from the given DSN.
func (d *Driver) Open(name string) (driver.Conn, error) {
	connector, err := d.OpenConnector(name)
	if err != nil {
		return nil, err
	}
	return connector.Connect(context.Background())
}

// OpenConnector returns a Connector for the given DSN.
func (d *Driver) OpenConnector(name string) (driver.Connector, error) {
	cfg, err := ParseDSN(name)
	if err != nil {
		return nil, err
	}
	return &connector{driver: d, cfg: cfg}, nil
}

type connector struct {
	driver *Driver
	cfg    Config
}

func (c *connector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := Open(c.cfg)
	if err != nil {
		return nil, err
	}
	return &sqlConn{c: conn}, nil
}

func (c *connector) Driver() driver.Driver {
	return c.driver
}

// ParseDSN parses a URL-style ("nextsql://user:pass@host:port/database?insecure=true")
// or key-value style ("address=127.0.0.1:7210 user=app password=pw database=main") DSN.
func ParseDSN(dsn string) (Config, error) {
	const op = "nextsql.ParseDSN"
	var cfg Config

	if strings.HasPrefix(dsn, "nextsql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return cfg, nerr.New(nerr.InvalidArgument, op, "invalid connection URL: "+err.Error())
		}
		if u.User != nil {
			cfg.User = u.User.Username()
			cfg.Password, _ = u.User.Password()
		}
		cfg.Address = u.Host
		path := strings.TrimPrefix(u.Path, "/")
		cfg.Database = path

		q := u.Query()
		if q.Get("insecure") == "true" || q.Get("sslmode") == "disable" {
			cfg.InsecureNoTLS = true
		}
		if r := q.Get("realm"); r != "" {
			cfg.Realm = r
		}
		if rc := q.Get("read_consistency"); rc != "" {
			c, err := parseConsistency(rc)
			if err != nil {
				return cfg, nerr.New(nerr.InvalidArgument, op, err.Error())
			}
			cfg.ReadConsistency = c
		} else if rc := q.Get("consistency"); rc != "" {
			c, err := parseConsistency(rc)
			if err != nil {
				return cfg, nerr.New(nerr.InvalidArgument, op, err.Error())
			}
			cfg.ReadConsistency = c
		}
		if ms := q.Get("max_staleness"); ms != "" {
			d, err := parseStaleness(ms)
			if err != nil {
				return cfg, nerr.New(nerr.InvalidArgument, op, err.Error())
			}
			cfg.MaxStaleness = d
		} else if ms := q.Get("max_staleness_ms"); ms != "" {
			d, err := parseStaleness(ms)
			if err != nil {
				return cfg, nerr.New(nerr.InvalidArgument, op, err.Error())
			}
			cfg.MaxStaleness = d
		}
		if nodes := q.Get("nodes"); nodes != "" {
			for _, n := range strings.Split(nodes, ",") {
				if trimmed := strings.TrimSpace(n); trimmed != "" {
					cfg.Nodes = append(cfg.Nodes, trimmed)
				}
			}
		}
		return cfg, nil
	}

	// Key-value parsing
	parts := strings.Fields(dsn)
	for _, p := range parts {
		k, v, ok := strings.Cut(p, "=")
		if !ok {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.TrimSpace(v)
		v = strings.Trim(v, `"'`)
		switch k {
		case "address", "addr", "host":
			cfg.Address = v
		case "user", "username":
			cfg.User = v
		case "password", "pass":
			cfg.Password = v
		case "database", "db", "dbname":
			cfg.Database = v
		case "realm":
			cfg.Realm = v
		case "read_consistency", "consistency":
			c, err := parseConsistency(v)
			if err != nil {
				return cfg, nerr.New(nerr.InvalidArgument, op, err.Error())
			}
			cfg.ReadConsistency = c
		case "max_staleness", "max_staleness_ms":
			d, err := parseStaleness(v)
			if err != nil {
				return cfg, nerr.New(nerr.InvalidArgument, op, err.Error())
			}
			cfg.MaxStaleness = d
		case "nodes":
			for _, n := range strings.Split(v, ",") {
				if trimmed := strings.TrimSpace(n); trimmed != "" {
					cfg.Nodes = append(cfg.Nodes, trimmed)
				}
			}
		case "insecure", "insecure_no_tls":
			cfg.InsecureNoTLS = (v == "true" || v == "1")
		case "sslmode":
			if v == "disable" {
				cfg.InsecureNoTLS = true
			}
		}
	}

	if cfg.Address == "" {
		return cfg, nerr.New(nerr.InvalidArgument, op, "address is required in DSN")
	}
	if cfg.User == "" {
		return cfg, nerr.New(nerr.InvalidArgument, op, "user is required in DSN")
	}
	return cfg, nil
}

func parseConsistency(s string) (ReadConsistency, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "strong":
		return Strong, nil
	case "bounded":
		return Bounded, nil
	case "stale":
		return Stale, nil
	default:
		return Strong, fmt.Errorf("unknown read_consistency %q (must be strong, bounded, or stale)", s)
	}
}

func parseStaleness(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}
	if ms, err := strconv.ParseInt(s, 10, 64); err == nil && ms >= 0 {
		return time.Duration(ms) * time.Millisecond, nil
	}
	return 0, fmt.Errorf("invalid max_staleness %q", s)
}

// sqlConn wraps a NextSQL *Conn to implement database/sql/driver interfaces.
type sqlConn struct {
	c *Conn
}

var _ driver.Conn = (*sqlConn)(nil)
var _ driver.ConnBeginTx = (*sqlConn)(nil)
var _ driver.ConnPrepareContext = (*sqlConn)(nil)
var _ driver.ExecerContext = (*sqlConn)(nil)
var _ driver.QueryerContext = (*sqlConn)(nil)
var _ driver.Pinger = (*sqlConn)(nil)

func (s *sqlConn) Prepare(query string) (driver.Stmt, error) {
	return s.PrepareContext(context.Background(), query)
}

func (s *sqlConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	stmt, err := s.c.Prepare(ctx, query)
	if err != nil {
		return nil, err
	}
	return &sqlStmt{conn: s, stmt: stmt, query: query}, nil
}

func (s *sqlConn) Close() error {
	return s.c.Close()
}

func (s *sqlConn) Begin() (driver.Tx, error) {
	return s.BeginTx(context.Background(), driver.TxOptions{})
}

func (s *sqlConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	sql := "BEGIN"
	if opts.ReadOnly {
		sql = "BEGIN TRANSACTION READ ONLY"
	}
	if _, err := s.c.Exec(ctx, sql); err != nil {
		return nil, err
	}
	return &sqlTx{s: s}, nil
}

func (s *sqlConn) Ping(ctx context.Context) error {
	_, err := s.c.Exec(ctx, "SELECT 1")
	return err
}

func (s *sqlConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	vals, err := namedValuesToTypesValues(args)
	if err != nil {
		return nil, err
	}
	res, err := s.c.Exec(ctx, query, vals...)
	if err != nil {
		return nil, err
	}
	return driver.RowsAffected(res.Affected), nil
}

func (s *sqlConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	vals, err := namedValuesToTypesValues(args)
	if err != nil {
		return nil, err
	}
	rows, err := s.c.Query(ctx, query, vals...)
	if err != nil {
		return nil, err
	}
	return &sqlRows{rows: rows}, nil
}

type sqlTx struct {
	s *sqlConn
}

func (tx *sqlTx) Commit() error {
	_, err := tx.s.c.Exec(context.Background(), "COMMIT")
	return err
}

func (tx *sqlTx) Rollback() error {
	_, err := tx.s.c.Exec(context.Background(), "ROLLBACK")
	return err
}

type sqlStmt struct {
	conn  *sqlConn
	stmt  *Stmt
	query string
}

func (st *sqlStmt) Close() error {
	return st.stmt.Close()
}

func (st *sqlStmt) NumInput() int {
	return -1
}

func (st *sqlStmt) Exec(args []driver.Value) (driver.Result, error) {
	return st.ExecContext(context.Background(), valuesToNamed(args))
}

func (st *sqlStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	vals, err := namedValuesToTypesValues(args)
	if err != nil {
		return nil, err
	}
	res, err := st.stmt.Exec(ctx, vals...)
	if err != nil {
		return nil, err
	}
	return driver.RowsAffected(res.Affected), nil
}

func (st *sqlStmt) Query(args []driver.Value) (driver.Rows, error) {
	return st.QueryContext(context.Background(), valuesToNamed(args))
}

func (st *sqlStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	vals, err := namedValuesToTypesValues(args)
	if err != nil {
		return nil, err
	}
	rows, err := st.stmt.Query(ctx, vals...)
	if err != nil {
		return nil, err
	}
	return &sqlRows{rows: rows}, nil
}

type sqlRows struct {
	rows *Rows
}

func (r *sqlRows) Columns() []string {
	return r.rows.Columns()
}

func (r *sqlRows) Close() error {
	return r.rows.Close()
}

func (r *sqlRows) Next(dest []driver.Value) error {
	if !r.rows.Next() {
		if err := r.rows.Err(); err != nil {
			return err
		}
		return io.EOF
	}

	vals := r.rows.Values()
	for i, v := range vals {
		if i >= len(dest) {
			break
		}
		dest[i] = valueToDriverValue(v)
	}
	return nil
}

func valueToDriverValue(v types.Value) driver.Value {
	if v.Null {
		return nil
	}
	switch v.Typ.Kind {
	case types.KindBool:
		return v.Bool
	case types.KindInt8, types.KindInt16, types.KindInt32, types.KindInt64:
		return v.Int
	case types.KindUint8, types.KindUint16, types.KindUint32, types.KindUint64:
		return int64(v.Uint)
	case types.KindFloat32, types.KindFloat64:
		return v.Flt
	case types.KindString, types.KindText, types.KindChar, types.KindVarchar:
		return v.Str
	case types.KindBlob:
		return []byte(v.Str)
	case types.KindJSON:
		return append([]byte(nil), v.JSON...)
	case types.KindDecimal:
		return v.Dec.String()
	case types.KindUUID:
		return fmt.Sprintf("%x-%x-%x-%x-%x", v.UUID[0:4], v.UUID[4:6], v.UUID[6:8], v.UUID[8:10], v.UUID[10:16])
	case types.KindTimestamp, types.KindTimestampTZ, types.KindDate, types.KindTime:
		return time.Unix(0, v.Time).UTC()
	default:
		return v.String()
	}
}

func valuesToNamed(args []driver.Value) []driver.NamedValue {
	out := make([]driver.NamedValue, len(args))
	for i, a := range args {
		out[i] = driver.NamedValue{Ordinal: i + 1, Value: a}
	}
	return out
}

func namedValuesToTypesValues(args []driver.NamedValue) ([]types.Value, error) {
	out := make([]types.Value, len(args))
	for i, a := range args {
		val, err := goValueToTypeValue(a.Value)
		if err != nil {
			return nil, err
		}
		out[i] = val
	}
	return out, nil
}

func goValueToTypeValue(v any) (types.Value, error) {
	if v == nil {
		return types.Value{Null: true}, nil
	}
	switch val := v.(type) {
	case types.Value:
		return val, nil
	case bool:
		return types.BoolValue(val), nil
	case int:
		return types.Int64Value(int64(val)), nil
	case int8:
		return types.Int8Value(val), nil
	case int16:
		return types.Int16Value(val), nil
	case int32:
		return types.Int32Value(val), nil
	case int64:
		return types.Int64Value(val), nil
	case uint:
		return types.Uint64Value(uint64(val)), nil
	case uint8:
		return types.Uint8Value(val), nil
	case uint16:
		return types.Uint16Value(val), nil
	case uint32:
		return types.Uint32Value(val), nil
	case uint64:
		return types.Uint64Value(val), nil
	case float32:
		return types.Float32Value(float64(val)), nil
	case float64:
		return types.Float64Value(val), nil
	case string:
		return types.StringValue(val), nil
	case []byte:
		return types.BlobValue(val), nil
	case time.Time:
		return types.Value{Typ: types.TimestampTZ(), Time: val.UnixNano()}, nil
	case driver.Valuer:
		dval, err := val.Value()
		if err != nil {
			return types.Value{}, err
		}
		return goValueToTypeValue(dval)
	default:
		return types.StringValue(fmt.Sprint(v)), nil
	}
}
