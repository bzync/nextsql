// Package nextsql is the official NextSQL Go driver.
// Encryption keys are supplied through Config.KeyProvider, never through a URL.
package nextsql

import (
	"context"
	"crypto/tls"
	"net"
	"strings"
	"sync"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/protocol"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/types"
)

// Config is the only supported way to open a connection. Do not put keys in URLs.
type Config struct {
	Address       string
	Database      string
	User          string
	Password      string
	KeyProvider   crypto.KeyProvider // reserved for client-held keys; never place keys in a URL
	TLS           *tls.Config
	InsecureNoTLS bool
}

// Conn is one authenticated session.
type Conn struct {
	cfg    Config
	mu     sync.Mutex
	raw    net.Conn
	secret uint64
	lim    protocol.Limits
	busy   bool
}

type Result struct {
	Affected int64
	Columns  []string
	Rows     [][]types.Value
}

type Rows struct {
	c        *Conn
	cols     []string
	types    []types.Type
	batch    [][]types.Value
	i        int
	done     bool
	closed   bool
	err      error
	affected int64
	stop     func() bool
}

type Stmt struct {
	c  *Conn
	id uint32
}

func Open(cfg Config) (*Conn, error) {
	return OpenContext(context.Background(), cfg)
}

func OpenContext(ctx context.Context, cfg Config) (*Conn, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	d := net.Dialer{}
	raw, err := d.DialContext(ctx, "tcp", cfg.Address)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "nextsql.Open", "dial", err)
	}
	if cfg.TLS != nil {
		tc := cfg.TLS.Clone()
		if tc.MinVersion < tls.VersionTLS13 {
			tc.MinVersion = tls.VersionTLS13
		}
		if tc.ServerName == "" {
			host, _, _ := net.SplitHostPort(cfg.Address)
			tc.ServerName = host
		}
		tlsConn := tls.Client(raw, tc)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = raw.Close()
			return nil, nerr.Wrap(nerr.Protocol, "nextsql.Open", "tls handshake", err)
		}
		raw = tlsConn
	}
	c := &Conn{cfg: cfg, raw: raw, lim: protocol.DefaultLimits()}
	if err := c.handshake(); err != nil {
		_ = raw.Close()
		return nil, err
	}
	return c, nil
}

func validateConfig(cfg Config) error {
	if cfg.Address == "" {
		return nerr.New(nerr.InvalidArgument, "nextsql.Open", "address is required")
	}
	addr := strings.ToLower(cfg.Address)
	if strings.Contains(addr, "://") || strings.Contains(addr, "key=") || strings.Contains(addr, "password=") {
		return nerr.New(nerr.InvalidArgument, "nextsql.Open", "keys and credentials must not be passed in a URL")
	}
	if cfg.TLS == nil && !cfg.InsecureNoTLS {
		return nerr.New(nerr.InvalidArgument, "nextsql.Open", "TLS is required for remote connections")
	}
	if cfg.InsecureNoTLS && security.RequireTLS(cfg.Address) {
		return nerr.New(nerr.InvalidArgument, "nextsql.Open", "plaintext is only allowed on loopback")
	}
	if cfg.User == "" {
		return nerr.New(nerr.InvalidArgument, "nextsql.Open", "user is required")
	}
	return nil
}

