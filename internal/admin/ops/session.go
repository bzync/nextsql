package ops

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"sync"
	"time"

	nextsql "github.com/bzync/nextsql/drivers/go"
	"github.com/bzync/nextsql/internal/admin/studio"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

// session is one logged-in operator. It owns a live driver connection to
// nextsqld opened with that operator's own credentials; every query the
// Manager runs for this session goes through it, so server-side RBAC applies.
// A Conn is not safe for concurrent queries, so callers hold mu.
type session struct {
	id        string
	csrf      string
	user      string
	database  string
	realm     string
	createdAt time.Time

	// mu serializes use of conn. Operations read-models wait their turn;
	// Studio execution uses TryLock so a second editor run fails fast rather
	// than growing an unbounded request queue behind a long query.
	mu   sync.Mutex
	conn *nextsql.Conn

	// stateMu is deliberately separate from mu: an authenticated cancellation
	// request must be able to refresh the idle clock and cancel a query while
	// that query owns the connection lock. It also guards realm/database and
	// the read-consistency mode, which Studio session-control requests can
	// change while a read-model handler is reading them.
	stateMu     sync.Mutex
	lastSeen    time.Time
	inFlight    int
	activeQuery *activeStudioQuery
	// readMode is "" (⇒ strong), "bounded", or "stale"; readMaxStalenessMS
	// is the BOUNDED freshness bound in milliseconds (0 ⇒ server default).
	readMode           string
	readMaxStalenessMS int64
}

// target returns the realm and database the session's connection is
// currently bound to. Guarded by stateMu because reconnect mutates them.
func (s *session) target() (realm, database string) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.realm, s.database
}

// readConsistency reports the session's current read-consistency mode. An
// empty stored mode is reported as the default, studio.ReadStrong.
func (s *session) readConsistency() (mode string, maxStalenessMS int64) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.readMode == "" {
		return studio.ReadStrong, 0
	}
	return s.readMode, s.readMaxStalenessMS
}

// setReadConsistency applies a read-consistency change to the live
// connection (a session-control message, not a reconnect) and records it for
// display. It fails fast (nerr.Conflict) if a query is in flight, and leaves
// the recorded mode untouched if the wire call fails.
func (s *session) setReadConsistency(mode nextsql.ReadConsistency, maxStaleness time.Duration, modeStr string, maxStalenessMS int64) error {
	if !s.mu.TryLock() {
		return nerr.New(nerr.Conflict, "ops.session.setReadConsistency",
			"this connection is busy; cancel the running query first")
	}
	defer s.mu.Unlock()
	if s.conn == nil {
		return nerr.New(nerr.Unavailable, "ops.session.setReadConsistency", "session connection is closed")
	}
	if err := s.conn.SetReadConsistency(context.Background(), mode, maxStaleness); err != nil {
		return err
	}
	s.stateMu.Lock()
	s.readMode = modeStr
	s.readMaxStalenessMS = maxStalenessMS
	s.stateMu.Unlock()
	return nil
}

// reconnect atomically replaces the session's driver connection with an
// already-opened one bound to a different realm/database (same nextsqld,
// same NSQL user). The caller opens newConn first; reconnect installs it and
// closes the old one only on success, so a failed switch leaves the session
// exactly as it was. It fails fast (nerr.Conflict) if a Studio query is
// in flight — the switch must not race an active query on the old
// connection.
func (s *session) reconnect(newConn *nextsql.Conn, realm, database string) error {
	if !s.mu.TryLock() {
		return nerr.New(nerr.Conflict, "ops.session.reconnect",
			"this connection is busy; cancel the running query before switching")
	}
	defer s.mu.Unlock()
	old := s.conn
	s.conn = newConn
	s.stateMu.Lock()
	s.realm = realm
	s.database = database
	// A freshly opened connection is STRONG; the previous connection's
	// read-consistency choice does not carry over.
	s.readMode = ""
	s.readMaxStalenessMS = 0
	s.stateMu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	return nil
}

type activeStudioQuery struct {
	id     string
	cancel context.CancelFunc
}

// touch updates the idle clock. Caller holds no lock.
func (s *session) touch() {
	s.stateMu.Lock()
	s.lastSeen = time.Now()
	s.stateMu.Unlock()
}

// beginRequest marks one authenticated request as in flight and refreshes the
// idle clock; endRequest clears it and restarts the clock from the moment the
// request finished. A session with a request in flight is never *idle*: an
// Operations action can legitimately outlive the idle timeout (BACKUP DATABASE
// is allowed 30 minutes, a cluster drain up to its own timeout) while the SPA
// sends nothing else, because the view keeps the originating button disabled
// for the whole call and auto-refresh is off by default. Without this, the
// sweeper evicted the session mid-action and the operator was logged out the
// moment their backup returned.
//
// The absolute session lifetime is deliberately *not* held off this way — it
// is a security bound, not an activity measure.
func (s *session) beginRequest() {
	s.stateMu.Lock()
	s.inFlight++
	s.lastSeen = time.Now()
	s.stateMu.Unlock()
}

