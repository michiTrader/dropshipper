package store

import (
	"context"
	"database/sql"
	"errors"
)

// Keys starting with "_" are internal markers (revision touches).
const (
	revKey        = "_rev"
	catalogRevKey = "_catalog_rev"
)

// GetSetting returns one stored setting value (ErrNotFound when absent).
func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return v, err
}

// SetSetting upserts a setting and bumps the global revision, so other
// processes polling SettingsRevision notice the change.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings(key, value, rev, updated_at)
		VALUES(?, ?, (SELECT COALESCE(MAX(rev),0)+1 FROM settings), ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, rev=excluded.rev, updated_at=excluded.updated_at`,
		key, value, now())
	return err
}

// DeleteSetting removes a setting and touches the revision marker so a
// deletion is also visible to pollers.
func (s *Store) DeleteSetting(ctx context.Context, key string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM settings WHERE key=?`, key); err != nil {
		return err
	}
	return s.SetSetting(ctx, revKey, now().Format("20060102150405.000000000"))
}

// SettingsRevision is the highest revision stored (0 when empty).
func (s *Store) SettingsRevision(ctx context.Context) (int64, error) {
	var r int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(rev),0) FROM settings`).Scan(&r)
	return r, err
}

// ---------- products ----------

// ProductRow is one catalog entry as stored (data is the product JSON).
type ProductRow struct {
	SKU      string
	Data     string
	Active   bool
	Position int
}

// ListProductRows returns all products in display order.
func (s *Store) ListProductRows(ctx context.Context) ([]ProductRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT sku, data, active, position FROM products ORDER BY position, sku`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProductRow
	for rows.Next() {
		var r ProductRow
		var a int
		if err := rows.Scan(&r.SKU, &r.Data, &a, &r.Position); err != nil {
			return nil, err
		}
		r.Active = a != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// SaveProductRow inserts or updates a product. New products go last.
func (s *Store) SaveProductRow(ctx context.Context, r ProductRow) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO products(sku, data, active, position, updated_at)
		VALUES(?, ?, ?, (SELECT COALESCE(MAX(position),0)+1 FROM products), ?)
		ON CONFLICT(sku) DO UPDATE SET data=excluded.data, active=excluded.active, updated_at=excluded.updated_at`,
		r.SKU, r.Data, boolInt(r.Active), now())
	if err != nil {
		return err
	}
	return s.SetSetting(ctx, catalogRevKey, now().Format("20060102150405.000000000"))
}

// DeleteProductRow removes a product.
func (s *Store) DeleteProductRow(ctx context.Context, sku string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM products WHERE sku=?`, sku); err != nil {
		return err
	}
	return s.SetSetting(ctx, catalogRevKey, now().Format("20060102150405.000000000"))
}

// CountOrdersForSKU counts orders that reference a product.
func (s *Store) CountOrdersForSKU(ctx context.Context, sku string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM orders WHERE product_sku=?`, sku).Scan(&n)
	return n, err
}
