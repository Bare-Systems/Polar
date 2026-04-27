package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"polar/pkg/contracts"
)

const (
	DialectSQLite   = "sqlite"
	DialectPostgres = "postgres"
)

const (
	snapshotKindWeatherCurrent     = "weather_current"
	snapshotKindWeatherForecast    = "weather_forecast"
	snapshotKindAirQualityCurrent  = "air_quality_current"
	snapshotKindAirQualityForecast = "air_quality_forecast"
	snapshotKindWildfire           = "wildfire"
	snapshotKindPollen             = "pollen"
	snapshotKindUV                 = "uv"
	snapshotKindPurpleAir          = "purple_air"
	snapshotKindAstronomy          = "astronomy"
)

type Repository struct {
	db      *sql.DB
	dialect string
}

func NewRepository(db *sql.DB, dialect string) *Repository {
	if dialect == "" {
		dialect = DialectSQLite
	}
	return &Repository{db: db, dialect: dialect}
}

func (r *Repository) Migrate(ctx context.Context) error {
	for _, stmt := range r.migrationStatements() {
		if _, err := r.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) InsertReadings(ctx context.Context, readings []contracts.Reading) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, r.bind(`
		INSERT INTO readings(station_id, sensor_id, metric, value, unit, source, quality_flag, recorded_at, received_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
	`))
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, rd := range readings {
		if _, err := stmt.ExecContext(ctx,
			rd.StationID,
			rd.SensorID,
			rd.Metric,
			rd.Value,
			rd.Unit,
			rd.Source,
			rd.QualityFlag,
			rd.RecordedAt.UTC(),
			rd.ReceivedAt.UTC(),
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *Repository) LatestReadings(ctx context.Context) ([]contracts.Reading, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT station_id, sensor_id, metric, value, unit, source, quality_flag, recorded_at, received_at
		FROM readings
		WHERE id IN (SELECT MAX(id) FROM readings GROUP BY metric)
		ORDER BY metric ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []contracts.Reading
	for rows.Next() {
		var rd contracts.Reading
		if err := rows.Scan(
			&rd.StationID, &rd.SensorID, &rd.Metric, &rd.Value, &rd.Unit,
			&rd.Source, &rd.QualityFlag, &rd.RecordedAt, &rd.ReceivedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, rd)
	}
	return out, rows.Err()
}

func (r *Repository) QueryReadings(ctx context.Context, metric string, from, to time.Time) ([]contracts.Reading, error) {
	rows, err := r.db.QueryContext(ctx, r.bind(`
		SELECT station_id, sensor_id, metric, value, unit, source, quality_flag, recorded_at, received_at
		FROM readings WHERE metric = ? AND recorded_at BETWEEN ? AND ?
		ORDER BY recorded_at ASC
	`), metric, from.UTC(), to.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []contracts.Reading
	for rows.Next() {
		var rd contracts.Reading
		if err := rows.Scan(
			&rd.StationID, &rd.SensorID, &rd.Metric, &rd.Value, &rd.Unit,
			&rd.Source, &rd.QualityFlag, &rd.RecordedAt, &rd.ReceivedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, rd)
	}
	return out, rows.Err()
}

func (r *Repository) InsertForecast(ctx context.Context, snapshot contracts.ForecastSnapshot) error {
	if snapshot.TargetID == "" {
		snapshot.TargetID = snapshot.StationID
	}
	if err := r.storeSnapshot(ctx, snapshot.TargetID, snapshotKindWeatherForecast, snapshot.Provider, snapshot.FetchedAt, snapshot); err != nil {
		return err
	}
	return r.insertLegacyForecast(ctx, snapshot)
}

func (r *Repository) LatestForecast(ctx context.Context) (contracts.ForecastSnapshot, error) {
	var payload string
	err := r.db.QueryRowContext(ctx, `SELECT payload_json FROM forecasts ORDER BY fetched_at DESC LIMIT 1`).Scan(&payload)
	if err != nil {
		return contracts.ForecastSnapshot{}, err
	}
	var out contracts.ForecastSnapshot
	if err := json.Unmarshal([]byte(payload), &out); err != nil {
		return contracts.ForecastSnapshot{}, fmt.Errorf("forecast decode: %w", err)
	}
	return out, nil
}

func (r *Repository) LatestForecastForTarget(ctx context.Context, targetID string) (contracts.ForecastSnapshot, error) {
	var snap contracts.ForecastSnapshot
	if err := r.latestSnapshot(ctx, targetID, snapshotKindWeatherForecast, &snap); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return r.LatestForecast(ctx)
		}
		return contracts.ForecastSnapshot{}, err
	}
	return snap, nil
}

func (r *Repository) StoreWeatherCurrent(ctx context.Context, current contracts.WeatherCurrent) error {
	return r.storeSnapshot(ctx, current.TargetID, snapshotKindWeatherCurrent, current.Source, current.FetchedAt, current)
}

