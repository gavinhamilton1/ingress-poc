// mock-multi-cdn-traffic-manager simulates a DNS-based multi-CDN traffic
// manager (the role products like NS1, Cedexis, or F5 DNS Load Balancer
// play in front of multiple CDN providers) sitting in front of both the
// Akamai and Cloudflare mock stacks. It is the actual internet-facing front
// door — it holds the jpm.com TLS cert and picks, per request, which CDN
// provider's own load balancer (mock-akamai-gtm or mock-cf-lb) to forward
// to, based on configurable weights and each provider's live health.
//
// This is what makes the setup active-active rather than active-passive:
// both providers take real traffic concurrently (split by weight), not
// "one provider until it fails over to the other."
package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"github.com/jpmc/ingress-poc/pkg/httputil"
	"github.com/jpmc/ingress-poc/pkg/middleware"
	"github.com/jpmc/ingress-poc/pkg/otel"
)

type cdnProvider struct {
	name    string
	baseURL string
	weight  int
}

var (
	port        string
	httpsPort   string
	sslCertFile string
	sslKeyFile  string

	providers []cdnProvider

	healthMu   sync.RWMutex
	providerUp = map[string]bool{}
)

func getEnvOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func intEnvOr(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func init() {
	port = getEnvOr("PORT", "8010")
	httpsPort = getEnvOr("HTTPS_PORT", "8443")
	sslCertFile = getEnvOr("SSL_CERTFILE", "/certs/jpm.com.crt")
	sslKeyFile = getEnvOr("SSL_KEYFILE", "/certs/jpm.com.key")

	providers = []cdnProvider{
		{name: "akamai", baseURL: getEnvOr("AKAMAI_GTM_URL", "http://mock-akamai-gtm:8010"), weight: intEnvOr("AKAMAI_WEIGHT", 50)},
		{name: "cloudflare", baseURL: getEnvOr("CF_LB_URL", "http://mock-cf-lb:8030"), weight: intEnvOr("CLOUDFLARE_WEIGHT", 50)},
	}
	for _, p := range providers {
		providerUp[p.name] = true // assume healthy until the first check completes
	}
}

// startHealthChecks periodically probes each provider's /health endpoint.
// Active-active still needs this: splitting traffic by weight across a
// provider that's actually down just turns "active-active" into "50% error
// rate," which defeats the point.
func startHealthChecks(interval time.Duration) {
	client := &http.Client{Timeout: 3 * time.Second}
	check := func() {
		for _, p := range providers {
			resp, err := client.Get(p.baseURL + "/health")
			up := err == nil && resp.StatusCode == http.StatusOK
			if resp != nil {
				resp.Body.Close()
			}
			healthMu.Lock()
			if providerUp[p.name] != up {
				log.Printf("multi-cdn-traffic-manager: provider %q health changed -> %v", p.name, up)
			}
			providerUp[p.name] = up
			healthMu.Unlock()
		}
	}
	check() // synchronous first check so routing decisions from the start reflect reality
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			check()
		}
	}()
}

// pickProvider does a weighted random selection among currently-healthy
// providers. If every provider looks down, it still picks one (weighted)
// rather than refusing outright — the caller gets a real upstream error in
// that case, which is more informative than a traffic-manager-side 503.
func pickProvider() cdnProvider {
	healthMu.RLock()
	defer healthMu.RUnlock()

	candidates := make([]cdnProvider, 0, len(providers))
	totalWeight := 0
	for _, p := range providers {
		if providerUp[p.name] {
			candidates = append(candidates, p)
			totalWeight += p.weight
		}
	}
	if len(candidates) == 0 {
		candidates = providers
		totalWeight = 0
		for _, p := range candidates {
			totalWeight += p.weight
		}
	}
	if totalWeight <= 0 {
		return candidates[rand.Intn(len(candidates))]
	}
	r := rand.Intn(totalWeight)
	for _, p := range candidates {
		if r < p.weight {
			return p
		}
		r -= p.weight
	}
	return candidates[len(candidates)-1]
}

