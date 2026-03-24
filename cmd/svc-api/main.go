package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/jpmc/ingress-poc/pkg/middleware"
	pkgotel "github.com/jpmc/ingress-poc/pkg/otel"
)

var (
	serviceName string
	port        string
	opaURL      string
	tracer      trace.Tracer
)

func init() {
	serviceName = os.Getenv("SERVICE_NAME")
	if serviceName == "" {
		serviceName = "svc-api"
	}
	port = os.Getenv("PORT")
	if port == "" {
		port = "8005"
	}
	opaURL = os.Getenv("OPA_URL")
	if opaURL == "" {
		opaURL = "http://opa:8181"
	}
}

// opaResult holds the fine-grained OPA check response.
type opaResult struct {
	Allow      bool   `json:"allow"`
	DenyReason string `json:"deny_reason"`
}

// checkFineOPA performs the L5 fine-grained OPA check.
func checkFineOPA(ctx context.Context, sessionInfo map[string]interface{}, action, path string) opaResult {
	_, span := tracer.Start(ctx, "opa.fine")
	defer span.End()

	opaInput := map[string]interface{}{
		"input": map[string]interface{}{
			"session": sessionInfo,
			"action":  action,
			"path":    path,
		},
	}

	body, err := json.Marshal(opaInput)
	if err != nil {
		span.SetAttributes(
			attribute.Bool("opa.allow", true),
			attribute.String("opa.error", err.Error()),
		)
		return opaResult{Allow: true}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		opaURL+"/v1/data/ingress/policy/fine", bytes.NewReader(body))
	if err != nil {
		span.SetAttributes(
			attribute.Bool("opa.allow", true),
			attribute.String("opa.error", err.Error()),
		)
		return opaResult{Allow: true}
	}

	req.Header.Set("Content-Type", "application/json")
	// Inject trace headers
	for k, vals := range pkgotel.InjectTraceHeaders(ctx) {
		for _, v := range vals {
			req.Header.Set(k, v)
		}
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		span.SetAttributes(
			attribute.Bool("opa.allow", true),
			attribute.String("opa.error", err.Error()),
		)
		return opaResult{Allow: true}
	}
	defer resp.Body.Close()

	var opaResp struct {
		Result struct {
			Allow      bool   `json:"allow"`
			DenyReason string `json:"deny_reason"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&opaResp); err != nil {
		span.SetAttributes(
			attribute.Bool("opa.allow", true),
			attribute.String("opa.error", err.Error()),
		)
		return opaResult{Allow: true}
	}

	allowed := opaResp.Result.Allow
	denyReason := opaResp.Result.DenyReason

	span.SetAttributes(attribute.Bool("opa.allow", allowed))
	if denyReason != "" {
		span.SetAttributes(attribute.String("opa.deny_reason", denyReason))
	}

	return opaResult{Allow: allowed, DenyReason: denyReason}
}

func main() {
	tp, t := pkgotel.InitOTEL(serviceName)
	tracer = t
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

	catchAll := func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		ctx, span := tracer.Start(ctx, "service.request")
		defer span.End()

		path := req.URL.Path

		// Extract auth headers
		authHeaders := map[string]string{}
		for key, vals := range req.Header {
			lk := strings.ToLower(key)
			if strings.HasPrefix(lk, "x-auth-") || lk == "x-request-id" {
				authHeaders[lk] = vals[0]
				span.SetAttributes(attribute.String(lk, vals[0]))
			}
		}

		span.SetAttributes(
			attribute.String("service.name", serviceName),
			attribute.String("request.path", path),
			attribute.String("auth.subject", req.Header.Get("X-Auth-Subject")),
		)

		// Build session info for fine-grained OPA
		roles := []interface{}{}
		if raw, ok := authHeaders["x-auth-roles"]; ok {
			_ = json.Unmarshal([]byte(raw), &roles)
		}

		sessionInfo := map[string]interface{}{
			"sub":    authHeaders["x-auth-subject"],
			"roles":  roles,
			"entity": authHeaders["x-auth-entity"],
		}

		action := "read"
		if req.Method != http.MethodGet {
			action = "write"
		}
		if strings.Contains(path, "admin") {
			action = "admin"
		}

		result := checkFineOPA(ctx, sessionInfo, action, path)

		w.Header().Set("Content-Type", "application/json")

		if !result.Allow {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":       "Forbidden",
				"reason":      result.DenyReason,
				"status_code": 403,
				"service":     serviceName,
			})
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"service":      serviceName,
			"path":         path,
			"method":       req.Method,
			"timestamp":    float64(time.Now().UnixMilli()) / 1000.0,
			"auth_context": authHeaders,
			"opa_fine":     result,
			"message":      fmt.Sprintf("Response from %s", serviceName),
		})
	}

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
