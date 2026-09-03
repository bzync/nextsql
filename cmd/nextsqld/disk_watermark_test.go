package main

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/bzync/nextsql/internal/diskspace"
	"github.com/bzync/nextsql/internal/executor"
)

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestDiskWatermarkTickTripsAtRejectThreshold(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()

	// A reject threshold of 0 is always met (usedPercent is never negative),
	// so this must trip on the very first tick regardless of real disk state.
	if err := diskWatermarkTick(db, dir, 0, 0, quietLog()); err != nil {
		t.Fatal(err)
	}
	if !db.DiskWatermarkTripped() {
		t.Fatal("expected DiskWatermarkTripped after a tick at/over the reject threshold")
	}
}

func TestDiskWatermarkTickClearsBelowWarnThreshold(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()
	db.SetDiskWatermarkTripped(true)

	// A warn threshold of 100 can only be met by a completely full
	// filesystem, which a fresh temp dir never is, so this must clear.
	if err := diskWatermarkTick(db, dir, 100, 100, quietLog()); err != nil {
		t.Fatal(err)
	}
	if db.DiskWatermarkTripped() {
		t.Fatal("expected DiskWatermarkTripped to clear once usage is below the warn threshold")
	}
}

func TestDiskWatermarkTickHysteresisStaysTrippedBetweenWarnAndReject(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()
	u, err := diskspace.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	used := u.UsedFraction() * 100

	db.SetDiskWatermarkTripped(true)
	// warn just below current usage, reject far above it: usage sits between
	// the two, which per the documented hysteresis must NOT clear the
	// tripped state (only dropping below warn clears it).
	warn := used - 0.01
	if warn < 0 {
		warn = 0
	}
	reject := used + 50
	if reject > 100 {
		reject = 100
	}
	if err := diskWatermarkTick(db, dir, warn, reject, quietLog()); err != nil {
		t.Fatal(err)
	}
	if !db.DiskWatermarkTripped() {
		t.Fatal("expected DiskWatermarkTripped to remain set between the warn and reject thresholds")
	}
}

func TestDiskWatermarkTickRejectsMissingPath(t *testing.T) {
	db := testDB(t)
	if err := diskWatermarkTick(db, "/this/path/almost-certainly-does-not-exist-nextsql-test", 85, 95, quietLog()); err == nil {
		t.Fatal("expected an error for a nonexistent data dir")
	}
}

func TestStartDiskWatermarkMonitorNoopWithoutPolicyOrDataDir(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := quietLog()
	startDiskWatermarkMonitor(ctx, nil, "/tmp/whatever", 1000, 85, 95, log)
	startDiskWatermarkMonitor(ctx, &executor.DB{}, "", 1000, 85, 95, log)
	startDiskWatermarkMonitor(ctx, &executor.DB{}, "/tmp/whatever", 0, 85, 95, log)
}