func (s *session) endRequest() {
	s.stateMu.Lock()
	if s.inFlight > 0 {
		s.inFlight--
	}
	s.lastSeen = time.Now()
	s.stateMu.Unlock()
}

func (s *session) expired(now time.Time, idle, lifetime time.Duration) bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if now.Sub(s.createdAt) > lifetime {
		return true
	}
	return s.inFlight == 0 && now.Sub(s.lastSeen) > idle
}

// resultJSON is the generic shape every result set is rendered as: string (or
// null) cells only, so the UI never decodes types and the Manager never
// reinterprets a server value. Affected carries a statement's row/unit count
// for the statements that report one without returning any columns (ANALYZE,
// MAINTAIN, REBUILD INDEX all resolve to just an affected count) — omitted
// for a result that has real columns instead.
type resultJSON struct {
	Columns  []string    `json:"columns"`
	Rows     [][]*string `json:"rows"`
	Affected int64       `json:"affected,omitempty"`

	// columnTypes stays internal so the established Operations API shape is
	// unchanged. Studio's metadata adapter uses it for typed rendering.
	columnTypes []string
}

// query runs sql on the session's connection and renders the rows generically.
func (s *session) query(ctx context.Context, sql string, params ...types.Value) (resultJSON, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return resultJSON{}, nerr.New(nerr.Unavailable, "ops.session", "session connection is closed")
	}
	rows, err := s.conn.Query(ctx, sql, params...)
	if err != nil {
		return resultJSON{}, err
	}
	defer rows.Close()

	// Columns/Rows are initialized non-nil even when empty: a statement like
	// ANALYZE/MAINTAIN/REBUILD INDEX that reports only an affected count has
	// zero columns from the wire, and Go's nil-slice JSON encoding would
	// otherwise render "columns":null — the frontend's ResultSet.columns is
	// typed as a plain (non-nullable) array, so that would be a latent
	// null-vs-[] contract break for any future caller that renders an
	// action's result through the same ResultTable bundle tables use.
	colTypes := rows.ColumnTypes()
	typeNames := make([]string, len(colTypes))
	for i := range colTypes {
		typeNames[i] = colTypes[i].String()
	}
	out := resultJSON{
		Columns:     append([]string{}, rows.Columns()...),
		Rows:        [][]*string{},
		columnTypes: typeNames,
	}
	for rows.Next() {
		vals := rows.Values()
		rec := make([]*string, len(vals))
		for i, v := range vals {
			if v.Null {
				rec[i] = nil
				continue
			}
			str := v.String()
			rec[i] = &str
		}
		out.Rows = append(out.Rows, rec)
	}
	if err := rows.Err(); err != nil {
		return resultJSON{}, err
	}
	out.Affected = rows.Affected()
	return out, nil
}

// studioQuery executes one editor request through the same authenticated
// official-driver connection Operations mode already owns. It fails fast if
// the connection is busy, registers a client-chosen query id before touching
// the network, and delegates result bounding to studio.Collect.
func (s *session) studioQuery(ctx context.Context, queryID, sql string, params []types.Value) (studio.ResultSet, error) {
	if !s.mu.TryLock() {
		return studio.ResultSet{}, nerr.New(nerr.Conflict, "ops.session.studioQuery",
			"this connection is already running a query")
	}
	defer s.mu.Unlock()
	if s.conn == nil {
		return studio.ResultSet{}, nerr.New(nerr.Unavailable, "ops.session.studioQuery",
			"session connection is closed")
	}

	queryCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := s.beginStudioQuery(queryID, cancel); err != nil {
		return studio.ResultSet{}, err
	}
	defer s.finishStudioQuery(queryID)

	started := time.Now()
	rows, err := s.conn.Query(queryCtx, sql, params...)
	if err != nil {
		return studio.ResultSet{}, err
	}
	out, err := studio.Collect(queryCtx, rows, cancel)
	out.ElapsedMS = time.Since(started).Milliseconds()
	return out, err
}

// studioStreamQuery is the progressive counterpart to studioQuery. It keeps
// the same one-query-per-session and cancellation ownership rules, but emits
// bounded result batches as the official driver releases them instead of
// retaining the whole preview in Admin first.
func (s *session) studioStreamQuery(ctx context.Context, queryID, sql string, params []types.Value, emit func(studio.StreamFrame) error) error {
	if !s.mu.TryLock() {
		return nerr.New(nerr.Conflict, "ops.session.studioStreamQuery",
			"this connection is already running a query")
	}
	defer s.mu.Unlock()
	if s.conn == nil {
		return nerr.New(nerr.Unavailable, "ops.session.studioStreamQuery",
			"session connection is closed")
	}

	queryCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := s.beginStudioQuery(queryID, cancel); err != nil {
		return err
	}
	defer s.finishStudioQuery(queryID)

	rows, err := s.conn.Query(queryCtx, sql, params...)
	if err != nil {
		return err
	}
	return studio.Stream(queryCtx, rows, cancel, emit)
}

