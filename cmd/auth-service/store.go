package main

import (
	"encoding/json"
	"log"
	"sync"

	"github.com/jmoiron/sqlx"
)

// DemoUser represents a pre-configured demo user.
type DemoUser struct {
	Password string   `json:"-"`
	Name     string   `json:"name"`
	Roles    []string `json:"roles"`
	Entity   string   `json:"entity"`
}

// AuthCode stores PKCE authorization code data.
type AuthCode struct {
	Sub                 string `json:"sub"`
	CodeChallenge       string `json:"code_challenge"`
	CodeChallengeMethod string `json:"code_challenge_method"`
	ClientID            string `json:"client_id"`
	RedirectURI         string `json:"redirect_uri"`
}

// Session stores session data.
type Session struct {
	SID       string   `json:"sid"`
	Sub       string   `json:"sub"`
	Email     string   `json:"email"`
	Name      string   `json:"name"`
	Roles     []string `json:"roles"`
	Entity    string   `json:"entity"`
	CreatedAt int64    `json:"created_at"`
	ExpiresAt int64    `json:"expires_at"`
	DPoPJKT   *string  `json:"dpop_jkt"`
	Status    string   `json:"status"`
}

// Store holds all in-memory data with optional Postgres write-through.
// The in-memory maps are the primary source of truth for live lookups;
// Postgres provides durability across pod restarts.
type Store struct {
	mu sync.RWMutex

	db *sqlx.DB // nil = in-memory only

	demoUsers       map[string]*DemoUser
	authCodes       map[string]*AuthCode
	sessions        map[string]*Session
	revokedSessions map[string]struct{}
	dpopJTICache    map[string]struct{}
}

// NewStore creates a Store pre-populated with demo users.
// If db is non-nil, existing sessions are loaded from Postgres on startup.
func NewStore(db *sqlx.DB) *Store {
	s := &Store{
		db: db,
		demoUsers: map[string]*DemoUser{
			"admin@demo.local": {
				Password: "demo1234",
				Name:     "Admin User",
				Roles:    []string{"architect", "platform-admin"},
				Entity:   "PLATFORM",
			},
			"trader@demo.local": {
				Password: "demo1234",
				Name:     "Trader User",
				Roles:    []string{"trader"},
				Entity:   "MARKETS",
			},
			"readonly@demo.local": {
				Password: "demo1234",
				Name:     "Read-Only User",
				Roles:    []string{"readonly"},
				Entity:   "OPS",
			},
		},
		authCodes:       make(map[string]*AuthCode),
		sessions:        make(map[string]*Session),
		revokedSessions: make(map[string]struct{}),
		dpopJTICache:    make(map[string]struct{}),
	}
	if db != nil {
		s.loadFromDB()
	}
	return s
}

// loadFromDB hydrates the in-memory session maps from Postgres on startup.
// This ensures sessions survive pod restarts.
func (s *Store) loadFromDB() {
	type dbRow struct {
		SID       string `db:"sid"`
		Sub       string `db:"sub"`
		Email     string `db:"email"`
		Name      string `db:"name"`
		Roles     []byte `db:"roles"`
		Entity    string `db:"entity"`
		CreatedAt int64  `db:"created_at"`
		ExpiresAt int64  `db:"expires_at"`
		DPoPJKT   string `db:"dpop_jkt"`
		Status    string `db:"status"`
	}

	var rows []dbRow
	if err := s.db.Select(&rows, `
		SELECT sid, sub, email, name, roles, entity, created_at, expires_at, dpop_jkt, status
		FROM auth_sessions
		ORDER BY created_at DESC
	`); err != nil {
		log.Printf("auth-service: failed to load sessions from Postgres: %v", err)
		return
	}

	for _, r := range rows {
		var roles []string
		_ = json.Unmarshal(r.Roles, &roles)
		if roles == nil {
			roles = []string{}
		}

		sess := &Session{
			SID:       r.SID,
			Sub:       r.Sub,
			Email:     r.Email,
			Name:      r.Name,
			Roles:     roles,
			Entity:    r.Entity,
			CreatedAt: r.CreatedAt,
			ExpiresAt: r.ExpiresAt,
			Status:    r.Status,
		}
		if r.DPoPJKT != "" {
			jkt := r.DPoPJKT
			sess.DPoPJKT = &jkt
		}

		s.sessions[r.SID] = sess
		if r.Status == "revoked" {
			s.revokedSessions[r.SID] = struct{}{}
		}
	}

	log.Printf("auth-service: loaded %d sessions from Postgres", len(rows))
}

