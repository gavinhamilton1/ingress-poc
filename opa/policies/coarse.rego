package ingress.policy.coarse

default allow = false

allow {
    not_revoked
    has_roles
    route_permitted
}

not_revoked { not input.session.revoked }
has_roles { count(input.session.roles) > 0 }

route_permitted {
    role := input.session.roles[_]
    route_acl[input.route][_] == role
}

route_permitted { route_acl[input.route][_] == "*" }

# Relationship-based mock (production uses SpiceDB)
route_permitted {
    input.route == "/api/markets"
    input.session.entity == "MARKETS"
    input.session.roles[_] == "trader"
}

route_acl := {
    "/api/public":   ["*"],
    "/api/readonly": ["readonly", "trader", "architect", "platform-admin"],
    "/api/markets":  ["trader", "architect", "platform-admin"],
    "/api/admin":    ["architect", "platform-admin"],
    "/web/portal":   ["*"],
    "/web/admin":    ["architect", "platform-admin"],
}

deny_reason = "session revoked"          { input.session.revoked }
deny_reason = "no valid roles"           { not has_roles }
deny_reason = "role not permitted"       { not route_permitted; has_roles; not_revoked }

obligations[ob] { allow; ob := {"type": "audit-log", "required": true} }
obligations[ob] { allow; input.session.entity == "MARKETS"
                  ob := {"type": "data-classification", "level": "confidential"} }
