package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel/attribute"

	"github.com/jpmc/ingress-poc/pkg/middleware"
	pkgotel "github.com/jpmc/ingress-poc/pkg/otel"
)

//go:embed static/*.html
var sampleAppFS embed.FS

var (
	serviceName string
	port        string
)

func init() {
	serviceName = os.Getenv("SERVICE_NAME")
	if serviceName == "" {
		serviceName = "svc-web"
	}
	port = os.Getenv("PORT")
	if port == "" {
		port = "8004"
	}
}

func main() {
	tp, tracer := pkgotel.InitOTEL(serviceName)
	defer tp.Shutdown(nil)

	r := chi.NewRouter()
	r.Use(middleware.CORS())
	r.Use(pkgotel.Middleware(serviceName))

	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "ok",
			"service": serviceName,
		})
	})

	// Sample app HTML pages (embedded from static/ dir)
	sampleApps := map[string]string{
		"/research": "static/research.html",
	}

	// Catch-all for GET, POST, PUT, DELETE, PATCH
	catchAll := func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		_, span := tracer.Start(ctx, "service.request")
		defer span.End()

		path := req.URL.Path

		// Extract auth headers and trace propagation headers
		authHeaders := map[string]string{}
		for key, vals := range req.Header {
			lk := strings.ToLower(key)
			if strings.HasPrefix(lk, "x-auth-") || lk == "x-request-id" || lk == "traceparent" || lk == "tracestate" {
				authHeaders[lk] = vals[0]
				span.SetAttributes(attribute.String(lk, vals[0]))
			}
		}

		span.SetAttributes(
			attribute.String("service.name", serviceName),
			attribute.String("request.path", path),
			attribute.String("auth.subject", req.Header.Get("X-Auth-Subject")),
		)

		// Serve HTML sample app if browser requests it
		accept := req.Header.Get("Accept")
		if req.Method == "GET" && strings.Contains(accept, "text/html") {
			if htmlFile, ok := sampleApps[path]; ok {
				data, err := sampleAppFS.ReadFile(htmlFile)
				if err == nil {
					span.SetAttributes(attribute.String("response.type", "html"))
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					w.Write(data)
					return
				}
			}
		}

		// Default: JSON API response
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"service":      serviceName,
			"path":         path,
			"method":       req.Method,
			"timestamp":    float64(time.Now().UnixMilli()) / 1000.0,
			"auth_context": authHeaders,
			"message":      fmt.Sprintf("Response from %s", serviceName),
		})
	}

	// Auth proxy — forwards /auth/* and /session/* to auth-service so the sample app
	// can make same-origin auth calls without CORS or mixed-content issues
	authServiceURL := os.Getenv("AUTH_SERVICE_URL")
	if authServiceURL == "" {
		authServiceURL = "http://auth-service:8001"
	}
	authProxy := func(w http.ResponseWriter, req *http.Request) {
		// Strip the app prefix to get the real auth path
		authPath := req.URL.Path
		// /research/_auth/auth/authorize -> /auth/authorize
		if idx := strings.Index(authPath, "/_auth/"); idx >= 0 {
			authPath = authPath[idx+len("/_auth"):]
		}
		targetURL := authServiceURL + authPath
		if req.URL.RawQuery != "" {
			targetURL += "?" + req.URL.RawQuery
		}
		body, _ := io.ReadAll(req.Body)
		proxyReq, err := http.NewRequestWithContext(req.Context(), req.Method, targetURL, strings.NewReader(string(body)))
		if err != nil {
			http.Error(w, "proxy error", 502)
			return
		}
		proxyReq.Header.Set("Content-Type", req.Header.Get("Content-Type"))
		resp, err := http.DefaultClient.Do(proxyReq)
		if err != nil {
			http.Error(w, "auth service unreachable", 502)
			return
		}
		defer resp.Body.Close()
		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}
	r.Post("/research/_auth/*", authProxy)
	r.Get("/research/_auth/*", authProxy)

	r.Get("/*", catchAll)
	r.Post("/*", catchAll)
	r.Put("/*", catchAll)
	r.Delete("/*", catchAll)
	r.Patch("/*", catchAll)

	log.Printf("%s starting on :%s", serviceName, port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
