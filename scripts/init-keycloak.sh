#!/bin/bash
# init-keycloak.sh — Provisions Keycloak realm, users, groups, and OIDC client
# for the Team Visualizer demo with oauth2-proxy.
#
# Idempotent: every resource is created only if it is missing, so this script
# is safe to re-run and will self-heal a half-provisioned realm (e.g. a realm
# that exists but is missing its client/mappers because an earlier run failed).
set -euo pipefail

KC_URL="http://keycloak:8080"
ADMIN_USER="admin"
ADMIN_PASS="admin"
REALM="teamviz"
CLIENT_SLUG="teamviz-demo"

echo "Assuming Keycloak is ready."

# ---- helpers ---------------------------------------------------------------
# Get an admin access token (re-fetched each run; tokens are short-lived).
get_token() {
  curl -fs -X POST "$KC_URL/realms/master/protocol/openid-connect/token" \
    -d "grant_type=password" -d "client_id=admin-cli" \
    -d "username=$ADMIN_USER" -d "password=$ADMIN_PASS" \
    | python3 -c "import sys,json;print(json.load(sys.stdin)['access_token'])"
}

# `exists <path>` → returns 0 if the given admin resource responds 200.
exists() {
  curl -fs -H "Authorization: Bearer $TOKEN" "$KC_URL/admin/$1" >/dev/null 2>&1
}

# `get_id <path>` → prints the id field of the first element returned.
get_id() {
  curl -fs -H "Authorization: Bearer $TOKEN" "$KC_URL/admin/$1" \
    | python3 -c "import sys,json;d=json.load(sys.stdin);print(d[0]['id'] if d else '')"
}

TOKEN="$(get_token)"
if [ -z "${TOKEN:-}" ]; then echo "ERROR: no admin token"; exit 1; fi

H_JSON="Content-Type: application/json"

# ---- realm -----------------------------------------------------------------
if exists "realms/$REALM"; then
  echo "Realm '$REALM' already exists."
else
  echo "=== Creating realm: $REALM ==="
  curl -fs -X POST -H "Authorization: Bearer $TOKEN" -H "$H_JSON" \
    "$KC_URL/admin/realms" -d '{
      "realm":"teamviz","enabled":true,"sslRequired":"none",
      "accessTokenLifespan":36000,"ssoSessionIdleTimeout":36000
    }'
fi

# ---- groups ----------------------------------------------------------------
echo "=== Ensuring groups ==="
declare -A GROUP_IDS
for G in tvz-admin tvz-normal tvz-readonly; do
  GID="$(get_id "realms/$REALM/groups?search=$G")"
  if [ -z "$GID" ]; then
    curl -fs -X POST -H "Authorization: Bearer $TOKEN" -H "$H_JSON" \
      "$KC_URL/admin/realms/$REALM/groups" -d "{\"name\":\"$G\"}"
    GID="$(get_id "realms/$REALM/groups?search=$G")"
    echo "  Created group: $G"
  else
    echo "  Group already present: $G"
  fi
  GROUP_IDS["$G"]="$GID"
done

# ---- users -----------------------------------------------------------------
echo "=== Ensuring users ==="
create_user() {
  local U="$1" P="$2" G="$3" FN="$4" LN="$5"
  local U_ID
  U_ID="$(get_id "realms/$REALM/users?username=$U&exact=true")"
  if [ -z "$U_ID" ]; then
    curl -fs -X POST -H "Authorization: Bearer $TOKEN" -H "$H_JSON" \
      "$KC_URL/admin/realms/$REALM/users" -d "{
        \"username\":\"$U\",\"firstName\":\"$FN\",\"lastName\":\"$LN\",
        \"email\":\"$U@example.com\",\"emailVerified\":true,\"enabled\":true,
        \"credentials\":[{\"type\":\"password\",\"value\":\"$P\",\"temporary\":false}]
      }"
    U_ID="$(get_id "realms/$REALM/users?username=$U&exact=true")"
    echo "  Created user: $U"
  else
    echo "  User already present: $U"
  fi
  curl -fs -X PUT -H "Authorization: Bearer $TOKEN" \
    "$KC_URL/admin/realms/$REALM/users/$U_ID/groups/${GROUP_IDS[$G]}" \
    >/dev/null 2>&1 || true   # assigning an already-assigned group is not an error
}

