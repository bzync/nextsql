package maintenance

import (
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

func TestManagerSerializesAndReports(t *testing.T) {
	m := New()
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := m.Run("dead_versions", "database", func() (int, error) {
			close(entered)
			<-release
			return 7, nil
		})
		done <- err
	}()
	<-entered
	st := m.Status()
	if st.Active == nil || st.Active.Scope != "database" {
		t.Fatalf("active status %+v", st)
	}
	if _, err := m.Run("other", "x", func() (int, error) { return 0, nil }); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("concurrent run: %v", err)
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("run did not finish")
	}
	st = m.Status()
	if st.Active != nil || st.Last == nil || st.Last.Affected != 7 || st.Last.Finished.IsZero() {
		t.Fatalf("last status %+v", st)
	}
}

func TestManagerPauseResume(t *testing.T) {
	m := New()
	m.Pause()
	if _, err := m.Run("x", "y", func() (int, error) { return 1, nil }); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("paused run: %v", err)
	}
	if !m.Status().Paused {
		t.Fatal("pause not visible")
	}
	m.Resume()
	if n, err := m.Run("x", "y", func() (int, error) { return 1, nil }); err != nil || n != 1 {
		t.Fatalf("resumed run = %d, %v", n, err)
	}
}

func TestBudgetCPUAndMemory(t *testing.T) {
	m := New()
	if err := m.SetLimits(Limits{CPU: time.Nanosecond, Memory: 32, IO: 8}); err != nil {
		t.Fatal(err)
	}
	_, err := m.RunBudgeted("x", "cpu", func(b *Budget) (int, error) {
		time.Sleep(time.Millisecond)
		return 0, b.Check()
	})
	if !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("CPU budget: %v", err)
	}
	if st := m.Status(); st.Last == nil || !st.Last.Failed || st.Last.CPUUsed <= 0 {
		t.Fatalf("CPU status %+v", st)
	}
	if err := m.SetLimits(Limits{CPU: time.Second, Memory: 32, IO: 8}); err != nil {
		t.Fatal(err)
	}
	_, err = m.RunBudgeted("x", "memory", func(b *Budget) (int, error) {
		return 0, b.ReserveMemory(33)
	})
	if !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("memory budget: %v", err)
	}
	if err := m.SetLimits(Limits{CPU: time.Second, Memory: 64, IO: 1}); err != nil {
		t.Fatal(err)
	}
	_, err = m.RunBudgeted("x", "io", func(b *Budget) (int, error) { return 0, b.ConsumeIO(2) })
	if !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("I/O budget: %v", err)
	}
}
