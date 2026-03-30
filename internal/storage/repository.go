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
	}
}
