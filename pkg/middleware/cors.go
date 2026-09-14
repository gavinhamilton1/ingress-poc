package middleware

import (
	"net/http"

	"github.com/rs/cors"
)

// CORS returns a permissive CORS middleware matching the Python allow_origins=["*"]
// config. Origins are reflected via AllowOriginFunc rather than passed as a literal
// "*" in AllowedOrigins: github.com/rs/cors sends that string verbatim as the
// Access-Control-Allow-Origin header, which browsers reject on any preflight for a
// credentialed request ("...must not be the wildcard '*' when the request's
// credentials mode is 'include'"). Reflecting the origin keeps the "allow anything"
// intent while staying spec-compliant — the same thing FastAPI's CORSMiddleware does
// automatically once allow_credentials=True.
func CORS() func(http.Handler) http.Handler {
	c := cors.New(cors.Options{
		AllowOriginFunc:  func(origin string) bool { return true },
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS", "HEAD"},
		AllowedHeaders:   []string{"*"},
		AllowCredentials: true,
		MaxAge:           300,
	})
	return c.Handler
}