func (s *session) beginStudioQuery(queryID string, cancel context.CancelFunc) error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.activeQuery != nil {
		return nerr.New(nerr.Conflict, "ops.session.beginStudioQuery", "a Studio query is already active")
	}
	s.activeQuery = &activeStudioQuery{id: queryID, cancel: cancel}
	return nil
}

func (s *session) finishStudioQuery(queryID string) {
	s.stateMu.Lock()
	if s.activeQuery != nil && s.activeQuery.id == queryID {
		s.activeQuery = nil
	}
	s.stateMu.Unlock()
}

// cancelStudioQuery cancels only the matching query in this browser session.
// It never consults or reaches another session's active query record.
func (s *session) cancelStudioQuery(queryID string) bool {
	s.stateMu.Lock()
	var cancel context.CancelFunc
	if s.activeQuery != nil && s.activeQuery.id == queryID {
		cancel = s.activeQuery.cancel
	}
	s.stateMu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

// ping checks that the session's driver connection can still talk to
// nextsqld. A query already in flight means the connection is in use, so
// ping reports healthy without interrupting it. A closed or dead connection
// returns Unavailable. Callers must not treat ping failure as session
// expiry — the admin cookie is independent of the nextsqld socket.
func (s *session) ping(ctx context.Context) error {
	if !s.mu.TryLock() {
		return nil
	}
	defer s.mu.Unlock()
	if s.conn == nil {
		return nerr.New(nerr.Unavailable, "ops.session.ping", "session connection is closed")
	}
	rows, err := s.conn.Query(ctx, "SELECT 1")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
	}
	return rows.Err()
}

func (s *session) close() {
	s.stateMu.Lock()
	var cancel context.CancelFunc
	if s.activeQuery != nil {
		cancel = s.activeQuery.cancel
	}
	s.stateMu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.mu.Lock()
	if s.conn != nil {
		_ = s.conn.Close()
		s.conn = nil
	}
	s.mu.Unlock()
}

// sessionStore is a bounded, self-expiring in-memory set of sessions. It owns
// one sweeper goroutine, stopped by close.
type sessionStore struct {
	max      int
	idle     time.Duration
	lifetime time.Duration

	mu       sync.Mutex
	byID     map[string]*session
	stopOnce sync.Once
	stop     chan struct{}
}

func newSessionStore(max int, idle, lifetime time.Duration) *sessionStore {
	st := &sessionStore{
		max:      max,
		idle:     idle,
		lifetime: lifetime,
		byID:     make(map[string]*session),
		stop:     make(chan struct{}),
	}
	go st.sweepLoop()
	return st
}

// create registers a new session for an already-open connection. It returns
// nerr.Exhausted when the store is full.
func (st *sessionStore) create(conn *nextsql.Conn, user, database, realm string) (*session, error) {
	id, err := randToken()
	if err != nil {
		return nil, err
	}
	csrf, err := randToken()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	s := &session{
		id: id, csrf: csrf, user: user, database: database, realm: realm,
		createdAt: now, lastSeen: now, conn: conn,
	}

	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.byID) >= st.max {
		return nil, nerr.New(nerr.Exhausted, "ops.sessionStore",
			"the maximum number of Manager sessions is already active")
	}
	st.byID[id] = s
	return s, nil
}

// get returns the session for id after checking it against expiry. A missing
// or expired session returns nil (and is evicted if expired).
func (st *sessionStore) get(id string) *session {
	if id == "" {
		return nil
	}
	st.mu.Lock()
	s, ok := st.byID[id]
	st.mu.Unlock()
	if !ok {
		return nil
	}
	if s.expired(time.Now(), st.idle, st.lifetime) {
		st.remove(id)
		return nil
	}
	return s
}

func (st *sessionStore) remove(id string) {
	st.mu.Lock()
	s, ok := st.byID[id]
	if ok {
		delete(st.byID, id)
	}
	st.mu.Unlock()
	if ok {
		s.close()
	}
}

func (st *sessionStore) len() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	return len(st.byID)
}

func (st *sessionStore) sweepLoop() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-st.stop:
			return
		case <-t.C:
			st.sweep()
		}
	}
}

func (st *sessionStore) sweep() {
	now := time.Now()
	var dead []*session
	st.mu.Lock()
	for id, s := range st.byID {
		if s.expired(now, st.idle, st.lifetime) {
			dead = append(dead, s)
			delete(st.byID, id)
		}
	}
	st.mu.Unlock()
	for _, s := range dead {
		s.close()
	}
}

func (st *sessionStore) close() {
	st.stopOnce.Do(func() { close(st.stop) })
	st.mu.Lock()
	all := make([]*session, 0, len(st.byID))
	for id, s := range st.byID {
		all = append(all, s)
		delete(st.byID, id)
	}
	st.mu.Unlock()
	for _, s := range all {
		s.close()
	}
}

// checkCSRF compares a supplied token against the session's in constant time.
func (s *session) checkCSRF(token string) bool {
	return token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(s.csrf)) == 1
}

func randToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nerr.Wrap(nerr.Internal, "ops", "generate token", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
