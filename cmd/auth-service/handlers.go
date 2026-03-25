package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeError writes a JSON error response matching FastAPI's HTTPException shape.
func writeError(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"detail": detail,
	})
}

// handleOIDCDiscovery returns the OpenID Connect discovery document.
func handleOIDCDiscovery(issuer string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]interface{}{
			"issuer":                                issuer,
			"authorization_endpoint":                issuer + "/auth/authorize",
			"token_endpoint":                        issuer + "/auth/token",
			"jwks_uri":                              issuer + "/.well-known/jwks.json",
			"userinfo_endpoint":                     issuer + "/session/{sid}",
			"revocation_endpoint":                   issuer + "/session/revoke/{sid}",
			"response_types_supported":              []string{"code"},
			"grant_types_supported":                 []string{"authorization_code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"ES256"},
			"code_challenge_methods_supported":      []string{"S256"},
			"dpop_signing_alg_values_supported":     []string{"ES256"},
			"token_endpoint_auth_methods_supported": []string{"none"},
		})
	}
}

// --- Request models ---

type authorizeRequest struct {
	Email               string `json:"email"`
	Password            string `json:"password"`
	ClientID            string `json:"client_id"`
	RedirectURI         string `json:"redirect_uri"`
	CodeChallenge       string `json:"code_challenge"`
	CodeChallengeMethod string `json:"code_challenge_method"`
}

type tokenRequest struct {
	GrantType   string `json:"grant_type"`
	Code        string `json:"code"`
	RedirectURI string `json:"redirect_uri"`
	ClientID    string `json:"client_id"`
	CodeVerifier string `json:"code_verifier"`
}

type sessionCreateRequest struct {
	AccessToken string                 `json:"access_token"`
	DPoPJWK     map[string]interface{} `json:"dpop_jwk,omitempty"`
}

type extAuthzRequest struct {
	Headers map[string]string `json:"headers"`
	Method  string            `json:"method"`
	Path    string            `json:"path"`
}

// --- Handlers ---

func handleAuthorize(store *Store, tracer trace.Tracer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, span := tracer.Start(r.Context(), "auth.pkce.authorize")
		defer span.End()
		_ = ctx

		var req authorizeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, 400, "Invalid request body")
			return
		}

		// Apply defaults matching Python Pydantic model
		if req.ClientID == "" {
			req.ClientID = "ingress-console"
		}
		if req.RedirectURI == "" {
			req.RedirectURI = "http://localhost:3000/callback"
		}
		if req.CodeChallengeMethod == "" {
			req.CodeChallengeMethod = "S256"
		}

		user, ok := store.GetDemoUser(req.Email)
		if !ok || user.Password != req.Password {
			span.SetAttributes(attribute.String("auth.result", "REJECT"))
			writeError(w, 401, "Invalid credentials")
			return
		}

		code := generateSecureToken(32)
		store.StoreAuthCode(code, &AuthCode{
			Sub:                 req.Email,
			CodeChallenge:       req.CodeChallenge,
			CodeChallengeMethod: req.CodeChallengeMethod,
			ClientID:            req.ClientID,
			RedirectURI:         req.RedirectURI,
		})

		span.SetAttributes(
			attribute.String("user.sub", req.Email),
			attribute.Bool("pkce.valid", true),
		)

		writeJSON(w, 200, map[string]interface{}{
			"code":  code,
			"state": "ok",
		})
	}
}

