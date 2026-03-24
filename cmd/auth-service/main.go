package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"

	"github.com/jpmc/ingress-poc/pkg/middleware"
	"github.com/jpmc/ingress-poc/pkg/otel"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8001"
	}

	authServiceURL := os.Getenv("AUTH_SERVICE_URL")
	if authServiceURL == "" {
		authServiceURL = fmt.Sprintf("http://localhost:%s", port)
	}

	// Initialise OpenTelemetry
	tp, tracer := otel.InitOTEL("auth-service")
	defer tp.Shutdown(nil)

	// Generate key pairs (equivalent to Python module-level key generation)
	idpKey := generateKeyPair("idp-key-1")
	sessionKey := generateKeyPair("session-key-1")

	// In-memory store
	store := NewStore()

	// Router
	r := chi.NewRouter()
	r.Use(middleware.CORS())
	r.Use(otel.Middleware("auth-service"))

	// PKCE auth endpoints
	r.Post("/auth/authorize", handleAuthorize(store, tracer))
	r.Post("/auth/token", handleToken(store, tracer, idpKey, authServiceURL))

	// JWK endpoints
	r.Get("/.well-known/jwks.json", handleIDPJWKS(idpKey))
	r.Get("/session/jwks.json", handleSessionJWKS(sessionKey))

	// Session endpoints
	r.Post("/session/create", handleSessionCreate(store, tracer, idpKey, sessionKey, authServiceURL))
	r.Post("/session/revoke/{sid}", handleSessionRevoke(store, tracer))
	r.Get("/session/{sid}", handleGetSession(store))
	r.Get("/sessions", handleListSessions(store))
	r.Get("/revocations", handleListRevocations(store))

	// Ext-authz endpoint
	r.Post("/gateway/ext-authz", handleExtAuthz(store, tracer))

	// Demo / health
	r.Get("/demo/users", handleDemoUsers(store))
	r.Get("/health", handleHealth())

	log.Printf("auth-service starting on :%s", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
