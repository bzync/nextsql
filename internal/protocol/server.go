package protocol

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/replication"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/types"
)

// Server accepts native-protocol connections against one local database.
type Server struct {
	DB       *executor.DB
	Auth     *auth.Store
	Tokens   *auth.TokenVerifier
	ACL      *security.ACL
	Audit    *security.Log
	Registry *security.Registry
	TLS      *tls.Config
	Limits   Limits
	Log      *slog.Logger
	Database string
	Tasks    *executor.TaskRuntime

	RequireClientKey bool
	// RequireServiceIdentity binds the verified client-certificate URI
	// nextsql://service/<principal> to Hello.User before password/RBAC checks.
	RequireServiceIdentity bool
	Unlock                 func(*crypto.DEK) error

	mu       sync.Mutex
	ln       net.Listener
	conns    map[net.Conn]struct{}
	backends map[uint64]*backend
	nconn    int
	closed   bool
}

type backend struct {
	id     uint64
	secret uint64
	user   string
	sess   *executor.Session
	unreg  func()
	conn   net.Conn

	mu        sync.Mutex
	cancel    context.CancelFunc
	queryConn net.Conn
	prepared  map[uint32]string
	nextStmt  uint32
}

func (b *backend) requestCancel() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cancel != nil {
		b.cancel()
	}
	if b.queryConn != nil {
		_ = b.queryConn.SetDeadline(time.Now())
	}
}

func (b *backend) kill() {
	b.requestCancel()
	b.mu.Lock()
	c := b.conn
	b.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}

func (s *Server) readUnlock(conn net.Conn, lim Limits) error {
	_ = conn.SetDeadline(time.Now().Add(lim.Idle))
	typ, payload, err := ReadFrame(conn, lim.MaxPacket)
	if err != nil {
		return err
	}
	if typ != TypeUnlock {
		return nerr.New(nerr.Protocol, "protocol", "client key required")
	}
	if s.Unlock == nil {
		return nerr.New(nerr.Unavailable, "protocol", "unlock is not configured")
	}
	root, err := crypto.ParseUnlockMaterial(payload)
	if err != nil {
		return err
	}
	if err := s.Unlock(root); err != nil {
		return err
	}
	return WriteFrame(conn, TypeUnlockOK, nil, lim.MaxPacket)
}

func NewServer(db *executor.DB, users *auth.Store) *Server {
	return &Server{
		DB:       db,
		Auth:     users,
		Limits:   DefaultLimits(),
		conns:    make(map[net.Conn]struct{}),
		backends: make(map[uint64]*backend),
	}
}

func (s *Server) log() *slog.Logger {
	if s != nil && s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// ListenAndServe listens on addr. TLS wraps the listener when s.TLS is set.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nerr.Wrap(nerr.IO, "protocol.ListenAndServe", "listen", err)
	}
	if s.TLS != nil {
		ln = tls.NewListener(ln, s.TLS)
	}
	return s.Serve(ctx, ln)
}

func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = ln.Close()
		return nerr.New(nerr.Unavailable, "protocol.Serve", "server closed")
	}
	s.ln = ln
	s.mu.Unlock()

	go func() {
		<-ctx.Done()
		_ = s.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed || ctx.Err() != nil {
				return nil
			}
			return nerr.Wrap(nerr.IO, "protocol.Serve", "accept", err)
		}
		s.mu.Lock()
		if s.nconn >= s.Limits.normalized().MaxSessions {
			s.mu.Unlock()
			_ = conn.Close()
			continue
		}
		s.nconn++
		if s.conns == nil {
			s.conns = make(map[net.Conn]struct{})
		}
		s.conns[conn] = struct{}{}
		s.mu.Unlock()
		go s.serveConn(ctx, conn)
	}
}

func (s *Server) Addr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return nil
	}
	return s.ln.Addr()
}

// DatabaseHandle returns the currently unlocked database under the same lock
// used to publish it. Client-key mode installs the handle only after recovery,
// Raft, and the task runtime are ready.
func (s *Server) DatabaseHandle() *executor.DB {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.DB
}

func (s *Server) SetDatabase(db *executor.DB) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.DB = db
	s.mu.Unlock()
}