func handleToken(store *Store, tracer trace.Tracer, idpKey *KeyPair, authServiceURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, span := tracer.Start(r.Context(), "auth.pkce.token")
		defer span.End()
		_ = ctx

		var req tokenRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, 400, "Invalid request body")
			return
		}

		// Apply defaults
		if req.GrantType == "" {
			req.GrantType = "authorization_code"
		}
		if req.RedirectURI == "" {
			req.RedirectURI = "http://localhost:3000/callback"
		}
		if req.ClientID == "" {
			req.ClientID = "ingress-console"
		}

		stored, ok := store.PopAuthCode(req.Code)
		if !ok {
			span.SetAttributes(attribute.Bool("pkce.verified", false))
			writeError(w, 400, "Invalid or expired code")
			return
		}

		// Verify PKCE
		hash := sha256.Sum256([]byte(req.CodeVerifier))
		verifierHash := base64.RawURLEncoding.EncodeToString(hash[:])
		if verifierHash != stored.CodeChallenge {
			span.SetAttributes(attribute.Bool("pkce.verified", false))
			writeError(w, 400, "PKCE verification failed")
			return
		}

		user, _ := store.GetDemoUser(stored.Sub)
		now := nowUnix()

		accessPayload := map[string]interface{}{
			"iss":       authServiceURL,
			"sub":       stored.Sub,
			"aud":       "ingress-gateway",
			"iat":       now,
			"exp":       now + 3600,
			"email":     stored.Sub,
			"name":      user.Name,
			"roles":     user.Roles,
			"entity":    user.Entity,
			"client_id": stored.ClientID,
		}

		accessToken, err := signJWT(accessPayload, idpKey.PrivateKey, idpKey.Kid)
		if err != nil {
			writeError(w, 500, "Failed to sign access token")
			return
		}

		idPayload := copyMap(accessPayload)
		idPayload["aud"] = stored.ClientID
		idToken, err := signJWT(idPayload, idpKey.PrivateKey, idpKey.Kid)
		if err != nil {
			writeError(w, 500, "Failed to sign ID token")
			return
		}

		span.SetAttributes(
			attribute.Bool("pkce.verified", true),
			attribute.Int64("token.expiry", now+3600),
		)

		writeJSON(w, 200, map[string]interface{}{
			"access_token": accessToken,
			"id_token":     idToken,
			"token_type":   "DPoP",
			"expires_in":   3600,
		})
	}
}

func handleIDPJWKS(idpKey *KeyPair) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]interface{}{
			"keys": []map[string]string{idpKey.JWK},
		})
	}
}

func handleSessionJWKS(sessionKey *KeyPair) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]interface{}{
			"keys": []map[string]string{sessionKey.JWK},
		})
	}
}

func handleSessionCreate(store *Store, tracer trace.Tracer, idpKey *KeyPair, sessionKey *KeyPair, authServiceURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, span := tracer.Start(r.Context(), "session.create")
		defer span.End()
		_ = ctx

		var req sessionCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, 400, "Invalid request body")
			return
		}

		// Try to decode the access token - first verified, then unverified for flexibility
		claims, err := parseJWTUnverified(req.AccessToken)
		if err != nil {
			writeError(w, 400, "Invalid access token")
			return
		}

		sid := uuid.New().String()

		var jkt *string
		if req.DPoPJWK != nil {
			t := computeJKT(req.DPoPJWK)
			jkt = &t
		}

		now := nowUnix()

		sessionPayload := map[string]interface{}{
			"iss":       authServiceURL,
			"sub":       claimString(claims, "sub"),
			"sid":       sid,
			"aud":       "ingress-gateway",
			"iat":       now,
			"exp":       now + 3600,
			"email":     claimString(claims, "email"),
			"name":      claimString(claims, "name"),
			"roles":     claimSlice(claims, "roles"),
			"entity":    claimString(claims, "entity"),
			"client_id": claimStringDefault(claims, "client_id", "ingress-console"),
		}
		if jkt != nil {
			sessionPayload["cnf"] = map[string]string{"jkt": *jkt}
		}

		sessionJWT, err := signJWT(sessionPayload, sessionKey.PrivateKey, sessionKey.Kid)
		if err != nil {
			writeError(w, 500, "Failed to sign session JWT")
			return
		}

		store.StoreSession(sid, &Session{
			SID:       sid,
			Sub:       claimString(claims, "sub"),
			Email:     claimString(claims, "email"),
			Name:      claimString(claims, "name"),
			Roles:     claimSlice(claims, "roles"),
			Entity:    claimString(claims, "entity"),
			CreatedAt: now,
			ExpiresAt: now + 3600,
			DPoPJKT:   jkt,
			Status:    "active",
		})

		jktStr := ""
		if jkt != nil {
			jktStr = *jkt
		}
		truncatedJKT := jktStr
		if len(truncatedJKT) > 12 {
			truncatedJKT = truncatedJKT[:12]
		}

		span.SetAttributes(
			attribute.String("session.id", sid),
			attribute.String("dpop.jkt", truncatedJKT),
			attribute.String("session.subject", claimString(claims, "sub")),
		)

		writeJSON(w, 200, map[string]interface{}{
			"session_jwt": sessionJWT,
			"sid":         sid,
			"expires_in":  3600,
		})
	}
}