func (r *Repository) LatestWeatherCurrent(ctx context.Context, targetID string) (contracts.WeatherCurrent, error) {
	var current contracts.WeatherCurrent
	err := r.latestSnapshot(ctx, targetID, snapshotKindWeatherCurrent, &current)
	return current, err
}

func (r *Repository) StoreAirQualityCurrent(ctx context.Context, current contracts.AirQualityCurrent) error {
	return r.storeSnapshot(ctx, current.TargetID, snapshotKindAirQualityCurrent, current.Source, current.FetchedAt, current)
}

func (r *Repository) LatestAirQualityCurrent(ctx context.Context, targetID string) (contracts.AirQualityCurrent, error) {
	var current contracts.AirQualityCurrent
	err := r.latestSnapshot(ctx, targetID, snapshotKindAirQualityCurrent, &current)
	return current, err
}

func (r *Repository) StoreAirQualityForecast(ctx context.Context, forecast contracts.AirQualityForecast) error {
	return r.storeSnapshot(ctx, forecast.TargetID, snapshotKindAirQualityForecast, forecast.Source, forecast.FetchedAt, forecast)
}

func (r *Repository) LatestAirQualityForecast(ctx context.Context, targetID string) (contracts.AirQualityForecast, error) {
	var forecast contracts.AirQualityForecast
	err := r.latestSnapshot(ctx, targetID, snapshotKindAirQualityForecast, &forecast)
	return forecast, err
}

// StoreContextSnapshot stores any Phase C context payload under a named kind.
func (r *Repository) StoreContextSnapshot(ctx context.Context, targetID, kind, provider string, fetchedAt time.Time, payload any) error {
	return r.storeSnapshot(ctx, targetID, kind, provider, fetchedAt, payload)
}

// LatestWildfireContext retrieves the most recent wildfire context for a target.
func (r *Repository) LatestWildfireContext(ctx context.Context, targetID string) (contracts.WildfireContext, error) {
	var out contracts.WildfireContext
	err := r.latestSnapshot(ctx, targetID, snapshotKindWildfire, &out)
	return out, err
}

// LatestPollenContext retrieves the most recent pollen context for a target.
func (r *Repository) LatestPollenContext(ctx context.Context, targetID string) (contracts.PollenContext, error) {
	var out contracts.PollenContext
	err := r.latestSnapshot(ctx, targetID, snapshotKindPollen, &out)
	return out, err
}

// LatestUVContext retrieves the most recent UV context for a target.
func (r *Repository) LatestUVContext(ctx context.Context, targetID string) (contracts.UVContext, error) {
	var out contracts.UVContext
	err := r.latestSnapshot(ctx, targetID, snapshotKindUV, &out)
	return out, err
}

// LatestPurpleAirAQ retrieves the most recent PurpleAir AQ reading for a target.
func (r *Repository) LatestPurpleAirAQ(ctx context.Context, targetID string) (contracts.PurpleAirAQ, error) {
	var out contracts.PurpleAirAQ
	err := r.latestSnapshot(ctx, targetID, snapshotKindPurpleAir, &out)
	return out, err
}

// LatestAstronomyContext retrieves the most recent astronomy context for a target.
func (r *Repository) LatestAstronomyContext(ctx context.Context, targetID string) (contracts.AstronomyContext, error) {
	var out contracts.AstronomyContext
	err := r.latestSnapshot(ctx, targetID, snapshotKindAstronomy, &out)
	return out, err
}

func (r *Repository) ReplaceAlerts(ctx context.Context, targetID, provider string, fetchedAt time.Time, alerts []contracts.WeatherAlert) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, r.bind(`DELETE FROM weather_alerts WHERE target_id = ? AND provider = ?`), targetID, provider); err != nil {
		return err
	}

	stmt, err := tx.PrepareContext(ctx, r.bind(`
		INSERT INTO weather_alerts(target_id, alert_id, provider, fetched_at, payload_json)
		VALUES(?, ?, ?, ?, ?)
	`))
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, alert := range alerts {
		payload, err := json.Marshal(alert)
		if err != nil {
			return err
		}
		if _, err := stmt.ExecContext(ctx, targetID, alert.ID, provider, fetchedAt.UTC(), string(payload)); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (r *Repository) ActiveAlerts(ctx context.Context, targetID string) ([]contracts.WeatherAlert, error) {
	rows, err := r.db.QueryContext(ctx, r.bind(`
		SELECT payload_json FROM weather_alerts WHERE target_id = ? ORDER BY fetched_at DESC, alert_id ASC
	`), targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]contracts.WeatherAlert, 0)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var alert contracts.WeatherAlert
		if err := json.Unmarshal([]byte(payload), &alert); err != nil {
			return nil, err
		}
		out = append(out, alert)
	}
	return out, rows.Err()
}

