#!/bin/sh
set -eu

origin=${WITHHUMAN_ORIGIN:-http://localhost:8080}
app_api=$origin/api/v1
bootstrap_secret=${WITHHUMAN_BOOTSTRAP_SECRET:-change-me-before-production}
local_password=${WITHHUMAN_SMOKE_PASSWORD:-local-development-password}
cookie_jar=$(mktemp)
invitee_jar=$(mktemp)
bootstrap_response=$(mktemp)
trap 'rm -f "$cookie_jar" "$invitee_jar" "$bootstrap_response"' EXIT

until curl --silent --fail "$origin/readyz" >/dev/null; do sleep 1; done

bootstrap_body=$(jq --null-input --compact-output --arg secret "$bootstrap_secret" --arg password "$local_password" '{
  secret: $secret,
  organization_slug: "acme",
  organization_name: "Acme",
  display_name: "Test Owner",
  email: "owner@example.com",
  password: $password
}')
bootstrap_status=$(curl --silent --output "$bootstrap_response" --write-out '%{http_code}' --cookie-jar "$cookie_jar" \
  --header 'Content-Type: application/json' --data "$bootstrap_body" "$origin/auth/bootstrap")

if [ "$bootstrap_status" != "201" ] && [ "$bootstrap_status" != "409" ]; then
  cat "$bootstrap_response"
  exit 1
fi

# Email-first login: method discovery answers uniformly for unknown
# emails, a wrong password is refused, and a single-organization user
# completes the login in one step.
curl --silent --fail --header 'Content-Type: application/json' \
  --data '{"email":"nobody@example.com"}' "$origin/auth/login/methods" \
  | jq -e '.password == true and (.sso_organizations | length) == 0' >/dev/null
wrong_login_status=$(curl --silent --output /dev/null --write-out '%{http_code}' --header 'Content-Type: application/json' \
  --data '{"email":"owner@example.com","password":"definitely-not-the-password"}' \
  "$origin/auth/login")
test "$wrong_login_status" = "401"
curl --silent --fail --cookie-jar "$cookie_jar" --header 'Content-Type: application/json' \
  --data "$(jq --null-input --compact-output --arg password "$local_password" '{email:"owner@example.com",password:$password}')" \
  "$origin/auth/login" | jq -e '.status == "complete"' >/dev/null

unique=$(basename "$cookie_jar" | tr '[:upper:]' '[:lower:]')
approval_trace_id=${WITHHUMAN_SMOKE_REQUEST_ID:-smoke-approval-$unique}
decision_trace_id=${WITHHUMAN_SMOKE_DECISION_REQUEST_ID:-smoke-decision-$unique}

# Invitation lifecycle: create, duplicate conflict, public preview by
# token, accept as a new user with a set password, then manage the
# membership. The enriched /me proves who is signed in.
invitee_email="invitee-$unique@example.com"
reviewer_policy='{"scope_kind":"organization","resource_ids":[],"permissions":["request.read","request.claim","request.decide"]}'
auditor_policy='{"scope_kind":"organization","resource_ids":[],"permissions":["request.read.unrouted"]}'
invitation=$(curl --silent --fail --cookie "$cookie_jar" --header 'Content-Type: application/json' \
  --data "{\"email\":\"$invitee_email\",\"permission_policies\":[$reviewer_policy]}" "$app_api/invitations")
invitation_id=$(printf '%s' "$invitation" | jq -r .invitation.id)
invite_url=$(printf '%s' "$invitation" | jq -r .invite_url)
invite_token=${invite_url##*/invite/}
duplicate_invite_status=$(curl --silent --output /dev/null --write-out '%{http_code}' --cookie "$cookie_jar" \
  --header 'Content-Type: application/json' --data "{\"email\":\"$invitee_email\",\"permission_policies\":[$reviewer_policy]}" "$app_api/invitations")
test "$duplicate_invite_status" = "409"

curl --silent --fail --header 'Content-Type: application/json' \
  --data "{\"token\":\"$invite_token\"}" "$origin/auth/invitations/preview" \
  | jq -e '.organization_name == "Acme" and .existing_user == false' >/dev/null

curl --silent --fail --cookie-jar "$invitee_jar" --header 'Content-Type: application/json' \
  --data "$(jq --null-input --compact-output --arg token "$invite_token" --arg password "$local_password" \
    '{token: $token, display_name: "Smoke Invitee", password: $password}')" \
  "$origin/auth/invitations/accept" >/dev/null
curl --silent --fail --cookie "$invitee_jar" "$app_api/me" \
  | jq -e --arg email "$invitee_email" '.user.email == $email and any(.assignments[]; (.permissions | index("request.decide")) != null and .scope_kind == "organization")' >/dev/null
reaccept_status=$(curl --silent --output /dev/null --write-out '%{http_code}' --header 'Content-Type: application/json' \
  --data "{\"token\":\"$invite_token\",\"display_name\":\"Again\",\"password\":\"$local_password\"}" \
  "$origin/auth/invitations/accept")
test "$reaccept_status" = "409"

# A fresh login proves the invitee received real local credentials.
curl --silent --fail --header 'Content-Type: application/json' \
  --data "$(jq --null-input --compact-output --arg email "$invitee_email" --arg password "$local_password" \
    '{email: $email, password: $password}')" \
  "$origin/auth/login" | jq -e '.status == "complete"' >/dev/null

member_id=$(curl --silent --fail --cookie "$cookie_jar" "$app_api/members" \
  | jq -r --arg email "$invitee_email" '.members[] | select(.email == $email) | .id')
curl --silent --fail --cookie "$cookie_jar" --header 'Content-Type: application/json' \
  --header "Idempotency-Key: audit-$unique" --data "$auditor_policy" \
  "$app_api/members/$member_id/permission_policies" | jq -e '.permissions == ["request.read.unrouted"]' >/dev/null
self_patch_status=$(curl --silent --output /dev/null --write-out '%{http_code}' --cookie "$invitee_jar" \
  --header 'Content-Type: application/json' --header "Idempotency-Key: self-$unique" \
  --data "$reviewer_policy" "$app_api/members/$member_id/permission_policies")
test "$self_patch_status" = "403"
curl --silent --fail --cookie "$cookie_jar" --request PATCH --header 'Content-Type: application/json' \
  --data '{"status":"suspended"}' "$app_api/members/$member_id" >/dev/null
suspended_me_status=$(curl --silent --output /dev/null --write-out '%{http_code}' --cookie "$invitee_jar" "$app_api/me")
test "$suspended_me_status" = "401" || test "$suspended_me_status" = "403"
curl --silent --fail --cookie "$cookie_jar" --request PATCH --header 'Content-Type: application/json' \
  --data '{"status":"active"}' "$app_api/members/$member_id" >/dev/null

# Resend rotates the token; revoke ends redemption.
second_email="invitee2-$unique@example.com"
second=$(curl --silent --fail --cookie "$cookie_jar" --header 'Content-Type: application/json' \
  --data "{\"email\":\"$second_email\",\"permission_policies\":[$auditor_policy]}" "$app_api/invitations")
second_id=$(printf '%s' "$second" | jq -r .invitation.id)
old_url=$(printf '%s' "$second" | jq -r .invite_url)
old_token=${old_url##*/invite/}
resent=$(curl --silent --fail --cookie "$cookie_jar" --request POST "$app_api/invitations/$second_id/resend")
new_url=$(printf '%s' "$resent" | jq -r .invite_url)
new_token=${new_url##*/invite/}
old_preview_status=$(curl --silent --output /dev/null --write-out '%{http_code}' --header 'Content-Type: application/json' \
  --data "{\"token\":\"$old_token\"}" "$origin/auth/invitations/preview")
test "$old_preview_status" = "404"
curl --silent --fail --cookie "$cookie_jar" --request DELETE "$app_api/invitations/$second_id" >/dev/null
revoked_accept_status=$(curl --silent --output /dev/null --write-out '%{http_code}' --header 'Content-Type: application/json' \
  --data "{\"token\":\"$new_token\",\"display_name\":\"Too Late\",\"password\":\"$local_password\"}" \
  "$origin/auth/invitations/accept")
test "$revoked_accept_status" = "404"

enrollment_agent=$(curl --silent --fail --cookie "$cookie_jar" --header 'Content-Type: application/json' \
  --data "{\"slug\":\"enrolled-$unique\",\"name\":\"Enrolled $unique\"}" "$app_api/agents")
enrollment_agent_slug=$(printf '%s' "$enrollment_agent" | jq -r .slug)
test "$enrollment_agent_slug" = "enrolled-$unique"
enrollment=$(curl --silent --fail --header 'Content-Type: application/json' \
  --data "{\"runtime\":\"claude-code\",\"requested_agent_slug\":\"enrolled-$unique\",\"instance_name\":\"local-$unique\",\"instance_metadata\":{\"environment\":\"smoke\"}}" \
  "$origin/auth/agent_enrollments")
enrollment_code=$(printf '%s' "$enrollment" | jq -r .enrollment_code)
user_code=$(printf '%s' "$enrollment" | jq -r .user_code)
curl --silent --fail --cookie "$cookie_jar" --header 'Content-Type: application/json' \
  --data "{\"user_code\":\"$user_code\",\"agent_slug\":\"$enrollment_agent_slug\"}" "$app_api/agent_enrollments/authorize" >/dev/null
enrolled_instance=$(curl --silent --fail --header 'Content-Type: application/json' \
  --data "{\"enrollment_code\":\"$enrollment_code\"}" "$origin/auth/agent_enrollments/token")
enrollment_token=$(printf '%s' "$enrolled_instance" | jq -r .credential.token)
curl --silent --fail --header "Authorization: Bearer $enrollment_token" "$origin/api/v1/agent_instances/self" | jq -e '.agent_slug | length > 0' >/dev/null

agent=$(curl --silent --fail --cookie "$cookie_jar" --header 'Content-Type: application/json' \
  --data "{\"slug\":\"smoke-$unique\",\"name\":\"Smoke $unique\"}" "$app_api/agents")
agent_slug=$(printf '%s' "$agent" | jq -r .slug)
test "$agent_slug" = "smoke-$unique"
# The slug is the identity: taking it again is refused, and so is a live
# agent's display name under another slug.
agent_conflict_status=$(curl --silent --output /dev/null --write-out '%{http_code}' --cookie "$cookie_jar" \
  --header 'Content-Type: application/json' --data "{\"slug\":\"smoke-$unique\",\"name\":\"Another $unique\"}" "$app_api/agents")
test "$agent_conflict_status" = "409"
name_conflict_status=$(curl --silent --output /dev/null --write-out '%{http_code}' --cookie "$cookie_jar" \
  --header 'Content-Type: application/json' --data "{\"slug\":\"smoke-other-$unique\",\"name\":\"Smoke $unique\"}" "$app_api/agents")
test "$name_conflict_status" = "409"
instance=$(curl --silent --fail --cookie "$cookie_jar" --header 'Content-Type: application/json' \
  --data '{"name":"smoke runner","metadata":{"environment":"test"}}' "$app_api/agents/$agent_slug/instances")
token=$(printf '%s' "$instance" | jq -r .credential.token)
instance_conflict_status=$(curl --silent --output /dev/null --write-out '%{http_code}' --cookie "$cookie_jar" \
  --header 'Content-Type: application/json' --data '{"name":"smoke runner"}' "$app_api/agents/$agent_slug/instances")
test "$instance_conflict_status" = "409"
curl --silent --fail --cookie "$cookie_jar" "$app_api/agents?limit=100" | jq -e --arg slug "$agent_slug" '.agents | any(.slug == $slug)' >/dev/null

# Provisioner-token minting: an admin issues an agent-scoped provisioner,
# a fleet orchestrator mints instance credentials with it, and the
# credential classes stay disjoint.
provisioner=$(curl --silent --fail --cookie "$cookie_jar" --request POST "$app_api/agents/$agent_slug/provisioners")
provisioner_id=$(printf '%s' "$provisioner" | jq -r .provisioner.id)
provisioner_token=$(printf '%s' "$provisioner" | jq -r .credential.token)
minted=$(curl --silent --fail --header "Authorization: Bearer $provisioner_token" --header 'Content-Type: application/json' \
  --data '{"instance_metadata":{"region":"smoke"},"ttl_seconds":3600}' "$origin/api/aap/v1/instances")
printf '%s' "$minted" | jq -e '.agent_instance.name | length > 0' >/dev/null
minted_token=$(printf '%s' "$minted" | jq -r .credential.token)
curl --silent --fail --header "Authorization: Bearer $minted_token" "$origin/api/v1/agent_instances/self" >/dev/null
provisioner_on_instance_status=$(curl --silent --output /dev/null --write-out '%{http_code}' \
  --header "Authorization: Bearer $provisioner_token" "$origin/api/v1/agent_instances/self")
test "$provisioner_on_instance_status" = "401"
instance_token_mint_status=$(curl --silent --output /dev/null --write-out '%{http_code}' \
  --header "Authorization: Bearer $minted_token" --request POST "$origin/api/aap/v1/instances")
test "$instance_token_mint_status" = "401"
curl --silent --fail --cookie "$cookie_jar" --request DELETE "$app_api/agent-provisioners/$provisioner_id" >/dev/null
revoked_mint_status=$(curl --silent --output /dev/null --write-out '%{http_code}' \
  --header "Authorization: Bearer $provisioner_token" --request POST "$origin/api/aap/v1/instances")
test "$revoked_mint_status" = "401"

request=$(curl --silent --fail --header "Authorization: Bearer $token" --header 'Content-Type: application/json' \
	--header "X-Request-ID: $approval_trace_id" \
  --header "Idempotency-Key: smoke-request-$unique" --data '{"tool":"deploy.production","arguments":{"version":"smoke"},"agent_reasoning":"Deploying the smoke-test version","timeout":"2m"}' \
  "$origin/api/aap/v1/requests")
request_id=$(printf '%s' "$request" | jq -r .id)
retry_id=$(curl --silent --fail --header "Authorization: Bearer $token" --header 'Content-Type: application/json' \
  --header "Idempotency-Key: smoke-request-$unique" --data '{"tool":"deploy.production","arguments":{"version":"smoke"},"agent_reasoning":"Deploying the smoke-test version","timeout":"2m"}' \
  "$origin/api/aap/v1/requests" | jq -r .id)
test "$retry_id" = "$request_id"
request_conflict_status=$(curl --silent --output /dev/null --write-out '%{http_code}' \
  --header "Authorization: Bearer $token" --header 'Content-Type: application/json' \
  --header "Idempotency-Key: smoke-request-$unique" --data '{"tool":"deploy.production","arguments":{"version":"different"},"agent_reasoning":"Deploying the smoke-test version","timeout":"2m"}' \
  "$origin/api/aap/v1/requests")
test "$request_conflict_status" = "409"

curl --silent --fail --cookie "$cookie_jar" --header 'Content-Type: application/json' \
	--header "X-Request-ID: $decision_trace_id" \
  --header "Idempotency-Key: smoke-decision-$unique" --data '{"status":"approved","note":"Automated end-to-end verification","channel":"web"}' \
  "$app_api/requests/$request_id/decision" >/dev/null
curl --silent --fail --cookie "$cookie_jar" --header 'Content-Type: application/json' \
  --header "Idempotency-Key: smoke-decision-$unique" --data '{"status":"approved","note":"Automated end-to-end verification","channel":"web"}' \
  "$app_api/requests/$request_id/decision" >/dev/null
decision_conflict_status=$(curl --silent --output /dev/null --write-out '%{http_code}' \
  --cookie "$cookie_jar" --header 'Content-Type: application/json' \
  --header "Idempotency-Key: smoke-decision-$unique" --data '{"status":"denied","note":"Changed outcome","channel":"web"}' \
  "$app_api/requests/$request_id/decision")
test "$decision_conflict_status" = "409"

status=$(curl --silent --fail --header "Authorization: Bearer $token" "$origin/api/aap/v1/requests/$request_id" | jq -r .status)
test "$status" = "approved"

expiring=$(curl --silent --fail --header "Authorization: Bearer $token" --header 'Content-Type: application/json' \
  --header "Idempotency-Key: smoke-expiry-$unique" --data '{"tool":"deploy.preview","arguments":{"version":"expired"},"agent_reasoning":"Exercising the durable approval deadline","timeout":"1s"}' \
  "$origin/api/aap/v1/requests")
expiring_id=$(printf '%s' "$expiring" | jq -r .id)
expired_status=$(curl --silent --fail --header "Authorization: Bearer $token" \
  "$origin/api/aap/v1/requests/$expiring_id?wait=3s" | jq -r .status)
test "$expired_status" = "expired"

# Withdrawal: the instance that opened a request takes it back, the
# resource comes back cancelled, a second cancel is idempotent, and a
# human can no longer decide it.
withdrawn=$(curl --silent --fail --header "Authorization: Bearer $token" --header 'Content-Type: application/json' \
  --header "Idempotency-Key: smoke-cancel-$unique" --data '{"tool":"deploy.preview","arguments":{"version":"withdrawn"},"agent_reasoning":"Exercising agent-side withdrawal","timeout":"5m"}' \
  "$origin/api/aap/v1/requests")
withdrawn_id=$(printf '%s' "$withdrawn" | jq -r .id)
cancelled_status=$(curl --silent --fail --request DELETE --header "Authorization: Bearer $token" \
  "$origin/api/aap/v1/requests/$withdrawn_id" | jq -r .status)
test "$cancelled_status" = "cancelled"
curl --silent --fail --request DELETE --header "Authorization: Bearer $token" \
  "$origin/api/aap/v1/requests/$withdrawn_id" | jq -e '.status == "cancelled" and .decision.channel == "aap"' >/dev/null
cancelled_decision_status=$(curl --silent --output /dev/null --write-out '%{http_code}' \
  --cookie "$cookie_jar" --header 'Content-Type: application/json' \
  --header "Idempotency-Key: smoke-cancelled-decision-$unique" --data '{"status":"approved","note":"Too late","channel":"web"}' \
  "$app_api/requests/$withdrawn_id/decision")
test "$cancelled_decision_status" = "409"
approved_cancel_status=$(curl --silent --output /dev/null --write-out '%{http_code}' --request DELETE \
  --header "Authorization: Bearer $token" "$origin/api/aap/v1/requests/$request_id")
test "$approved_cancel_status" = "409"

# Gateway surfaces, exercised only when the instance has an encryption
# key and a gateway token configured. The gateway token and agent
# credentials are disjoint by design: the token reads gateway
# configuration for any organization but can never create approval
# requests, and a whc_ token can never read gateway configuration. The
# machine surface names the organization in the path because one gateway
# serves every organization.
gateway_enabled=$(curl --silent --fail "$origin/api/runtime-config" | jq -r .gateway_enabled)
if [ "$gateway_enabled" = "true" ] && [ -n "${WITHHUMAN_GATEWAY_TOKEN:-}" ]; then
  gateway_server=$(curl --silent --fail --cookie "$cookie_jar" --header 'Content-Type: application/json' \
    --data "{\"name\":\"Smoke Downstream\",\"slug\":\"smoke-$unique\",\"url\":\"http://downstream.invalid/mcp\",\"auth_mode\":\"none\",\"gated_tools\":[\"deploy_*\"]}" \
    "$app_api/gateway/servers")
  gateway_server_id=$(printf '%s' "$gateway_server" | jq -r .server_id)
  organization_id=$(curl --silent --fail --cookie "$cookie_jar" "$app_api/me" | jq -r .principal.organization_id)
  gateway_org="$origin/api/gateway/v1/organizations/$organization_id"

  curl --silent --fail --header "Authorization: Bearer $WITHHUMAN_GATEWAY_TOKEN" "$origin/api/gateway/v1/ping" >/dev/null
  # Revision 1 is inactive until activated, so the gateway serves nothing yet.
  curl --silent --fail --header "Authorization: Bearer $WITHHUMAN_GATEWAY_TOKEN" "$gateway_org/servers" \
    | jq -e --arg slug "smoke-$unique" '.servers | map(select(.slug == $slug)) | length == 0' >/dev/null
  curl --silent --fail --cookie "$cookie_jar" --header 'If-Match: "0"' --request POST \
    "$app_api/gateway/servers/smoke-$unique/revisions/1/activate" >/dev/null
  curl --silent --fail --header "Authorization: Bearer $WITHHUMAN_GATEWAY_TOKEN" "$gateway_org/servers" \
    | jq -e --arg slug "smoke-$unique" '.servers | map(select(.slug == $slug)) | length == 1' >/dev/null
  curl --silent --fail --header "Authorization: Bearer $WITHHUMAN_GATEWAY_TOKEN" \
    "$gateway_org/servers/$gateway_server_id/credential" \
    | jq -e '.headers == {}' >/dev/null
  curl --silent --fail --header "Authorization: Bearer $WITHHUMAN_GATEWAY_TOKEN" --header 'Content-Type: application/json' \
    --data "{\"token\":\"$token\"}" "$origin/api/gateway/v1/introspect-agent-token" \
    | jq -e --arg org "$organization_id" '.agent_instance_id != "" and .organization_id == $org' >/dev/null

  agent_on_gateway_status=$(curl --silent --output /dev/null --write-out '%{http_code}' \
    --header "Authorization: Bearer $token" "$gateway_org/servers")
  test "$agent_on_gateway_status" = "401"
  gateway_on_aap_status=$(curl --silent --output /dev/null --write-out '%{http_code}' \
    --header "Authorization: Bearer $WITHHUMAN_GATEWAY_TOKEN" --header 'Content-Type: application/json' \
    --header "Idempotency-Key: smoke-gateway-$unique" \
    --data '{"tool":"deploy.production","arguments":{},"timeout":"2m"}' "$origin/api/aap/v1/requests")
  test "$gateway_on_aap_status" = "401"

  curl --silent --fail --cookie "$cookie_jar" --request DELETE \
    "$app_api/gateway/servers/smoke-$unique" >/dev/null
  echo "withHuman gateway smoke passed: server $gateway_server_id registered and removed"
fi

echo "withHuman smoke test passed: $request_id approved, $expiring_id expired, $withdrawn_id withdrawn (traces: $approval_trace_id, $decision_trace_id)"
