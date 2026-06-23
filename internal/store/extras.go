// Copyright 2026 johnnyrobot and contributors. Licensed under Apache-2.0. See LICENSE.

package store

import (
	"context"
	"database/sql"
	"fmt"
)

// migrateExtras runs after the generated store migrations and before the
// schema-version stamp. It is the canonical place for novel-feature auxiliary
// tables that need to live in the local store.
//
// Edit this file when adding tables for novel commands. Keep migrations
// idempotent with CREATE TABLE IF NOT EXISTS / CREATE INDEX IF NOT EXISTS so
// every store open can safely re-run them.
func (s *Store) migrateExtras(ctx context.Context, conn *sql.Conn) error {
	migrations := []string{
		// Latest seat detail per section (from the class-detail ICAction POST).
		`CREATE TABLE IF NOT EXISTS section_detail (
			class_nbr TEXT PRIMARY KEY,
			enrollment_capacity INTEGER,
			enrolled INTEGER,
			available_seats INTEGER,
			waitlist_capacity INTEGER,
			waitlist_total INTEGER,
			units TEXT,
			instruction_mode TEXT,
			status TEXT,
			fetched_at DATETIME
		)`,
		// Append-only history of section status/seats observed at each sync or
		// watch, powering watch diffs and the 'changed' delta view.
		`CREATE TABLE IF NOT EXISTS seat_snapshots (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			class_nbr TEXT NOT NULL,
			college TEXT,
			term TEXT,
			subject TEXT,
			status TEXT,
			available INTEGER,
			waitlist_total INTEGER,
			observed_at DATETIME NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_seat_snapshots_class ON seat_snapshots(class_nbr, observed_at)`,
		`CREATE INDEX IF NOT EXISTS idx_seat_snapshots_scope ON seat_snapshots(college, term, subject)`,
	}
	for _, m := range migrations {
		if _, err := conn.ExecContext(ctx, m); err != nil {
			return fmt.Errorf("extra migration failed: %w", err)
		}
	}
	return nil
}