func (r *Repository) UpsertProviderStatus(ctx context.Context, status contracts.ProviderStatus) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, r.bind(`
		DELETE FROM provider_status WHERE target_id = ? AND provider = ? AND component = ?
	`), status.TargetID, status.Provider, status.Component); err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, r.bind(`
		INSERT INTO provider_status(target_id, provider, component, status, last_success_at, last_failure_at, last_error, fresh_until, stale, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`),
		status.TargetID,
		status.Provider,
		status.Component,
		status.Status,
		status.LastSuccessAt,
		status.LastFailureAt,
		status.LastError,
		status.FreshUntil,
		status.Stale,
		time.Now().UTC(),
	)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (r *Repository) ProviderStatuses(ctx context.Context, targetID string) ([]contracts.ProviderStatus, error) {
	rows, err := r.db.QueryContext(ctx, r.bind(`
		SELECT target_id, provider, component, status, last_success_at, last_failure_at, last_error, fresh_until, stale
		FROM provider_status WHERE target_id = ?
		ORDER BY provider ASC, component ASC
	`), targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]contracts.ProviderStatus, 0)
	for rows.Next() {
		var status contracts.ProviderStatus
		if err := rows.Scan(
			&status.TargetID,
			&status.Provider,
			&status.Component,
			&status.Status,
			&status.LastSuccessAt,
			&status.LastFailureAt,
			&status.LastError,
			&status.FreshUntil,
			&status.Stale,
		); err != nil {
			return nil, err
		}
		out = append(out, status)
	}
	return out, rows.Err()
}

func (r *Repository) InsertAudit(ctx context.Context, eventType, message string) error {
	_, err := r.db.ExecContext(ctx, r.bind(`
		INSERT INTO audit_events(event_type, message, created_at) VALUES(?, ?, ?)
	`), eventType, message, time.Now().UTC())
	return err
}

func (r *Repository) QueryAudit(ctx context.Context, from, to time.Time, eventType string) ([]map[string]any, error) {
	rows, err := r.db.QueryContext(ctx, r.bind(`
		SELECT event_type, message, created_at FROM audit_events
		WHERE created_at BETWEEN ? AND ? AND (? = '' OR event_type = ?)
		ORDER BY created_at DESC LIMIT 200
	`), from.UTC(), to.UTC(), eventType, eventType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]map[string]any, 0)
	for rows.Next() {
		var t, m string
		var created time.Time
		if err := rows.Scan(&t, &m, &created); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"event_type": t, "message": m, "created_at": created})
	}
	return out, rows.Err()
}

// --- Source license registry (A-5) ---

// UpsertSourceLicense inserts or replaces a provider license record.
func (r *Repository) UpsertSourceLicense(ctx context.Context, lic contracts.SourceLicense) error {
	_, err := r.db.ExecContext(ctx, r.bind(`
		INSERT INTO source_licenses(provider, license, attribution_required, license_url, retention_days, notes, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(provider) DO UPDATE SET
			license = excluded.license,
			attribution_required = excluded.attribution_required,
			license_url = excluded.license_url,
			retention_days = excluded.retention_days,
			notes = excluded.notes,
			updated_at = excluded.updated_at
	`),
		lic.Provider,
		lic.License,
		lic.AttributionRequired,
		lic.LicenseURL,
		lic.RetentionDays,
		lic.Notes,
		time.Now().UTC(),
	)
	return err
}

// ListSourceLicenses returns all registered provider licenses.
func (r *Repository) ListSourceLicenses(ctx context.Context) ([]contracts.SourceLicense, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT provider, license, attribution_required, license_url, retention_days, notes
		FROM source_licenses ORDER BY provider ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]contracts.SourceLicense, 0)
	for rows.Next() {
		var lic contracts.SourceLicense
		if err := rows.Scan(
			&lic.Provider, &lic.License, &lic.AttributionRequired,
			&lic.LicenseURL, &lic.RetentionDays, &lic.Notes,
		); err != nil {
			return nil, err
		}
		out = append(out, lic)
	}
	return out, rows.Err()
}

// --- Consent grants (X-4) ---

