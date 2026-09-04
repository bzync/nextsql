// Command nextsql-manager is the NextSQL Manager: a loopback HTTP service
// that serves the operational-administration web UI plus a JSON API. It is a
// pure client of a running nextsqld — it performs every operation as the
// logged-in operator's own NSQL user (server-side RBAC applies), holds no
// credentials of its own, and has no data-directory or key access.
//
// Usage:
//
//	nextsql-manager --server-addr 127.0.0.1:7210 --insecure
//	nextsql-manager --listen 0.0.0.0:7220 --tls-cert m.pem --tls-key m.key \
//	  --server-addr db.internal:7210 --tls-ca ca.pem
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bzync/nextsql/internal/logging"
	"github.com/bzync/nextsql/internal/manager"
	"github.com/bzync/nextsql/internal/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "nextsql-manager: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	fs := flag.NewFlagSet("nextsql-manager", flag.ContinueOnError)
	listen := fs.String("listen", manager.DefaultListen, "Manager HTTP listener; a non-loopback address requires --tls-cert/--tls-key")
	tlsCert := fs.String("tls-cert", "", "TLS 1.3 certificate (PEM) for the Manager listener")
	tlsKey := fs.String("tls-key", "", "TLS 1.3 private key (PEM) for the Manager listener")
	serverAddr := fs.String("server-addr", manager.DefaultServerAddr, "nextsqld address")
	tlsCA := fs.String("tls-ca", "", "PEM CA / server certificate for the nextsqld connection")
	tlsServerName := fs.String("tls-server-name", "", "TLS server name for the nextsqld connection (default: address host)")
	clientCert := fs.String("tls-client-cert", "", "mTLS client certificate (PEM) for the nextsqld connection")
	clientKey := fs.String("tls-client-key", "", "mTLS client private key (PEM) for the nextsqld connection")
	insecure := fs.Bool("insecure", false, "allow a plaintext nextsqld connection (loopback only)")
	maxSessions := fs.Int("max-sessions", manager.DefaultMaxSessions, "maximum concurrent operator sessions")
	idleTimeout := fs.Duration("idle-timeout", manager.DefaultIdleTimeout, "session idle expiry")
	sessionLifetime := fs.Duration("session-lifetime", manager.DefaultSessionLifetime, "session absolute expiry")
	logLevel := fs.String("log-level", "info", "log level: debug | info | warn | error")
	showVersion := fs.Bool("version", false, "print version and exit")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}
	if *showVersion {
		fmt.Println(version.String)
		return nil
	}

	log := logging.New(*logLevel, os.Stderr)
	srv, err := manager.New(manager.Config{
		Listen:          *listen,
		ListenTLSCert:   *tlsCert,
		ListenTLSKey:    *tlsKey,
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
	}, manager.Options{Logger: log})
	if err != nil {
		return err
	}
	defer srv.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve() }()
	log.Info("nextsql-manager: listening",
		"addr", srv.Addr().String(), "tls", srv.TLS(), "server_addr", *serverAddr)

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	case err := <-errc:
		return err
	}
}