func main() {
	tp, tracer := otel.InitOTEL("multi-cdn.traffic-manager")
	defer tp.Shutdown(nil)

	startHealthChecks(5 * time.Second)

	r := chi.NewRouter()
	r.Use(middleware.CORS())
	r.Use(otel.Middleware("multi-cdn.traffic-manager"))

	r.Get("/health", func(w http.ResponseWriter, req *http.Request) {
		healthMu.RLock()
		snapshot := map[string]bool{}
		for k, v := range providerUp {
			snapshot[k] = v
		}
		healthMu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":    "ok",
			"service":   "mock-multi-cdn-traffic-manager",
			"providers": snapshot,
		})
	})

	// /_set_cookie: this is now the real internet-facing front door (it holds
	// the TLS cert), so it — not mock-akamai-gtm — is what the console
	// actually reaches at https://jpmm.jpm.com/_set_cookie. Same behavior as
	// mock-akamai-gtm's copy: plants the session cookie under the jpm.com
	// domain after login so gateway-served page refreshes don't need
	// re-authentication.
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
			SameSite: http.SameSiteNoneMode,
			Secure:   true,
		})

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	r.HandleFunc("/*", func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		ctx, span := tracer.Start(ctx, "multi_cdn.route")
		defer span.End()

		requestID := uuid.New().String()
		chosen := pickProvider()

		span.SetAttributes(
			attribute.String("multi_cdn.provider", chosen.name),
			attribute.String("multi_cdn.request_id", requestID),
		)

		path := strings.TrimPrefix(req.URL.Path, "/")
		fullURL := fmt.Sprintf("%s/%s", chosen.baseURL, path)

		extra := map[string]string{
			"X-Multi-Cdn-Request-Id": requestID,
		}
		if req.Host != "" {
			extra["X-Forwarded-Host"] = req.Host
		}

		resp, err := httputil.ForwardRequest(ctx, httputil.DefaultClient, req, fullURL, extra)
		if err != nil {
			span.SetAttributes(attribute.Int("http.status_code", 502), attribute.String("error", err.Error()))
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Multi-Cdn-Provider-Selected", chosen.name)
			w.WriteHeader(502)
			fmt.Fprintf(w, `{"error": "%s unreachable: %s"}`, chosen.name, err.Error())
			return
		}
		defer resp.Body.Close()

		span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))

		httputil.CopyResponseHeaders(w, resp)
		w.Header().Set("X-Multi-Cdn-Provider-Selected", chosen.name)
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	})

	// HTTPS server — this component is now the one holding the jpm.com cert,
	// since it replaced mock-akamai-gtm as the actual internet-facing front
	// door. Same load/skip-if-missing behavior as before.
	go func() {
		if _, err := os.Stat(sslCertFile); os.IsNotExist(err) {
			log.Printf("multi-cdn-traffic-manager: SSL cert not found at %s, skipping HTTPS", sslCertFile)
			return
		}
		if _, err := os.Stat(sslKeyFile); os.IsNotExist(err) {
			log.Printf("multi-cdn-traffic-manager: SSL key not found at %s, skipping HTTPS", sslKeyFile)
			return
		}
		cert, err := tls.LoadX509KeyPair(sslCertFile, sslKeyFile)
		if err != nil {
			log.Printf("multi-cdn-traffic-manager: failed to load TLS keypair: %v", err)
			return
		}
		httpsServer := &http.Server{
			Addr:      ":" + httpsPort,
			Handler:   r,
			TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}},
		}
		log.Printf("multi-cdn-traffic-manager HTTPS starting on :%s", httpsPort)
		if err := httpsServer.ListenAndServeTLS("", ""); err != nil {
			log.Printf("multi-cdn-traffic-manager HTTPS error: %v", err)
		}
	}()

	log.Printf("multi-cdn-traffic-manager HTTP starting on :%s", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