// UpsertConsentGrant inserts or replaces a consent grant keyed on (target_id, provider).
func (r *Repository) UpsertConsentGrant(ctx context.Context, g contracts.ConsentGrant) error {
	scopesJSON, _ := json.Marshal(g.GrantedScopes)
	classesJSON, _ := json.Marshal(g.DataClasses)
	_, err := r.db.ExecContext(ctx, r.bind(`
		INSERT INTO consent_grants(
			id, target_id, provider, account_subject,
			granted_scopes_json, data_classes_json,
			retention_days, share_with_agents, share_with_dashboards,
			license_requirements, granted_at, revoked_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(target_id, provider) DO UPDATE SET
			account_subject       = excluded.account_subject,
			granted_scopes_json   = excluded.granted_scopes_json,
			data_classes_json     = excluded.data_classes_json,
			retention_days        = excluded.retention_days,
			share_with_agents     = excluded.share_with_agents,
			share_with_dashboards = excluded.share_with_dashboards,
			license_requirements  = excluded.license_requirements,
			granted_at            = excluded.granted_at,
			revoked_at            = excluded.revoked_at
	`),
		g.ID, g.TargetID, g.Provider, g.AccountSubject,
		string(scopesJSON), string(classesJSON),
		g.RetentionDays, g.ShareWithAgents, g.ShareWithDashboards,
		g.LicenseRequirements, g.GrantedAt.UTC(), nullTime(g.RevokedAt),
	)
	return err
}

// ListConsentGrants returns all active (non-revoked) consent grants.
func (r *Repository) ListConsentGrants(ctx context.Context) ([]contracts.ConsentGrant, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, target_id, provider, account_subject,
		       granted_scopes_json, data_classes_json,
		       retention_days, share_with_agents, share_with_dashboards,
		       license_requirements, granted_at, revoked_at
		FROM consent_grants
		ORDER BY target_id ASC, provider ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []contracts.ConsentGrant
	for rows.Next() {
		var g contracts.ConsentGrant
		var scopesJSON, classesJSON string
		var revokedAt sql.NullTime
		if err := rows.Scan(
			&g.ID, &g.TargetID, &g.Provider, &g.AccountSubject,
			&scopesJSON, &classesJSON,
			&g.RetentionDays, &g.ShareWithAgents, &g.ShareWithDashboards,
			&g.LicenseRequirements, &g.GrantedAt, &revokedAt,
		); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(scopesJSON), &g.GrantedScopes)
		_ = json.Unmarshal([]byte(classesJSON), &g.DataClasses)
		if revokedAt.Valid {
			t := revokedAt.Time
			g.RevokedAt = &t
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}

// --- Commands (D-1) ---

// InsertCommand persists a new Command record with status "pending".
// If a record with the same idempotency_key already exists for the target, the
// existing command_id is returned unchanged (idempotent submit).
func (r *Repository) InsertCommand(ctx context.Context, cmd contracts.Command) error {
	argsJSON, _ := json.Marshal(cmd.Arguments)
	_, err := r.db.ExecContext(ctx, r.bind(`
		INSERT INTO commands(
			command_id, target_id, device_id, capability,
			arguments_json, actor_kind, actor_id,
			idempotency_key, status, requested_at, expires_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`),
		cmd.CommandID, cmd.TargetID, cmd.DeviceID, cmd.Capability,
		string(argsJSON), cmd.Actor.Kind, cmd.Actor.ID,
		cmd.IdempotencyKey, string(cmd.Status),
		cmd.RequestedAt.UTC(), nullTime(cmd.ExpiresAt),
	)
	return err
}

// GetCommand fetches a single Command by ID.
func (r *Repository) GetCommand(ctx context.Context, commandID string) (contracts.Command, error) {
	row := r.db.QueryRowContext(ctx, r.bind(`
		SELECT command_id, target_id, device_id, capability,
		       arguments_json, actor_kind, actor_id,
		       idempotency_key, status, requested_at, expires_at
		FROM commands WHERE command_id = ?
	`), commandID)

	var cmd contracts.Command
	var argsJSON string
	var expiresAt sql.NullTime
	if err := row.Scan(
		&cmd.CommandID, &cmd.TargetID, &cmd.DeviceID, &cmd.Capability,
		&argsJSON, &cmd.Actor.Kind, &cmd.Actor.ID,
		&cmd.IdempotencyKey, (*string)(&cmd.Status),
		&cmd.RequestedAt, &expiresAt,
	); err != nil {
		return contracts.Command{}, err
	}
	_ = json.Unmarshal([]byte(argsJSON), &cmd.Arguments)
	if expiresAt.Valid {
		t := expiresAt.Time
		cmd.ExpiresAt = &t
	}
	return cmd, nil
}

// ListCommandsForTarget returns the most recent commands for a target (newest first).
func (r *Repository) ListCommandsForTarget(ctx context.Context, targetID string, limit int) ([]contracts.Command, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, r.bind(`
		SELECT command_id, target_id, device_id, capability,
		       arguments_json, actor_kind, actor_id,
		       idempotency_key, status, requested_at, expires_at
		FROM commands
		WHERE target_id = ?
		ORDER BY requested_at DESC
		LIMIT ?
	`), targetID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []contracts.Command
	for rows.Next() {
		var cmd contracts.Command
		var argsJSON string
		var expiresAt sql.NullTime
		if err := rows.Scan(
			&cmd.CommandID, &cmd.TargetID, &cmd.DeviceID, &cmd.Capability,
			&argsJSON, &cmd.Actor.Kind, &cmd.Actor.ID,
			&cmd.IdempotencyKey, (*string)(&cmd.Status),
			&cmd.RequestedAt, &expiresAt,
		); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(argsJSON), &cmd.Arguments)
		if expiresAt.Valid {
			t := expiresAt.Time
			cmd.ExpiresAt = &t
		}
		out = append(out, cmd)
	}
	return out, rows.Err()
}

