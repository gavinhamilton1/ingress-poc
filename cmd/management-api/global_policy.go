package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/open-policy-agent/opa/rego"
)

// defaultGlobalPayloadPolicy carries just the generic injection-pattern
// checks that used to be duplicated inside every route-specific policy the
// onboarding agent generated. It has no knowledge of any particular route's
// schema — it only looks for known-bad patterns in string field values —
// which is exactly what makes it safe to run unconditionally on every route
// with a request body, not just ones a developer has explicitly onboarded.
const defaultGlobalPayloadPolicy = `package ingress.policy.payload.global

import rego.v1

# Unlike route-specific policies (default allow = false, building up to an
# explicit allow), this is a blocklist: allow unless a known-bad pattern is
# found. It has no basis to reject a request just for having an unfamiliar
# shape — only for containing something that looks like an attack.
default allow = true

injection_patterns := [
	` + "`" + `(?i)(union\s+select|drop\s+table|insert\s+into\s|delete\s+from\s|--|;\s*--|'\s*or\s*'?1'?\s*=\s*'?1)` + "`" + `,
	` + "`" + `(?i)<script|javascript:|onerror\s*=|onload\s*=` + "`" + `,
	` + "`" + `\$where|\{\{|\}\}|\$\{` + "`" + `,
]

has_injection if {
	is_object(input.body)
	some _, v in input.body
	is_string(v)
	some p in injection_patterns
	regex.match(p, v)
}

allow = false if has_injection

deny_reason contains "request body contains a potentially malicious pattern (possible injection attempt)" if { has_injection }
`

// seedGlobalPayloadPolicy inserts the default global policy on first boot.
// Runs unconditionally (unlike seedDefaults, which skips entirely once any
// route exists) — the global policy is an independent, platform-wide
// concern, not tied to whether routes have been seeded.
func seedGlobalPayloadPolicy(db *sqlx.DB) {
	var count int
	db.Get(&count, "SELECT COUNT(*) FROM global_payload_policy WHERE id='global'")
	if count > 0 {
		return
	}
	db.MustExec("INSERT INTO global_payload_policy (id, rego_source, updated_at) VALUES ('global', $1, $2)",
		defaultGlobalPayloadPolicy, float64(time.Now().Unix()))
	log.Printf("Seeded default global payload policy (package ingress.policy.payload.global)")
}

// getGlobalPolicy returns the current global payload policy — polled by
// auth-service every 10s alongside its per-route cache refresh.
func getGlobalPolicy(w http.ResponseWriter, r *http.Request) {
	var row struct {
		RegoSource string  `db:"rego_source"`
		UpdatedAt  float64 `db:"updated_at"`
	}
	if err := db.Get(&row, "SELECT rego_source, updated_at FROM global_payload_policy WHERE id='global'"); err != nil {
		writeJSON(w, 404, map[string]string{"detail": "global policy not found"})
		return
	}
	writeJSON(w, 200, map[string]interface{}{
		"rego_source": row.RegoSource,
		"updated_at":  row.UpdatedAt,
	})
}

// updateGlobalPolicy replaces the platform-wide payload policy. This is the
// single lever for responding to a newly discovered attack pattern across
// every route at once — no per-route regeneration needed. Compile-checked
// in-process via the OPA Go SDK before being stored, same as
// attach_payload_policy for route-specific policies.
func updateGlobalPolicy(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RegoSource string `json:"rego_source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"detail": "invalid JSON body"})
		return
	}
	if body.RegoSource == "" {
		writeJSON(w, 400, map[string]string{"detail": "rego_source is required"})
		return
	}
	const expectedPkg = "package ingress.policy.payload.global"
	if !strings.Contains(body.RegoSource, expectedPkg) {
		writeJSON(w, 400, map[string]string{"detail": "rego_source must declare " + expectedPkg})
		return
	}

	if _, err := rego.New(
		rego.Query("data.ingress.policy.payload.global"),
		rego.Module("global.rego", body.RegoSource),
	).PrepareForEval(r.Context()); err != nil {
		writeJSON(w, 400, map[string]interface{}{
			"detail":    "Rego failed to compile (see opa_error)",
			"opa_error": err.Error(),
		})
		return
	}

	if err := orch.WritePayloadPolicy("global", body.RegoSource); err != nil {
		log.Printf("Warning: failed to write global payload policy to GitOps repo: %v", err)
	}

	now := float64(time.Now().Unix())
	db.MustExec(`INSERT INTO global_payload_policy (id, rego_source, updated_at) VALUES ('global', $1, $2)
		ON CONFLICT (id) DO UPDATE SET rego_source=$1, updated_at=$2`, body.RegoSource, now)

	addAudit("global", "UPDATE_GLOBAL_POLICY", strOr(getActorFromRequest(r), "system"),
		"Updated the platform-wide payload validation policy")

	writeJSON(w, 200, map[string]interface{}{"rego_source": body.RegoSource, "updated_at": now})
}
