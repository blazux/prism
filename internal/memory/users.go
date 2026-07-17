package memory

// Multi-user identity store (Prism heavy). Users, groups, and server-side login
// sessions. Passwords are hashed by the caller (server/auth.go, bcrypt); this
// layer only stores and compares the opaque hash.

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Roles and statuses (kept as plain strings in the DB for simplicity).
const (
	RoleGlobalAdmin = "global_admin"
	RoleMember      = "member"

	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusDisabled = "disabled"

	GroupRoleAdmin  = "admin"
	GroupRoleMember = "member"
)

// ErrUserNotFound is returned when a lookup matches no row.
var ErrUserNotFound = errors.New("user not found")

// User is the public view of an account (never carries the password hash).
type User struct {
	ID          int64     `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"displayName"`
	Role        string    `json:"role"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"createdAt"`
}

func (u *User) IsGlobalAdmin() bool { return u != nil && u.Role == RoleGlobalAdmin }

type Group struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
}

// ─── users ──────────────────────────────────────────────────────────────────

// CountUsers reports how many accounts exist — used to make the very first
// signup the global admin.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// CountGlobalAdmins counts approved global admins — used to protect the last
// one from being disabled or demoted (lockout guard).
func (s *Store) CountGlobalAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM users WHERE role = 'global_admin' AND status = 'approved'
	`).Scan(&n)
	return n, err
}

// CreateUser inserts an account with an already-hashed password and returns it.
func (s *Store) CreateUser(ctx context.Context, email, passwordHash, displayName, role, status string) (*User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, display_name, role, status)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, email, display_name, role, status, created_at
	`, email, passwordHash, displayName, role, status).
		Scan(&u.ID, &u.Email, &u.DisplayName, &u.Role, &u.Status, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// GetUserByEmail returns the account plus its password hash (for login checks).
func (s *Store) GetUserByEmail(ctx context.Context, email string) (*User, string, error) {
	var u User
	var hash string
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, display_name, role, status, created_at, password_hash
		FROM users WHERE email = $1
	`, email).Scan(&u.ID, &u.Email, &u.DisplayName, &u.Role, &u.Status, &u.CreatedAt, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", ErrUserNotFound
	}
	if err != nil {
		return nil, "", err
	}
	return &u, hash, nil
}

// UserByPhone finds an approved user whose profile phone matches the given
// digits-only caller number. Matching is on the trailing significant digits so
// "+1 868 555 0123", "8685550123" and "5550123"-with-context all resolve — SIP
// From headers vary by carrier. `digits` must already be stripped to [0-9]; a
// caller shorter than 6 digits (an internal extension) never matches, avoiding
// false identifications. Empty profile phones are ignored.
func (s *Store) UserByPhone(ctx context.Context, digits string) (*User, error) {
	if len(digits) < 6 {
		return nil, ErrUserNotFound
	}
	n := len(digits)
	if n > 9 {
		n = 9
	}
	suffix := digits[len(digits)-n:]
	var u User
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, display_name, role, status, created_at
		FROM users
		WHERE status = 'approved'
		  AND phone <> ''
		  AND length(regexp_replace(phone, '\D', '', 'g')) >= $2
		  AND right(regexp_replace(phone, '\D', '', 'g'), $2) = $1
		LIMIT 1
	`, suffix, n).Scan(&u.ID, &u.Email, &u.DisplayName, &u.Role, &u.Status, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// DirEntry is one line of the phone directory: an approved user with a phone.
type DirEntry struct {
	Name  string
	Phone string
}

// DirectoryEntries lists approved users who have a phone number on their profile —
// the live phone directory (Vortex: transfer_call resolves against this, not a
// separate contacts table).
func (s *Store) DirectoryEntries(ctx context.Context) ([]DirEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT display_name, phone FROM users
		WHERE status = 'approved' AND phone <> ''
		ORDER BY display_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DirEntry
	for rows.Next() {
		var e DirEntry
		if err := rows.Scan(&e.Name, &e.Phone); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) GetUserByID(ctx context.Context, id int64) (*User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, display_name, role, status, created_at
		FROM users WHERE id = $1
	`, id).Scan(&u.ID, &u.Email, &u.DisplayName, &u.Role, &u.Status, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// ListUsers returns all accounts, newest first (admin view).
func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, email, display_name, role, status, created_at
		FROM users ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Email, &u.DisplayName, &u.Role, &u.Status, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SetUserStatus approves / disables an account. Disabling also drops the user's
// live login sessions so access is revoked immediately.
func (s *Store) SetUserStatus(ctx context.Context, id int64, status string) error {
	if _, err := s.pool.Exec(ctx, `UPDATE users SET status = $1 WHERE id = $2`, status, id); err != nil {
		return err
	}
	if status != StatusApproved {
		s.pool.Exec(ctx, `DELETE FROM user_sessions WHERE user_id = $1`, id)
	}
	return nil
}

func (s *Store) SetUserRole(ctx context.Context, id int64, role string) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET role = $1 WHERE id = $2`, role, id)
	return err
}

// ─── login sessions ───────────────────────────────────────────────────────────

// CreateLoginSession stores an opaque token → user mapping with an expiry.
func (s *Store) CreateLoginSession(ctx context.Context, token string, userID int64, expires time.Time) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO user_sessions (token, user_id, expires_at) VALUES ($1, $2, $3)
	`, token, userID, expires)
	return err
}

// UserByLoginToken resolves a session cookie to its (approved, non-expired) user.
// Expired or disabled sessions resolve to ErrUserNotFound.
func (s *Store) UserByLoginToken(ctx context.Context, token string) (*User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		SELECT u.id, u.email, u.display_name, u.role, u.status, u.created_at
		FROM user_sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token = $1 AND s.expires_at > NOW()
	`, token).Scan(&u.ID, &u.Email, &u.DisplayName, &u.Role, &u.Status, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	if u.Status != StatusApproved {
		return nil, ErrUserNotFound
	}
	return &u, nil
}

func (s *Store) DeleteLoginSession(ctx context.Context, token string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM user_sessions WHERE token = $1`, token)
	return err
}

// ─── groups (schema + basics; group management fleshed out in Phase 2) ─────────

func (s *Store) CreateGroup(ctx context.Context, name string) (*Group, error) {
	var g Group
	err := s.pool.QueryRow(ctx, `
		INSERT INTO groups (name) VALUES ($1)
		RETURNING id, name, created_at
	`, name).Scan(&g.ID, &g.Name, &g.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &g, nil
}

func (s *Store) ListGroups(ctx context.Context) ([]Group, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, created_at FROM groups ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.Name, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) AddGroupMember(ctx context.Context, groupID, userID int64, groupRole string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO group_members (group_id, user_id, group_role) VALUES ($1, $2, $3)
		ON CONFLICT (group_id, user_id) DO UPDATE SET group_role = EXCLUDED.group_role
	`, groupID, userID, groupRole)
	return err
}
