package oidcclient

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

// BrowserOpener launches the user's browser at url. A nil opener means the CLI
// only prints the URL for the user to open manually.
type BrowserOpener func(url string) error

// DefaultBrowserOpener opens url with the platform's default handler.
func DefaultBrowserOpener(url string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd, args = "open", []string{url}
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		cmd, args = "xdg-open", []string{url}
	}
	c := exec.Command(cmd, args...)
	if err := c.Start(); err != nil {
		return nerr.Wrap(nerr.Unavailable, "oidcclient", "launch browser", err)
	}
	go func() { _ = c.Wait() }()
	return nil
}

// LoginOptions configures an interactive Authorization Code + PKCE login.
type LoginOptions struct {
	Profile  IdPProfile
	HTTP     Doer          // defaults to DefaultHTTP()
	Browser  BrowserOpener // nil => print URL only
	Progress io.Writer     // human-facing progress messages; nil => discard
	Database string
	Realm    string
	// CallbackHost is the loopback host the transient redirect listener binds
	// to. Defaults to "127.0.0.1".
	CallbackHost string
	// Timeout bounds the wait for the browser redirect. Defaults to 3 minutes.
	Timeout time.Duration
}

// Login runs the full interactive flow: OIDC discovery, a PKCE authorization
// code exchange through a transient loopback callback, and a broker token
// exchange. It returns the minted credential and the IdP token set (whose
// refresh token, if any, the caller should store for silent renewal).
func Login(ctx context.Context, opts LoginOptions) (BrokerResult, TokenSet, error) {
	const op = "oidcclient.Login"
	hc := opts.HTTP
	if hc == nil {
		hc = DefaultHTTP()
	}
	host := opts.CallbackHost
	if host == "" {
		host = "127.0.0.1"
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}
	progress := opts.Progress
	if progress == nil {
		progress = io.Discard
	}
	if opts.Profile.BrokerURL == "" {
		return BrokerResult{}, TokenSet{}, nerr.New(nerr.InvalidArgument, op, "identity provider profile has no broker_url")
	}

	md, err := Discover(ctx, hc, opts.Profile.Issuer)
	if err != nil {
		return BrokerResult{}, TokenSet{}, err
	}

	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return BrokerResult{}, TokenSet{}, nerr.Wrap(nerr.IO, op, "bind loopback callback listener", err)
	}
	defer ln.Close()
	redirectURI := fmt.Sprintf("http://%s/callback", ln.Addr().String())

	pkce, err := NewPKCE()
	if err != nil {
		return BrokerResult{}, TokenSet{}, err
	}
	state, err := randToken(24)
	if err != nil {
		return BrokerResult{}, TokenSet{}, err
	}
	nonce, err := randToken(24)
	if err != nil {
		return BrokerResult{}, TokenSet{}, err
	}

	cbCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	codeCh := make(chan callbackResult, 1)
	srv := &http.Server{
		Handler:           callbackHandler(state, codeCh),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       5 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		shutCtx, c := context.WithTimeout(context.Background(), time.Second)
		defer c()
		_ = srv.Shutdown(shutCtx)
	}()

	authURL := AuthCodeURL(md, opts.Profile, redirectURI, state, nonce, pkce.Challenge)
	if opts.Browser != nil {
		fmt.Fprintln(progress, "Opening your browser to sign in…")
		if err := opts.Browser(authURL); err != nil {
			fmt.Fprintf(progress, "Could not open a browser automatically. Visit this URL to sign in:\n\n  %s\n\n", authURL)
		}
	} else {
		fmt.Fprintf(progress, "Visit this URL to sign in:\n\n  %s\n\n", authURL)
	}

	var cb callbackResult
	select {
	case <-cbCtx.Done():
		return BrokerResult{}, TokenSet{}, nerr.New(nerr.Canceled, op, "timed out waiting for the browser sign-in to complete")
	case cb = <-codeCh:
	}
	if cb.err != nil {
		return BrokerResult{}, TokenSet{}, cb.err
	}

	fmt.Fprintln(progress, "Completing sign-in…")
	ts, err := RedeemCode(ctx, hc, md, opts.Profile, cb.code, pkce.Verifier, redirectURI)
	if err != nil {
		return BrokerResult{}, TokenSet{}, err
	}
	res, err := ExchangeAtBroker(ctx, hc, opts.Profile.BrokerURL, opts.Profile.Name, ts.IDToken, nonce, opts.Database, opts.Realm)
	if err != nil {
		return BrokerResult{}, TokenSet{}, err
	}
	return res, ts, nil
}

type callbackResult struct {
	code string
	err  error
}

func callbackHandler(wantState string, out chan<- callbackResult) http.Handler {
	var once sync.Once
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		// An unsolicited request must not consume the one callback result and
		// deny the legitimate browser redirect. Only a matching state can end
		// the flow.
		if q.Get("state") != wantState {
			http.Error(w, "callback state did not match", http.StatusBadRequest)
			return
		}
		var res callbackResult
		switch {
		case q.Get("error") != "":
			res.err = nerr.New(nerr.Unauthorized, "oidcclient", "identity provider returned an error: "+q.Get("error"))
		case q.Get("code") == "":
			res.err = nerr.New(nerr.Protocol, "oidcclient", "callback carried no authorization code")
		default:
			res.code = q.Get("code")
		}
		if res.err != nil {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, "Sign-in failed. You can close this tab and return to the terminal.\n")
		} else {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, callbackSuccessHTML)
		}
		once.Do(func() { out <- res })
	})
}

const callbackSuccessHTML = `<!doctype html><html><head><meta charset="utf-8"><title>NextSQL</title></head>` +
	`<body style="font-family:system-ui,sans-serif;margin:3rem;text-align:center">` +
	`<h1>Signed in</h1><p>You can close this tab and return to the terminal.</p></body></html>`
