package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync/atomic"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"github.com/jpmc/ingress-poc/pkg/httputil"
	"github.com/jpmc/ingress-poc/pkg/middleware"
	"github.com/jpmc/ingress-poc/pkg/otel"
)

var (
	port        string
	httpsPort   string
	forwardTo   string
	sslCertFile string
	sslKeyFile  string

	datacenters = []string{"us-east", "us-west", "eu-west", "ap-southeast"}
	dcIdx       uint64
)

func init() {
	port = os.Getenv("PORT")
	if port == "" {
		port = "8010"
	}
	httpsPort = os.Getenv("HTTPS_PORT")
	if httpsPort == "" {
		httpsPort = "8443"
	}
	forwardTo = os.Getenv("FORWARD_TO")
	if forwardTo == "" {
		forwardTo = "http://mock-akamai-edge:8011"
	}
	sslCertFile = os.Getenv("SSL_CERTFILE")
	if sslCertFile == "" {
		sslCertFile = "/certs/jpm.com.crt"
	}
	sslKeyFile = os.Getenv("SSL_KEYFILE")
	if sslKeyFile == "" {
		sslKeyFile = "/certs/jpm.com.key"
	}
}

func main() {
	tp, tracer := otel.InitOTEL("akamai.gtm")
	defer tp.Shutdown(nil)

	r := chi.NewRouter()
	r.Use(middleware.CORS())
	r.Use(otel.Middleware("akamai.gtm"))

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "ok",
			"service": "mock-akamai-gtm",
		})
	})

	// /_set_cookie: called by the console (localhost:3000) after login to plant
	// the session cookie under the jpmm.jpm.com domain so that browser page
	// refreshes on gateway-served routes don't require re-authentication.
	setCookieCORS := func(w http.ResponseWriter, req *http.Request) {
		origin := req.Header.Get("Origin")
		if origin == "" {
			origin = "http://localhost:3000"
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Vary", "Origin")
	}

	r.Options("/_set_cookie", func(w http.ResponseWriter, req *http.Request) {
		setCookieCORS(w, req)
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Max-Age", "300")
		w.WriteHeader(http.StatusNoContent)
	})

	r.Post("/_set_cookie", func(w http.ResponseWriter, req *http.Request) {
		setCookieCORS(w, req)

		var body struct {
			SessionJWT string `json:"session_jwt"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.SessionJWT == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "missing session_jwt"})
			return
		}

		http.SetCookie(w, &http.Cookie{
			Name:     "ingress_session",
			Value:    body.SessionJWT,
			Path:     "/",
			MaxAge:   86400,
			SameSite: http.SameSiteNoneMode, // cross-site fetch from localhost:3000
			Secure:   true,                  // required for SameSite=None; jpmm.jpm.com is HTTPS
		})

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	r.HandleFunc("/*", func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		ctx, span := tracer.Start(ctx, "akamai.gtm.forward")
		defer span.End()

		requestID := req.Header.Get("X-Akamai-Request-Id")
		if requestID == "" {
			requestID = uuid.New().String()
		}

		// Cycle through datacenters
		idx := atomic.AddUint64(&dcIdx, 1) - 1
		datacenter := datacenters[idx%uint64(len(datacenters))]

		span.SetAttributes(
			attribute.String("akamai.service", "gtm"),
			attribute.String("akamai.request_id", requestID),
			attribute.String("akamai.datacenter", datacenter),
			attribute.String("akamai.forward_to", "mock-akamai-edge"),
			attribute.String("auth.subject", req.Header.Get("X-Auth-Subject")),
		)

		path := strings.TrimPrefix(req.URL.Path, "/")

		// Build extra headers
		extra := map[string]string{
			"X-Akamai-Request-Id":      requestID,
			"X-Akamai-Gtm-Datacenter":  datacenter,
			"X-Akamai-Gtm-Reason":      "load-balance",
		}

		// Preserve original Host for subdomain-based routing downstream
		originalHost := req.Host
		if originalHost != "" {
			extra["X-Forwarded-Host"] = originalHost
		}

		fullURL := fmt.Sprintf("%s/%s", forwardTo, path)

		resp, err := httputil.ForwardRequest(ctx, httputil.DefaultClient, req, fullURL, extra)
		if err != nil {
			span.SetAttributes(attribute.Int("http.status_code", 502), attribute.String("error", err.Error()))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(502)
			fmt.Fprintf(w, `{"error": "Edge unreachable: %s"}`, err.Error())
			return
		}
		defer resp.Body.Close()

		span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))

		httputil.CopyResponseHeaders(w, resp)
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	})

	// Start HTTPS server in a goroutine if certs exist
	go func() {
		if _, err := os.Stat(sslCertFile); os.IsNotExist(err) {
			log.Printf("mock-akamai-gtm: SSL cert not found at %s, skipping HTTPS", sslCertFile)
			return
		}
		if _, err := os.Stat(sslKeyFile); os.IsNotExist(err) {
			log.Printf("mock-akamai-gtm: SSL key not found at %s, skipping HTTPS", sslKeyFile)
			return
		}

		cert, err := tls.LoadX509KeyPair(sslCertFile, sslKeyFile)
		if err != nil {
			log.Printf("mock-akamai-gtm: failed to load TLS keypair: %v", err)
			return
		}

		tlsConfig := &tls.Config{
			Certificates: []tls.Certificate{cert},
		}

		httpsServer := &http.Server{
			Addr:      ":" + httpsPort,
			Handler:   r,
			TLSConfig: tlsConfig,
		}

		log.Printf("mock-akamai-gtm HTTPS starting on :%s", httpsPort)
		if err := httpsServer.ListenAndServeTLS("", ""); err != nil {
			log.Printf("mock-akamai-gtm HTTPS error: %v", err)
		}
	}()

	// HTTP server on main goroutine
	log.Printf("mock-akamai-gtm HTTP starting on :%s", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
