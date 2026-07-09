#!/bin/bash
# init-keycloak.sh — Provisions Keycloak realm, users, groups, and OIDC client
# for the Team Visualizer demo with oauth2-proxy.
set -e

KC_URL="http://keycloak:8080"
ADMIN_USER="admin"
ADMIN_PASS="admin"
REALM="teamviz"

echo "Assuming Keycloak is ready."

# Get admin token
TOKEN=$(curl -fs -X POST "$KC_URL/realms/master/protocol/openid-connect/token" \
  -d "grant_type=password" -d "client_id=admin-cli" \
  -d "username=$ADMIN_USER" -d "password=$ADMIN_PASS" \
  | python3 -c "import sys,json;print(json.load(sys.stdin)['access_token'])" 2>/dev/null)

if [ -z "$TOKEN" ]; then echo "ERROR: no admin token"; exit 1; fi

H_AUTH="Authorization: Bearer $TOKEN"
H_JSON="Content-Type: application/json"

# Check if realm exists
if curl -fs -H "$H_AUTH" "$KC_URL/admin/realms/$REALM" >/dev/null 2>&1; then
  echo "Realm '$REALM' already exists — skipping."
  exit 0
fi

echo "=== Creating realm: $REALM ==="
curl -fs -X POST -H "$H_AUTH" -H "$H_JSON" "$KC_URL/admin/realms" -d '{
  "realm":"teamviz","enabled":true,"sslRequired":"none",
  "accessTokenLifespan":36000,"ssoSessionIdleTimeout":36000
}'

# ---- Create groups ----
echo "=== Creating groups ==="
for G in tvz-admin tvz-normal tvz-readonly; do
  curl -fs -X POST -H "$H_AUTH" -H "$H_JSON" "$KC_URL/admin/realms/$REALM/groups" \
    -d "{\"name\":\"$G\"}" 2>/dev/null || true
  echo "  Group: $G"
done

# Get group IDs
ADMIN_GID=$(curl -fs -H "$H_AUTH" "$KC_URL/admin/realms/$REALM/groups?search=tvz-admin" | python3 -c "import sys,json;print(json.load(sys.stdin)[0]['id'])")
NORMAL_GID=$(curl -fs -H "$H_AUTH" "$KC_URL/admin/realms/$REALM/groups?search=tvz-normal" | python3 -c "import sys,json;print(json.load(sys.stdin)[0]['id'])")
READONLY_GID=$(curl -fs -H "$H_AUTH" "$KC_URL/admin/realms/$REALM/groups?search=tvz-readonly" | python3 -c "import sys,json;print(json.load(sys.stdin)[0]['id'])")

# ---- Create users ----
echo "=== Creating users ==="
create_user() {
  local U="$1" P="$2" GID="$3" FN="$4" LN="$5"
  echo "  User: $U (password: $P)"
  # Create user
  curl -fs -X POST -H "$H_AUTH" -H "$H_JSON" "$KC_URL/admin/realms/$REALM/users" -d "{
    \"username\":\"$U\",\"firstName\":\"$FN\",\"lastName\":\"$LN\",
    \"email\":\"$U@example.com\",\"emailVerified\":true,\"enabled\":true,
    \"credentials\":[{\"type\":\"password\",\"value\":\"$P\",\"temporary\":false}]
  }" 2>/dev/null || true
  # Get user ID
  U_ID=$(curl -fs -H "$H_AUTH" "$KC_URL/admin/realms/$REALM/users?username=$U&exact=true" | python3 -c "import sys,json;print(json.load(sys.stdin)[0]['id'])" 2>/dev/null)
  # Assign group
  curl -fs -X PUT -H "$H_AUTH" "$KC_URL/admin/realms/$REALM/users/$U_ID/groups/$GID" 2>/dev/null || true
}

create_user "admin"  "admin"  "$ADMIN_GID"    "Admin"  "User"
create_user "user"   "user"   "$NORMAL_GID"   "Normal" "User"
create_user "rouser" "rouser" "$READONLY_GID" "Read"   "Only"

# ---- Create OIDC client for oauth2-proxy ----
echo "=== Creating OIDC client: teamviz-demo ==="
curl -fs -X POST -H "$H_AUTH" -H "$H_JSON" "$KC_URL/admin/realms/$REALM/clients" -d '{
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

# Get client ID
CLIENT_ID=$(curl -fs -H "$H_AUTH" "$KC_URL/admin/realms/$REALM/clients?clientId=teamviz-demo" | python3 -c "import sys,json;print(json.load(sys.stdin)[0]['id'])")

# ---- Create group protocol mapper (adds groups to token + userinfo) ----
echo "=== Creating group membership mapper ==="
curl -fs -X POST -H "$H_AUTH" -H "$H_JSON" \
  "$KC_URL/admin/realms/$REALM/clients/$CLIENT_ID/protocol-mappers/models" -d '{
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

# Also add a preferred_username mapper (for X-Forwarded-User)
echo "=== Creating preferred_username mapper ==="
curl -fs -X POST -H "$H_AUTH" -H "$H_JSON" \
  "$KC_URL/admin/realms/$REALM/clients/$CLIENT_ID/protocol-mappers/models" -d '{
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
echo " Client: teamviz-demo"
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