// FindCommandByIdempotencyKey returns the existing command for a (target_id,
// idempotency_key) pair, or sql.ErrNoRows if no match exists. Only meaningful
// when idempotency_key is non-empty.
func (r *Repository) FindCommandByIdempotencyKey(ctx context.Context, targetID, key string) (contracts.Command, error) {
	row := r.db.QueryRowContext(ctx, r.bind(`
		SELECT command_id, target_id, device_id, capability,
		       arguments_json, actor_kind, actor_id,
		       idempotency_key, status, requested_at, expires_at
		FROM commands
		WHERE target_id = ? AND idempotency_key = ? AND idempotency_key != ''
		ORDER BY requested_at DESC
		LIMIT 1
	`), targetID, key)

	var cmd contracts.Command
	var argsJSON string
	var expiresAt sql.NullTime
	if err := row.Scan(
		&cmd.CommandID, &cmd.TargetID, &cmd.DeviceID, &cmd.Capability,
		&argsJSON, &cmd.Actor.Kind, &cmd.Actor.ID,
		&cmd.IdempotencyKey, (*string)(&cmd.Status),
		&cmd.RequestedAt, &expiresAt,
	); err != nil {
		return contracts.Command{}, err
	}
	_ = json.Unmarshal([]byte(argsJSON), &cmd.Arguments)
	if expiresAt.Valid {
		t := expiresAt.Time
		cmd.ExpiresAt = &t
	}
	return cmd, nil
}

