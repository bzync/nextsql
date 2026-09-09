package security

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ocsp"

	"github.com/bzync/nextsql/internal/nerr"
)

// OCSPMode controls whether and how OCSP status is verified.
type OCSPMode string

const (
	OCSPModeDisabled OCSPMode = "disabled"
	OCSPModeOptional OCSPMode = "optional"
	OCSPModeEnforce  OCSPMode = "enforce"

	defaultOCSPTimeout   = 3 * time.Second
	defaultOCSPCacheTTL  = time.Hour
	maxOCSPCacheEntries  = 1024
	maxOCSPResponseBytes = 64 << 10 // 64 KiB
)

// ParseOCSPMode normalizes and validates an OCSP mode string.
func ParseOCSPMode(s string) (OCSPMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", string(OCSPModeDisabled):
		return OCSPModeDisabled, nil
	case string(OCSPModeOptional):
		return OCSPModeOptional, nil
	case string(OCSPModeEnforce), "required":
		return OCSPModeEnforce, nil
	default:
		return "", nerr.New(nerr.InvalidArgument, "security.ParseOCSPMode", "ocsp_mode must be disabled, optional, or enforce")
	}
}

// OCSPConfig configures certificate status checking via OCSP.
type OCSPConfig struct {
	Mode         OCSPMode
	ResponderURL string
	CacheTTL     time.Duration
	Timeout      time.Duration
	Now          func() time.Time
	HTTPClient   *http.Client
}

type cachedOCSPStatus struct {
	status     int
	nextUpdate time.Time
}

type ocspCache struct {
	mu      sync.Mutex
	entries map[string]cachedOCSPStatus
	order   []string
}

var globalOCSPCache = &ocspCache{
	entries: make(map[string]cachedOCSPStatus),
}

func (c *ocspCache) get(key string, now time.Time) (cachedOCSPStatus, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return cachedOCSPStatus{}, false
	}
	if now.After(entry.nextUpdate) {
		delete(c.entries, key)
		return cachedOCSPStatus{}, false
	}
	return entry, true
}

func (c *ocspCache) put(key string, entry cachedOCSPStatus) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Keep the insertion-order index bounded too. Expired entries are removed
	// from entries by get; repeated refreshes of one certificate must not leave
	// duplicate/stale keys accumulating forever in order.
	compacted := c.order[:0]
	for _, candidate := range c.order {
		if candidate == key {
			continue
		}
		if _, live := c.entries[candidate]; live {
			compacted = append(compacted, candidate)
		}
	}
	c.order = compacted
	delete(c.entries, key)
	if len(c.entries) >= maxOCSPCacheEntries && len(c.order) > 0 {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}
	c.entries[key] = entry
	c.order = append(c.order, key)
}

func ocspResponseCurrent(resp *ocsp.Response, now time.Time, maxAge time.Duration) bool {
	if resp == nil || resp.ThisUpdate.IsZero() || now.Before(resp.ThisUpdate) {
		return false
	}
	if !resp.NextUpdate.IsZero() {
		return !now.After(resp.NextUpdate)
	}
	return maxAge > 0 && !now.After(resp.ThisUpdate.Add(maxAge))
}

