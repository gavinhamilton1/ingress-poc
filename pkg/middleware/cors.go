package middleware

import (
	"net/http"

	"github.com/rs/cors"
)

// CORS returns a permissive CORS middleware matching the Python allow_origins=["*"] config.
func CORS() func(http.Handler) http.Handler {
	c := cors.New(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS", "HEAD"},
		AllowedHeaders:   []string{"*"},
		AllowCredentials: true,
		MaxAge:           300,
	})
	return c.Handler
}
