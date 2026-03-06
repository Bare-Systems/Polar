package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"polar/pkg/contracts"
)

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Migrate(ctx context.Context) error {
	stmts := []string{
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
		`CREATE TABLE IF NOT EXISTS audit_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			event_type TEXT NOT NULL,
			message TEXT NOT NULL,
			created_at DATETIME NOT NULL
		);`,
	}
	for _, stmt := range stmts {
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

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO readings(station_id, sensor_id, metric, value, unit, source, quality_flag, recorded_at, received_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
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
	q := `
		SELECT station_id, sensor_id, metric, value, unit, source, quality_flag, recorded_at, received_at
		FROM readings
		WHERE id IN (SELECT MAX(id) FROM readings GROUP BY metric)
		ORDER BY metric ASC
	`
	rows, err := r.db.QueryContext(ctx, q)
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
	rows, err := r.db.QueryContext(ctx, `
		SELECT station_id, sensor_id, metric, value, unit, source, quality_flag, recorded_at, received_at
		FROM readings WHERE metric = ? AND recorded_at BETWEEN ? AND ?
		ORDER BY recorded_at ASC
	`, metric, from.UTC(), to.UTC())
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
	b, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO forecasts(station_id, provider, latitude, longitude, fetched_at, payload_json)
		VALUES(?, ?, ?, ?, ?, ?)
	`, snapshot.StationID, snapshot.Provider, snapshot.Latitude, snapshot.Longitude, snapshot.FetchedAt.UTC(), string(b))
	return err
}

func (r *Repository) LatestForecast(ctx context.Context) (contracts.ForecastSnapshot, error) {
	var payload string
	err := r.db.QueryRowContext(ctx, `
		SELECT payload_json FROM forecasts ORDER BY fetched_at DESC LIMIT 1
	`).Scan(&payload)
	if err != nil {
		return contracts.ForecastSnapshot{}, err
	}
	var out contracts.ForecastSnapshot
	if err := json.Unmarshal([]byte(payload), &out); err != nil {
		return contracts.ForecastSnapshot{}, fmt.Errorf("forecast decode: %w", err)
	}
	return out, nil
}

func (r *Repository) InsertAudit(ctx context.Context, eventType, message string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO audit_events(event_type, message, created_at) VALUES(?, ?, ?)
	`, eventType, message, time.Now().UTC())
	return err
}

func (r *Repository) QueryAudit(ctx context.Context, from, to time.Time, eventType string) ([]map[string]any, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT event_type, message, created_at FROM audit_events
		WHERE created_at BETWEEN ? AND ? AND (? = '' OR event_type = ?)
		ORDER BY created_at DESC LIMIT 200
	`, from.UTC(), to.UTC(), eventType, eventType)
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