func handleSessionRevoke(store *Store, tracer trace.Tracer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, span := tracer.Start(r.Context(), "session.revoke")
		defer span.End()
		_ = ctx

		sid := chi.URLParam(r, "sid")

		if !store.RevokeSession(sid) {
			writeError(w, 404, "Session not found")
			return
		}

		span.SetAttributes(
			attribute.String("session.id", sid),
			attribute.Int64("revocation.ts", time.Now().Unix()),
		)

		writeJSON(w, 200, map[string]interface{}{
			"status": "revoked",
			"sid":    sid,
		})
	}
}

func handleGetSession(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sid := chi.URLParam(r, "sid")
		sess, ok := store.GetSession(sid)
		if !ok {
			writeError(w, 404, "Session not found")
			return
		}
		writeJSON(w, 200, sess)
	}
}

func handleListSessions(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, store.ListSessions())
	}
}

func handleListRevocations(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, store.ListRevocations())
	}
}

func handleExtAuthz(store *Store, tracer trace.Tracer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, span := tracer.Start(r.Context(), "ext_authz.validate")
		defer span.End()
		_ = ctx

		var req extAuthzRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, 400, "Invalid request body")
			return
		}

		if req.Method == "" {
			req.Method = "GET"
		}
		if req.Path == "" {
			req.Path = "/"
		}

		// Normalize header keys to lowercase for lookup
		headers := make(map[string]string)
		for k, v := range req.Headers {
			headers[strings.ToLower(k)] = v
		}

		authHeader := headers["authorization"]
		dpopHeader := headers["dpop"]

		if !strings.HasPrefix(authHeader, "Bearer ") {
			span.SetAttributes(
				attribute.String("auth.result", "REJECT"),
				attribute.String("auth.reject_reason", "missing_bearer_token"),
			)
			writeJSON(w, 200, map[string]interface{}{
				"allowed":     false,
				"reason":      "Missing Bearer token",
				"status_code": 401,
			})
			return
		}

		tokenStr := authHeader[7:]

		// Decode session JWT without verification
		claims, err := parseJWTUnverified(tokenStr)
		if err != nil {
			span.SetAttributes(
				attribute.String("auth.result", "REJECT"),
				attribute.String("auth.reject_reason", "invalid_jwt"),
			)
			writeJSON(w, 200, map[string]interface{}{
				"allowed":     false,
				"reason":      "Invalid JWT",
				"status_code": 401,
			})
			return
		}

		sid := claimString(claims, "sid")
		sub := claimString(claims, "sub")
		roles := claimSlice(claims, "roles")
		entity := claimString(claims, "entity")

		rolesJSON, _ := json.Marshal(roles)

		span.SetAttributes(
			attribute.String("session.id", sid),
			attribute.String("session.subject", sub),
			attribute.String("session.roles", string(rolesJSON)),
			attribute.String("session.entity", entity),
		)

		// DPoP verification
		dpopValid := true
		dpopError := ""
		dpopJKT := ""

		func() {
			_, dpopSpan := tracer.Start(r.Context(), "dpop.verify")
			defer dpopSpan.End()

			if dpopHeader != "" {
				dpopClaims, err := parseJWTUnverified(dpopHeader)
				if err != nil {
					dpopValid = false
					dpopError = err.Error()
					dpopSpan.SetAttributes(attribute.Bool("dpop.valid", false))
					return
				}

				dpopHeaders, err := parseJWTHeaderUnverified(dpopHeader)
				if err != nil {
					dpopValid = false
					dpopError = err.Error()
					dpopSpan.SetAttributes(attribute.Bool("dpop.valid", false))
					return
				}

				if jwkRaw, ok := dpopHeaders["jwk"]; ok {
					if jwkMap, ok := jwkRaw.(map[string]interface{}); ok {
						dpopJKT = computeJKT(jwkMap)
					}
				}

				// Verify htm
				htm := claimString(dpopClaims, "htm")
				if !strings.EqualFold(htm, req.Method) {
					dpopValid = false
					dpopError = "htm mismatch"
				}

				// Check jti uniqueness
				jti := claimString(dpopClaims, "jti")
				if store.CheckAndStoreDPoPJTI(jti) {
					dpopValid = false
					dpopError = "jti replay"
				}

				// Check iat freshness (5 minute window)
				iat := claimFloat(dpopClaims, "iat")
				if math.Abs(float64(time.Now().Unix())-iat) > 300 {
					dpopValid = false
					dpopError = "iat too old"
				}

				// Check cnf binding
				if cnfRaw, ok := claims["cnf"]; ok {
					if cnfMap, ok := cnfRaw.(map[string]interface{}); ok {
						if boundJKT, ok := cnfMap["jkt"].(string); ok && boundJKT != "" {
							if boundJKT != dpopJKT {
								dpopValid = false
								dpopError = "jkt mismatch"
							}
						}
					}
				}

				dpopSpan.SetAttributes(
					attribute.String("dpop.htm", htm),
					attribute.String("dpop.htu", claimString(dpopClaims, "htu")),
					attribute.String("dpop.jti", jti),
				)
			}

			truncJKT := dpopJKT
			if len(truncJKT) > 12 {
				truncJKT = truncJKT[:12]
			}
			dpopSpan.SetAttributes(
				attribute.Bool("dpop.valid", dpopValid),
				attribute.String("dpop.jkt", truncJKT),
			)
			if dpopError != "" {
				dpopSpan.SetAttributes(attribute.String("dpop.error", dpopError))
			}
		}()

		if !dpopValid {
			span.SetAttributes(
				attribute.String("auth.result", "REJECT"),
				attribute.String("auth.reject_reason", fmt.Sprintf("dpop_failed: %s", dpopError)),
			)
			writeJSON(w, 200, map[string]interface{}{
				"allowed":     false,
				"reason":      fmt.Sprintf("DPoP verification failed: %s", dpopError),
				"status_code": 401,
			})
			return
		}

		// Revocation check
		func() {
			_, revokeSpan := tracer.Start(r.Context(), "revoke_cache.check")
			defer revokeSpan.End()

			isRevoked := store.IsRevoked(sid)
			revokeSpan.SetAttributes(
				attribute.Bool("revoke_cache.hit", isRevoked),
				attribute.String("session.id", sid),
			)

			if isRevoked {
				span.SetAttributes(
					attribute.String("auth.result", "REJECT"),
					attribute.String("auth.reject_reason", "session_revoked"),
				)
				writeJSON(w, 200, map[string]interface{}{
					"allowed":     false,
					"reason":      "Session has been revoked",
					"status_code": 401,
				})
				return
			}
		}()

		// If revoked, the response was already written inside the closure.
		// We need to check again to avoid writing a second response.
		if store.IsRevoked(sid) {
			return
		}

		truncJKT := dpopJKT
		if len(truncJKT) > 12 {
			truncJKT = truncJKT[:12]
		}

		span.SetAttributes(
			attribute.String("auth.result", "PASS"),
			attribute.Bool("dpop.valid", dpopValid),
			attribute.String("dpop.jkt", truncJKT),
		)

		writeJSON(w, 200, map[string]interface{}{
			"allowed": true,
			"headers": map[string]string{
				"x-auth-subject":    sub,
				"x-auth-session-id": sid,
				"x-auth-roles":      string(rolesJSON),
				"x-auth-entity":     entity,
				"x-auth-client-id":  claimString(claims, "client_id"),
				"x-auth-dpop-jkt":   truncJKT,
				"x-auth-email":      claimString(claims, "email"),
				"x-auth-name":       claimString(claims, "name"),
			},
			"claims": claims,
		})
	}
}