func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	ln := s.ln
	tasks := s.Tasks
	s.Tasks = nil
	connections := make([]net.Conn, 0, len(s.conns))
	for conn := range s.conns {
		connections = append(connections, conn)
	}
	for _, b := range s.backends {
		b.mu.Lock()
		if b.cancel != nil {
			b.cancel()
		}
		b.mu.Unlock()
	}
	s.mu.Unlock()
	for _, conn := range connections {
		_ = conn.Close()
	}
	if tasks != nil {
		_ = tasks.Close()
	}
	if ln != nil {
		return ln.Close()
	}
	return nil
}

// TerminateConnections closes every accepted connection, including TLS
// handshakes that have not authenticated yet. Security reloads use this after
// publishing new mTLS trust/revocation state so no in-flight connection can
// become a session under the previous snapshot.
func (s *Server) TerminateConnections() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	connections := make([]net.Conn, 0, len(s.conns))
	for conn := range s.conns {
		connections = append(connections, conn)
	}
	s.mu.Unlock()
	for _, conn := range connections {
		_ = conn.Close()
	}
	return len(connections)
}

// SetTaskRuntime attaches the database's bounded automation runtime. Replacing
// one closes the old runtime before returning.
func (s *Server) SetTaskRuntime(tasks *executor.TaskRuntime) {
	if s == nil {
		return
	}
	s.mu.Lock()
	old := s.Tasks
	s.Tasks = tasks
	s.mu.Unlock()
	if old != nil && old != tasks {
		_ = old.Close()
	}
}

