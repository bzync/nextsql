package ops

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/bzync/nextsql/internal/admin/credential"
	"github.com/bzync/nextsql/internal/admin/profile"
	"github.com/bzync/nextsql/internal/nerr"
)

// maxSwitchUserBytes bounds the NSQL user name on a switch request.
const maxSwitchUserBytes = 128

// handleProfiles is the pre-auth profile list the sign-in page needs to offer
// a server choice. It carries IDs, display names, and environment labels
// only — never an address, a user hint, or a path — so an unauthenticated
// client learns nothing about where the servers are.
func (s *Server) handleProfiles(w http.ResponseWriter, _ *http.Request) {
	out := make([]profile.Public, 0, len(s.profiles))
	for _, p := range s.profiles {
		out = append(out, p.Public())
	}
	writeJSON(w, http.StatusOK, map[string]any{"default": profile.DefaultID, "profiles": out})
}

// handleSessionProfiles is the authenticated profile list the switch dialog
// uses: each profile's address, TLS posture, and user hint, so an operator
// can see where a password would go before typing it. Local file paths are
// never included.
func (s *Server) handleSessionProfiles(w http.ResponseWriter, _ *http.Request, sess *session) {
	out := make([]profile.Detail, 0, len(s.profiles))
	for _, p := range s.profiles {
		out = append(out, p.Detail())
	}
	writeJSON(w, http.StatusOK, map[string]any{"current": s.sessionProfile(sess).ID, "profiles": out})
}

type switchRequest struct {
	Profile  string `json:"profile"`
	User     string `json:"user"`
	Password string `json:"password"`
	// UseSavedPassword signs in with a password this session's principal
	// previously saved for the target; SavePassword stores a typed one after
	// it has been verified by a successful sign-in.
	UseSavedPassword bool `json:"use_saved_password"`
	SavePassword     bool `json:"save_password"`
}

func (r *switchRequest) validate() error {
	r.User = strings.TrimSpace(r.User)
	if r.User == "" {
		return nerr.New(nerr.InvalidArgument, "ops.switch", "user is required")
	}
	if len(r.User) > maxSwitchUserBytes || strings.IndexFunc(r.User, unicode.IsControl) >= 0 {
		return nerr.New(nerr.InvalidArgument, "ops.switch", "user is not a valid NextSQL user name")
	}
	if r.UseSavedPassword == (r.Password != "") {
		return nerr.New(nerr.InvalidArgument, "ops.switch", "enter a password or choose the saved password, not both")
	}
	if r.SavePassword && r.UseSavedPassword {
		return nerr.New(nerr.InvalidArgument, "ops.switch", "only a typed password can be saved")
	}
	return nil
}

// principalOf is the credential-store identity of a session's principal.
func (s *Server) principalOf(sess *session) credential.Principal {
	p := s.sessionProfile(sess)
	return credential.Principal{Address: p.Address, ServerName: p.ServerName(), User: sess.user}
}

func targetOf(p profile.Profile, user string) credential.Target {
	return credential.Target{
		Principal: credential.Principal{Address: p.Address, ServerName: p.ServerName(), User: user},
		Database:  p.Database,
	}
}

// handleSwitch moves the operator to another connection profile (or the same
// one as a different user). It is a fresh sign-in, not a re-point of the
// existing session: the new connection is authenticated first, and only on
// success is a NEW session (new cookie, new CSRF token) swapped in for the
// old one, whose connection is then closed. A failed switch leaves the
// current session exactly as it was. Operations and Studio both follow,
// because they share the session.
func (s *Server) handleSwitch(w http.ResponseWriter, r *http.Request, sess *session) {
	var req switchRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxLoginBody)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := req.validate(); err != nil {
		writeError(w, http.StatusBadRequest, userError(err))
		return
	}
	target, ok := s.lookupProfile(req.Profile)
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown connection profile")
		return
	}
	if sess.busy() {
		writeError(w, http.StatusConflict, "a query is running on this connection; cancel it before switching servers")
		return
	}

	origin := s.principalOf(sess)
	key := credential.DelegationKey(origin, targetOf(target, req.User))
	password := req.Password
	if req.UseSavedPassword {
		saved, err := s.cfg.CredentialStore.Get(key)
		if err != nil || saved == "" {
			writeError(w, http.StatusUnauthorized, "no saved password is available for this server and user from your current sign-in; enter the password")
			return
		}
		password = saved
	}

	base, err := driverConfigFor(target)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ops TLS configuration error")
		s.log.Error("ops switch driver config", "profile", target.ID, "err", err.Error())
		return
	}
	base.User = req.User
	base.Password = password

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	conn, err := s.open(ctx, base)
	if err != nil {
		status, msg := loginErrorStatus(err)
		if req.UseSavedPassword && status == http.StatusUnauthorized {
			// A saved password the server now rejects is stale; keeping it
			// would only fail again. Forget it and say so.
			_ = s.cfg.CredentialStore.Delete(key)
			msg = "the saved password was rejected and has been forgotten; enter the current password"
		}
		writeError(w, status, msg)
		s.log.Info("ops switch failed", "from_profile", sess.profile, "to_profile", target.ID, "user", req.User, "status", status)
		return
	}

	next, err := s.sessions.replace(sess.id, conn, req.User, base.Database, target.ID)
	if err != nil {
		if conn != nil {
			_ = conn.Close()
		}
		if nerr.HasCode(err, nerr.Exhausted) {
			writeError(w, http.StatusServiceUnavailable, "too many active Manager sessions")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not start session")
		return
	}
	setSessionCookie(w, next.id, s.tls)

	view := s.sessionView(next)
	if req.SavePassword {
		if err := s.cfg.CredentialStore.Set(key, password); err != nil {
			view.Warning = "Switched, but the operating-system credential store could not save the password."
			s.log.Warn("ops switch credential save failed", "profile", target.ID, "user", req.User, "err", err.Error())
		} else {
			view.CredentialSaved = true
		}
	}
	s.log.Info("ops switch", "from_profile", sess.profile, "from_user", sess.user,
		"to_profile", target.ID, "user", req.User, "saved_password", req.UseSavedPassword)
	writeJSON(w, http.StatusOK, view)
}

type forgetCredentialRequest struct {
	Profile string `json:"profile"`
	User    string `json:"user"`
}

// handleForgetCredential deletes the password this session's principal saved
// for a profile and user. It only ever reaches the caller's own delegation —
// the key is bound to the current principal — and answers 204 whether or not
// one existed.
func (s *Server) handleForgetCredential(w http.ResponseWriter, r *http.Request, sess *session) {
	var req forgetCredentialRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxLoginBody)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	req.User = strings.TrimSpace(req.User)
	if req.User == "" || len(req.User) > maxSwitchUserBytes {
		writeError(w, http.StatusBadRequest, "user is required")
		return
	}
	target, ok := s.lookupProfile(req.Profile)
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown connection profile")
		return
	}
	_ = s.cfg.CredentialStore.Delete(credential.DelegationKey(s.principalOf(sess), targetOf(target, req.User)))
	w.WriteHeader(http.StatusNoContent)
}
