// Command nextsql-admin is NextSQL Admin: the single unified binary for
// installing, operating, and (eventually) developing against NextSQL —
// formerly three separate products (Installer, Manager, Studio).
//
// It serves one loopback HTTP service that picks a mode at startup: Setup
// mode (no installation found yet — a token-authenticated wizard that drives
// `nextsql setup` as a subprocess, never touching the engine directly) or
// Operations mode (an installation exists — operator NSQL-credential
// sessions against a running nextsqld, performing every operation as that
// operator's own user so server-side RBAC applies). See
// docs/design-admin.md.
//
// Usage:
//
//	nextsql-admin                                   # auto-detect Setup vs Operations
//	nextsql-admin --mode setup --no-browser
//	nextsql-admin --mode operate --server-addr 127.0.0.1:7210 --insecure
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bzync/nextsql/internal/admin"
	"github.com/bzync/nextsql/internal/admin/setup"
	"github.com/bzync/nextsql/internal/browseropen"
	"github.com/bzync/nextsql/internal/logging"
	"github.com/bzync/nextsql/internal/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "nextsql-admin: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	fs := flag.NewFlagSet("nextsql-admin", flag.ContinueOnError)

	mode := fs.String("mode", "", "override mode auto-detection: setup | operate")
	listen := fs.String("listen", "", "Admin's own HTTP listener; a non-loopback address requires --tls-cert/--tls-key (default: ephemeral in Setup mode, 127.0.0.1:7220 in Operations mode)")
	tlsCert := fs.String("tls-cert", "", "TLS 1.3 certificate (PEM) for the Admin listener")
	tlsKey := fs.String("tls-key", "", "TLS 1.3 private key (PEM) for the Admin listener")
	dataDirHint := fs.String("data-dir", "", "data directory probed for mode auto-detection (default: the same OS-appropriate default the Setup wizard suggests)")

	// Setup-mode flags.
	nextsqlBin := fs.String("nextsql-bin", "", "path to the nextsql binary Admin drives (default: next to this executable, then PATH)")
	noBrowser := fs.Bool("no-browser", false, "Setup mode: print the URL instead of opening a browser (headless/testing)")

	// Operations-mode flags.
	serverAddr := fs.String("server-addr", admin.DefaultServerAddr, "Operations mode: nextsqld address")
	tlsCA := fs.String("tls-ca", "", "Operations mode: PEM CA / server certificate for the nextsqld connection")
	tlsServerName := fs.String("tls-server-name", "", "Operations mode: TLS server name for the nextsqld connection (default: address host)")
	clientCert := fs.String("tls-client-cert", "", "Operations mode: mTLS client certificate (PEM) for the nextsqld connection")
	clientKey := fs.String("tls-client-key", "", "Operations mode: mTLS client private key (PEM) for the nextsqld connection")
	insecure := fs.Bool("insecure", false, "Operations mode: allow a plaintext nextsqld connection (loopback only)")
	maxSessions := fs.Int("max-sessions", 16, "Operations mode: maximum concurrent operator sessions")
	idleTimeout := fs.Duration("idle-timeout", 15*time.Minute, "Operations mode: session idle expiry")
	sessionLifetime := fs.Duration("session-lifetime", 12*time.Hour, "Operations mode: session absolute expiry")

	logLevel := fs.String("log-level", "info", "log level: debug | info | warn | error")
	showVersion := fs.Bool("version", false, "print version and exit")
	grantPort80Flag := fs.Bool("grant-port-80", false,
		"Linux: grant this installed binary permission to bind privileged ports (e.g. 127.0.0.1:80) without root, then exit; the default 127.0.0.1:7220 listener needs no such grant")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}
	if *showVersion {
		fmt.Println(version.String)
		return nil
	}
	if *grantPort80Flag {
		msg, err := grantPort80(defaultPort80Deps())
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "nextsql-admin: %s\n", msg)
		return nil
	}

	// The nextsql binary is needed for mode auto-detection (always) and for
	// Setup mode's own subprocess calls. It is not required when --mode
	// operate is explicit — Operations mode never shells out.
	bin, binErr := setup.ResolveNextSQLBin(*nextsqlBin)
	if binErr != nil && admin.Mode(*mode) != admin.ModeOperate {
		return binErr
	}

	log := logging.New(*logLevel, os.Stderr)
	srv, err := admin.New(admin.Config{
		Mode:            admin.Mode(*mode),
		Listen:          *listen,
		ListenTLSCert:   *tlsCert,
		ListenTLSKey:    *tlsKey,
		NextSQLBin:      bin,
		DataDirHint:     *dataDirHint,
		ServerAddr:      *serverAddr,
		ServerTLSCA:     *tlsCA,
		ServerTLSName:   *tlsServerName,
		ClientCert:      *clientCert,
		ClientKey:       *clientKey,
		InsecureServer:  *insecure,
		MaxSessions:     *maxSessions,
		IdleTimeout:     *idleTimeout,
		SessionLifetime: *sessionLifetime,
		LogLevel:        *logLevel,
	}, admin.Options{Logger: log})
	if err != nil {
		return err
	}
	defer srv.Close()

	if url, ok := srv.SetupURL(); ok {
		fmt.Fprintf(os.Stdout, "NextSQL Setup: %s\n", url)
		fmt.Fprintf(os.Stdout, "Press Ctrl+C to stop once setup is complete.\n")
		if !*noBrowser {
			if err := browseropen.Open(url); err != nil {
				fmt.Fprintf(os.Stderr, "nextsql-admin: could not open a browser automatically: %v\n", err)
				fmt.Fprintf(os.Stderr, "nextsql-admin: open the URL above manually.\n")
			}
		}
	} else {
		log.Info("nextsql-admin: listening",
			"mode", srv.Mode(), "addr", srv.Addr().String(), "server_addr", *serverAddr)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve() }()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	case <-srv.Done():
		// Setup mode only: the operator clicked "Finish" on the completion
		// screen — end the process without needing a terminal Ctrl+C.
		fmt.Fprintln(os.Stdout, "nextsql-admin: setup finished, stopping.")
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	case err := <-errCh:
		return err
	}
}