func (s *Server) serveConn(ctx context.Context, conn net.Conn) {
	defer func() {
		_ = conn.Close()
		s.mu.Lock()
		delete(s.conns, conn)
		s.nconn--
		s.mu.Unlock()
	}()
	lim := s.Limits.normalized()
	_ = conn.SetDeadline(time.Now().Add(lim.Idle))
	typ, payload, err := ReadFrame(conn, lim.MaxPacket)
	if err != nil {
		return
	}
	if typ != TypeHello {
		s.writeErr(conn, nerr.New(nerr.Protocol, "protocol", "expected hello"), lim)
		return
	}
	hello, err := DecodeHello(payload, lim)
	if err != nil {
		s.writeErr(conn, err, lim)
		return
	}
	if hello.Flags&FlagCancel != 0 {
		s.handleCancel(conn, hello.Secret, lim)
		return
	}
	if hello.Version != 0 && hello.Version != Version {
		s.writeErr(conn, nerr.New(nerr.Protocol, "protocol", "unsupported protocol version"), lim)
		return
	}
	identitySource := "native"
	if s.RequireServiceIdentity {
		identitySource = "mtls+native"
		tlsConn, ok := conn.(*tls.Conn)
		if !ok {
			s.auditAuth(hello.User, false, "mtls", "connect", conn.RemoteAddr().String())
			s.writeErr(conn, nerr.New(nerr.Unauthorized, "protocol", "verified client certificate required"), lim)
			return
		}
		if err := matchServiceIdentity(tlsConn.ConnectionState(), hello.User); err != nil {
			s.auditAuth(hello.User, false, "mtls", "connect", conn.RemoteAddr().String())
			s.writeErr(conn, nerr.New(nerr.Unauthorized, "protocol", "client certificate identity does not match requested user"), lim)
			return
		}
	}
	if s.Database != "" && hello.Database != "" && hello.Database != s.Database {
		s.writeErr(conn, nerr.New(nerr.NotFound, "protocol", "unknown database"), lim)
		return
	}
	secret, err := randomSecret()
	if err != nil {
		return
	}
	method := uint8(AuthPassword)
	if s.RequireClientKey {
		method = AuthPasswordKey
	}
	if err := WriteFrame(conn, TypeHelloOK, EncodeHelloOK(HelloOK{
		Version:    Version,
		AuthMethod: method,
		Secret:     secret,
	}), lim.MaxPacket); err != nil {
		return
	}
	_ = conn.SetDeadline(time.Now().Add(lim.Idle))
	typ, payload, err = ReadFrame(conn, lim.MaxPacket)
	if err != nil {
		return
	}
	if typ != TypeAuth {
		s.writeErr(conn, nerr.New(nerr.Protocol, "protocol", "expected auth"), lim)
		return
	}
	authMsg, err := DecodeAuth(payload, lim)
	if err != nil {
		s.writeErr(conn, err, lim)
		return
	}
	if s.Auth == nil {
		s.writeErr(conn, nerr.New(nerr.Unavailable, "protocol", "authentication is not configured"), lim)
		return
	}
	var tokenClaims *auth.TokenClaims
	if s.Tokens != nil && auth.LooksLikeToken(authMsg.Password) {
		claims, err := s.Tokens.Verify(authMsg.Password)
		if err == nil && !strings.EqualFold(strings.TrimSpace(claims.Principal), strings.TrimSpace(hello.User)) {
			err = nerr.New(nerr.Unauthorized, "protocol", "credential principal mismatch")
		}
		if err == nil && !s.Auth.Has(claims.Principal) {
			err = nerr.New(nerr.Unauthorized, "protocol", "unknown credential principal")
		}
		if err == nil && claims.Database != "" && s.Database != "" && !strings.EqualFold(claims.Database, s.Database) {
			err = nerr.New(nerr.Unauthorized, "protocol", "credential database scope mismatch")
		}
		identitySource = tokenIdentitySource(identitySource)
		if err != nil {
			s.log().Info("authentication failed", "user", hello.User)
			s.auditAuth(hello.User, false, identitySource, "", conn.RemoteAddr().String())
			s.writeErr(conn, nerr.New(nerr.Unauthorized, "protocol", "authentication failed"), lim)
			return
		}
		tokenClaims = claims
	} else if err := s.Auth.Verify(hello.User, authMsg.Password); err != nil {
		s.log().Info("authentication failed", "user", hello.User)
		s.auditAuth(hello.User, false, identitySource, "", conn.RemoteAddr().String())
		s.writeErr(conn, err, lim)
		return
	}
	var tokenRoles []string
	if tokenClaims != nil {
		tokenRoles = tokenClaims.Roles
	}
	if s.ACL != nil && !s.ACL.AllowedScoped(hello.User, tokenRoles, security.PrivConnect, security.ScopeDatabase, s.Database) &&
		!s.ACL.AllowedScoped(hello.User, tokenRoles, security.PrivAdmin, security.ScopeCluster, "") {
		s.log().Info("authentication failed", "user", hello.User)
		s.auditAuth(hello.User, false, identitySource, "connect", conn.RemoteAddr().String())
		s.writeErr(conn, nerr.New(nerr.Forbidden, "protocol", "permission denied"), lim)
		return
	}
	if err := WriteFrame(conn, TypeAuthOK, nil, lim.MaxPacket); err != nil {
		return
	}
	if s.RequireClientKey {
		if err := s.readUnlock(conn, lim); err != nil {
			s.writeErr(conn, err, lim)
			return
		}
	}
	if err := WriteFrame(conn, TypeReady, nil, lim.MaxPacket); err != nil {
		return
	}

	db := s.DatabaseHandle()
	if db == nil {
		s.writeErr(conn, nerr.New(nerr.Unavailable, "protocol", "database is locked"), lim)
		return
	}

	b := &backend{
		secret:   secret,
		user:     hello.User,
		sess:     db.Session(),
		conn:     conn,
		prepared: make(map[uint32]string),
	}
	b.sess.SetLimits(lim.Query)
	b.sess.SetIdentity(hello.User)
	if tokenClaims != nil {
		b.sess.SetAuthRoles(tokenClaims.Roles)
		d := time.Until(tokenClaims.ExpiresAt)
		if d < time.Millisecond {
			d = time.Millisecond
		}
		expiry := time.AfterFunc(d, b.kill)
		defer expiry.Stop()
	}
	b.sess.SetACL(s.ACL)
	b.sess.SetAudit(s.Audit)
	b.sess.SetAuth(s.Auth)
	b.sess.SetRegistry(s.Registry)
	b.sess.SetRemote(conn.RemoteAddr().String())
	if s.Registry != nil {
		b.unreg = s.Registry.Register(hello.User, b.kill)
	}
	s.mu.Lock()
	s.backends[secret] = b
	s.mu.Unlock()
	defer func() {
		if b.unreg != nil {
			b.unreg()
		}
		s.mu.Lock()
		delete(s.backends, secret)
		s.mu.Unlock()
	}()

	s.auditAuth(hello.User, true, identitySource, "", conn.RemoteAddr().String())
	s.log().Info("session authenticated", "user", hello.User)
	for {
		_ = conn.SetDeadline(time.Now().Add(lim.Idle))
		typ, payload, err = ReadFrame(conn, lim.MaxPacket)
		if err != nil {
			return
		}
		switch typ {
		case TypeTerminate:
			return
		case TypeCancel:
			b.requestCancel()
			_ = WriteFrame(conn, TypeReady, nil, lim.MaxPacket)
		case TypeQuery:
			q, err := DecodeQuery(payload, lim)
			if err != nil {
				s.writeErrReady(conn, err, lim)
				continue
			}
			s.runSQL(ctx, conn, b, q.SQL, q.Params, lim)
		case TypeIdempotentQuery:
			q, err := DecodeIdempotentQuery(payload, lim)
			if err != nil {
				s.writeErrReady(conn, err, lim)
				continue
			}
			s.runIdempotentSQL(ctx, conn, b, q, lim)
		case TypePrepare:
			sql, err := DecodePrepare(payload, lim)
			if err != nil {
				s.writeErrReady(conn, err, lim)
				continue
			}
			b.mu.Lock()
			if len(b.prepared) >= lim.MaxPrepared {
				b.mu.Unlock()
				s.writeErrReady(conn, nerr.New(nerr.Exhausted, "protocol", "too many prepared statements"), lim)
				continue
			}
			b.nextStmt++
			id := b.nextStmt
			b.prepared[id] = sql
			b.mu.Unlock()
			if err := WriteFrame(conn, TypePrepareOK, EncodePrepareOK(id), lim.MaxPacket); err != nil {
				return
			}
			if err := WriteFrame(conn, TypeReady, nil, lim.MaxPacket); err != nil {
				return
			}
		case TypeExecute:
			x, err := DecodeExecute(payload, lim)
			if err != nil {
				s.writeErrReady(conn, err, lim)
				continue
			}
			b.mu.Lock()
			sql, ok := b.prepared[x.ID]
			b.mu.Unlock()
			if !ok {
				s.writeErrReady(conn, nerr.New(nerr.NotFound, "protocol", "unknown prepared statement"), lim)
				continue
			}
			s.runSQL(ctx, conn, b, sql, x.Params, lim)
		case TypeCloseStmt:
			id, err := DecodeCloseStmt(payload)
			if err != nil {
				s.writeErrReady(conn, err, lim)
				continue
			}
			b.mu.Lock()
			delete(b.prepared, id)
			b.mu.Unlock()
			if err := WriteFrame(conn, TypeCloseOK, nil, lim.MaxPacket); err != nil {
				return
			}
			if err := WriteFrame(conn, TypeReady, nil, lim.MaxPacket); err != nil {
				return
			}
		case TypeSetReadConsistency:
			m, err := DecodeSetReadConsistency(payload)
			if err != nil {
				s.writeErrReady(conn, err, lim)
				continue
			}
			if err := applyReadConsistency(b.sess, m); err != nil {
				s.writeErrReady(conn, err, lim)
				continue
			}
			if err := WriteFrame(conn, TypeReady, nil, lim.MaxPacket); err != nil {
				return
			}
		case TypeNodeStatus:
			out, err := EncodeNodeStatus(s.nodeStatus(), lim)
			if err != nil {
				s.writeErrReady(conn, err, lim)
				continue
			}
			if err := WriteFrame(conn, TypeNodeStatusResp, out, lim.MaxPacket); err != nil {
				return
			}
			if err := WriteFrame(conn, TypeReady, nil, lim.MaxPacket); err != nil {
				return
			}
		case TypeFlowAck:
			// stray ack; ignore
		default:
			s.writeErrReady(conn, nerr.New(nerr.Protocol, "protocol", "unexpected message type"), lim)
		}
	}
}

