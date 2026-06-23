// Copyright 2026 johnnyrobot and contributors. Licensed under Apache-2.0. See LICENSE.

package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SectionDetailRow is the latest seat detail for one section.
type SectionDetailRow struct {
	ClassNbr        string    `json:"class_nbr"`
	EnrollCap       int       `json:"enrollment_capacity"`
	Enrolled        int       `json:"enrolled"`
	AvailableSeats  int       `json:"available_seats"`
	WaitlistCap     int       `json:"waitlist_capacity"`
	WaitlistTotal   int       `json:"waitlist_total"`
	Units           string    `json:"units"`
	InstructionMode string    `json:"instruction_mode"`
	Status          string    `json:"status"`
	FetchedAt       time.Time `json:"fetched_at"`
}

// SeatSnapshot is one observation of a section's status/seats. Available and
// WaitlistTotal are pointers because grid-only snapshots record status without
// exact seat counts (the counts come only from the detail page).
type SeatSnapshot struct {
	ClassNbr      string    `json:"class_nbr"`
	College       string    `json:"college,omitempty"`
	Term          string    `json:"term,omitempty"`
	Subject       string    `json:"subject,omitempty"`
	Status        string    `json:"status"`
	Available     *int      `json:"available,omitempty"`
	WaitlistTotal *int      `json:"waitlist_total,omitempty"`
	ObservedAt    time.Time `json:"observed_at"`
}

// SectionsByScope returns the raw section JSON for sections matching the given
// scope. Empty filter fields are ignored, so passing only college+term+subject
// returns every section of a subject. catalog narrows to one course.
func (s *Store) SectionsByScope(college, term, subject, catalog string) ([]json.RawMessage, error) {
	var where []string
	var args []any
	add := func(col, val string) {
		if v := strings.TrimSpace(val); v != "" {
			where = append(where, col+" = ?")
			args = append(args, v)
		}
	}
	add("college", strings.ToUpper(college))
	add("term", term)
	add("subject", strings.ToUpper(subject))
	add("catalog", catalog)

	query := `SELECT data FROM sections`
	if len(where) > 0 {
		// #nosec G202 -- where clauses are built only from the literal
		// "col = ?" fragments above with constant column names; all user
		// values are bound via args, never interpolated into the SQL text.
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY catalog, section"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query sections: %w", err)
	}
	defer rows.Close()
	var out []json.RawMessage
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		out = append(out, json.RawMessage(data))
	}
	return out, rows.Err()
}

// UpsertSectionDetail records the latest seat detail for a section.
func (s *Store) UpsertSectionDetail(d SectionDetailRow) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(
		`INSERT INTO section_detail
		   (class_nbr, enrollment_capacity, enrolled, available_seats, waitlist_capacity, waitlist_total, units, instruction_mode, status, fetched_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(class_nbr) DO UPDATE SET
		   enrollment_capacity=excluded.enrollment_capacity,
		   enrolled=excluded.enrolled,
		   available_seats=excluded.available_seats,
		   waitlist_capacity=excluded.waitlist_capacity,
		   waitlist_total=excluded.waitlist_total,
		   units=excluded.units,
		   instruction_mode=excluded.instruction_mode,
		   status=excluded.status,
		   fetched_at=excluded.fetched_at`,
		d.ClassNbr, d.EnrollCap, d.Enrolled, d.AvailableSeats, d.WaitlistCap, d.WaitlistTotal,
		d.Units, d.InstructionMode, d.Status, d.FetchedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("upsert section_detail: %w", err)
	}
	return nil
}

