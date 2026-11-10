#!/usr/bin/env bash
# API-level e2e for coreban issue #1 stories 53-56 (keys, targeted reads, token-only access, fail-closed boot).
set -u
S=/tmp/claude-1000/-home-ejs-personal-coregateway/4ed3463e-3611-4747-aacb-1de08fe6273a/scratchpad
G=http://localhost:18080
C=http://localhost:6972
A=$S/admin.jar; D=$S/demo.jar
code() { curl -s -o /tmp/claude-1000/-home-ejs-personal-coregateway/4ed3463e-3611-4747-aacb-1de08fe6273a/scratchpad/body.json -w '%{http_code}' "$@"; }
body() { cat $S/body.json; }
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "PASS  $1 ($3)"; else echo "FAIL  $1 (want $2, got $3) $(body | head -c 200)"; fi
}

curl -s -c $A -H 'Content-Type: application/json' -d '{"email":"admin@e2e.local","password":"e2e-admin-pass-123"}' $G/api/auth/login >/dev/null
curl -s -c $D -X POST $G/api/auth/demo >/dev/null
ADMIN_ID=$(curl -s -b $A $G/api/auth/me | jq -r .id)
DEMO_ID=$(curl -s -b $D $G/api/auth/me | jq -r .id)
echo "admin=$ADMIN_ID demo=$DEMO_ID"

mk() { curl -s -b $A -H 'Content-Type: application/json' -d "{\"label\":\"e2e $1\",\"scopes\":[\"$1\"]}" $G/api/auth/keys | jq -r .key; }
KR=$(mk coreban:read); KW=$(mk coreban:write); KF=$(mk corefinance:read)

echo "== 53: API keys scoped coreban:read / coreban:write"
check "read key GET boards" 200 "$(code -H "Authorization: Bearer $KR" $G/api/coreban/boards)"
echo "      boards seen: $(body | jq -c '[.[]?.name] // .' 2>/dev/null)"
check "read key POST board refused" 403 "$(code -H "Authorization: Bearer $KR" -H 'Content-Type: application/json' -d '{"name":"nope"}' $G/api/coreban/boards)"
check "write key POST board" 201 "$(code -H "Authorization: Bearer $KW" -H 'Content-Type: application/json' -d '{"name":"Scripted board"}' $G/api/coreban/boards)"
SB=$(body | jq -r .id)
check "write key POST card" 201 "$(code -H "Authorization: Bearer $KW" -H 'Content-Type: application/json' -d '{"title":"made by a script"}' $G/api/coreban/boards/$SB/cards)"
check "corefinance key on coreban refused" 403 "$(code -H "Authorization: Bearer $KF" $G/api/coreban/boards)"
check "bogus key" 401 "$(code -H 'Authorization: Bearer cgw_live_bogus' $G/api/coreban/boards)"

echo "== 54: targeted reads (act_as) for admin, writes bound to caller"
check "demo creates own board" 201 "$(code -b $D -H 'Content-Type: application/json' -d '{"name":"Demo private board"}' $G/api/coreban/boards)"
check "admin targeted read of demo boards" 200 "$(code -b $A "$G/api/coreban/boards?user_id=$DEMO_ID")"
echo "      admin sees as demo: $(body | jq -c '[.[]?.name]')"
check "admin targeted read of demo reminders" 200 "$(code -b $A "$G/api/coreban/reminders?user_id=$DEMO_ID")"
check "admin write with user_id" 201 "$(code -b $A -H 'Content-Type: application/json' -d '{"name":"Admin write w/ user_id"}' "$G/api/coreban/boards?user_id=$DEMO_ID")"
DEMO_NAMES=$(curl -s -b $D $G/api/coreban/boards | jq -c '[.[].name]')
ADMIN_NAMES=$(curl -s -b $A $G/api/coreban/boards | jq -c '[.[].name]')
echo "      demo now has: $DEMO_NAMES"
echo "      admin now has: $ADMIN_NAMES"
case "$ADMIN_NAMES" in *"Admin write w/ user_id"*) case "$DEMO_NAMES" in *"Admin write"*) echo "FAIL  write leaked to target";; *) echo "PASS  write bound to caller, not target";; esac;; *) echo "FAIL  write not found on admin";; esac
check "non-admin targeting admin refused" 403 "$(code -b $D "$G/api/coreban/boards?user_id=$ADMIN_ID")"

echo "== 55: coreban accepts only the gateway internal token, audience coreban"
check "direct, no token" 401 "$(code $C/api/coreban/boards)"
check "direct, forged X-User-ID" 401 "$(code -H "X-User-ID: $ADMIN_ID" $C/api/coreban/boards)"
check "direct, raw API key" 401 "$(code -H "Authorization: Bearer $KW" $C/api/coreban/boards)"
mint() { node "$S/mint.mjs" "$@"; }
check "direct, valid gateway token aud=coreban (control)" 200 "$(code -H "Authorization: Bearer $(mint $S/jwt.pem coreban $ADMIN_ID e2e)" $C/api/coreban/boards)"
check "direct, gateway key but aud=coredraw" 401 "$(code -H "Authorization: Bearer $(mint $S/jwt.pem coredraw $ADMIN_ID e2e)" $C/api/coreban/boards)"
openssl genpkey -algorithm ed25519 -out $S/rogue.pem 2>/dev/null
check "direct, rogue key same kid" 401 "$(code -H "Authorization: Bearer $(mint $S/rogue.pem coreban $ADMIN_ID e2e)" $C/api/coreban/boards)"
check "direct, expired token" 401 "$(code -H "Authorization: Bearer $(mint $S/jwt.pem coreban $ADMIN_ID e2e expired)" $C/api/coreban/boards)"
check "unauthenticated /health liveness" 200 "$(code $C/health)"

echo "== 56: refuses to start without the gateway's JWKS"
cd /home/ejs/personal/coregateway/services/coreban
env -i PATH=$PATH HOME=$HOME GOPATH=$HOME/go PORT=16999 DATABASE_URL="postgresql://coreban_role:cbpw@localhost:55432/coredb?sslmode=disable" JWT_ISSUER=https://gateway.internal JWT_AUDIENCE=coreban JWT_JWKS_URL=http://localhost:1/.well-known/jwks.json timeout 60 go run ./cmd/coreban > $S/failclosed.log 2>&1
rc=$?
echo "      exit=$rc; last log: $(tail -1 $S/failclosed.log)"
if [ $rc -ne 0 ] && [ $rc -ne 124 ] && ! curl -s -m1 localhost:16999/health >/dev/null; then echo "PASS  refused to start"; else echo "FAIL  started or hung (rc=$rc)"; fi

# cleanup e2e keys
for id in $(curl -s -b $A $G/api/auth/keys | jq -r '.[] | select(.label|startswith("e2e ")) | .id'); do curl -s -b $A -X DELETE $G/api/auth/keys/$id >/dev/null; done