func matchServiceIdentity(state tls.ConnectionState, user string) error {
	serviceUser, err := security.ServiceIdentity(state)
	if err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(user), serviceUser) {
		return nerr.New(nerr.Unauthorized, "protocol", "client certificate identity does not match requested user")
	}
	return nil
}

// tokenIdentitySource labels an auth that presented a short-lived credential,
// preserving the mTLS prefix when the connection also carried a client cert.
func tokenIdentitySource(base string) string {
	if base == "mtls+native" {
		return "mtls+token"
	}
	return "token"
}

func (s *Server) auditAuth(actor string, success bool, source, object, remote string) {
	if s.Audit == nil {
		return
	}
	action := security.ActionAuthFailure
	outcome := "failure"
	if success {
		action = security.ActionAuthSuccess
		outcome = "success"
	}
	s.Audit.Record(security.Event{
		Actor: actor, Action: action, Object: object, Outcome: outcome, Remote: remote,
		IdentitySource: source,
	})
}

func applyReadConsistency(sess *executor.Session, m SetReadConsistency) error {
	var mode executor.ReadConsistency
	switch m.Mode {
	case ReadModeStrong:
		mode = executor.ReadStrong
	case ReadModeBounded:
		mode = executor.ReadBounded
	case ReadModeStale:
		mode = executor.ReadStale
	default:
		return nerr.New(nerr.Protocol, "protocol", "unknown read consistency mode")
	}
	sess.SetMaxStaleness(time.Duration(m.MaxStalenessMS) * time.Millisecond)
	return sess.SetReadConsistency(mode)
}

