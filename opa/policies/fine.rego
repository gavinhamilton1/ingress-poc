package ingress.policy.fine

default allow = false

allow {
    has_valid_session
    action_permitted
}

has_valid_session { input.session.sub != "" }

action_permitted {
    input.action == "read"
}

action_permitted {
    input.action == "write"
    input.session.roles[_] == "trader"
}

action_permitted {
    input.action == "write"
    input.session.roles[_] == "architect"
}

action_permitted {
    input.action == "write"
    input.session.roles[_] == "platform-admin"
}

action_permitted {
    input.action == "admin"
    input.session.roles[_] == "platform-admin"
}

action_permitted {
    input.action == "admin"
    input.session.roles[_] == "architect"
}

deny_reason = "no valid session" { not has_valid_session }
deny_reason = "action not permitted" { has_valid_session; not action_permitted }
