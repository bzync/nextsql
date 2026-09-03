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
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bzync/nextsql/internal/authbroker"
	"github.com/bzync/nextsql/internal/logging"
	"github.com/bzync/nextsql/internal/nerr"
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

	srv, err := authbroker.NewHTTPServer(cfg, broker.Handler())
	if err != nil {
		return err
	}
	defer srv.Close()

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
		errc <- srv.Serve()
	}()
	log.Info("nextsql-auth-broker: listening", "addr", srv.Addr().String(), "tls", srv.TLS(), "profiles", len(cfg.Profiles))

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	case err := <-errc:
		return err
	}
}
