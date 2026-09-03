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
	"github.com/bzync/nextsql/internal/dbmanager"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/hosting"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/replication"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/types"
)

// Server accepts native-protocol connections against one local database.
type Server struct {
	DB     *executor.DB
	Auth   *auth.Store
	Tokens *auth.TokenVerifier
	// TokenIdentitySourceHints maps a successfully verified credential's NSTK
	// key id to an audit-only source. Only "oidc" is recognized. Unverified
	// credentials and unknown values always retain the generic token label.
	TokenIdentitySourceHints map[uint32]string
	ACL                      *security.ACL
	Audit                    *security.Log
	Registry                 *security.Registry
	TLS                      *tls.Config
	Limits                   Limits
	Log                      *slog.Logger
	Database                 string
	// Realm is the hosted realm this Server serves, if any (M2-2). Empty
	// means "don't care", mirroring Database's shape. When Databases is nil
	// this is pure identity validation against a flat configured name; when
	// Databases is set, a matching non-default Hello.Realm/Database can
	// route to a different open database (M2-3a). Not realm-scoped auth
	// (M2-4); do not confuse with the unrelated Registry field above
	// (*security.Registry, RBAC/audit).
	Realm string
	Tasks *executor.TaskRuntime
	// Databases resolves a Hello-selected realm/database to an open
	// *executor.DB on demand (M2-3a), bounded to a small fixed number of
	// distinct open databases; a database closes once idle and reopens on
	// demand (M2-3b-1). Nil (the default) means "no manager configured":
	// every connection uses DB, byte-for-byte the pre-M2-3a behavior.
	Databases *dbmanager.Manager
	// HostingRegistry backs system.realms/system.databases (M2-4a),
	// read-only. Nil on a legacy/non-hosted deployment — those views then
	// return zero rows, never an error.
	HostingRegistry *hosting.Registry

	RequireClientKey bool
	// RequireServiceIdentity binds the verified client-certificate URI
	// nextsql://service/<principal> to Hello.User before password/RBAC checks.
	RequireServiceIdentity bool
	Unlock                 func(*crypto.DEK) error
	// DrainTimeout, when positive, makes context cancellation passed to Serve
	// trigger Drain(DrainTimeout) instead of an immediate Close. Zero (the
	// default) preserves the pre-existing immediate-hard-close behavior.
	DrainTimeout time.Duration

	mu         sync.Mutex
	ln         net.Listener
	conns      map[net.Conn]struct{}
	backends   map[uint64]*backend
	nconn      int
	userConns  map[string]int
	dbConns    map[dbConnKey]int
	realmConns map[string]int
	closed     bool
	draining   bool
}

// dbConnKey identifies one (realm, database) pair for MaxSessionsPerDatabase
// accounting. A legacy/non-hosted deployment always uses the same pinned
// pair, so the map never grows past one entry there.
type dbConnKey struct {
	realm, database string
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

// idleDeadline is how long the connection may wait for its next frame. It is
// normally lim.Idle; while sess has an open transaction and lim.IdleTxn is
// set, the (typically tighter) IdleTxn bound applies instead, so a
// transaction can never outlive Idle even when IdleTxn is misconfigured
// larger than it.
func idleDeadline(lim Limits, sess *executor.Session) time.Duration {
	d := lim.Idle
	if lim.IdleTxn > 0 && sess != nil && sess.InTxn() && (d <= 0 || lim.IdleTxn < d) {
		d = lim.IdleTxn
	}
	return d
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
		if s.DrainTimeout > 0 {
			s.Drain(s.DrainTimeout)
			return
		}
		_ = s.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed || s.draining
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
	if db != nil {
		db.SetDatabaseName(s.Database)
	}
	s.mu.Lock()
	s.DB = db
	s.mu.Unlock()
}

// SetDatabaseManager installs the bounded database manager (M2-3a) a
// connection's Hello.Realm/Hello.Database routes through. Nil disables
// routing: every connection uses DB, the pre-M2-3a behavior.
func (s *Server) SetDatabaseManager(m *dbmanager.Manager) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.Databases = m
	s.mu.Unlock()
}

