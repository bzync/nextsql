package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bzync/nextsql/internal/cli"
	"github.com/bzync/nextsql/internal/config"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/hosting"
	"github.com/bzync/nextsql/internal/nerr"
)

// keyCmd manages the unlock keys of the database in --data-dir.
//
// A NextSQL database is sealed under one external root unlock key. A recovery
// key is an optional second, independent key that unlocks the same database,
// so losing the root key file is survivable. Neither key is ever stored in the
// data directory, and neither is ever printed.
func keyCmd(args []string) error {
	if len(args) == 0 {
		return nerr.New(nerr.InvalidArgument, "nextsql key",
			"expected status, add-recovery, verify-recovery, remove-recovery, or recover")
	}
	switch args[0] {
	case "status":
		return keyStatusCmd(args[1:])
	case "add-recovery":
		return keyAddRecoveryCmd(args[1:])
	case "verify-recovery":
		return keyVerifyRecoveryCmd(args[1:])
	case "remove-recovery":
		return keyRemoveRecoveryCmd(args[1:])
	case "recover":
		return keyRecoverCmd(args[1:])
	default:
		return nerr.New(nerr.InvalidArgument, "nextsql key", "unknown subcommand")
	}
}

// A default deployment has two keystores, each sealed under its own external
// root key: the database itself (--key-file) and the deployment registry
// (--instance-key-file, which nextsqld defaults to KEY-FILE.instance). Losing
// either one is fatal on its own, so a recovery key is configurable for each,
// selected by --keystore.
const (
	keystoreDatabase = "database"
	keystoreInstance = "instance"
)

func keystorePathFor(dataDir, which string) (string, error) {
	switch which {
	case keystoreDatabase:
		return crypto.KeystorePath(filepath.Join(dataDir, config.DataFileName)), nil
	case keystoreInstance:
		return hosting.KeyStorePath(filepath.Join(dataDir, hosting.RegistryFileName)), nil
	default:
		return "", nerr.New(nerr.InvalidArgument, "nextsql key",
			"--keystore must be "+keystoreDatabase+" or "+keystoreInstance)
	}
}

func keystoreFlag(fs *flag.FlagSet) *string {
	return fs.String("keystore", keystoreDatabase,
		"which keystore to act on: "+keystoreDatabase+" (--key-file) or "+keystoreInstance+" (--instance-key-file)")
}

// warnIfUnderDataDir reports a key file written inside the directory it
// unlocks. That is not a second copy of anything — whoever can read the data
// volume then holds the key to it — so it is called out loudly, in the same
// spirit as the root unlock key's own off-volume requirement.
func warnIfUnderDataDir(op, keyPath, dataDir string) {
	absKey, err := filepath.Abs(keyPath)
	if err != nil {
		return
	}
	absDir, err := filepath.Abs(dataDir)
	if err != nil {
		return
	}
	rel, err := filepath.Rel(absDir, absKey)
	if err != nil {
		return
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return // outside the data directory, which is what we want
	}
	fmt.Fprintf(os.Stderr,
		"warning: %s wrote a key inside the data directory it unlocks (%s).\n"+
			"         Move it to separate, backed-up storage — a key kept beside its data is not a backup.\n",
		op, absKey)
}

type keystoreReport struct {
	Keystore        string `json:"keystore"`
	Path            string `json:"path"`
	Present         bool   `json:"present"`
	KeystoreVersion int    `json:"keystore_version,omitempty"`
	RecoveryKey     bool   `json:"recovery_key_configured"`
	RecoveryVersion uint32 `json:"recovery_key_version,omitempty"`
	Shredded        bool   `json:"shredded"`
}

type keyStatusReport struct {
	DataDir   string           `json:"data_dir"`
	Keystores []keystoreReport `json:"keystores"`
}

