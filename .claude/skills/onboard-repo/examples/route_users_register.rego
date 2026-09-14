package ingress.policy.payload.route_users_register

import rego.v1

default allow = false

required_fields := {"full_name", "email", "phone", "date_of_birth"}

body_ok if is_object(input.body)

has_field(f) if {
	body_ok
	object.get(input.body, f, null) != null
}

email_valid if {
	has_field("email")
	is_string(input.body.email)
	regex.match(`^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$`, input.body.email)
}

phone_valid if {
	has_field("phone")
	is_string(input.body.phone)
	regex.match(`^\+?[1-9]\d{7,14}$`, input.body.phone)
}

dob_valid if {
	has_field("date_of_birth")
	is_string(input.body.date_of_birth)
	regex.match(`^\d{4}-\d{2}-\d{2}$`, input.body.date_of_birth)
}

full_name_valid if {
	has_field("full_name")
	is_string(input.body.full_name)
	trim_space(input.body.full_name) != ""
}

all_required_present if {
	every f in required_fields {
		has_field(f)
	}
}

allow if {
	body_ok
	all_required_present
	email_valid
	phone_valid
	dob_valid
	full_name_valid
}

deny_reason contains "request body must be a JSON object" if { not body_ok }

deny_reason contains sprintf("missing required field: %s", [f]) if {
	body_ok
	some f in required_fields
	not has_field(f)
}

deny_reason contains "email is not a valid email address" if { body_ok; has_field("email"); not email_valid }

deny_reason contains "phone is not a valid E.164 phone number" if { body_ok; has_field("phone"); not phone_valid }

deny_reason contains "date_of_birth must be in YYYY-MM-DD format" if { body_ok; has_field("date_of_birth"); not dob_valid }

deny_reason contains "full_name must be a non-empty string" if { body_ok; has_field("full_name"); not full_name_valid }

# Generic injection-pattern checks (SQLi/XSS/NoSQL markers) are NOT
# duplicated here — they're handled once, platform-wide, by the global
# policy (package ingress.policy.payload.global) that auth-service
# evaluates against every route before this one. This route-specific
# policy only needs to know about its own fields.
