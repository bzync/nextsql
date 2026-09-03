package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
)

func auditCmd(args []string) error {
	if len(args) == 0 {
		return nerr.New(nerr.InvalidArgument, "nextsql audit", "expected keygen, rotate, retire, list-keys, export-public, or verify")
	}
	switch args[0] {
	case "keygen":
		return auditKeygen(args[1:])
	case "rotate":
		return auditRotate(args[1:])
	case "retire":
		return auditRetire(args[1:])
	case "list-keys":
		return auditListKeys(args[1:])
	case "export-public":
		return auditExportPublic(args[1:])
	case "verify":
		return auditVerify(args[1:])
	default:
		return nerr.New(nerr.InvalidArgument, "nextsql audit", "unknown audit command")
	}
}

func auditKeygen(args []string) error {
	fs := flag.NewFlagSet("audit keygen", flag.ContinueOnError)
	keyset := fs.String("keyset", "", "path to the audit signing keyset file to create")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *keyset == "" {
		return nerr.New(nerr.InvalidArgument, "nextsql audit keygen", "--keyset is required")
	}
	ks, err := security.CreateAuditKeyset(*keyset)
	if err != nil {
		return err
	}
	for _, k := range ks.List() {
		fmt.Printf("created audit signing keyset %s\ncurrent key id: %d\n", *keyset, k.ID)
	}
	fmt.Println("configure nextsqld --audit-signing-keyset with this file to sign new entries; distribute a verify-only copy with `nextsql audit export-public`")
	return nil
}

func auditRotate(args []string) error {
	fs := flag.NewFlagSet("audit rotate", flag.ContinueOnError)
	keyset := fs.String("keyset", "", "path to the audit signing keyset file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *keyset == "" {
		return nerr.New(nerr.InvalidArgument, "nextsql audit rotate", "--keyset is required")
	}
	ks, err := security.OpenAuditKeyset(*keyset)
	if err != nil {
		return err
	}
	id, err := ks.AddKey()
	if err != nil {
		return err
	}
	fmt.Printf("added key id %d and made it current; retire the previous key after the overlap window with `nextsql audit retire`\n", id)
	return nil
}

func auditRetire(args []string) error {
	fs := flag.NewFlagSet("audit retire", flag.ContinueOnError)
	keyset := fs.String("keyset", "", "path to the audit signing keyset file")
	id := fs.Uint("key-id", 0, "signing key id to retire")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *keyset == "" || *id == 0 {
		return nerr.New(nerr.InvalidArgument, "nextsql audit retire", "--keyset and --key-id are required")
	}
	ks, err := security.OpenAuditKeyset(*keyset)
	if err != nil {
		return err
	}
	if err := ks.Retire(uint32(*id)); err != nil {
		return err
	}
	fmt.Printf("retired key id %d for new signing; entries it already signed still verify\n", *id)
	return nil
}

func auditListKeys(args []string) error {
	fs := flag.NewFlagSet("audit list-keys", flag.ContinueOnError)
	keyset := fs.String("keyset", "", "path to a keyset file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *keyset == "" {
		return nerr.New(nerr.InvalidArgument, "nextsql audit list-keys", "--keyset is required")
	}
	ks, err := security.OpenAuditKeyset(*keyset)
	if err != nil {
		return err
	}
	fmt.Printf("%-6s %-8s %-8s %-8s %s\n", "ID", "CURRENT", "RETIRED", "PRIVATE", "CREATED")
	for _, k := range ks.List() {
		fmt.Printf("%-6d %-8t %-8t %-8t %s\n", k.ID, k.Current, k.Retired, k.HasPrivate, k.Created.Format(time.RFC3339))
	}
	return nil
}

func auditExportPublic(args []string) error {
	fs := flag.NewFlagSet("audit export-public", flag.ContinueOnError)
	keyset := fs.String("keyset", "", "path to the audit signing keyset file")
	out := fs.String("out", "", "path to write the verify-only keyset")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *keyset == "" || *out == "" {
		return nerr.New(nerr.InvalidArgument, "nextsql audit export-public", "--keyset and --out are required")
	}
	ks, err := security.OpenAuditKeyset(*keyset)
	if err != nil {
		return err
	}
	if err := ks.WritePublic(*out); err != nil {
		return err
	}
	fmt.Printf("wrote verify-only keyset to %s (no private material); use it with `nextsql audit verify --pubkey %s`\n", *out, *out)
	return nil
}

func auditVerify(args []string) error {
	fs := flag.NewFlagSet("audit verify", flag.ContinueOnError)
	file := fs.String("file", "", "path to the audit log to verify")
	keyset := fs.String("keyset", "", "path to a signing keyset (checks signatures)")
	pubkey := fs.String("pubkey", "", "path to a verify-only keyset (checks signatures); alias for --keyset")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" {
		return nerr.New(nerr.InvalidArgument, "nextsql audit verify", "--file is required")
	}
	if *keyset != "" && *pubkey != "" {
		return nerr.New(nerr.InvalidArgument, "nextsql audit verify", "--keyset and --pubkey are mutually exclusive")
	}
	keysetPath := *keyset
	if keysetPath == "" {
		keysetPath = *pubkey
	}
	var verifiers *security.AuditKeyset
	if keysetPath != "" {
		ks, err := security.OpenAuditKeyset(keysetPath)
		if err != nil {
			return err
		}
		verifiers = ks
	}

	report, err := security.VerifyFile(*file, verifiers)
	if err != nil {
		return err
	}

	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{
			"file":               *file,
			"verified":           report.Verified,
			"lines":              report.Lines,
			"legacy":             report.Legacy,
			"chained":            report.Chained,
			"signed":             report.Signed,
			"signing_started":    report.SigningStarted,
			"signatures_checked": report.SignaturesChecked,
			"first_bad_line":     report.FirstBadLine,
			"problem":            report.Problem,
		})
	}

	fmt.Printf("file:               %s\n", *file)
	fmt.Printf("lines:              %d (legacy %d, chained %d, signed %d)\n", report.Lines, report.Legacy, report.Chained, report.Signed)
	fmt.Printf("signatures checked: %t\n", report.SignaturesChecked)
	if report.Verified {
		if report.Chained == 0 {
			fmt.Println("result:             READABLE — legacy-only file; no hash-chain integrity claim is available")
			return nil
		}
		fmt.Println("result:             OK — the hash chain is intact" + verifiedSignaturesSuffix(report))
		return nil
	}
	fmt.Printf("result:             FAILED at line %d: %s\n", report.FirstBadLine, report.Problem)
	return nerr.New(nerr.InvalidFormat, "nextsql audit verify", "audit chain verification failed")
}

func verifiedSignaturesSuffix(report security.VerifyReport) string {
	if !report.SignaturesChecked {
		return " (signatures not checked — pass --keyset or --pubkey)"
	}
	if report.Signed == 0 {
		return " (no signed lines to check)"
	}
	return " and every signature verified"
}
