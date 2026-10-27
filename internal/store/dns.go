package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// DNSProfile is a set of DNS provider credentials used for DNS-01
// challenges. Config is sealed JSON (see secrets.Box) bound to the profile ID.
type DNSProfile struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Provider    string     `json:"provider"`
	Config      string     `json:"-"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	LastTestAt  *time.Time `json:"last_test_at"`
	LastTestOK  bool       `json:"last_test_ok"`
	LastTestMsg string     `json:"last_test_msg"`
}

const dnsCols = `id, name, provider, config, created_at, updated_at, last_test_at, last_test_ok, last_test_msg`

func scanDNS(row interface{ Scan(...any) error }) (*DNSProfile, error) {
	var p DNSProfile
	var last sql.NullTime
	if err := row.Scan(&p.ID, &p.Name, &p.Provider, &p.Config, &p.CreatedAt, &p.UpdatedAt, &last, &p.LastTestOK, &p.LastTestMsg); err != nil {
		return nil, notFound(err)
	}
	p.LastTestAt = nullTime(last)
	return &p, nil
}

// CreateDNSProfile inserts a profile. Because the sealed config is bound to
// the row ID, the caller passes a function that seals the config once the ID
// is known; both happen in one transaction.
func (s *Store) CreateDNSProfile(ctx context.Context, p *DNSProfile, seal func(id int64) (string, error)) error {
	now := s.now()
	return s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO dns_profiles(name, provider, config, created_at, updated_at) VALUES (?, ?, '', ?, ?)`,
			p.Name, p.Provider, now, now)
		if isUnique(err) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		sealed, err := seal(id)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE dns_profiles SET config = ? WHERE id = ?`, sealed, id); err != nil {
			return err
		}
		p.ID, p.Config, p.CreatedAt, p.UpdatedAt = id, sealed, now, now
		return nil
	})
}

// UpdateDNSProfile replaces the name and sealed config of a profile.
func (s *Store) UpdateDNSProfile(ctx context.Context, p *DNSProfile) error {
	p.UpdatedAt = s.now()
	return s.execOne(ctx, `UPDATE dns_profiles SET name = ?, config = ?, updated_at = ?, last_test_at = NULL, last_test_ok = 0, last_test_msg = '' WHERE id = ?`,
		p.Name, p.Config, p.UpdatedAt, p.ID)
}

// RecordDNSTest stores the outcome of a profile test.
func (s *Store) RecordDNSTest(ctx context.Context, id int64, ok bool, msg string) error {
	return s.execOne(ctx, `UPDATE dns_profiles SET last_test_at = ?, last_test_ok = ?, last_test_msg = ? WHERE id = ?`, s.now(), ok, msg, id)
}

// GetDNSProfile returns a profile by ID.
func (s *Store) GetDNSProfile(ctx context.Context, id int64) (*DNSProfile, error) {
	return scanDNS(s.db.QueryRowContext(ctx, `SELECT `+dnsCols+` FROM dns_profiles WHERE id = ?`, id))
}

// ListDNSProfiles returns all profiles.
func (s *Store) ListDNSProfiles(ctx context.Context) ([]*DNSProfile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+dnsCols+` FROM dns_profiles ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*DNSProfile
	for rows.Next() {
		p, err := scanDNS(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ErrInUse is returned when deleting a row still referenced elsewhere.
var ErrInUse = errorString("store: still in use")

type errorString string

func (e errorString) Error() string { return string(e) }

// DeleteDNSProfile deletes a profile that no domain uses.
func (s *Store) DeleteDNSProfile(ctx context.Context, id int64) error {
	err := s.execOne(ctx, `DELETE FROM dns_profiles WHERE id = ?`, id)
	if err != nil && strings.Contains(err.Error(), "FOREIGN KEY") {
		return ErrInUse
	}
	return err
}

// Domain is a DNS zone served by Marineris. With a DNS profile, certificates
// are issued through DNS-01 (wildcard by default); without, through HTTP-01.
type Domain struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	DNSProfileID *int64    `json:"dns_profile_id"`
	Wildcard     bool      `json:"wildcard"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

const domainCols = `id, name, dns_profile_id, wildcard, created_at, updated_at`

func scanDomain(row interface{ Scan(...any) error }) (*Domain, error) {
	var d Domain
	var pid sql.NullInt64
	if err := row.Scan(&d.ID, &d.Name, &pid, &d.Wildcard, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return nil, notFound(err)
	}
	if pid.Valid {
		d.DNSProfileID = &pid.Int64
	}
	return &d, nil
}

// CreateDomain inserts a domain.
func (s *Store) CreateDomain(ctx context.Context, d *Domain) error {
	now := s.now()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO domains(name, dns_profile_id, wildcard, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		d.Name, d.DNSProfileID, d.Wildcard, now, now)
	if isUnique(err) {
		return ErrConflict
	}
	if err != nil {
		if strings.Contains(err.Error(), "FOREIGN KEY") {
			return ErrNotFound
		}
		return err
	}
	d.ID, err = res.LastInsertId()
	d.CreatedAt, d.UpdatedAt = now, now
	return err
}

// UpdateDomain changes the DNS profile and wildcard flag of a domain.
func (s *Store) UpdateDomain(ctx context.Context, d *Domain) error {
	d.UpdatedAt = s.now()
	err := s.execOne(ctx, `UPDATE domains SET dns_profile_id = ?, wildcard = ?, updated_at = ? WHERE id = ?`,
		d.DNSProfileID, d.Wildcard, d.UpdatedAt, d.ID)
	if err != nil && strings.Contains(err.Error(), "FOREIGN KEY") {
		return ErrNotFound
	}
	return err
}

// GetDomain returns a domain by ID.
func (s *Store) GetDomain(ctx context.Context, id int64) (*Domain, error) {
	return scanDomain(s.db.QueryRowContext(ctx, `SELECT `+domainCols+` FROM domains WHERE id = ?`, id))
}

// ListDomains returns all domains.
func (s *Store) ListDomains(ctx context.Context) ([]*Domain, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+domainCols+` FROM domains ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Domain
	for rows.Next() {
		d, err := scanDomain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeleteDomain deletes a domain.
func (s *Store) DeleteDomain(ctx context.Context, id int64) error {
	return s.execOne(ctx, `DELETE FROM domains WHERE id = ?`, id)
}