func handleDemoUsers(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, store.ListDemoUsers())
	}
}

func handleHealth() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]interface{}{
			"status":  "ok",
			"service": "auth-service",
		})
	}
}

// --- Helpers ---

func claimString(claims map[string]interface{}, key string) string {
	if v, ok := claims[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func claimStringDefault(claims map[string]interface{}, key, def string) string {
	s := claimString(claims, key)
	if s == "" {
		return def
	}
	return s
}

func claimFloat(claims map[string]interface{}, key string) float64 {
	if v, ok := claims[key]; ok {
		switch n := v.(type) {
		case float64:
			return n
		case json.Number:
			f, _ := n.Float64()
			return f
		}
	}
	return 0
}

func claimSlice(claims map[string]interface{}, key string) []string {
	if v, ok := claims[key]; ok {
		switch arr := v.(type) {
		case []interface{}:
			result := make([]string, 0, len(arr))
			for _, item := range arr {
				if s, ok := item.(string); ok {
					result = append(result, s)
				}
			}
			return result
		case []string:
			return arr
		}
	}
	return []string{}
}

func copyMap(m map[string]interface{}) map[string]interface{} {
	cp := make(map[string]interface{}, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

// generateSecureToken creates a URL-safe random token of n bytes.
func generateSecureToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
