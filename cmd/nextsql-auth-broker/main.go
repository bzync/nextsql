// Command nextsql-auth-broker is the NextSQL authentication broker: a small
// HTTPS service that runs an OpenID Connect token exchange and mints an
// ordinary `NSSC1.` short-lived credential. `nextsqld` never talks to it and
// gains no OIDC parsing — it keeps verifying `NSSC1.` credentials offline.
//
// Usage:
//
//	nextsql-auth-broker --config /etc/nextsql/auth-broker.conf
//
// SIGHUP reloads the identity policy and the issuing keyset with last
// known-good rollback.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bzync/nextsql/internal/authbroker"
	"github.com/bzync/nextsql/internal/logging"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "nextsql-auth-broker: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	fs := flag.NewFlagSet("nextsql-auth-broker", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to the broker configuration file")
	showVersion := fs.Bool("version", false, "print version and exit")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}
	if *showVersion {
		fmt.Println(version.String)
		return nil
	}
	if *configPath == "" {
		return nerr.New(nerr.InvalidArgument, "nextsql-auth-broker", "--config is required")
	}

	cfg, err := authbroker.LoadConfig(*configPath)
	if err != nil {
		return err
	}
	log := logging.New(cfg.LogLevel, os.Stderr)

	broker, err := authbroker.New(cfg, authbroker.Options{Logger: log})
	if err != nil {
		return err
	}

	if security.RequireTLS(cfg.Listen) && cfg.TLSCert == "" {
		return nerr.New(nerr.InvalidArgument, "nextsql-auth-broker", "a non-loopback listen address requires tls_cert and tls_key")
	}

	srv := &http.Server{
		Handler:           broker.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	if cfg.TLSCert != "" {
		tlsCfg, err := security.ServerTLS(cfg.TLSCert, cfg.TLSKey)
		if err != nil {
			return err
		}
		srv.TLSConfig = tlsCfg
	}

	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return nerr.Wrap(nerr.IO, "nextsql-auth-broker", "listen", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-hup:
				_ = broker.Reload()
			}
		}
	}()

	errc := make(chan error, 1)
	go func() {
		if srv.TLSConfig != nil {
			errc <- srv.ServeTLS(tlsListener(ln, srv.TLSConfig), "", "")
		} else {
			errc <- srv.Serve(ln)
		}
	}()
	log.Info("nextsql-auth-broker: listening", "addr", cfg.Listen, "tls", srv.TLSConfig != nil, "profiles", len(cfg.Profiles))

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func tlsListener(inner net.Listener, cfg *tls.Config) net.Listener {
	return tls.NewListener(inner, cfg)
}
