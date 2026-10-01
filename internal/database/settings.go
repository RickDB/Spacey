package database

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
)

func (d *DB) AppSettings(ctx context.Context, out any) (bool, error) {
	var raw []byte
	err := d.Pool.QueryRow(ctx, "SELECT data FROM app_settings WHERE id=1").Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(raw, out)
}
func (d *DB) SaveAppSettings(ctx context.Context, value any, initial bool) (bool, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return false, err
	}
	if initial {
		result, err := d.Pool.Exec(ctx, "INSERT INTO app_settings(id,data) VALUES(1,$1) ON CONFLICT DO NOTHING", raw)
		return result.RowsAffected() == 1, err
	}
	result, err := d.Pool.Exec(ctx, "UPDATE app_settings SET data=$1,updated_at=now() WHERE id=1", raw)
	return result.RowsAffected() == 1, err
}