// UpdateCommandStatus sets the status column on an existing command row.
func (r *Repository) UpdateCommandStatus(ctx context.Context, commandID string, status contracts.CommandStatus) error {
	res, err := r.db.ExecContext(ctx, r.bind(`
		UPDATE commands SET status = ? WHERE command_id = ?
	`), string(status), commandID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// UpsertCommandResult inserts or replaces the result record for a command.
func (r *Repository) UpsertCommandResult(ctx context.Context, result contracts.CommandResult) error {
	_, err := r.db.ExecContext(ctx, r.bind(`
		INSERT INTO command_results(
			command_id, status, accepted_at, provider_acknowledged_at,
			observed_effect, final_status, error, updated_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(command_id) DO UPDATE SET
			status                   = excluded.status,
			accepted_at              = excluded.accepted_at,
			provider_acknowledged_at = excluded.provider_acknowledged_at,
			observed_effect          = excluded.observed_effect,
			final_status             = excluded.final_status,
			error                    = excluded.error,
			updated_at               = excluded.updated_at
	`),
		result.CommandID, string(result.Status),
		nullTime(result.AcceptedAt), nullTime(result.ProviderAcknowledgedAt),
		result.ObservedEffect, string(result.FinalStatus), result.Error,
		result.UpdatedAt.UTC(),
	)
	return err
}

// GetCommandResult fetches the result record for a command.
func (r *Repository) GetCommandResult(ctx context.Context, commandID string) (contracts.CommandResult, error) {
	row := r.db.QueryRowContext(ctx, r.bind(`
		SELECT command_id, status, accepted_at, provider_acknowledged_at,
		       observed_effect, final_status, error, updated_at
		FROM command_results WHERE command_id = ?
	`), commandID)

	var res contracts.CommandResult
	var acceptedAt, providerAckAt sql.NullTime
	if err := row.Scan(
		&res.CommandID, (*string)(&res.Status),
		&acceptedAt, &providerAckAt,
		&res.ObservedEffect, (*string)(&res.FinalStatus),
		&res.Error, &res.UpdatedAt,
	); err != nil {
		return contracts.CommandResult{}, err
	}
	if acceptedAt.Valid {
		t := acceptedAt.Time
		res.AcceptedAt = &t
	}
	if providerAckAt.Valid {
		t := providerAckAt.Time
		res.ProviderAcknowledgedAt = &t
	}
	return res, nil
}

// --- Retention pruning (A-1) ---

// PruneOutdoorSnapshots deletes outdoor_snapshots rows older than the given cutoff.
func (r *Repository) PruneOutdoorSnapshots(ctx context.Context, before time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, r.bind(`DELETE FROM outdoor_snapshots WHERE fetched_at < ?`), before.UTC())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PruneAlerts deletes weather_alerts rows fetched before the given cutoff.
func (r *Repository) PruneAlerts(ctx context.Context, before time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, r.bind(`DELETE FROM weather_alerts WHERE fetched_at < ?`), before.UTC())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PruneAuditEvents deletes audit_events rows created before the given cutoff.
func (r *Repository) PruneAuditEvents(ctx context.Context, before time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, r.bind(`DELETE FROM audit_events WHERE created_at < ?`), before.UTC())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PruneLegacyForecasts deletes forecasts rows fetched before the given cutoff.
func (r *Repository) PruneLegacyForecasts(ctx context.Context, before time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, r.bind(`DELETE FROM forecasts WHERE fetched_at < ?`), before.UTC())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PruneOutdoorSnapshotsByProvider deletes outdoor_snapshots for a specific
// provider (and optionally a target) fetched before the cutoff. Used by
// consent-aware retention to apply per-provider retention_days.
func (r *Repository) PruneOutdoorSnapshotsByProvider(ctx context.Context, targetID, provider string, before time.Time) (int64, error) {
	var res sql.Result
	var err error
	if targetID == "" || targetID == "*" {
		res, err = r.db.ExecContext(ctx, r.bind(
			`DELETE FROM outdoor_snapshots WHERE provider = ? AND fetched_at < ?`,
		), provider, before.UTC())
	} else {
		res, err = r.db.ExecContext(ctx, r.bind(
			`DELETE FROM outdoor_snapshots WHERE target_id = ? AND provider = ? AND fetched_at < ?`,
		), targetID, provider, before.UTC())
	}
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ExpireStaleCommands marks any non-terminal command whose expires_at has passed
// as "expired" and returns how many rows were updated.
func (r *Repository) ExpireStaleCommands(ctx context.Context, now time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, r.bind(`
		UPDATE commands
		SET    status = 'expired'
		WHERE  expires_at IS NOT NULL
		AND    expires_at < ?
		AND    status NOT IN ('succeeded', 'failed', 'expired', 'rejected')
	`), now.UTC())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PruneCommands deletes terminal command rows (and their results via the FK
// reference) that were requested before the given cutoff.
// Results must be deleted first because of the FK constraint on command_results.
func (r *Repository) PruneCommands(ctx context.Context, before time.Time) (int64, error) {
	// Delete results for commands that will be pruned.
	if _, err := r.db.ExecContext(ctx, r.bind(`
		DELETE FROM command_results
		WHERE command_id IN (
			SELECT command_id FROM commands
			WHERE  requested_at < ?
			AND    status IN ('succeeded', 'failed', 'expired', 'rejected')
		)
	`), before.UTC()); err != nil {
		return 0, err
	}
	res, err := r.db.ExecContext(ctx, r.bind(`
		DELETE FROM commands
		WHERE  requested_at < ?
		AND    status IN ('succeeded', 'failed', 'expired', 'rejected')
	`), before.UTC())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// --- Internal helpers ---

func (r *Repository) storeSnapshot(ctx context.Context, targetID, kind, provider string, fetchedAt time.Time, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, r.bind(`
		INSERT INTO outdoor_snapshots(target_id, kind, provider, fetched_at, payload_json)
		VALUES(?, ?, ?, ?, ?)
	`), targetID, kind, provider, fetchedAt.UTC(), string(raw))
	return err
}

func (r *Repository) latestSnapshot(ctx context.Context, targetID, kind string, out any) error {
	var payload string
	err := r.db.QueryRowContext(ctx, r.bind(`
		SELECT payload_json FROM outdoor_snapshots
		WHERE target_id = ? AND kind = ?
		ORDER BY fetched_at DESC LIMIT 1
	`), targetID, kind).Scan(&payload)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(payload), out)
}

func (r *Repository) insertLegacyForecast(ctx context.Context, snapshot contracts.ForecastSnapshot) error {
	b, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, r.bind(`
		INSERT INTO forecasts(station_id, provider, latitude, longitude, fetched_at, payload_json)
		VALUES(?, ?, ?, ?, ?, ?)
	`), snapshot.StationID, snapshot.Provider, snapshot.Latitude, snapshot.Longitude, snapshot.FetchedAt.UTC(), string(b))
	return err
}

func (r *Repository) bind(query string) string {
	if r.dialect != DialectPostgres {
		return query
	}

	var b strings.Builder
	index := 1
	for _, ch := range query {
		if ch == '?' {
			b.WriteString(fmt.Sprintf("$%d", index))
			index++
			continue
		}
		b.WriteRune(ch)
	}
	return b.String()
}

func (r *Repository) migrationStatements() []string {
	if r.dialect == DialectPostgres {
		return []string{
			`CREATE TABLE IF NOT EXISTS readings (
				id BIGSERIAL PRIMARY KEY,
				station_id TEXT NOT NULL,
				sensor_id TEXT NOT NULL,
				metric TEXT NOT NULL,
				value DOUBLE PRECISION NOT NULL,
				unit TEXT NOT NULL,
				source TEXT NOT NULL,
				quality_flag TEXT NOT NULL,
				recorded_at TIMESTAMPTZ NOT NULL,
				received_at TIMESTAMPTZ NOT NULL
			);`,
			`CREATE INDEX IF NOT EXISTS idx_readings_metric_time ON readings(metric, recorded_at DESC);`,
			`CREATE TABLE IF NOT EXISTS forecasts (
				id BIGSERIAL PRIMARY KEY,
				station_id TEXT NOT NULL,
				provider TEXT NOT NULL,
				latitude DOUBLE PRECISION NOT NULL,
				longitude DOUBLE PRECISION NOT NULL,
				fetched_at TIMESTAMPTZ NOT NULL,
				payload_json TEXT NOT NULL
			);`,
			`CREATE TABLE IF NOT EXISTS outdoor_snapshots (
				id BIGSERIAL PRIMARY KEY,
				target_id TEXT NOT NULL,
				kind TEXT NOT NULL,
				provider TEXT NOT NULL,
				fetched_at TIMESTAMPTZ NOT NULL,
				payload_json TEXT NOT NULL
			);`,
			`CREATE INDEX IF NOT EXISTS idx_outdoor_target_kind_time ON outdoor_snapshots(target_id, kind, fetched_at DESC);`,
			`CREATE TABLE IF NOT EXISTS weather_alerts (
				target_id TEXT NOT NULL,
				alert_id TEXT NOT NULL,
				provider TEXT NOT NULL,
				fetched_at TIMESTAMPTZ NOT NULL,
				payload_json TEXT NOT NULL,
				PRIMARY KEY(target_id, alert_id, provider)
			);`,
			`CREATE TABLE IF NOT EXISTS provider_status (
				target_id TEXT NOT NULL,
				provider TEXT NOT NULL,
				component TEXT NOT NULL,
				status TEXT NOT NULL,
				last_success_at TIMESTAMPTZ NULL,
				last_failure_at TIMESTAMPTZ NULL,
				last_error TEXT NOT NULL DEFAULT '',
				fresh_until TIMESTAMPTZ NULL,
				stale BOOLEAN NOT NULL DEFAULT FALSE,
				updated_at TIMESTAMPTZ NOT NULL,
				PRIMARY KEY(target_id, provider, component)
			);`,
			`CREATE TABLE IF NOT EXISTS audit_events (
				id BIGSERIAL PRIMARY KEY,
				event_type TEXT NOT NULL,
				message TEXT NOT NULL,
				created_at TIMESTAMPTZ NOT NULL
			);`,
			`CREATE TABLE IF NOT EXISTS source_licenses (
				provider TEXT NOT NULL PRIMARY KEY,
				license TEXT NOT NULL DEFAULT '',
				attribution_required BOOLEAN NOT NULL DEFAULT FALSE,
				license_url TEXT NOT NULL DEFAULT '',
				retention_days INTEGER NOT NULL DEFAULT 0,
				notes TEXT NOT NULL DEFAULT '',
				updated_at TIMESTAMPTZ NOT NULL
			);`,
			`CREATE TABLE IF NOT EXISTS consent_grants (
				id TEXT NOT NULL PRIMARY KEY,
				target_id TEXT NOT NULL,
				provider TEXT NOT NULL,
				account_subject TEXT NOT NULL DEFAULT '',
				granted_scopes_json TEXT NOT NULL DEFAULT '[]',
				data_classes_json TEXT NOT NULL DEFAULT '[]',
				retention_days INTEGER NOT NULL DEFAULT 0,
				share_with_agents BOOLEAN NOT NULL DEFAULT FALSE,
				share_with_dashboards BOOLEAN NOT NULL DEFAULT FALSE,
				license_requirements TEXT NOT NULL DEFAULT '',
				granted_at TIMESTAMPTZ NOT NULL,
				revoked_at TIMESTAMPTZ NULL
			);`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_consent_target_provider ON consent_grants(target_id, provider);`,
			`CREATE TABLE IF NOT EXISTS commands (
				command_id TEXT NOT NULL PRIMARY KEY,
				target_id TEXT NOT NULL,
				device_id TEXT NOT NULL DEFAULT '',
				capability TEXT NOT NULL,
				arguments_json TEXT NOT NULL DEFAULT '{}',
				actor_kind TEXT NOT NULL DEFAULT '',
				actor_id TEXT NOT NULL DEFAULT '',
				idempotency_key TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT 'pending',
				requested_at TIMESTAMPTZ NOT NULL,
				expires_at TIMESTAMPTZ NULL
			);`,
			`CREATE INDEX IF NOT EXISTS idx_commands_target_time ON commands(target_id, requested_at DESC);`,
			`CREATE TABLE IF NOT EXISTS command_results (
				command_id TEXT NOT NULL PRIMARY KEY REFERENCES commands(command_id),
				status TEXT NOT NULL,
				accepted_at TIMESTAMPTZ NULL,
				provider_acknowledged_at TIMESTAMPTZ NULL,
				observed_effect TEXT NOT NULL DEFAULT '',
				final_status TEXT NOT NULL DEFAULT '',
				error TEXT NOT NULL DEFAULT '',
				updated_at TIMESTAMPTZ NOT NULL
			);`,
		}
	}

	return []string{
		`CREATE TABLE IF NOT EXISTS readings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			station_id TEXT NOT NULL,
			sensor_id TEXT NOT NULL,
			metric TEXT NOT NULL,
			value REAL NOT NULL,
			unit TEXT NOT NULL,
			source TEXT NOT NULL,
			quality_flag TEXT NOT NULL,
			recorded_at DATETIME NOT NULL,
			received_at DATETIME NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_readings_metric_time ON readings(metric, recorded_at DESC);`,
		`CREATE TABLE IF NOT EXISTS forecasts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			station_id TEXT NOT NULL,
			provider TEXT NOT NULL,
			latitude REAL NOT NULL,
			longitude REAL NOT NULL,
			fetched_at DATETIME NOT NULL,
			payload_json TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS outdoor_snapshots (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			target_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			provider TEXT NOT NULL,
			fetched_at DATETIME NOT NULL,
			payload_json TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_outdoor_target_kind_time ON outdoor_snapshots(target_id, kind, fetched_at DESC);`,
		`CREATE TABLE IF NOT EXISTS weather_alerts (
			target_id TEXT NOT NULL,
			alert_id TEXT NOT NULL,
			provider TEXT NOT NULL,
			fetched_at DATETIME NOT NULL,
			payload_json TEXT NOT NULL,
			PRIMARY KEY(target_id, alert_id, provider)
		);`,
		`CREATE TABLE IF NOT EXISTS provider_status (
			target_id TEXT NOT NULL,
			provider TEXT NOT NULL,
			component TEXT NOT NULL,
			status TEXT NOT NULL,
			last_success_at DATETIME NULL,
			last_failure_at DATETIME NULL,
			last_error TEXT NOT NULL DEFAULT '',
			fresh_until DATETIME NULL,
			stale BOOLEAN NOT NULL DEFAULT 0,
			updated_at DATETIME NOT NULL,
			PRIMARY KEY(target_id, provider, component)
		);`,
		`CREATE TABLE IF NOT EXISTS audit_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			event_type TEXT NOT NULL,
			message TEXT NOT NULL,
			created_at DATETIME NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS source_licenses (
			provider TEXT NOT NULL PRIMARY KEY,
			license TEXT NOT NULL DEFAULT '',
			attribution_required INTEGER NOT NULL DEFAULT 0,
			license_url TEXT NOT NULL DEFAULT '',
			retention_days INTEGER NOT NULL DEFAULT 0,
			notes TEXT NOT NULL DEFAULT '',
			updated_at DATETIME NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS consent_grants (
			id TEXT NOT NULL PRIMARY KEY,
			target_id TEXT NOT NULL,
			provider TEXT NOT NULL,
			account_subject TEXT NOT NULL DEFAULT '',
			granted_scopes_json TEXT NOT NULL DEFAULT '[]',
			data_classes_json TEXT NOT NULL DEFAULT '[]',
			retention_days INTEGER NOT NULL DEFAULT 0,
			share_with_agents INTEGER NOT NULL DEFAULT 0,
			share_with_dashboards INTEGER NOT NULL DEFAULT 0,
			license_requirements TEXT NOT NULL DEFAULT '',
			granted_at DATETIME NOT NULL,
			revoked_at DATETIME NULL
		);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_consent_target_provider ON consent_grants(target_id, provider);`,
		`CREATE TABLE IF NOT EXISTS commands (
			command_id TEXT NOT NULL PRIMARY KEY,
			target_id TEXT NOT NULL,
			device_id TEXT NOT NULL DEFAULT '',
			capability TEXT NOT NULL,
			arguments_json TEXT NOT NULL DEFAULT '{}',
			actor_kind TEXT NOT NULL DEFAULT '',
			actor_id TEXT NOT NULL DEFAULT '',
			idempotency_key TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			requested_at DATETIME NOT NULL,
			expires_at DATETIME NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_commands_target_time ON commands(target_id, requested_at DESC);`,
		`CREATE TABLE IF NOT EXISTS command_results (
			command_id TEXT NOT NULL PRIMARY KEY REFERENCES commands(command_id),
			status TEXT NOT NULL,
			accepted_at DATETIME NULL,
			provider_acknowledged_at DATETIME NULL,
			observed_effect TEXT NOT NULL DEFAULT '',
			final_status TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			updated_at DATETIME NOT NULL
		);`,
	}
}
