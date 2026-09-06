package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"strings"

	"github.com/bzync/nextsql/internal/nerr"
)

// detectResult is the handful of `nextsql lifecycle detect --json` fields
// mode detection needs — a local echo of cmd/nextsql's own detectResult
// shape, not a shared type, since this package does not import cmd/nextsql.
type detectResult struct {
	Status string `json:"status"`
}

// detectMode shells out to `nextsql lifecycle detect --json --data-dir
// dataDir` — the same read-only, already-tested classification the CLI and
// every OS installer already share — and maps its verdict to a Mode. Setup
// mode and Operations mode are sequential lifecycle phases of the same
// install, never concurrent, so this always resolves to exactly one.
//
// Detection is itself a subprocess call, like everything else Setup mode
// does, so Admin never has to special-case an engine-package import just to
// answer "has this host been set up yet."
func detectMode(ctx context.Context, bin, dataDir string) (Mode, error) {
	if bin == "" {
		return "", nerr.New(nerr.InvalidArgument, "admin.detectMode",
			"cannot auto-detect mode: nextsql binary not found; pass --nextsql-bin or --mode")
	}
	cmd := exec.CommandContext(ctx, bin, "lifecycle", "detect", "--json", "--data-dir", dataDir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", nerr.Wrap(nerr.Unavailable, "admin.detectMode", "nextsql lifecycle detect: "+msg, err)
	}

	var r detectResult
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &r); err != nil {
		return "", nerr.Wrap(nerr.Internal, "admin.detectMode", "parse nextsql lifecycle detect output", err)
	}

	switch r.Status {
	case "none", "config-only":
		return ModeSetup, nil
	case "initialized", "running":
		return ModeOperate, nil
	default:
		return "", nerr.New(nerr.Internal, "admin.detectMode", "unrecognized install status: "+r.Status)
	}
}
