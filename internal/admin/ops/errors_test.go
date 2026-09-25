package ops

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

// A dead nextsqld connection surfaces as a framing error that names an
// internal routine and carries both ends of the TCP connection. Neither is
// actionable, and the ephemeral local port is not something Admin means to
// publish — system.replication.leader_addr is "[redacted]" for the same
// reason. The operator gets one clear sentence instead.
func TestUserErrorCollapsesTransportFailures(t *testing.T) {
	wire := nerr.Wrap(nerr.IO, "protocol.WriteFrame", "frame",
		errors.New("write tcp 127.0.0.1:44496->127.0.0.1:7310: write: broken pipe"))
	got := userError(wire)
	if got != unreachableMessage {
		t.Fatalf("userError(transport) = %q, want the unreachable message", got)
	}
	for _, leak := range []string{"44496", "7310", "WriteFrame", "broken pipe"} {
		if strings.Contains(got, leak) {
			t.Errorf("transport message leaks %q: %s", leak, got)
		}
	}

	// The framing error is wrapped by the driver call that noticed it, so the
	// chain has to be walked rather than only the outermost error inspected.
	nested := nerr.Wrap(nerr.IO, "nextsql.Query", "exec", wire)
	if got := userError(nested); got != unreachableMessage {
		t.Fatalf("userError(wrapped transport) = %q, want the unreachable message", got)
	}
	if got := userError(nerr.Wrap(nerr.IO, "nextsql.Open", "dial", errors.New("connection refused"))); got != unreachableMessage {
		t.Fatalf("userError(dial) = %q, want the unreachable message", got)
	}
}

// A server-side fault also arrives as nerr.IO. Collapsing it into "cannot
// reach nextsqld" would point the operator at the wrong subsystem, so the
// framing operation — not the code — is what decides.
func TestUserErrorKeepsNonTransportErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"unknown table", nerr.New(nerr.NotFound, "nextsql", "unknown table: nope"), "unknown table: nope"},
		{"syntax", nerr.New(nerr.Syntax, "nextsql", "expected a statement"), "expected a statement"},
		{"duplicate", nerr.New(nerr.AlreadyExists, "nextsql", "duplicate key"), "duplicate key"},
		{"server disk fault", nerr.Wrap(nerr.IO, "storage.WritePage", "write", errors.New("no space left on device")), "no space left on device"},
	} {
		got := userError(tc.err)
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: userError = %q, want it to contain %q", tc.name, got, tc.want)
		}
		if got == unreachableMessage {
			t.Errorf("%s: was wrongly collapsed to the unreachable message", tc.name)
		}
	}
}

// Socket redaction is defense in depth for any path that reports a
// non-transport error whose cause still carries an OpError.
func TestRedactSockets(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"write tcp 127.0.0.1:44496->127.0.0.1:7310: broken pipe", "write tcp [redacted]: broken pipe"},
		{"dial tcp4 10.0.0.1:1->10.0.0.2:2: refused", "dial tcp [redacted]: refused"},
		{"nothing to redact here", "nothing to redact here"},
	} {
		if got := redactSockets(tc.in); got != tc.want {
			t.Errorf("redactSockets(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// studioErrorStatus defaults to 502, so every code the operator's own
// statement can provoke must be mapped. A constraint violation is their
// result, not a broken gateway.
func TestStudioErrorStatusClassifiesStatementErrors(t *testing.T) {
	for _, tc := range []struct {
		code nerr.Code
		want int
	}{
		{nerr.AlreadyExists, http.StatusConflict},
		{nerr.ForeignKey, http.StatusConflict},
		{nerr.Conflict, http.StatusConflict},
		{nerr.Deadlock, http.StatusConflict},
		{nerr.Serialization, http.StatusConflict},
		{nerr.Unavailable, http.StatusConflict},
		{nerr.InvalidArgument, http.StatusBadRequest},
		{nerr.InvalidFormat, http.StatusBadRequest},
		{nerr.Syntax, http.StatusBadRequest},
		{nerr.NotFound, http.StatusNotFound},
		{nerr.Unauthorized, http.StatusForbidden},
		{nerr.Forbidden, http.StatusForbidden},
		{nerr.Exhausted, http.StatusRequestEntityTooLarge},
		{nerr.Canceled, http.StatusRequestTimeout},
		// Genuinely upstream: these stay 502.
		{nerr.Internal, http.StatusBadGateway},
		{nerr.Corruption, http.StatusBadGateway},
	} {
		got := studioErrorStatus(nerr.New(tc.code, "nextsql", "x"))
		if got != tc.want {
			t.Errorf("studioErrorStatus(%s) = %d, want %d", tc.code, got, tc.want)
		}
	}
}

// The Cancel route cancels the statement's own child context, never the
// handler's request context, so the handler cannot observe a cancel. This
// relabelling is what gives the operator "query canceled" at 408 rather than
// the engine's raw text. It is written against the pre-fix engine error
// (nerr.Exhausted) on purpose: the relabelling must not depend on the engine
// having been corrected, so an older server still classifies correctly.
func TestStudioCancelErrorRelabelsOnlyEndedContexts(t *testing.T) {
	engineCancelled := nerr.New(nerr.Exhausted, "scheduler.Budget", "query cancelled")

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	got := studioCancelError(canceled, engineCancelled)
	if !nerr.HasCode(got, nerr.Canceled) {
		t.Fatalf("canceled context: got %v, want a Canceled error", got)
	}
	if studioErrorStatus(got) != http.StatusRequestTimeout {
		t.Fatalf("canceled status = %d, want 408", studioErrorStatus(got))
	}

	expired, cancel2 := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel2()
	got = studioCancelError(expired, engineCancelled)
	if !nerr.HasCode(got, nerr.Canceled) {
		t.Fatalf("expired context: got %v, want a Canceled error", got)
	}
	if !strings.Contains(got.Error(), "Studio statement limit") {
		t.Errorf("deadline message should name the limit: %v", got)
	}

	// A live context means the error is the engine's own verdict. A real
	// resource bound must keep reading as one.
	live := context.Background()
	budget := nerr.New(nerr.Exhausted, "scheduler.Budget", "memory budget exceeded")
	if got := studioCancelError(live, budget); got != budget {
		t.Fatalf("live context rewrote a genuine budget error: %v", got)
	}
	if studioErrorStatus(studioCancelError(live, budget)) != http.StatusRequestEntityTooLarge {
		t.Error("a real memory bound must stay 413")
	}
	// Success is never relabelled, even on a context that has since ended.
	if got := studioCancelError(canceled, nil); got != nil {
		t.Fatalf("nil error became %v", got)
	}
}
