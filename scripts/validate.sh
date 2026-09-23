#!/usr/bin/env sh
set -eu
project_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$project_root"
set -a
if [ -f .env ]; then . ./.env; else . ./.env.example; fi
set +a
(command -v jq >/dev/null 2>&1) || { echo "jq is required for API validation" >&2; exit 1; }
(cd backend && go test ./... && go build ./...)
(cd frontend && npm install --no-audit --no-fund && npm run build)
docker compose config --quiet
docker compose up -d --build
cleanup() { docker compose down -v --remove-orphans; }
if [ "${KEEP_RUNNING:-0}" = "1" ]; then
  trap cleanup INT TERM
else
  trap cleanup EXIT INT TERM
fi
i=0
until curl -fsS "http://127.0.0.1:${BACKEND_PORT:-19517}/healthz" >/dev/null; do
  i=$((i+1)); [ "$i" -lt 60 ] || { docker compose logs; exit 1; }; sleep 2
done
curl -fsS "http://127.0.0.1:${FRONTEND_PORT:-18517}/" >/dev/null
login_token() {
  curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT:-19517}/api/auth/login" -H 'Content-Type: application/json' \
    -d "{\"username\":\"$1\",\"password\":\"Admin123!\"}" | jq -er '.data.token'
}
token=$(login_token admin)
viewer_token=$(login_token viewer)
operator_token=$(login_token operator)
reviewer_token=$(login_token reviewer)
[ -n "$token" ]
curl -fsS "http://127.0.0.1:${BACKEND_PORT:-19517}/api/overview" -H "Authorization: Bearer $token" >/dev/null
curl -fsS "http://127.0.0.1:${BACKEND_PORT}/api/session" -H "Authorization: Bearer $token" | jq -e '.data.role == "admin" and (.data.requestId | length > 0)' >/dev/null
curl -fsS "http://127.0.0.1:${BACKEND_PORT}/api/runtime" -H "Authorization: Bearer $token" | jq -e '.data.appName and .data.databaseDriver and (.data.requestLimit > 0)' >/dev/null
paths=$(sed -n "s/.*path: '\\([^']*\\)'.*/\\1/p" frontend/src/types/status.ts)
for path in $paths; do
  curl -fsS "http://127.0.0.1:${BACKEND_PORT}/api/$path?page=1&pageSize=20" -H "Authorization: Bearer $token" | jq -e '.data | type == "array"' >/dev/null
done
entity_config=$(sed -n "s/.*path: '\\([^']*\\)'.*statuses: \\['\\([^']*\\)', '\\([^']*\\)'.*/\\1|\\2|\\3/p" frontend/src/types/status.ts | head -n 1)
resource=$(printf '%s' "$entity_config" | cut -d '|' -f 1)
initial_status=$(printf '%s' "$entity_config" | cut -d '|' -f 2)
next_status=$(printf '%s' "$entity_config" | cut -d '|' -f 3)
now=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
code="SMOKE-$(date +%s)"
payload=$(printf '{"code":"%s","name":"Runtime smoke record","description":"Automated Compose workflow validation","facility":"Validation Lab","owner":"admin","category":"smoke","riskLevel":"low","metricValue":1,"metricUnit":"unit","effectiveAt":"%s","evidence":"scripts/validate.sh","relatedCode":"SMOKE"}' "$code" "$now")
created=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/$resource" -H "Authorization: Bearer $token" -H 'Content-Type: application/json' -d "$payload")
id=$(printf '%s' "$created" | jq -er '.data.id')
version=$(printf '%s' "$created" | jq -er '.data.version')
printf '%s' "$created" | jq -e --arg status "$initial_status" '.data.status == $status' >/dev/null
transition=$(printf '{"status":"%s","expectedVersion":%s,"reason":"automated runtime validation"}' "$next_status" "$version")
curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/$resource/$id/transition" -H "Authorization: Bearer $token" -H 'Content-Type: application/json' -d "$transition" | jq -e --arg status "$next_status" '.data.status == $status' >/dev/null
curl -fsS "http://127.0.0.1:${BACKEND_PORT}/api/audits?page=1&pageSize=100" -H "Authorization: Bearer $token" | jq -e '.meta.total >= 2' >/dev/null
curl -fsS "http://127.0.0.1:${BACKEND_PORT}/api/audit-summary?windowHours=24" -H "Authorization: Bearer $token" | jq -e '.data.total >= 2 and .data.transitions >= 1' >/dev/null

# A viewer may inspect operations but may never mutate them or inspect audits.
viewer_write_status=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:${BACKEND_PORT}/api/runs" \
  -H "Authorization: Bearer $viewer_token" -H 'Content-Type: application/json' -d "$payload")
[ "$viewer_write_status" = "403" ]
viewer_audit_status=$(curl -sS -o /dev/null -w '%{http_code}' "http://127.0.0.1:${BACKEND_PORT}/api/audits" -H "Authorization: Bearer $viewer_token")
[ "$viewer_audit_status" = "403" ]