// persistSession upserts a session to Postgres. Called with s.mu held.
func (s *Store) persistSession(sess *Session) {
	if s.db == nil {
		return
	}
	rolesJSON, _ := json.Marshal(sess.Roles)
	dpopJKT := ""
	if sess.DPoPJKT != nil {
		dpopJKT = *sess.DPoPJKT
	}
	if _, err := s.db.Exec(`
		INSERT INTO auth_sessions (sid, sub, email, name, roles, entity, created_at, expires_at, dpop_jkt, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (sid) DO UPDATE SET
			status     = EXCLUDED.status,
			expires_at = EXCLUDED.expires_at
	`, sess.SID, sess.Sub, sess.Email, sess.Name,
		rolesJSON, sess.Entity, sess.CreatedAt, sess.ExpiresAt, dpopJKT, sess.Status,
	); err != nil {
		log.Printf("auth-service: failed to persist session %s: %v", sess.SID, err)
	}
}

// GetDemoUser returns a demo user by email.
func (s *Store) GetDemoUser(email string) (*DemoUser, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.demoUsers[email]
	return u, ok
}

// ListDemoUsers returns all demo users as a slice of maps.
func (s *Store) ListDemoUsers() []map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]map[string]interface{}, 0, len(s.demoUsers))
	for email, u := range s.demoUsers {
		result = append(result, map[string]interface{}{
			"email":  email,
			"name":   u.Name,
			"roles":  u.Roles,
			"entity": u.Entity,
		})
	}
	return result
}

// StoreAuthCode saves an authorization code.
func (s *Store) StoreAuthCode(code string, ac *AuthCode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authCodes[code] = ac
}

// PopAuthCode retrieves and removes an authorization code.
func (s *Store) PopAuthCode(code string) (*AuthCode, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ac, ok := s.authCodes[code]
	if ok {
		delete(s.authCodes, code)
	}
	return ac, ok
}

// StoreSession saves a session, revoking any existing active sessions for the
// same subject first. This enforces a single active session per user — a new
// login always supersedes the previous one.
func (s *Store) StoreSession(sid string, sess *Session) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Revoke any existing active sessions for the same user.
	for existingSID, existing := range s.sessions {
		if existing.Sub == sess.Sub && existing.Status == "active" {
			existing.Status = "revoked"
			s.revokedSessions[existingSID] = struct{}{}
			// Persist the revocation to DB.
			if s.db != nil {
				if _, err := s.db.Exec(
					"UPDATE auth_sessions SET status='revoked' WHERE sid=$1", existingSID,
				); err != nil {
					log.Printf("auth-service: failed to revoke old session %s in DB: %v", existingSID, err)
				}
			}
		}
	}

	s.sessions[sid] = sess
	s.persistSession(sess)
}

// GetSession retrieves a session by SID.
func (s *Store) GetSession(sid string) (*Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[sid]
	return sess, ok
}

// ListSessions returns all sessions (from in-memory map, which is seeded from
// Postgres on startup and kept in sync on every write).
func (s *Store) ListSessions() []*Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		result = append(result, sess)
	}
	return result
}

// RevokeSession marks a session as revoked.
func (s *Store) RevokeSession(sid string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sid]
	if !ok {
		return false
	}
	s.revokedSessions[sid] = struct{}{}
	sess.Status = "revoked"
	if s.db != nil {
		if _, err := s.db.Exec(
			"UPDATE auth_sessions SET status='revoked' WHERE sid=$1", sid,
		); err != nil {
			log.Printf("auth-service: failed to revoke session %s in DB: %v", sid, err)
		}
	}
	return true
}

// IsRevoked checks whether a session ID has been revoked.
func (s *Store) IsRevoked(sid string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.revokedSessions[sid]
	return ok
}

// ListRevocations returns all revoked session IDs.
func (s *Store) ListRevocations() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]string, 0, len(s.revokedSessions))
	for sid := range s.revokedSessions {
		result = append(result, sid)
	}
	return result
}

// CheckAndStoreDPoPJTI checks if a JTI has been seen; if not, stores it. Returns true if replay.
func (s *Store) CheckAndStoreDPoPJTI(jti string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.dpopJTICache[jti]; ok {
		return true // replay
	}
	s.dpopJTICache[jti] = struct{}{}
	return false
}
