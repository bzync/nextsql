package manager

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"sync"
	"time"

	nextsql "github.com/bzync/nextsql/drivers/go"
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

	mu       sync.Mutex
	conn     *nextsql.Conn
	lastSeen time.Time
}

// touch updates the idle clock. Caller holds no lock.
func (s *session) touch() {
	s.mu.Lock()
	s.lastSeen = time.Now()
	s.mu.Unlock()
}

func (s *session) expired(now time.Time, idle, lifetime time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return now.Sub(s.lastSeen) > idle || now.Sub(s.createdAt) > lifetime
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
}

// query runs sql on the session's connection and renders the rows generically.
func (s *session) query(ctx context.Context, sql string, params ...types.Value) (resultJSON, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return resultJSON{}, nerr.New(nerr.Unavailable, "manager.session", "session connection is closed")
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
	out := resultJSON{Columns: append([]string{}, rows.Columns()...), Rows: [][]*string{}}
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

func (s *session) close() {
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
		return nil, nerr.New(nerr.Exhausted, "manager.sessionStore",
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
		return "", nerr.Wrap(nerr.Internal, "manager", "generate token", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