create_user "admin"  "admin"  "tvz-admin"    "Admin"  "User"
create_user "user"   "user"   "tvz-normal"   "Normal" "User"
create_user "rouser" "rouser" "tvz-readonly" "Read"   "Only"

# ---- OIDC client -----------------------------------------------------------
echo "=== Ensuring OIDC client: $CLIENT_SLUG ==="
CLIENT_ID="$(get_id "realms/$REALM/clients?clientId=$CLIENT_SLUG")"
if [ -z "$CLIENT_ID" ]; then
  curl -fs -X POST -H "Authorization: Bearer $TOKEN" -H "$H_JSON" \
    "$KC_URL/admin/realms/$REALM/clients" -d '{
      "clientId":"teamviz-demo",
      "name":"Team Visualizer Demo",
      "enabled":true,
      "protocol":"openid-connect",
      "publicClient":false,
      "secret":"demo-secret-not-for-production-use",
      "directAccessGrantsEnabled":true,
      "standardFlowEnabled":true,
      "redirectUris":["http://localhost:8080/oauth2/callback","http://localhost:8080/*"],
      "webOrigins":["http://localhost:8080"],
      "attributes":{
        "post.logout.redirect.uris":"http://localhost:8080/*",
        "oauth2.device.authorization.grant.enabled":"false"
      }
    }'
  CLIENT_ID="$(get_id "realms/$REALM/clients?clientId=$CLIENT_SLUG")"
  echo "  Created client: $CLIENT_SLUG"
else
  echo "  Client already present: $CLIENT_SLUG"
fi

# ---- protocol mappers ------------------------------------------------------
ensure_mapper() {
  local NAME="$1" PAYLOAD="$2"
  # Mappers are listed without an easy name filter; fetch all and look for ours.
  local HAVE
  HAVE="$(curl -fs -H "Authorization: Bearer $TOKEN" \
    "$KC_URL/admin/realms/$REALM/clients/$CLIENT_ID/protocol-mappers/models" \
    | python3 -c "import sys,json;print(any(m['name']=='$NAME' for m in json.load(sys.stdin)))")"
  if [ "$HAVE" = "True" ]; then
    echo "  Mapper already present: $NAME"
  else
    curl -fs -X POST -H "Authorization: Bearer $TOKEN" -H "$H_JSON" \
      "$KC_URL/admin/realms/$REALM/clients/$CLIENT_ID/protocol-mappers/models" \
      -d "$PAYLOAD"
    echo "  Created mapper: $NAME"
  fi
}

echo "=== Ensuring protocol mappers ==="
ensure_mapper "group-membership-mapper" '{
  "name":"group-membership-mapper",
  "protocol":"openid-connect",
  "protocolMapper":"oidc-group-membership-mapper",
  "consentRequired":false,
  "config":{
    "full.path":"false",
    "id.token.claim":"true",
    "access.token.claim":"true",
    "userinfo.token.claim":"true",
    "claim.name":"groups"
  }
}'

ensure_mapper "preferred-username-mapper" '{
  "name":"preferred-username-mapper",
  "protocol":"openid-connect",
  "protocolMapper":"oidc-usermodel-property-mapper",
  "consentRequired":false,
  "config":{
    "user.attribute":"username",
    "id.token.claim":"true",
    "access.token.claim":"true",
    "userinfo.token.claim":"true",
    "claim.name":"preferred_username",
    "jsonType.label":"String"
  }
}'

echo ""
echo "=============================================="
echo " Keycloak provisioning complete!"
echo "=============================================="
echo ""
echo " Realm: $REALM"
echo " Client: $CLIENT_SLUG"
echo ""
echo " Users (password = username):"
echo "   admin  → tvz-admin    → app: admin"
echo "   user   → tvz-normal   → app: normal"
echo "   rouser → tvz-readonly → app: readonly"
echo ""
echo " Keycloak console: http://localhost:8090"
echo " Admin login: admin / admin"
echo ""
echo " App URL: http://localhost:8080"
