package store

import (
	"context"
	"testing"
	"time"
)

// 写事务开事务即取写锁：先读后写的事务期间，另一个连接的写入排队等它提交，而不是抢先
// 提交、让它在写入时撞 SQLITE_BUSY_SNAPSHOT（续租心跳与提案落库就是这种并发）。
func TestWriteTransactionsHoldTheLockFromBegin(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	s.db.SetMaxOpenConns(2)
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE txlock_probe (n INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO txlock_probe (n) VALUES (0)`); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT n FROM txlock_probe`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	concurrent := make(chan error, 1)
	go func() {
		_, err := s.db.ExecContext(ctx, `UPDATE txlock_probe SET n = n + 10`)
		concurrent <- err
	}()
	time.Sleep(50 * time.Millisecond) // 给并发写入抢先提交的机会
	if _, err := tx.ExecContext(ctx, `UPDATE txlock_probe SET n = ?`, n+1); err != nil {
		t.Fatalf("read-then-write transaction lost its snapshot: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-concurrent; err != nil {
		t.Fatalf("concurrent writer must wait, not fail: %v", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT n FROM txlock_probe`).Scan(&n); err != nil || n != 11 {
		t.Fatalf("writes must serialize: n=%d err=%v", n, err)
	}
}
