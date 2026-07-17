package memory

// User profiles (display/first/last name, phone) and avatars for users and
// agents. Kept separate from the core user auth queries so the hot login path
// stays lean; the profile settings tab and chat UIs read these on demand.

import (
	"context"
	"time"
)

// Profile is the editable identity a user manages in Settings → Profile. Email is
// the login identifier and is read-only here. AvatarVer is the avatar's updated_at
// as a unix epoch (0 when none), used by clients to cache-bust /api/avatar.
type Profile struct {
	UserID      int64  `json:"userId"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
	FirstName   string `json:"firstName"`
	LastName    string `json:"lastName"`
	Phone       string `json:"phone"`
	AvatarVer   int64  `json:"avatarVer"`
}

func (s *Store) GetProfile(ctx context.Context, userID int64) (Profile, error) {
	var p Profile
	err := s.pool.QueryRow(ctx, `
		SELECT u.id, u.email, u.display_name, u.first_name, u.last_name, u.phone,
		       COALESCE(EXTRACT(EPOCH FROM a.updated_at)::bigint, 0)
		FROM users u
		LEFT JOIN avatars a ON a.scope = 'u' || u.id::text
		WHERE u.id = $1
	`, userID).Scan(&p.UserID, &p.Email, &p.DisplayName, &p.FirstName, &p.LastName, &p.Phone, &p.AvatarVer)
	return p, err
}

func (s *Store) UpdateProfile(ctx context.Context, userID int64, displayName, firstName, lastName, phone string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE users SET display_name = $1, first_name = $2, last_name = $3, phone = $4
		WHERE id = $5
	`, displayName, firstName, lastName, phone, userID)
	return err
}

// ─── Avatars ────────────────────────────────────────────────────────────────────

// GetAvatar returns the stored image for a scope, or (nil, "", zero, nil) if none.
func (s *Store) GetAvatar(ctx context.Context, scope string) (data []byte, mime string, updated time.Time, err error) {
	err = s.pool.QueryRow(ctx, `SELECT data, mime, updated_at FROM avatars WHERE scope = $1`, scope).
		Scan(&data, &mime, &updated)
	if err != nil {
		// No row → treat as "no avatar", not an error, so handlers can 404 cleanly.
		return nil, "", time.Time{}, err
	}
	return data, mime, updated, nil
}

// SetAvatar upserts the image for a scope and returns its new version (epoch).
func (s *Store) SetAvatar(ctx context.Context, scope, mime string, data []byte) (int64, error) {
	var ver int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO avatars (scope, mime, data, updated_at) VALUES ($1, $2, $3, NOW())
		ON CONFLICT (scope) DO UPDATE SET mime = EXCLUDED.mime, data = EXCLUDED.data, updated_at = NOW()
		RETURNING EXTRACT(EPOCH FROM updated_at)::bigint
	`, scope, mime, data).Scan(&ver)
	return ver, err
}

func (s *Store) DeleteAvatar(ctx context.Context, scope string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM avatars WHERE scope = $1`, scope)
	return err
}

// AvatarVer returns the avatar version (epoch) for a scope, or 0 if none.
func (s *Store) AvatarVer(ctx context.Context, scope string) int64 {
	var ver int64
	s.pool.QueryRow(ctx, `SELECT COALESCE(EXTRACT(EPOCH FROM updated_at)::bigint, 0) FROM avatars WHERE scope = $1`, scope).Scan(&ver)
	return ver
}
