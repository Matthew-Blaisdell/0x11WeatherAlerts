// Package store records which alerts have been delivered to which subscriber,
// so restarts and repeated polls never send the same notification twice.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // pure Go driver: no cgo, static binary
)

type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS deliveries (
	alert_id     TEXT    NOT NULL,
	subscriber   TEXT    NOT NULL,
	event        TEXT    NOT NULL,
	delivered_at INTEGER NOT NULL,
	expires_at   INTEGER NOT NULL,
	PRIMARY KEY (alert_id, subscriber)
);
CREATE INDEX IF NOT EXISTS deliveries_expires ON deliveries (expires_at);
`

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // single writer; avoids SQLITE_BUSY under WAL
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: migrating %s: %w", path, err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Delivered(ctx context.Context, alertID, subscriber string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM deliveries WHERE alert_id = ? AND subscriber = ?`, alertID, subscriber).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) RecordDelivery(ctx context.Context, alertID, subscriber, event string, at, expires time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO deliveries (alert_id, subscriber, event, delivered_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
		alertID, subscriber, event, at.Unix(), expires.Unix())
	return err
}

// Prune deletes delivery records for alerts that expired before cutoff.
// The NWS feed stops returning an alert once it expires, so its record is no
// longer needed for deduplication.
func (s *Store) Prune(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM deliveries WHERE expires_at > 0 AND expires_at < ?`, cutoff.Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
