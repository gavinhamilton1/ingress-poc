package ingress.policy.coarse

import rego.v1

default allow = false

allow if {
    not_revoked
    has_roles
    route_permitted
}

not_revoked if { not input.session.revoked }
has_roles if { count(input.session.roles) > 0 }

route_permitted if {
    role := input.session.roles[_]
    route_acl[input.route][_] == role
}

route_permitted if { route_acl[input.route][_] == "*" }

# Relationship-based mock (production uses SpiceDB)
route_permitted if {
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

deny_reason := "session revoked" if { input.session.revoked }
deny_reason := "no valid roles" if { not has_roles }
deny_reason := "role not permitted" if { not route_permitted; has_roles; not_revoked }

obligations contains ob if { allow; ob := {"type": "audit-log", "required": true} }
obligations contains ob if { allow; input.session.entity == "MARKETS"
                  ob := {"type": "data-classification", "level": "confidential"} }
