package authbroker

import (
	"log/slog"
	"time"
)

// exchangeAudit is the structured record the broker emits for every exchange
// attempt. It never carries the ID token, the minted credential, or a client
// secret. The subject is stored only as a short hash (design §8).
type exchangeAudit struct {
	Time           time.Time
	IdP            string
	Issuer         string
	SubjectHash    string
	RuleID         string
	Principal      string
	MappedRoles    []string
	EffectiveRoles []string
	TokenID        string
	ExpiresAt      time.Time
	Outcome        string // "granted", "denied", "error"
	Reason         string // populated for denied / error
}

func (b *Broker) logAudit(a exchangeAudit) {
	attrs := []any{
		slog.String("event", "authbroker.exchange"),
		slog.String("idp", a.IdP),
		slog.String("issuer", a.Issuer),
		slog.String("outcome", a.Outcome),
	}
	if a.SubjectHash != "" {
		attrs = append(attrs, slog.String("subject_hash", a.SubjectHash))
	}
	if a.RuleID != "" {
		attrs = append(attrs, slog.String("rule_id", a.RuleID))
	}
	if a.Principal != "" {
		attrs = append(attrs, slog.String("principal", a.Principal))
	}
	if len(a.MappedRoles) > 0 {
		attrs = append(attrs, slog.Any("mapped_roles", a.MappedRoles))
	}
	if len(a.EffectiveRoles) > 0 {
		attrs = append(attrs, slog.Any("effective_roles", a.EffectiveRoles))
	}
	if a.TokenID != "" {
		attrs = append(attrs, slog.String("token_id", a.TokenID))
	}
	if !a.ExpiresAt.IsZero() {
		attrs = append(attrs, slog.String("expires_at", a.ExpiresAt.Format(time.RFC3339)))
	}
	if a.Reason != "" {
		attrs = append(attrs, slog.String("reason", a.Reason))
	}

	switch a.Outcome {
	case "granted":
		b.log.Info("authbroker: credential minted", attrs...)
	case "error":
		b.log.Error("authbroker: exchange error", attrs...)
	default:
		b.log.Warn("authbroker: exchange denied", attrs...)
	}
}