func (c *Conn) handshake() error {
	payload, err := protocol.EncodeHello(protocol.Hello{
		Version:  protocol.Version,
		Database: c.cfg.Database,
		User:     c.cfg.User,
	}, c.lim)
	if err != nil {
		return err
	}
	if err := protocol.WriteFrame(c.raw, protocol.TypeHello, payload, c.lim.MaxPacket); err != nil {
		return err
	}
	typ, body, err := c.read()
	if err != nil {
		return err
	}
	if typ != protocol.TypeHelloOK {
		return unexpected(typ, body, c.lim)
	}
	ok, err := protocol.DecodeHelloOK(body)
	if err != nil {
		return err
	}
	c.secret = ok.Secret
	authPayload, err := protocol.EncodeAuth(protocol.Auth{Password: c.cfg.Password}, c.lim)
	if err != nil {
		return err
	}
	if err := protocol.WriteFrame(c.raw, protocol.TypeAuth, authPayload, c.lim.MaxPacket); err != nil {
		return err
	}
	typ, body, err = c.read()
	if err != nil {
		return err
	}
	if typ != protocol.TypeAuthOK {
		return unexpected(typ, body, c.lim)
	}
	if ok.AuthMethod == protocol.AuthPasswordKey {
		if c.cfg.KeyProvider == nil {
			return nerr.New(nerr.Unauthorized, "nextsql.Open", "server requires a client-held key")
		}
		dek, err := c.cfg.KeyProvider.Current()
		if err != nil {
			return err
		}
		mat, err := crypto.EncodeUnlockMaterial(dek)
		if err != nil {
			return err
		}
		if err := protocol.WriteFrame(c.raw, protocol.TypeUnlock, mat, c.lim.MaxPacket); err != nil {
			return err
		}
		typ, body, err = c.read()
		if err != nil {
			return err
		}
		if typ != protocol.TypeUnlockOK {
			return unexpected(typ, body, c.lim)
		}
	}
	typ, body, err = c.read()
	if err != nil {
		return err
	}
	if typ != protocol.TypeReady {
		return unexpected(typ, body, c.lim)
	}
	return nil
}

func (c *Conn) read() (protocol.Type, []byte, error) {
	return protocol.ReadFrame(c.raw, c.lim.MaxPacket)
}

func (c *Conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.raw == nil {
		return nil
	}
	_ = protocol.WriteFrame(c.raw, protocol.TypeTerminate, nil, c.lim.MaxPacket)
	err := c.raw.Close()
	c.raw = nil
	return err
}