// GetSectionDetail returns the stored detail for a section, or ok=false when
// none has been fetched yet.
func (s *Store) GetSectionDetail(classNbr string) (SectionDetailRow, bool, error) {
	var d SectionDetailRow
	var fetched sql.NullString
	var units, mode, status sql.NullString
	var cap, enr, avail, wcap, wtot sql.NullInt64
	err := s.db.QueryRow(
		`SELECT class_nbr, enrollment_capacity, enrolled, available_seats, waitlist_capacity, waitlist_total, units, instruction_mode, status, fetched_at
		 FROM section_detail WHERE class_nbr = ?`, classNbr,
	).Scan(&d.ClassNbr, &cap, &enr, &avail, &wcap, &wtot, &units, &mode, &status, &fetched)
	if err == sql.ErrNoRows {
		return d, false, nil
	}
	if err != nil {
		return d, false, err
	}
	d.EnrollCap = int(cap.Int64)
	d.Enrolled = int(enr.Int64)
	d.AvailableSeats = int(avail.Int64)
	d.WaitlistCap = int(wcap.Int64)
	d.WaitlistTotal = int(wtot.Int64)
	d.Units = units.String
	d.InstructionMode = mode.String
	d.Status = status.String
	if fetched.Valid {
		d.FetchedAt, _ = time.Parse(time.RFC3339, fetched.String)
	}
	return d, true, nil
}

// AddSeatSnapshot appends a status/seats observation for a section.
func (s *Store) AddSeatSnapshot(snap SeatSnapshot) error {
	if snap.ObservedAt.IsZero() {
		snap.ObservedAt = time.Now()
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(
		`INSERT INTO seat_snapshots (class_nbr, college, term, subject, status, available, waitlist_total, observed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		snap.ClassNbr, snap.College, snap.Term, snap.Subject, snap.Status,
		nullableInt(snap.Available), nullableInt(snap.WaitlistTotal), snap.ObservedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("insert seat_snapshot: %w", err)
	}
	return nil
}

func nullableInt(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}

// RecentSnapshots returns up to limit most-recent snapshots for one section,
// newest first.
func (s *Store) RecentSnapshots(classNbr string, limit int) ([]SeatSnapshot, error) {
	if limit <= 0 {
		limit = 2
	}
	rows, err := s.db.Query(
		`SELECT class_nbr, college, term, subject, status, available, waitlist_total, observed_at
		 FROM seat_snapshots WHERE class_nbr = ? ORDER BY observed_at DESC, id DESC LIMIT ?`,
		classNbr, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSnapshots(rows)
}

// SnapshotsFiltered returns every snapshot matching the scope, ordered by
// section then observation time, so the caller can compute new/dropped/changed
// deltas across the synced history.
func (s *Store) SnapshotsFiltered(college, term, subject string) ([]SeatSnapshot, error) {
	var where []string
	var args []any
	add := func(col, val string) {
		if v := strings.TrimSpace(val); v != "" {
			where = append(where, col+" = ?")
			args = append(args, v)
		}
	}
	add("college", strings.ToUpper(college))
	add("term", term)
	add("subject", strings.ToUpper(subject))
	query := `SELECT class_nbr, college, term, subject, status, available, waitlist_total, observed_at FROM seat_snapshots`
	if len(where) > 0 {
		// #nosec G202 -- where clauses are built only from the literal
		// "col = ?" fragments above with constant column names; all user
		// values are bound via args, never interpolated into the SQL text.
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY class_nbr, observed_at"
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSnapshots(rows)
}

func scanSnapshots(rows *sql.Rows) ([]SeatSnapshot, error) {
	var out []SeatSnapshot
	for rows.Next() {
		var snap SeatSnapshot
		var college, term, subject, status, observed sql.NullString
		var avail, wtot sql.NullInt64
		if err := rows.Scan(&snap.ClassNbr, &college, &term, &subject, &status, &avail, &wtot, &observed); err != nil {
			return nil, err
		}
		snap.College = college.String
		snap.Term = term.String
		snap.Subject = subject.String
		snap.Status = status.String
		if avail.Valid {
			v := int(avail.Int64)
			snap.Available = &v
		}
		if wtot.Valid {
			v := int(wtot.Int64)
			snap.WaitlistTotal = &v
		}
		if observed.Valid {
			snap.ObservedAt, _ = time.Parse(time.RFC3339, observed.String)
		}
		out = append(out, snap)
	}
	return out, rows.Err()
}
