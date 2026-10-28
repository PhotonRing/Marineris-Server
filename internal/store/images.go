package store

import (
	"context"
	"time"
)

// Image kinds.
const (
	ImageBrand   = "brand"   // key: favicon, logo, background
	ImageService = "service" // key: <stack>/<service>
)

// Image is an uploaded image (already validated by package icons).
type Image struct {
	ContentType string
	Data        []byte
	UpdatedAt   time.Time
}

// SetImage stores (or replaces) an image.
func (s *Store) SetImage(ctx context.Context, kind, key, contentType string, data []byte) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO images(kind, key, content_type, data, updated_at) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(kind, key) DO UPDATE SET content_type = excluded.content_type, data = excluded.data, updated_at = excluded.updated_at`,
		kind, key, contentType, data, s.now())
	return err
}

// GetImage returns an image.
func (s *Store) GetImage(ctx context.Context, kind, key string) (*Image, error) {
	var im Image
	err := s.db.QueryRowContext(ctx, `SELECT content_type, data, updated_at FROM images WHERE kind = ? AND key = ?`, kind, key).
		Scan(&im.ContentType, &im.Data, &im.UpdatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	return &im, nil
}

// ImageVersions returns the update time of every image of a kind, by key.
func (s *Store) ImageVersions(ctx context.Context, kind string) (map[string]time.Time, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, updated_at FROM images WHERE kind = ?`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var k string
		var t time.Time
		if err := rows.Scan(&k, &t); err != nil {
			return nil, err
		}
		out[k] = t
	}
	return out, rows.Err()
}

// DeleteImage removes an image (no error when absent).
func (s *Store) DeleteImage(ctx context.Context, kind, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM images WHERE kind = ? AND key = ?`, kind, key)
	return err
}

// DeleteImagesWithPrefix removes the images of a kind whose key starts with
// prefix (the icons of a deleted stack).
func (s *Store) DeleteImagesWithPrefix(ctx context.Context, kind, prefix string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM images WHERE kind = ? AND substr(key, 1, ?) = ?`, kind, len(prefix), prefix)
	return err
}

// Done reports whether a one-time task (a data migration) has run.
func (s *Store) Done(ctx context.Context, task string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM settings WHERE key = ?`, "done:"+task).Scan(&n)
	return n > 0, err
}

// MarkDone records that a one-time task has run.
func (s *Store) MarkDone(ctx context.Context, task string) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO settings(key, value) VALUES (?, ?)`, "done:"+task, s.now().Format(time.RFC3339))
	return err
}