// VerifyOCSP checks the certificate status of leaf against issuer using stapled
// OCSP or live responder queries.
func VerifyOCSP(leaf, issuer *x509.Certificate, stapled []byte, cfg OCSPConfig) error {
	const op = "security.VerifyOCSP"
	if cfg.Mode == "" || cfg.Mode == OCSPModeDisabled {
		return nil
	}
	if leaf == nil || issuer == nil {
		return nerr.New(nerr.InvalidArgument, op, "leaf and issuer certificates are required")
	}

	now := time.Now().UTC()
	if cfg.Now != nil {
		now = cfg.Now().UTC()
	}
	cacheTTL := cfg.CacheTTL
	if cacheTTL <= 0 {
		cacheTTL = defaultOCSPCacheTTL
	}

	// 1. Check stapled OCSP response if provided
	if len(stapled) > 0 {
		resp, err := ocsp.ParseResponseForCert(stapled, leaf, issuer)
		if err == nil {
			if !ocspResponseCurrent(resp, now, cacheTTL) {
				// Stapled response is expired or not yet valid
			} else {
				switch resp.Status {
				case ocsp.Good:
					return nil
				case ocsp.Revoked:
					return nerr.New(nerr.Unauthorized, op, "client certificate is revoked according to stapled OCSP response")
				case ocsp.Unknown:
					if cfg.Mode == OCSPModeEnforce {
						return nerr.New(nerr.Unauthorized, op, "client certificate status is unknown according to stapled OCSP response")
					}
				}
			}
		}
	}

	// 2. Discover responder URL
	responder := strings.TrimSpace(cfg.ResponderURL)
	if responder == "" && len(leaf.OCSPServer) > 0 {
		responder = strings.TrimSpace(leaf.OCSPServer[0])
	}
	if responder == "" {
		if cfg.Mode == OCSPModeEnforce {
			return nerr.New(nerr.Unauthorized, op, "certificate lacks OCSP responder and no stapled response provided")
		}
		return nil
	}

	// 3. Check in-memory cache
	cacheKeyHasher := sha256.New()
	_, _ = cacheKeyHasher.Write(leaf.Raw)
	_, _ = cacheKeyHasher.Write(issuer.Raw)
	_, _ = cacheKeyHasher.Write([]byte(responder))
	cacheKey := hex.EncodeToString(cacheKeyHasher.Sum(nil))

	if cached, ok := globalOCSPCache.get(cacheKey, now); ok {
		switch cached.status {
		case ocsp.Good:
			return nil
		case ocsp.Revoked:
			return nerr.New(nerr.Unauthorized, op, "client certificate is revoked (cached OCSP status)")
		case ocsp.Unknown:
			if cfg.Mode == OCSPModeEnforce {
				return nerr.New(nerr.Unauthorized, op, "client certificate status is unknown (cached OCSP status)")
			}
			return nil
		}
	}

	// 4. Query responder
	reqBytes, err := ocsp.CreateRequest(leaf, issuer, nil)
	if err != nil {
		if cfg.Mode == OCSPModeEnforce {
			return nerr.Wrap(nerr.Internal, op, "create ocsp request", err)
		}
		return nil
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultOCSPTimeout
	}

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout: timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}

	httpReq, err := http.NewRequest(http.MethodPost, responder, bytes.NewReader(reqBytes))
	if err != nil {
		if cfg.Mode == OCSPModeEnforce {
			return nerr.Wrap(nerr.Internal, op, "build http request", err)
		}
		return nil
	}
	httpReq.Header.Set("Content-Type", "application/ocsp-request")
	httpReq.Header.Set("Accept", "application/ocsp-response")

	httpResp, err := client.Do(httpReq)
	if err != nil {
		if cfg.Mode == OCSPModeEnforce {
			return nerr.Wrap(nerr.Unauthorized, op, "OCSP responder unreachable", err)
		}
		return nil
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		if cfg.Mode == OCSPModeEnforce {
			return nerr.New(nerr.Unauthorized, op, "OCSP responder returned HTTP error")
		}
		return nil
	}

	respBody, err := io.ReadAll(io.LimitReader(httpResp.Body, maxOCSPResponseBytes+1))
	if err != nil || len(respBody) > maxOCSPResponseBytes {
		if cfg.Mode == OCSPModeEnforce {
			return nerr.New(nerr.Unauthorized, op, "failed to read OCSP response or exceeded 64 KiB")
		}
		return nil
	}

	parsed, err := ocsp.ParseResponseForCert(respBody, leaf, issuer)
	if err != nil {
		if cfg.Mode == OCSPModeEnforce {
			return nerr.Wrap(nerr.Unauthorized, op, "parse ocsp response", err)
		}
		return nil
	}
	if !ocspResponseCurrent(parsed, now, cacheTTL) {
		if cfg.Mode == OCSPModeEnforce {
			return nerr.New(nerr.Unauthorized, op, "OCSP response is expired or not yet valid")
		}
		return nil
	}
	nextUp := parsed.NextUpdate
	if nextUp.IsZero() {
		nextUp = parsed.ThisUpdate.Add(cacheTTL)
	}
	if nextUp.After(now.Add(cacheTTL)) {
		nextUp = now.Add(cacheTTL)
	}

	globalOCSPCache.put(cacheKey, cachedOCSPStatus{
		status:     parsed.Status,
		nextUpdate: nextUp,
	})

	switch parsed.Status {
	case ocsp.Good:
		return nil
	case ocsp.Revoked:
		return nerr.New(nerr.Unauthorized, op, "client certificate is revoked by OCSP responder")
	case ocsp.Unknown:
		if cfg.Mode == OCSPModeEnforce {
			return nerr.New(nerr.Unauthorized, op, "client certificate status is unknown by OCSP responder")
		}
		return nil
	default:
		return nil
	}
}
