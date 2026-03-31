package main

import (
	"sync"
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

// Store holds all in-memory data with proper synchronization.
type Store struct {
	mu sync.RWMutex

	demoUsers      map[string]*DemoUser
	authCodes      map[string]*AuthCode
	sessions       map[string]*Session
	revokedSessions map[string]struct{}
	dpopJTICache   map[string]struct{}
}

// NewStore creates a Store pre-populated with demo users.
func NewStore() *Store {
	return &Store{
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
	for existingSID, existing := range s.sessions {
		if existing.Sub == sess.Sub && existing.Status == "active" {
			existing.Status = "revoked"
			s.revokedSessions[existingSID] = struct{}{}
		}
	}
	s.sessions[sid] = sess
}

// GetSession retrieves a session by SID.
func (s *Store) GetSession(sid string) (*Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[sid]
	return sess, ok
}

// ListSessions returns all sessions.
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