// keyStatusCmd reports what unlock paths exist, for every keystore in the
// deployment. It deliberately needs no key: an operator asking this question
// may be holding only one of them, or none.
func keyStatusCmd(args []string) error {
	fs := flag.NewFlagSet("key status", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "", "directory containing nextsql.db")
	asJSON := fs.Bool("json", false, "emit the report as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dataDir == "" {
		return cli.LocalMissing("nextsql key status", "--data-dir is required")
	}
	rep := keyStatusReport{DataDir: *dataDir}
	for _, which := range []string{keystoreDatabase, keystoreInstance} {
		path, err := keystorePathFor(*dataDir, which)
		if err != nil {
			return err
		}
		row := keystoreReport{Keystore: which, Path: path}
		// A deployment need not have both: report absence rather than failing,
		// so one missing keystore does not hide the other's state.
		if _, statErr := os.Stat(path); statErr == nil {
			env, err := crypto.OpenLocked(path)
			if err != nil {
				return err
			}
			row.Present = true
			row.KeystoreVersion = env.KeystoreFormatVersion()
			row.RecoveryKey = env.HasRecoveryKey()
			row.RecoveryVersion = uint32(env.RecoveryKeyVersion())
			row.Shredded = env.Shredded()
		}
		rep.Keystores = append(rep.Keystores, row)
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	for _, row := range rep.Keystores {
		fmt.Printf("%s %s\n", row.Keystore, row.Path)
		if !row.Present {
			fmt.Printf("  not present\n")
			continue
		}
		fmt.Printf("  keystore_version %d\n", row.KeystoreVersion)
		fmt.Printf("  shredded %t\n", row.Shredded)
		if row.RecoveryKey {
			fmt.Printf("  recovery_key configured (version %d)\n", row.RecoveryVersion)
		} else {
			fmt.Printf("  recovery_key none — losing this keystore's root key means it cannot be opened again\n")
		}
	}
	return nil
}

func keyAddRecoveryCmd(args []string) error {
	fs := flag.NewFlagSet("key add-recovery", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "", "directory containing nextsql.db")
	keyFile := fs.String("key-file", "", "root unlock key file for the selected keystore")
	out := fs.String("recovery-key-out", "", "path to write the new recovery key (must not exist)")
	replace := fs.Bool("replace", false, "replace an already-configured recovery key")
	which := keystoreFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dataDir == "" || *keyFile == "" || *out == "" {
		return cli.LocalMissing("nextsql key add-recovery",
			"--data-dir, --key-file, and --recovery-key-out are required")
	}
	ksPath, err := keystorePathFor(*dataDir, *which)
	if err != nil {
		return err
	}
	root, err := crypto.ReadKeyFile(*keyFile)
	if err != nil {
		return err
	}
	env, err := crypto.OpenEnvelope(ksPath, root)
	if err != nil {
		return err
	}
	defer env.Close()
	if env.HasRecoveryKey() && !*replace {
		return nerr.New(nerr.AlreadyExists, "nextsql key add-recovery",
			"a recovery key is already configured; pass --replace to supersede it (the existing one stops working)")
	}
	// Write the exported key before it is installed. If the write fails the
	// keystore is untouched; if the install fails the file is removed, so the
	// operator is never left holding a key that unlocks nothing or missing a
	// key that does.
	rec, err := crypto.CreateRecoveryKeyFile(*out, env.NextRecoveryKeyVersion())
	if err != nil {
		return err
	}
	if err := env.SetRecoveryKey(rec); err != nil {
		_ = os.Remove(*out)
		return err
	}
	if err := env.VerifyRecoveryKey(rec); err != nil {
		return err
	}
	warnIfUnderDataDir("nextsql key add-recovery", *out, *dataDir)
	fmt.Printf("recovery key written to %s (version %d, mode 0600)\n", *out, rec.Version)
	fmt.Printf("verified: it unlocks %s\n", ksPath)
	fmt.Println("Store it offline, separately from the root unlock key. It is the only copy.")
	return nil
}

func keyVerifyRecoveryCmd(args []string) error {
	fs := flag.NewFlagSet("key verify-recovery", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "", "directory containing nextsql.db")
	recFile := fs.String("recovery-key", "", "recovery key file to check")
	which := keystoreFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dataDir == "" || *recFile == "" {
		return cli.LocalMissing("nextsql key verify-recovery", "--data-dir and --recovery-key are required")
	}
	ksPath, err := keystorePathFor(*dataDir, *which)
	if err != nil {
		return err
	}
	rec, err := crypto.ReadRecoveryKeyFile(*recFile)
	if err != nil {
		return err
	}
	// Verify by actually unlocking, not by comparing metadata: a recovery key
	// that has never opened the keystore is a backup nobody has restored.
	env, err := crypto.OpenEnvelopeWithRecovery(ksPath, rec)
	if err != nil {
		return err
	}
	defer env.Close()
	fmt.Printf("ok: %s unlocks %s (recovery key version %d)\n",
		*recFile, ksPath, rec.Version)
	return nil
}

func keyRemoveRecoveryCmd(args []string) error {
	fs := flag.NewFlagSet("key remove-recovery", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "", "directory containing nextsql.db")
	keyFile := fs.String("key-file", "", "root unlock key file for the selected keystore")
	confirm := fs.Bool("confirm", false, "confirm that the root unlock key becomes the only way in")
	which := keystoreFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dataDir == "" || *keyFile == "" {
		return cli.LocalMissing("nextsql key remove-recovery", "--data-dir and --key-file are required")
	}
	ksPath, err := keystorePathFor(*dataDir, *which)
	if err != nil {
		return err
	}
	if !*confirm {
		return nerr.New(nerr.InvalidArgument, "nextsql key remove-recovery",
			"--confirm is required: after this the root unlock key is the only way to open this database")
	}
	root, err := crypto.ReadKeyFile(*keyFile)
	if err != nil {
		return err
	}
	env, err := crypto.OpenEnvelope(ksPath, root)
	if err != nil {
		return err
	}
	defer env.Close()
	if !env.HasRecoveryKey() {
		fmt.Println("no recovery key is configured; nothing to remove")
		return nil
	}
	if err := env.RemoveRecoveryKey(); err != nil {
		return err
	}
	fmt.Println("recovery key removed; the exported copy no longer opens this database")
	return nil
}

// keyRecoverCmd is the point of the whole feature: open a database whose root
// unlock key is gone, using the recovery key, and give it a fresh root.
func keyRecoverCmd(args []string) error {
	fs := flag.NewFlagSet("key recover", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "", "directory containing nextsql.db")
	recFile := fs.String("recovery-key", "", "recovery key file")
	out := fs.String("key-file-out", "", "path to write the newly generated root unlock key (must not exist)")
	confirm := fs.Bool("confirm", false, "confirm that any previous root unlock key stops working")
	which := keystoreFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dataDir == "" || *recFile == "" || *out == "" {
		return cli.LocalMissing("nextsql key recover",
			"--data-dir, --recovery-key, and --key-file-out are required")
	}
	ksPath, err := keystorePathFor(*dataDir, *which)
	if err != nil {
		return err
	}
	if !*confirm {
		return nerr.New(nerr.InvalidArgument, "nextsql key recover",
			"--confirm is required: this replaces the root unlock key, and any previous root key file stops working")
	}
	rec, err := crypto.ReadRecoveryKeyFile(*recFile)
	if err != nil {
		return err
	}
	env, err := crypto.OpenEnvelopeWithRecovery(ksPath, rec)
	if err != nil {
		return err
	}
	defer env.Close()
	root, err := crypto.CreateKeyFile(*out, 1)
	if err != nil {
		return err
	}
	if err := env.RotateRoot(root); err != nil {
		_ = os.Remove(*out)
		return err
	}
	warnIfUnderDataDir("nextsql key recover", *out, *dataDir)
	fmt.Printf("root unlock key written to %s (mode 0600)\n", *out)
	fmt.Printf("use it as --key-file from now on; any previous root key file no longer opens %s\n",
		ksPath)
	if env.HasRecoveryKey() {
		fmt.Printf("the recovery key %s still works and was not changed\n", *recFile)
	}
	// `nextsql setup` writes a nextsql.conf that pins key_file and
	// instance_key_file, and --config wins over the derived default, so a
	// recovered root at a new path is not picked up by passing --key-file
	// alone. Found live: a recovered deployment started with the new root
	// still failed on "open <old-root>.instance: no such file or directory"
	// because the config file named the deleted path.
	fmt.Printf("if a config file pins this key's path (`key_file` / `instance_key_file` in\n" +
		"nextsql.conf, written by `nextsql setup`), update it too — a --key-file flag\n" +
		"does not override the other key's pinned path.\n")
	if *which == keystoreDatabase {
		// nextsqld defaults --instance-key-file to KEY-FILE.instance, so a
		// recovered root at a new path silently stops resolving the registry
		// key that is still sitting next to the old one. Say so here rather
		// than letting the next start fail on a file the operator never
		// named. Verified live: without this the server exits with
		// "open <new-root>.instance: no such file or directory".
		instance := *out + ".instance"
		if _, err := os.Stat(instance); err != nil {
			fmt.Printf("\nnote: the deployment registry key is a separate key, and nextsqld looks for it\n"+
				"      at KEY-FILE.instance — that is %s, which does not exist.\n"+
				"      Pass --instance-key-file explicitly, or run:\n"+
				"        nextsql key status --data-dir %s\n", instance, *dataDir)
		}
	}
	return nil
}