func (s *Server) databaseManager() *dbmanager.Manager {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Databases
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

// Drain performs a graceful shutdown for planned maintenance: it stops
// accepting new connections immediately, then closes each existing
// connection as soon as it is idle (no in-flight statement and no open
// transaction), so an operator-triggered restart or rolling upgrade does not
// force-abort a transaction that is mid-flight. Any connection still busy
// once timeout elapses is force-closed, the same as Close. Drain blocks
// until every connection is closed or timeout elapses. Calling it more than
// once, or calling it after Close, is a safe no-op.
func (s *Server) Drain(timeout time.Duration) {
	s.mu.Lock()
	if s.closed || s.draining {
		s.mu.Unlock()
		return
	}
	s.draining = true
	ln := s.ln
	s.mu.Unlock()
	if ln != nil {
		_ = ln.Close()
	}
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if s.closeIdleConnections() == 0 || !time.Now().Before(deadline) {
			break
		}
		<-ticker.C
	}
	_ = s.Close()
}

// closeIdleConnections closes every connection that is either not yet
// authenticated (no backend session exists to lose work from) or whose
// backend session has no in-flight statement, no open transaction, and no
// response still being written back to the client. It returns the number of
// backend sessions still busy (and therefore left open) at the moment it
// ran.
//
// The response-in-flight check (b.queryConn != nil) matters beyond the
// statement/transaction state above: Session.CurrentQuery's "running" flag
// clears the instant QueryContext/ExecContext returns to runSQL, which is
// before runSQL's deferred cleanup writes the response frame(s) back over
// the wire — so without it, a connection can be reported idle (and get its
// raw net.Conn hard-closed here) while its own just-finished statement's
// response is still being flushed, corrupting that write. This is most
// likely to matter for the connection that itself issued the CLUSTER DRAIN
// that triggered this drain: the trigger and the race window are otherwise
// synchronous. b.queryConn is set before dispatch and cleared only after
// the response write completes, so checking it closes that window.
func (s *Server) closeIdleConnections() int {
	s.mu.Lock()
	busy := make(map[net.Conn]struct{}, len(s.backends))
	for _, b := range s.backends {
		if b.sess == nil {
			continue
		}
		b.mu.Lock()
		writing := b.queryConn != nil
		b.mu.Unlock()
		_, _, _, running := b.sess.CurrentQuery()
		_, _, _, _, active := b.sess.TxnSnapshot()
		if writing || running || active {
			busy[b.conn] = struct{}{}
		}
	}
	idle := make([]net.Conn, 0, len(s.conns))
	for conn := range s.conns {
		if _, ok := busy[conn]; !ok {
			idle = append(idle, conn)
		}
	}
	s.mu.Unlock()
	for _, conn := range idle {
		_ = conn.Close()
	}
	return len(busy)
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
	// The flat s.Realm/s.Database equality checks and the LookupRealm call
	// below all decide whether the Hello names a realm/database this server
	// actually has — but none of that may be disclosed before credentials
	// are checked: a Hello is the first frame from an entirely unauthenticated
	// peer, and returning a distinguishing NotFound here (before HelloOK is
	// even sent, let alone a password read) is a credential-free oracle for
	// probing which realm/database names exist. So none of these checks
	// return early. identityOK collects whether the requested realm/database
	// actually resolve; the deferred verification below (after the Auth
	// frame is read) folds a false identityOK into the exact same generic
	// "authentication failed" outcome — same message, same nerr code, and
	// (since it still runs the real or dummy password-hash comparison) the
	// same cost — as a valid realm/database with a wrong password, so an
	// unauthenticated prober cannot distinguish "wrong password" from
	// "no such realm" / "no such database" by response content or timing.
	// realmID always resolves to *some* value (hosting.ID{} when the lookup
	// fails) purely so the verification call below has a value to run
	// against; identityOK, not realmID's specific value, is what determines
	// the final outcome, so an invalid realm can never authenticate via
	// realmID's accidental fallback to the deployment-wide (hosting.ID{})
	// namespace.
	identityOK := true
	if s.HostingRegistry == nil && s.Realm != "" && hello.Realm != "" && hello.Realm != s.Realm {
		identityOK = false
	}
	if s.databaseManager() == nil && s.Database != "" && hello.Database != "" && hello.Database != s.Database {
		identityOK = false
	}
	realmName := hello.Realm
	if realmName == "" {
		realmName = s.Realm
	}
	dbName := hello.Database
	if dbName == "" {
		dbName = s.Database
	}
	var realmID hosting.ID
	if s.HostingRegistry != nil {
		if realm, lerr := s.HostingRegistry.LookupRealm(realmName); lerr == nil {
			realmID = realm.ID
		} else {
			identityOK = false
		}
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
	var authErr error
	if s.Tokens != nil && auth.LooksLikeToken(authMsg.Password) {
		baseIdentitySource := identitySource
		identitySource = tokenIdentitySource(baseIdentitySource, 0, false, nil)
		claims, verr := s.Tokens.Verify(authMsg.Password)
		if verr == nil {
			identitySource = tokenIdentitySource(baseIdentitySource, claims.KeyID, true, s.TokenIdentitySourceHints)
		}
		if verr == nil && !strings.EqualFold(strings.TrimSpace(claims.Principal), strings.TrimSpace(hello.User)) {
			verr = nerr.New(nerr.Unauthorized, "protocol", "credential principal mismatch")
		}
		if verr == nil && !s.Auth.HasInRealm(realmID, claims.Principal) {
			verr = nerr.New(nerr.Unauthorized, "protocol", "unknown credential principal")
		}
		if verr == nil && claims.Database != "" && s.Database != "" && !strings.EqualFold(claims.Database, s.Database) {
			verr = nerr.New(nerr.Unauthorized, "protocol", "credential database scope mismatch")
		}
		if verr == nil && claims.Realm != "" && !strings.EqualFold(claims.Realm, realmName) {
			verr = nerr.New(nerr.Unauthorized, "protocol", "credential realm scope mismatch")
		}
		if verr != nil {
			authErr = nerr.New(nerr.Unauthorized, "protocol", "authentication failed")
		} else {
			tokenClaims = claims
		}
	} else {
		authErr = s.Auth.VerifyInRealm(realmID, hello.User, authMsg.Password)
	}
	// identityOK is folded in here, after the real (or dummy, on a bad
	// username) password-hash comparison already ran above, rather than
	// short-circuited earlier — see the identityOK comment above for why.
	if authErr == nil && !identityOK {
		authErr = nerr.New(nerr.Unauthorized, "protocol", "authentication failed")
	}
	if authErr != nil {
		s.log().Info("authentication failed", "user", hello.User)
		s.auditAuth(hello.User, false, identitySource, "", conn.RemoteAddr().String())
		s.writeErr(conn, authErr, lim)
		return
	}
	var tokenRoles []string
	if tokenClaims != nil {
		tokenRoles = tokenClaims.Roles
	}
	if s.ACL != nil && !s.ACL.AllowedScopedInRealm(realmID, hello.User, tokenRoles, security.PrivConnect, security.ScopeDatabase, s.Database) &&
		!s.ACL.AllowedScopedInRealm(realmID, hello.User, tokenRoles, security.PrivAdmin, security.ScopeCluster, "") {
		s.log().Info("authentication failed", "user", hello.User)
		s.auditAuth(hello.User, false, identitySource, "connect", conn.RemoteAddr().String())
		s.writeErr(conn, nerr.New(nerr.Forbidden, "protocol", "permission denied"), lim)
		return
	}
	if lim.MaxSessionsPerUser > 0 {
		s.mu.Lock()
		if s.userConns == nil {
			s.userConns = make(map[string]int)
		}
		if s.userConns[hello.User] >= lim.MaxSessionsPerUser {
			s.mu.Unlock()
			s.auditAuth(hello.User, false, identitySource, "connect", conn.RemoteAddr().String())
			s.writeErr(conn, nerr.New(nerr.Exhausted, "protocol", "too many connections for user"), lim)
			return
		}
		s.userConns[hello.User]++
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			s.userConns[hello.User]--
			if s.userConns[hello.User] <= 0 {
				delete(s.userConns, hello.User)
			}
			s.mu.Unlock()
		}()
	}
	// MaxSessionsPerDatabase/MaxSessionsPerRealm (P27's own last open
	// exit-gate item): realmName/dbName are already resolved above and, by
	// this point, already passed the identityOK fold into authErr — an
	// unresolved realm/database never reaches here — so there is nothing
	// new to disclose by keying on them (the client supplied both in its
	// own Hello). Checked and released exactly like MaxSessionsPerUser.
	if lim.MaxSessionsPerDatabase > 0 || lim.MaxSessionsPerRealm > 0 {
		key := dbConnKey{realm: realmName, database: dbName}
		s.mu.Lock()
		if lim.MaxSessionsPerDatabase > 0 && s.dbConns[key] >= lim.MaxSessionsPerDatabase {
			s.mu.Unlock()
			s.auditAuth(hello.User, false, identitySource, "connect", conn.RemoteAddr().String())
			s.writeErr(conn, nerr.New(nerr.Exhausted, "protocol", "too many connections for database"), lim)
			return
		}
		if lim.MaxSessionsPerRealm > 0 && s.realmConns[realmName] >= lim.MaxSessionsPerRealm {
			s.mu.Unlock()
			s.auditAuth(hello.User, false, identitySource, "connect", conn.RemoteAddr().String())
			s.writeErr(conn, nerr.New(nerr.Exhausted, "protocol", "too many connections for realm"), lim)
			return
		}
		if lim.MaxSessionsPerDatabase > 0 {
			if s.dbConns == nil {
				s.dbConns = make(map[dbConnKey]int)
			}
			s.dbConns[key]++
		}
		if lim.MaxSessionsPerRealm > 0 {
			if s.realmConns == nil {
				s.realmConns = make(map[string]int)
			}
			s.realmConns[realmName]++
		}
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			if lim.MaxSessionsPerDatabase > 0 {
				s.dbConns[key]--
				if s.dbConns[key] <= 0 {
					delete(s.dbConns, key)
				}
			}
			if lim.MaxSessionsPerRealm > 0 {
				s.realmConns[realmName]--
				if s.realmConns[realmName] <= 0 {
					delete(s.realmConns, realmName)
				}
			}
			s.mu.Unlock()
		}()
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

	// Resolved (and, for a manager-routed connection, admitted/opened)
	// before TypeReady is sent: TypeReady is the wire protocol's definitive
	// "handshake succeeded" signal (drivers/go's Conn.handshake reads it
	// and returns success with no further reads), so any failure to
	// resolve a database must be reported before it, not after — a client
	// that already saw TypeReady never checks for a later error frame.
	var db *executor.DB
	var releaseDB func()
	if mgr := s.databaseManager(); mgr != nil {
		db, releaseDB, err = mgr.Acquire(realmName, dbName)
		if err != nil {
			s.writeErr(conn, err, lim)
			return
		}
	} else {
		db = s.DatabaseHandle()
		if db == nil {
			s.writeErr(conn, nerr.New(nerr.Unavailable, "protocol", "database is locked"), lim)
			return
		}
		db.SetDatabaseName(s.Database)
	}

	if err := WriteFrame(conn, TypeReady, nil, lim.MaxPacket); err != nil {
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
	b.sess.SetTxnTimeout(lim.TxnTimeout)
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
	b.sess.SetHostingRegistry(s.HostingRegistry)
	b.sess.SetRealmID(realmID)
	b.sess.SetRemote(conn.RemoteAddr().String())
	if s.Registry != nil {
		b.unreg = s.Registry.Register(hello.User, b.kill)
	}
	sessID := db.RegisterSession(b.sess)
	s.mu.Lock()
	s.backends[secret] = b
	s.mu.Unlock()
	defer func() {
		// A connection can end here with a transaction still open — an
		// idle-in-transaction timeout, a forced drain close once the drain
		// deadline elapses (see closeIdleConnections), or a bare client
		// disconnect. None of those paths issue a ROLLBACK, and nothing else
		// releases the transaction's locks, so it would otherwise stay open
		// (and its locks held) for as long as the *Session survives. Force it
		// closed here so a torn-down connection never leaks an open
		// transaction.
		if b.sess.InTxn() {
			_ = b.sess.Abort()
		}
		db.UnregisterSession(sessID)
		if releaseDB != nil {
			releaseDB()
		}
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
		_ = conn.SetDeadline(time.Now().Add(idleDeadline(lim, b.sess)))
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
			out, err := EncodeNodeStatus(s.nodeStatus(db), lim)
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
// The OIDC label is selected only from an operator-configured key id after the
// credential's signature has verified; no client-controlled claim is trusted.
func tokenIdentitySource(base string, keyID uint32, verified bool, hints map[uint32]string) string {
	source := "token"
	if verified && hints[keyID] == "oidc" {
		source = "oidc"
	}
	if base == "mtls+native" {
		return "mtls+" + source
	}
	return source
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

// nodeStatus returns db's replication health for follower-read routing (db
// is the connection's own resolved database — M2-3a routes different
// connections to different databases, so this must report the connection's
// database, not always the primary). A server with no attached cluster
// reports "standalone".
func (s *Server) nodeStatus(db *executor.DB) NodeStatus {
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