# Proof capture is operational work; accepting the proof is a reviewer action.
proof_code="CP-SMOKE-$(date +%s)"
proof_payload=$(printf '{"code":"%s","name":"Runtime proof gate","description":"Proof acceptance validation","facility":"Validation Lab","owner":"operator","category":"calibration","riskLevel":"low","metricValue":1.8,"metricUnit":"dE","effectiveAt":"%s","evidence":"proof strip measurements","relatedCode":"PR-001"}' "$proof_code" "$now")
proof=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/proofs" -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -d "$proof_payload")
proof_id=$(printf '%s' "$proof" | jq -er '.data.id')
proof_version=$(printf '%s' "$proof" | jq -er '.data.version')
proof_review=$(printf '{"status":"review","expectedVersion":%s,"reason":"measurement capture completed"}' "$proof_version")
proof=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/proofs/$proof_id/transition" -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -d "$proof_review")
proof_version=$(printf '%s' "$proof" | jq -er '.data.version')
proof_accept=$(printf '{"status":"accepted","expectedVersion":%s,"reason":"colour tolerance independently verified"}' "$proof_version")
operator_accept_status=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:${BACKEND_PORT}/api/proofs/$proof_id/transition" -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -d "$proof_accept")
[ "$operator_accept_status" = "403" ]
curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/proofs/$proof_id/transition" -H "Authorization: Bearer $reviewer_token" -H 'Content-Type: application/json' -d "$proof_accept" | jq -e '.data.status == "accepted"' >/dev/null

# A release draft binds one proofing batch and the accepted proof above.
run_code="PR-SMOKE-$(date +%s)"
run_payload=$(printf '{"code":"%s","name":"Runtime release run","description":"Batch for the release gate","facility":"Validation Lab","owner":"operator","category":"calibration","riskLevel":"medium","metricValue":0,"metricUnit":"dE","effectiveAt":"%s","evidence":"press run for smoke validation","relatedCode":"SMOKE","toleranceLimit":3}' "$run_code" "$now")
run=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/runs" -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -d "$run_payload")
run_id=$(printf '%s' "$run" | jq -er '.data.id')
run_version=$(printf '%s' "$run" | jq -er '.data.version')
for next in printing proofing; do
  run=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/runs/$run_id/transition" -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' \
    -d "$(printf '{"status":"%s","expectedVersion":%s,"reason":"smoke flow advances run to %s"}' "$next" "$run_version" "$next")")
  run_version=$(printf '%s' "$run" | jq -er '.data.version')
done

# A draft cannot be generated from a non-proofing batch or non-accepted proof.
bad_draft_status=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:${BACKEND_PORT}/api/release" \
  -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' \
  -d "{\"printRunId\":$run_id,\"colorProofId\":9999999}")
[ "$bad_draft_status" = "422" ]

# Release decisions are versioned and only reviewer/admin may cross the release gate.
decision_code="RD-SMOKE-$(date +%s)"
decision_payload=$(printf '{"code":"%s","description":"RBAC and immutable revision validation","printRunId":%s,"colorProofId":%s,"evidence":"spectrophotometer validation evidence"}' "$decision_code" "$run_id" "$proof_id")
decision=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/release" -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -H 'X-Request-ID: release-create-smoke' -d "$decision_payload")
decision_id=$(printf '%s' "$decision" | jq -er '.data.id')
decision_version=$(printf '%s' "$decision" | jq -er '.data.version')
printf '%s' "$decision" | jq -e --arg run "$run_code" --arg proof "$proof_code" \
  '.data.printRunCode == $run and .data.colorProofNo == $proof and .data.basisValid == true and .data.basisToleranceLimit == 3' >/dev/null
release_payload=$(printf '{"status":"release","expectedVersion":%s,"reason":"validated proof and colour tolerance"}' "$decision_version")
operator_release_status=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:${BACKEND_PORT}/api/release/$decision_id/transition" -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -d "$release_payload")
[ "$operator_release_status" = "403" ]
curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/release/$decision_id/transition" -H "Authorization: Bearer $reviewer_token" -H 'Content-Type: application/json' -H 'X-Request-ID: release-review-smoke' -d "$release_payload" | jq -e '.data.status == "release" and .data.version == 2' >/dev/null
curl -fsS "http://127.0.0.1:${BACKEND_PORT}/api/release/$decision_id" -H "Authorization: Bearer $reviewer_token" | jq -e '.data.revisions | length == 2 and .[0].requestId == "release-review-smoke" and .[1].requestId == "release-create-smoke"' >/dev/null
# The bound batch crosses the release gate in the same atomic operation.
curl -fsS "http://127.0.0.1:${BACKEND_PORT}/api/runs/$run_id" -H "Authorization: Bearer $reviewer_token" | jq -e '.data.status == "released"' >/dev/null
docker compose ps
[ "${KEEP_RUNNING:-0}" = "1" ] && echo "KEEP_RUNNING=1: containers left running for browser validation"