// nodeStatus returns this server node's key-free replication health for
// follower-read routing. A server with no attached cluster reports "standalone".
func (s *Server) nodeStatus() NodeStatus {
	db := s.DatabaseHandle()
	if db == nil {
		return NodeStatus{Role: "shutdown"}
	}
	h, ok := db.ClusterHealth()
	if !ok {
		return NodeStatus{Role: "standalone", HasLeader: true, Healthy: true}
	}
	ns := NodeStatus{
		Role:         h.Role,
		HasLeader:    h.HasLeader,
		Healthy:      h.Healthy,
		AppliedLSN:   uint64(h.AppliedLSN),
		ApplyBacklog: h.ApplyBacklog,
	}
	if h.LastContact == replication.NeverContacted {
		ns.LastContactMS = -1
	} else {
		ns.LastContactMS = h.LastContact.Milliseconds()
	}
	return ns
}

func (s *Server) handleCancel(conn net.Conn, secret uint64, lim Limits) {
	s.mu.Lock()
	b := s.backends[secret]
	s.mu.Unlock()
	if b != nil {
		b.requestCancel()
	}
	_ = WriteFrame(conn, TypeReady, nil, lim.MaxPacket)
}

func (s *Server) runSQL(parent context.Context, conn net.Conn, b *backend, sql string, params []executor.Param, lim Limits) {
	if len(sql) > lim.MaxSQL {
		s.writeErrReady(conn, nerr.New(nerr.Protocol, "protocol", "SQL exceeds limit"), lim)
		return
	}
	ctx, cancel := context.WithCancel(parent)
	b.mu.Lock()
	b.cancel = cancel
	b.queryConn = conn
	b.mu.Unlock()
	defer func() {
		cancel()
		b.mu.Lock()
		b.cancel = nil
		b.queryConn = nil
		b.mu.Unlock()
	}()

	res, err := b.sess.QueryContext(ctx, sql, params)
	_ = conn.SetDeadline(time.Now().Add(lim.Idle))
	if err != nil {
		s.writeErrReady(conn, err, lim)
		return
	}
	if err := s.streamResult(conn, res, lim); err != nil {
		_ = conn.SetDeadline(time.Now().Add(lim.Idle))
		s.writeErrReady(conn, err, lim)
		return
	}
}

func (s *Server) runIdempotentSQL(parent context.Context, conn net.Conn, b *backend, query IdempotentQuery, lim Limits) {
	if len(query.SQL) > lim.MaxSQL {
		s.writeErrReady(conn, nerr.New(nerr.Protocol, "protocol", "SQL exceeds limit"), lim)
		return
	}
	ctx, cancel := context.WithCancel(parent)
	b.mu.Lock()
	b.cancel = cancel
	b.queryConn = conn
	b.mu.Unlock()
	defer func() {
		cancel()
		b.mu.Lock()
		b.cancel = nil
		b.queryConn = nil
		b.mu.Unlock()
	}()

	res, err := b.sess.ExecIdempotent(ctx, query.Key, query.SQL, query.Params)
	_ = conn.SetDeadline(time.Now().Add(lim.Idle))
	if err != nil {
		s.writeErrReady(conn, err, lim)
		return
	}
	if err := s.streamResult(conn, res, lim); err != nil {
		_ = conn.SetDeadline(time.Now().Add(lim.Idle))
		s.writeErrReady(conn, err, lim)
	}
}

