package store

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"modernc.org/sqlite"
)

// A canceled SELECT must release its SQLite read transaction before its
// connection returns to the pool. Otherwise that connection can miss sessions
// committed later by another connection.
func TestCanceledQueryReleasesSessionSnapshot(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered := make(chan struct{})
	name := fmt.Sprintf("session_cancel_%d", time.Now().UnixNano())
	err := sqlite.RegisterScalarFunction(name, 0, func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) {
		close(entered)
		<-ctx.Done()
		return int64(42), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	if err := s.CreateSession(t.Context(), testSession("before-cancel", now)); err != nil {
		t.Fatal(err)
	}
	conn, err := s.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	done := make(chan error, 1)
	go func() {
		rows, err := conn.QueryContext(ctx, "SELECT "+name+"(), token_hash FROM sessions LIMIT 1")
		if rows != nil {
			_ = rows.Close()
		}
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("query did not start")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled query: %v", err)
	}
	created := testSession("after-cancel", now)
	if err := s.CreateSession(t.Context(), created); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := conn.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM sessions WHERE token_hash = ?", created.TokenHash[:]).Scan(&count); err != nil {
		t.Fatalf("cannot read a newly committed session: %v", err)
	}
	if count != 1 {
		t.Fatalf("pooled connection cannot see a newly committed session: count=%d", count)
	}
	if _, err := conn.ExecContext(t.Context(), "PRAGMA wal_checkpoint"); err != nil {
		t.Fatalf("canceled query left a live statement on the pooled connection: %v", err)
	}
}