func (c *Conn) Exec(ctx context.Context, sql string, params ...types.Value) (*Result, error) {
	rows, err := c.Query(ctx, sql, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &Result{Columns: rows.Columns()}
	for rows.Next() {
		out.Rows = append(out.Rows, append([]types.Value(nil), rows.Values()...))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out.Affected = rows.Affected()
	return out, nil
}

func (c *Conn) Query(ctx context.Context, sql string, params ...types.Value) (*Rows, error) {
	c.mu.Lock()
	if c.raw == nil {
		c.mu.Unlock()
		return nil, nerr.New(nerr.Unavailable, "nextsql.Query", "connection closed")
	}
	if c.busy {
		c.mu.Unlock()
		return nil, nerr.New(nerr.Conflict, "nextsql.Query", "connection is busy")
	}
	c.busy = true
	payload, err := protocol.EncodeQuery(protocol.Query{SQL: sql, Params: wireParams(params)}, c.lim)
	if err != nil {
		c.busy = false
		c.mu.Unlock()
		return nil, err
	}
	if err := protocol.WriteFrame(c.raw, protocol.TypeQuery, payload, c.lim.MaxPacket); err != nil {
		c.busy = false
		c.mu.Unlock()
		return nil, err
	}
	rows, err := c.readRows()
	if err != nil {
		return nil, err
	}
	if ctx != nil {
		conn := c
		rows.stop = context.AfterFunc(ctx, func() { _ = conn.Cancel(context.Background()) })
	}
	return rows, nil
}

func (c *Conn) readRows() (*Rows, error) {
	typ, body, err := c.read()
	if err != nil {
		c.busy = false
		c.mu.Unlock()
		return nil, err
	}
	rows := &Rows{c: c}
	switch typ {
	case protocol.TypeRowDesc:
		desc, err := protocol.DecodeRowDesc(body, c.lim)
		if err != nil {
			c.busy = false
			c.mu.Unlock()
			return nil, err
		}
		rows.cols = make([]string, len(desc.Columns))
		rows.types = make([]types.Type, len(desc.Columns))
		for i, col := range desc.Columns {
			rows.cols[i] = col.Name
			rows.types[i] = col.Type
		}
		return rows, nil
	case protocol.TypeCommandComplete:
		cc, err := protocol.DecodeCommandComplete(body)
		if err != nil {
			c.busy = false
			c.mu.Unlock()
			return nil, err
		}
		rows.affected = cc.Affected
		rows.done = true
		if err := c.expectReady(); err != nil {
			c.busy = false
			c.mu.Unlock()
			return nil, err
		}
		c.busy = false
		c.mu.Unlock()
		rows.closed = true
		return rows, nil
	default:
		err := unexpected(typ, body, c.lim)
		if typ == protocol.TypeError {
			// writeErrReady sends Error then Ready; drain Ready so the session stays usable.
			_ = c.expectReady()
		}
		c.busy = false
		c.mu.Unlock()
		return nil, err
	}
}

func (c *Conn) expectReady() error {
	typ, body, err := c.read()
	if err != nil {
		return err
	}
	if typ != protocol.TypeReady {
		return unexpected(typ, body, c.lim)
	}
	return nil
}

func (c *Conn) Prepare(ctx context.Context, sql string) (*Stmt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.raw == nil {
		return nil, nerr.New(nerr.Unavailable, "nextsql.Prepare", "connection closed")
	}
	payload, err := protocol.EncodePrepare(sql, c.lim)
	if err != nil {
		return nil, err
	}
	if err := protocol.WriteFrame(c.raw, protocol.TypePrepare, payload, c.lim.MaxPacket); err != nil {
		return nil, err
	}
	typ, body, err := c.read()
	if err != nil {
		return nil, err
	}
	if typ != protocol.TypePrepareOK {
		return nil, unexpected(typ, body, c.lim)
	}
	id, err := protocol.DecodePrepareOK(body)
	if err != nil {
		return nil, err
	}
	if err := c.expectReady(); err != nil {
		return nil, err
	}
	return &Stmt{c: c, id: id}, nil
}

func (c *Conn) Cancel(_ context.Context) error {
	// Must not take c.mu: an in-flight Query holds it until Rows.Close.
	secret := c.secret
	if secret == 0 {
		return nerr.New(nerr.Unavailable, "nextsql.Cancel", "not connected")
	}
	side, err := dialRaw(c.cfg.Address, c.cfg.TLS, c.cfg.InsecureNoTLS)
	if err != nil {
		return err
	}
	defer side.Close()
	lim := protocol.DefaultLimits()
	payload, err := protocol.EncodeHello(protocol.Hello{
		Version: protocol.Version,
		Flags:   protocol.FlagCancel,
		Secret:  secret,
	}, lim)
	if err != nil {
		return err
	}
	if err := protocol.WriteFrame(side, protocol.TypeHello, payload, lim.MaxPacket); err != nil {
		return err
	}
	typ, body, err := protocol.ReadFrame(side, lim.MaxPacket)
	if err != nil {
		return err
	}
	if typ != protocol.TypeReady {
		return unexpected(typ, body, lim)
	}
	return nil
}

func (s *Stmt) Exec(ctx context.Context, params ...types.Value) (*Result, error) {
	rows, err := s.Query(ctx, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &Result{Columns: rows.Columns()}
	for rows.Next() {
		out.Rows = append(out.Rows, append([]types.Value(nil), rows.Values()...))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out.Affected = rows.Affected()
	return out, nil
}

func (s *Stmt) Query(ctx context.Context, params ...types.Value) (*Rows, error) {
	c := s.c
	c.mu.Lock()
	if c.raw == nil {
		c.mu.Unlock()
		return nil, nerr.New(nerr.Unavailable, "nextsql.Stmt", "connection closed")
	}
	if c.busy {
		c.mu.Unlock()
		return nil, nerr.New(nerr.Conflict, "nextsql.Stmt", "connection is busy")
	}
	c.busy = true
	payload, err := protocol.EncodeExecute(protocol.Execute{ID: s.id, Params: wireParams(params)}, c.lim)
	if err != nil {
		c.busy = false
		c.mu.Unlock()
		return nil, err
	}
	if err := protocol.WriteFrame(c.raw, protocol.TypeExecute, payload, c.lim.MaxPacket); err != nil {
		c.busy = false
		c.mu.Unlock()
		return nil, err
	}
	rows, err := c.readRows()
	if err != nil {
		return nil, err
	}
	if ctx != nil {
		conn := c
		rows.stop = context.AfterFunc(ctx, func() { _ = conn.Cancel(context.Background()) })
	}
	return rows, nil
}

func (s *Stmt) Close() error {
	c := s.c
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.raw == nil || s.id == 0 {
		return nil
	}
	if err := protocol.WriteFrame(c.raw, protocol.TypeCloseStmt, protocol.EncodeCloseStmt(s.id), c.lim.MaxPacket); err != nil {
		return err
	}
	typ, body, err := c.read()
	if err != nil {
		return err
	}
	if typ != protocol.TypeCloseOK {
		return unexpected(typ, body, c.lim)
	}
	if err := c.expectReady(); err != nil {
		return err
	}
	s.id = 0
	return nil
}

func (r *Rows) Columns() []string { return append([]string(nil), r.cols...) }

func (r *Rows) ColumnTypes() []types.Type { return append([]types.Type(nil), r.types...) }

func (r *Rows) Affected() int64 { return r.affected }

func (r *Rows) Err() error { return r.err }

func (r *Rows) Values() []types.Value {
	if r.i <= 0 || r.i > len(r.batch) {
		return nil
	}
	return r.batch[r.i-1]
}

func (r *Rows) Next() bool {
	if r.closed || r.err != nil {
		return false
	}
	if r.i < len(r.batch) {
		r.i++
		return true
	}
	if r.done {
		return false
	}
	if err := r.fill(); err != nil {
		r.err = err
		r.finishLocked()
		return false
	}
	if r.i < len(r.batch) {
		r.i++
		return true
	}
	return false
}

func (r *Rows) fill() error {
	c := r.c
	if !r.done && len(r.batch) > 0 {
		if err := protocol.WriteFrame(c.raw, protocol.TypeFlowAck, nil, c.lim.MaxPacket); err != nil {
			return err
		}
	}
	typ, body, err := c.read()
	if err != nil {
		return err
	}
	switch typ {
	case protocol.TypeDataBatch:
		batch, err := protocol.DecodeDataBatch(body, c.lim)
		if err != nil {
			return err
		}
		r.batch = batch.Rows
		r.i = 0
		return nil
	case protocol.TypeCommandComplete:
		cc, err := protocol.DecodeCommandComplete(body)
		if err != nil {
			return err
		}
		r.affected = cc.Affected
		r.done = true
		r.batch = nil
		r.i = 0
		if err := c.expectReady(); err != nil {
			return err
		}
		r.finishLocked()
		return nil
	default:
		err := unexpected(typ, body, c.lim)
		if typ == protocol.TypeError {
			// Streaming errors are followed by Ready. Drain it before releasing
			// the connection so the next statement starts on a frame boundary.
			if readyErr := c.expectReady(); readyErr != nil {
				return readyErr
			}
		}
		return err
	}
}

func (r *Rows) Close() error {
	if r.closed {
		return r.err
	}
	for r.Next() {
	}
	if !r.closed && r.c != nil {
		r.finishLocked()
	}
	return r.err
}

func (r *Rows) finishLocked() {
	if r.stop != nil {
		r.stop()
		r.stop = nil
	}
	if r.c != nil && !r.closed {
		r.c.busy = false
		r.c.mu.Unlock()
	}
	r.closed = true
}

func wireParams(vals []types.Value) []executor.Param {
	if len(vals) == 0 {
		return nil
	}
	out := make([]executor.Param, len(vals))
	for i, v := range vals {
		out[i] = executor.Param{Value: v}
	}
	return out
}

func unexpected(typ protocol.Type, body []byte, lim protocol.Limits) error {
	if typ == protocol.TypeError {
		em, err := protocol.DecodeError(body, lim)
		if err != nil {
			return err
		}
		return nerr.New(nerr.Code(em.Code), "nextsql", em.Message)
	}
	return nerr.New(nerr.Protocol, "nextsql", "unexpected message type")
}

func dialRaw(addr string, tlsCfg *tls.Config, insecure bool) (net.Conn, error) {
	raw, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "nextsql.Cancel", "dial", err)
	}
	if tlsCfg != nil {
		tc := tlsCfg.Clone()
		if tc.MinVersion < tls.VersionTLS13 {
			tc.MinVersion = tls.VersionTLS13
		}
		if tc.ServerName == "" {
			host, _, _ := net.SplitHostPort(addr)
			tc.ServerName = host
		}
		tlsConn := tls.Client(raw, tc)
		if err := tlsConn.Handshake(); err != nil {
			_ = raw.Close()
			return nil, nerr.Wrap(nerr.Protocol, "nextsql.Cancel", "tls handshake", err)
		}
		return tlsConn, nil
	}
	_ = insecure
	return raw, nil
}
