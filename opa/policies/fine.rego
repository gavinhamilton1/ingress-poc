package ingress.policy.fine

import rego.v1

default allow = false

allow if {
    has_valid_session
    action_permitted
}

has_valid_session if { input.session.sub != "" }

action_permitted if {
    input.action == "read"
}

action_permitted if {
    input.action == "write"
    input.session.roles[_] == "trader"
}

action_permitted if {
    input.action == "write"
    input.session.roles[_] == "architect"
}

action_permitted if {
    input.action == "write"
    input.session.roles[_] == "platform-admin"
}

action_permitted if {
    input.action == "admin"
    input.session.roles[_] == "platform-admin"
}

action_permitted if {
    input.action == "admin"
    input.session.roles[_] == "architect"
}

deny_reason := "no valid session" if { not has_valid_session }
deny_reason := "action not permitted" if { has_valid_session; not action_permitted }