func (s *Server) streamResult(conn net.Conn, res *executor.Result, lim Limits) error {
	if res == nil {
		if err := WriteFrame(conn, TypeCommandComplete, EncodeCommandComplete(CommandComplete{}), lim.MaxPacket); err != nil {
			return err
		}
		return WriteFrame(conn, TypeReady, nil, lim.MaxPacket)
	}
	defer res.Close()
	if len(res.Columns) > 0 {
		desc := RowDesc{Columns: make([]Column, len(res.Columns))}
		for i, name := range res.Columns {
			typ := types.String()
			if len(res.Rows) > 0 && i < len(res.Rows[0]) {
				typ = res.Rows[0][i].Typ
			}
			desc.Columns[i] = Column{Name: name, Type: typ}
		}
		payload, err := EncodeRowDesc(desc, lim)
		if err != nil {
			return err
		}
		if err := WriteFrame(conn, TypeRowDesc, payload, lim.MaxPacket); err != nil {
			return err
		}
	}

	var sent int64
	flush := func(rows [][]types.Value) error {
		if len(rows) == 0 {
			return nil
		}
		payload, err := EncodeDataBatch(DataBatch{Rows: rows}, lim)
		if err != nil {
			return err
		}
		sent += int64(len(payload))
		if sent > lim.MaxResultBytes {
			return nerr.New(nerr.Exhausted, "protocol", "result size limit exceeded")
		}
		if err := WriteFrame(conn, TypeDataBatch, payload, lim.MaxPacket); err != nil {
			return err
		}
		return s.waitFlow(conn, lim)
	}

	batch := make([][]types.Value, 0, DefaultBatchRows)
	flushRows := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := flush(batch); err != nil {
			return err
		}
		for i := range batch {
			batch[i] = nil
		}
		batch = batch[:0]
		return nil
	}

	streamed := false
	if nb, err := res.NextBatch(); err != nil {
		return err
	} else if nb != nil && nb.Count > 0 {
		streamed = true
		if err := flush(nb.Rows()); err != nil {
			return err
		}
		for {
			nb, err = res.NextBatch()
			if err != nil {
				return err
			}
			if nb == nil || nb.Count == 0 {
				break
			}
			if err := flush(nb.Rows()); err != nil {
				return err
			}
		}
	}
	if !streamed {
		for i := range res.Rows {
			batch = append(batch, res.Rows[i])
			res.Rows[i] = nil
			if len(batch) >= DefaultBatchRows {
				if err := flushRows(); err != nil {
					return err
				}
			}
		}
		if err := flushRows(); err != nil {
			return err
		}
	}

	if err := WriteFrame(conn, TypeCommandComplete, EncodeCommandComplete(CommandComplete{Affected: res.Affected}), lim.MaxPacket); err != nil {
		return err
	}
	return WriteFrame(conn, TypeReady, nil, lim.MaxPacket)
}

func (s *Server) waitFlow(conn net.Conn, lim Limits) error {
	_ = conn.SetDeadline(time.Now().Add(lim.Idle))
	typ, _, err := ReadFrame(conn, lim.MaxPacket)
	if err != nil {
		return nerr.New(nerr.Canceled, "protocol", "query cancelled")
	}
	switch typ {
	case TypeFlowAck:
		return nil
	case TypeCancel:
		return nerr.New(nerr.Canceled, "protocol", "query cancelled")
	case TypeTerminate:
		return nerr.New(nerr.Canceled, "protocol", "session terminated")
	default:
		return nerr.New(nerr.Protocol, "protocol", "expected flow ack")
	}
}

func (s *Server) writeErr(conn net.Conn, err error, lim Limits) {
	payload, encErr := EncodeError(errorFrom(err), lim)
	if encErr != nil {
		return
	}
	_ = WriteFrame(conn, TypeError, payload, lim.MaxPacket)
}

func (s *Server) writeErrReady(conn net.Conn, err error, lim Limits) {
	s.writeErr(conn, err, lim)
	_ = WriteFrame(conn, TypeReady, nil, lim.MaxPacket)
}

func randomSecret() (uint64, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, err
	}
	n := binary.LittleEndian.Uint64(b[:])
	if n == 0 {
		n = 1
	}
	return n, nil
}
