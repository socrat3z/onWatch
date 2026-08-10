package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// AnthropicResetCycle represents an Anthropic quota reset cycle
type AnthropicResetCycle struct {
	ID              int64
	AccountID       int64
	QuotaName       string
	CycleStart      time.Time
	CycleEnd        *time.Time
	ResetsAt        *time.Time
	PeakUtilization float64
	TotalDelta      float64
}

// InsertAnthropicSnapshot inserts an Anthropic snapshot with its quota values.
func (s *Store) InsertAnthropicSnapshot(snapshot *api.AnthropicSnapshot) (int64, error) {
	if snapshot.AccountID == 0 {
		accountID, err := s.defaultProviderAccountID("anthropic")
		if err != nil {
			return 0, err
		}
		snapshot.AccountID = accountID
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.Exec(
		`INSERT INTO anthropic_snapshots (account_id, captured_at, raw_json, quota_count) VALUES (?, ?, ?, ?)`,
		snapshot.AccountID,
		snapshot.CapturedAt.Format(time.RFC3339Nano),
		snapshot.RawJSON,
		len(snapshot.Quotas),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to insert anthropic snapshot: %w", err)
	}

	snapshotID, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get snapshot ID: %w", err)
	}

	for _, q := range snapshot.Quotas {
		var resetsAt interface{}
		if q.ResetsAt != nil {
			resetsAt = q.ResetsAt.Format(time.RFC3339Nano)
		}
		_, err := tx.Exec(
			`INSERT INTO anthropic_quota_values (snapshot_id, quota_name, utilization, resets_at) VALUES (?, ?, ?, ?)`,
			snapshotID, q.Name, q.Utilization, resetsAt,
		)
		if err != nil {
			return 0, fmt.Errorf("failed to insert quota value %s: %w", q.Name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit: %w", err)
	}

	return snapshotID, nil
}

// QueryLatestAnthropic returns the most recent Anthropic snapshot with quotas.
func (s *Store) QueryLatestAnthropic(accountIDs ...int64) (*api.AnthropicSnapshot, error) {
	var snapshot api.AnthropicSnapshot
	var capturedAt string

	query := `SELECT id, account_id, captured_at, quota_count FROM anthropic_snapshots`
	args := []interface{}{}
	if len(accountIDs) > 0 && accountIDs[0] > 0 {
		query += ` WHERE account_id = ?`
		args = append(args, accountIDs[0])
	}
	err := s.db.QueryRow(query+` ORDER BY captured_at DESC LIMIT 1`, args...).Scan(&snapshot.ID, &snapshot.AccountID, &capturedAt, new(int))

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query latest anthropic: %w", err)
	}

	snapshot.CapturedAt, _ = time.Parse(time.RFC3339Nano, capturedAt)

	// Load quota values
	rows, err := s.db.Query(
		`SELECT quota_name, utilization, resets_at FROM anthropic_quota_values WHERE snapshot_id = ? ORDER BY quota_name`,
		snapshot.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query quota values: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var q api.AnthropicQuota
		var resetsAt sql.NullString
		if err := rows.Scan(&q.Name, &q.Utilization, &resetsAt); err != nil {
			return nil, fmt.Errorf("failed to scan quota value: %w", err)
		}
		// Hide historical rows for experimental/unknown quota keys (pre-whitelist data).
		if !api.IsKnownAnthropicQuota(q.Name) {
			continue
		}
		if resetsAt.Valid && resetsAt.String != "" {
			t, _ := time.Parse(time.RFC3339Nano, resetsAt.String)
			q.ResetsAt = &t
		}
		snapshot.Quotas = append(snapshot.Quotas, q)
	}

	return &snapshot, rows.Err()
}

// QueryAnthropicRangeForAccount returns only the chosen account's history.
// It keeps the legacy method intact while the dashboard transitions to account IDs.
func (s *Store) QueryAnthropicRangeForAccount(accountID int64, start, end time.Time, limit ...int) ([]*api.AnthropicSnapshot, error) {
	var err error
	accountID, err = s.scopedProviderAccountID("anthropic", []int64{accountID})
	if err != nil {
		return nil, err
	}
	snapshots, err := s.QueryAnthropicRange(start, end, limit...)
	if err != nil {
		return nil, err
	}
	filtered := snapshots[:0]
	for _, snapshot := range snapshots {
		var id int64
		if err := s.db.QueryRow(`SELECT account_id FROM anthropic_snapshots WHERE id = ?`, snapshot.ID).Scan(&id); err == nil && id == accountID {
			snapshot.AccountID = id
			filtered = append(filtered, snapshot)
		}
	}
	return filtered, nil
}

func (s *Store) QueryAnthropicUtilizationSeriesForAccount(accountID int64, quotaName string, since time.Time) ([]UtilizationPoint, error) {
	var err error
	accountID, err = s.scopedProviderAccountID("anthropic", []int64{accountID})
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT s.captured_at, qv.utilization FROM anthropic_quota_values qv JOIN anthropic_snapshots s ON s.id = qv.snapshot_id WHERE s.account_id = ? AND qv.quota_name = ? AND s.captured_at >= ? ORDER BY s.captured_at ASC`, accountID, quotaName, since.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("failed to query account utilization series: %w", err)
	}
	defer rows.Close()
	var points []UtilizationPoint
	for rows.Next() {
		var captured string
		var point UtilizationPoint
		if err := rows.Scan(&captured, &point.Utilization); err != nil {
			return nil, err
		}
		point.CapturedAt, _ = time.Parse(time.RFC3339Nano, captured)
		points = append(points, point)
	}
	return points, rows.Err()
}

// QueryAnthropicRange returns Anthropic snapshots within a time range with optional limit.
func (s *Store) QueryAnthropicRange(start, end time.Time, limit ...int) ([]*api.AnthropicSnapshot, error) {
	query := `SELECT id, captured_at, quota_count FROM anthropic_snapshots
		WHERE captured_at BETWEEN ? AND ? ORDER BY captured_at ASC`
	args := []interface{}{start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano)}
	if len(limit) > 0 && limit[0] > 0 {
		query = `SELECT id, captured_at, quota_count
			FROM (
				SELECT id, captured_at, quota_count
				FROM anthropic_snapshots
				WHERE captured_at BETWEEN ? AND ?
				ORDER BY captured_at DESC
				LIMIT ?
			) recent
			ORDER BY captured_at ASC`
		args = append(args, limit[0])
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query anthropic range: %w", err)
	}
	defer rows.Close()

	var snapshots []*api.AnthropicSnapshot
	for rows.Next() {
		var snap api.AnthropicSnapshot
		var capturedAt string
		if err := rows.Scan(&snap.ID, &capturedAt, new(int)); err != nil {
			return nil, fmt.Errorf("failed to scan anthropic snapshot: %w", err)
		}
		snap.CapturedAt, _ = time.Parse(time.RFC3339Nano, capturedAt)
		snapshots = append(snapshots, &snap)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Load quota values for each snapshot
	for _, snap := range snapshots {
		qRows, err := s.db.Query(
			`SELECT quota_name, utilization, resets_at FROM anthropic_quota_values WHERE snapshot_id = ? ORDER BY quota_name`,
			snap.ID,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to query quota values for snapshot %d: %w", snap.ID, err)
		}
		for qRows.Next() {
			var q api.AnthropicQuota
			var resetsAt sql.NullString
			if err := qRows.Scan(&q.Name, &q.Utilization, &resetsAt); err != nil {
				qRows.Close()
				return nil, fmt.Errorf("failed to scan quota value: %w", err)
			}
			// Hide historical rows for experimental/unknown quota keys.
			if !api.IsKnownAnthropicQuota(q.Name) {
				continue
			}
			if resetsAt.Valid && resetsAt.String != "" {
				t, _ := time.Parse(time.RFC3339Nano, resetsAt.String)
				q.ResetsAt = &t
			}
			snap.Quotas = append(snap.Quotas, q)
		}
		qRows.Close()
	}

	return snapshots, nil
}

// CreateAnthropicCycle creates a new Anthropic reset cycle.
func (s *Store) CreateAnthropicCycle(quotaName string, cycleStart time.Time, resetsAt *time.Time, accountIDs ...int64) (int64, error) {
	accountID, err := s.scopedProviderAccountID("anthropic", accountIDs)
	if err != nil {
		return 0, err
	}
	var resetsAtVal interface{}
	if resetsAt != nil {
		resetsAtVal = resetsAt.Format(time.RFC3339Nano)
	}

	result, err := s.db.Exec(
		`INSERT INTO anthropic_reset_cycles (account_id, quota_name, cycle_start, resets_at) VALUES (?, ?, ?, ?)`,
		accountID, quotaName, cycleStart.Format(time.RFC3339Nano), resetsAtVal,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to create anthropic cycle: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get cycle ID: %w", err)
	}
	return id, nil
}

// CloseAnthropicCycle closes an Anthropic reset cycle with final stats.
func (s *Store) CloseAnthropicCycle(quotaName string, cycleEnd time.Time, peak, delta float64, accountIDs ...int64) error {
	accountID, err := s.scopedProviderAccountID("anthropic", accountIDs)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`UPDATE anthropic_reset_cycles SET cycle_end = ?, peak_utilization = ?, total_delta = ?
		WHERE account_id = ? AND quota_name = ? AND cycle_end IS NULL`,
		cycleEnd.Format(time.RFC3339Nano), peak, delta, accountID, quotaName,
	)
	if err != nil {
		return fmt.Errorf("failed to close anthropic cycle: %w", err)
	}
	return nil
}

// UpdateAnthropicCycle updates the peak and delta for an active Anthropic cycle.
func (s *Store) UpdateAnthropicCycle(quotaName string, peak, delta float64, accountIDs ...int64) error {
	accountID, err := s.scopedProviderAccountID("anthropic", accountIDs)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`UPDATE anthropic_reset_cycles SET peak_utilization = ?, total_delta = ?
		WHERE account_id = ? AND quota_name = ? AND cycle_end IS NULL`,
		peak, delta, accountID, quotaName,
	)
	if err != nil {
		return fmt.Errorf("failed to update anthropic cycle: %w", err)
	}
	return nil
}

// QueryActiveAnthropicCycle returns the active cycle for an Anthropic quota.
func (s *Store) QueryActiveAnthropicCycle(quotaName string, accountIDs ...int64) (*AnthropicResetCycle, error) {
	accountID, err := s.scopedProviderAccountID("anthropic", accountIDs)
	if err != nil {
		return nil, err
	}
	var cycle AnthropicResetCycle
	var cycleStart string
	var cycleEnd, resetsAt sql.NullString

	err = s.db.QueryRow(
		`SELECT id, account_id, quota_name, cycle_start, cycle_end, resets_at, peak_utilization, total_delta
		FROM anthropic_reset_cycles WHERE account_id = ? AND quota_name = ? AND cycle_end IS NULL`,
		accountID, quotaName,
	).Scan(
		&cycle.ID, &cycle.AccountID, &cycle.QuotaName, &cycleStart, &cycleEnd, &resetsAt,
		&cycle.PeakUtilization, &cycle.TotalDelta,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query active anthropic cycle: %w", err)
	}

	cycle.CycleStart, _ = time.Parse(time.RFC3339Nano, cycleStart)
	if cycleEnd.Valid {
		t, _ := time.Parse(time.RFC3339Nano, cycleEnd.String)
		cycle.CycleEnd = &t
	}
	if resetsAt.Valid {
		t, _ := time.Parse(time.RFC3339Nano, resetsAt.String)
		cycle.ResetsAt = &t
	}

	return &cycle, nil
}

// QueryAnthropicCycleHistory returns completed cycles for an Anthropic quota with optional limit.
func (s *Store) QueryAnthropicCycleHistory(quotaName string, limit ...int) ([]*AnthropicResetCycle, error) {
	query := `SELECT id, quota_name, cycle_start, cycle_end, resets_at, peak_utilization, total_delta
		FROM anthropic_reset_cycles WHERE quota_name = ? AND cycle_end IS NOT NULL ORDER BY cycle_start DESC`
	args := []interface{}{quotaName}
	if len(limit) > 0 && limit[0] > 0 {
		query += ` LIMIT ?`
		args = append(args, limit[0])
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query anthropic cycles: %w", err)
	}
	defer rows.Close()

	var cycles []*AnthropicResetCycle
	for rows.Next() {
		var cycle AnthropicResetCycle
		var cycleStart, cycleEnd string
		var resetsAt sql.NullString

		if err := rows.Scan(&cycle.ID, &cycle.QuotaName, &cycleStart, &cycleEnd, &resetsAt,
			&cycle.PeakUtilization, &cycle.TotalDelta); err != nil {
			return nil, fmt.Errorf("failed to scan anthropic cycle: %w", err)
		}

		cycle.CycleStart, _ = time.Parse(time.RFC3339Nano, cycleStart)
		t, _ := time.Parse(time.RFC3339Nano, cycleEnd)
		cycle.CycleEnd = &t
		if resetsAt.Valid {
			rt, _ := time.Parse(time.RFC3339Nano, resetsAt.String)
			cycle.ResetsAt = &rt
		}

		cycles = append(cycles, &cycle)
	}

	return cycles, rows.Err()
}

func (s *Store) QueryAnthropicCycleHistoryForAccount(accountID int64, quotaName string, limit ...int) ([]*AnthropicResetCycle, error) {
	var err error
	accountID, err = s.scopedProviderAccountID("anthropic", []int64{accountID})
	if err != nil {
		return nil, err
	}
	cycles, err := s.QueryAnthropicCycleHistory(quotaName, limit...)
	if err != nil {
		return nil, err
	}
	filtered := cycles[:0]
	for _, cycle := range cycles {
		var id int64
		if err := s.db.QueryRow(`SELECT account_id FROM anthropic_reset_cycles WHERE id = ?`, cycle.ID).Scan(&id); err == nil && id == accountID {
			cycle.AccountID = id
			filtered = append(filtered, cycle)
		}
	}
	return filtered, nil
}

// QueryAnthropicCyclesSince returns completed cycles for a quota since a given time.
func (s *Store) QueryAnthropicCyclesSince(quotaName string, since time.Time) ([]*AnthropicResetCycle, error) {
	rows, err := s.db.Query(
		`SELECT id, quota_name, cycle_start, cycle_end, resets_at, peak_utilization, total_delta
		FROM anthropic_reset_cycles WHERE quota_name = ? AND cycle_end IS NOT NULL AND cycle_start >= ?
		ORDER BY cycle_start DESC`,
		quotaName, since.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query anthropic cycles since: %w", err)
	}
	defer rows.Close()

	var cycles []*AnthropicResetCycle
	for rows.Next() {
		var cycle AnthropicResetCycle
		var cycleStart, cycleEnd string
		var resetsAt sql.NullString

		if err := rows.Scan(&cycle.ID, &cycle.QuotaName, &cycleStart, &cycleEnd, &resetsAt,
			&cycle.PeakUtilization, &cycle.TotalDelta); err != nil {
			return nil, fmt.Errorf("failed to scan anthropic cycle: %w", err)
		}

		cycle.CycleStart, _ = time.Parse(time.RFC3339Nano, cycleStart)
		t, _ := time.Parse(time.RFC3339Nano, cycleEnd)
		cycle.CycleEnd = &t
		if resetsAt.Valid {
			rt, _ := time.Parse(time.RFC3339Nano, resetsAt.String)
			cycle.ResetsAt = &rt
		}

		cycles = append(cycles, &cycle)
	}

	return cycles, rows.Err()
}

// UtilizationPoint is a lightweight time+utilization pair for rate computation.
type UtilizationPoint struct {
	CapturedAt  time.Time
	Utilization float64
}

// QueryAnthropicUtilizationSeries returns per-quota utilization points since a given time.
// This is lighter than QueryAnthropicRange as it avoids loading all quotas per snapshot.
func (s *Store) QueryAnthropicUtilizationSeries(quotaName string, since time.Time) ([]UtilizationPoint, error) {
	rows, err := s.db.Query(
		`SELECT s.captured_at, qv.utilization
		FROM anthropic_quota_values qv
		JOIN anthropic_snapshots s ON s.id = qv.snapshot_id
		WHERE qv.quota_name = ? AND s.captured_at >= ?
		ORDER BY s.captured_at ASC`,
		quotaName, since.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query utilization series: %w", err)
	}
	defer rows.Close()

	var points []UtilizationPoint
	for rows.Next() {
		var capturedAt string
		var util float64
		if err := rows.Scan(&capturedAt, &util); err != nil {
			return nil, fmt.Errorf("failed to scan utilization point: %w", err)
		}
		t, _ := time.Parse(time.RFC3339Nano, capturedAt)
		points = append(points, UtilizationPoint{CapturedAt: t, Utilization: util})
	}

	return points, rows.Err()
}

// QueryAnthropicCycleOverview returns Anthropic cycles for a given quota
// with cross-quota snapshot data at the peak moment of each cycle.
// Includes the currently active cycle (if any) at the top.
func (s *Store) QueryAnthropicCycleOverview(groupBy string, limit int, accountIDs ...int64) ([]CycleOverviewRow, error) {
	accountID, err := s.scopedProviderAccountID("anthropic", accountIDs)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}

	// Get active cycle first (if any)
	var cycles []*AnthropicResetCycle
	activeCycle, err := s.QueryActiveAnthropicCycle(groupBy, accountID)
	if err != nil {
		return nil, fmt.Errorf("store.QueryAnthropicCycleOverview: active: %w", err)
	}
	if activeCycle != nil {
		cycles = append(cycles, activeCycle)
		limit-- // Reduce limit for completed cycles
	}

	// Get completed cycles
	completedCycles, err := s.QueryAnthropicCycleHistoryForAccount(accountID, groupBy, limit)
	if err != nil {
		return nil, fmt.Errorf("store.QueryAnthropicCycleOverview: %w", err)
	}
	cycles = append(cycles, completedCycles...)

	var overviewRows []CycleOverviewRow
	for _, c := range cycles {
		row := CycleOverviewRow{
			CycleID:    c.ID,
			QuotaType:  c.QuotaName,
			CycleStart: c.CycleStart,
			CycleEnd:   c.CycleEnd, // nil for active cycles
			PeakValue:  c.PeakUtilization,
			TotalDelta: c.TotalDelta,
		}

		// Determine the end boundary for the snapshot query
		// For active cycles (no cycle_end), use current time
		// For completed cycles, use cycle_end (exclusive, as it's the first snapshot of NEW cycle)
		var endBoundary time.Time
		if c.CycleEnd != nil {
			endBoundary = *c.CycleEnd
		} else {
			endBoundary = time.Now().Add(time.Minute) // Include current snapshots
		}

		// Find the snapshot where the primary quota peaked within this cycle
		var snapshotID int64
		var capturedAt string
		err := s.db.QueryRow(
			`SELECT s.id, s.captured_at FROM anthropic_snapshots s
			JOIN anthropic_quota_values qv ON qv.snapshot_id = s.id
			WHERE s.account_id = ? AND qv.quota_name = ? AND s.captured_at >= ? AND s.captured_at < ?
			ORDER BY qv.utilization DESC LIMIT 1`,
			accountID,
			groupBy,
			c.CycleStart.Format(time.RFC3339Nano),
			endBoundary.Format(time.RFC3339Nano),
		).Scan(&snapshotID, &capturedAt)

		if err == sql.ErrNoRows {
			overviewRows = append(overviewRows, row)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("store.QueryAnthropicCycleOverview: peak snapshot: %w", err)
		}

		row.PeakTime, _ = time.Parse(time.RFC3339Nano, capturedAt)

		// Get start values from first snapshot of cycle (for delta calculation)
		startValues := make(map[string]float64)
		var firstSnapshotID int64
		err = s.db.QueryRow(
			`SELECT id FROM anthropic_snapshots
			WHERE account_id = ? AND captured_at >= ? AND captured_at < ?
			ORDER BY captured_at ASC LIMIT 1`,
			accountID,
			c.CycleStart.Format(time.RFC3339Nano),
			endBoundary.Format(time.RFC3339Nano),
		).Scan(&firstSnapshotID)
		if err == nil {
			startRows, err := s.db.Query(
				`SELECT quota_name, utilization FROM anthropic_quota_values WHERE snapshot_id = ?`,
				firstSnapshotID,
			)
			if err == nil {
				for startRows.Next() {
					var name string
					var util float64
					if startRows.Scan(&name, &util) == nil {
						startValues[name] = util
					}
				}
				startRows.Close()
			}
		}

		// Load all quota values from peak snapshot
		qRows, err := s.db.Query(
			`SELECT quota_name, utilization FROM anthropic_quota_values WHERE snapshot_id = ? ORDER BY quota_name`,
			snapshotID,
		)
		if err != nil {
			return nil, fmt.Errorf("store.QueryAnthropicCycleOverview: quota values: %w", err)
		}
		for qRows.Next() {
			var entry CrossQuotaEntry
			if err := qRows.Scan(&entry.Name, &entry.Percent); err != nil {
				qRows.Close()
				return nil, fmt.Errorf("store.QueryAnthropicCycleOverview: scan quota: %w", err)
			}
			// Hide experimental/unknown quota keys from cross-quota history.
			if !api.IsKnownAnthropicQuota(entry.Name) {
				continue
			}
			entry.Value = entry.Percent // utilization is already a percentage
			entry.StartPercent = startValues[entry.Name]
			entry.Delta = entry.Percent - entry.StartPercent
			row.CrossQuotas = append(row.CrossQuotas, entry)
		}
		qRows.Close()

		overviewRows = append(overviewRows, row)
	}

	return overviewRows, nil
}

// AnthropicLatestQuota holds the most recent value for a single quota across all snapshots.
type AnthropicLatestQuota struct {
	Name        string
	Utilization float64
	ResetsAt    *time.Time
	CapturedAt  time.Time
	Source      string // "statusline" or "api"
}

// QueryAnthropicLatestPerQuota returns the most recent value for each distinct quota name.
// Scans only the last 50 snapshots (bounded) and merges in Go - fast even on large DBs.
func (s *Store) QueryAnthropicLatestPerQuota() ([]AnthropicLatestQuota, error) {
	rows, err := s.db.Query(`
		SELECT qv.quota_name, qv.utilization, qv.resets_at, s.captured_at, s.raw_json
		FROM anthropic_quota_values qv
		JOIN anthropic_snapshots s ON s.id = qv.snapshot_id
		WHERE s.id >= (SELECT MAX(id) - 50 FROM anthropic_snapshots)
		ORDER BY s.captured_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("failed to query latest per-quota: %w", err)
	}
	defer rows.Close()

	// Keep only the first (most recent) row per quota_name
	seen := make(map[string]bool)
	var results []AnthropicLatestQuota
	for rows.Next() {
		var name string
		var utilization float64
		var resetsAt sql.NullString
		var capturedAt, rawJSON string
		if err := rows.Scan(&name, &utilization, &resetsAt, &capturedAt, &rawJSON); err != nil {
			return nil, fmt.Errorf("failed to scan latest quota: %w", err)
		}
		// Hide historical rows for experimental/unknown quota keys (pre-whitelist data).
		if !api.IsKnownAnthropicQuota(name) {
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true

		q := AnthropicLatestQuota{
			Name:        name,
			Utilization: utilization,
		}
		q.CapturedAt, _ = time.Parse(time.RFC3339Nano, capturedAt)
		if resetsAt.Valid && resetsAt.String != "" {
			t, _ := time.Parse(time.RFC3339Nano, resetsAt.String)
			q.ResetsAt = &t
		}
		if strings.Contains(rawJSON, `"_source":"statusline"`) {
			q.Source = "statusline"
		} else {
			q.Source = "api"
		}
		results = append(results, q)
	}
	return results, rows.Err()
}

// QueryAllAnthropicQuotaNames returns all distinct quota names from Anthropic reset cycles.
func (s *Store) QueryAllAnthropicQuotaNames() ([]string, error) {
	rows, err := s.db.Query(
		`SELECT DISTINCT quota_name FROM anthropic_reset_cycles ORDER BY quota_name`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query anthropic quota names: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("failed to scan quota name: %w", err)
		}
		// Hide experimental/unknown quota keys written before the whitelist existed.
		if !api.IsKnownAnthropicQuota(name) {
			continue
		}
		names = append(names, name)
	}

	return names, rows.Err()
}
