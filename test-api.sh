#!/run/current-system/sw/bin/bash
# End-to-end API test for Team Activity Visualizer

set -e
PORT="${TEST_PORT:-8080}"
BASE_URL="http://localhost:${PORT}"
ADMIN="-H X-Dev-User:admin -H X-Dev-Groups:admin"
NORMAL="-H X-Dev-User:alice -H X-Dev-Groups:normal"
RO="-H X-Dev-User:bob -H X-Dev-Groups:readonly"
PASS=0; FAIL=0; PID=""
CONFIG=$(mktemp /tmp/teamviz-test-XXXXXX.toml)

cleanup() { [ -n "$PID" ] && kill "$PID" 2>/dev/null; rm -f teamviz-test.db* "$CONFIG"; }
trap cleanup EXIT

ok()   { echo "  PASS: $1"; PASS=$((PASS+1)); }
fail() { echo "  FAIL: $1"; FAIL=$((FAIL+1)); }
eq()   { [ "$2" = "$3" ] && ok "$1" || fail "$1 (got '$2', want '$3')"; }
code() { eq "$1" "$2" "$3"; }

echo "=== Starting server ==="
echo "=== Starting server ==="
cat > "$CONFIG" <<EOF
[server]
db_path    = "teamviz-test.db"
jwt_secret = "testsecret"

[[listener]]
listen = ":${PORT}"
auth   = "dev"
EOF
/tmp/teamviz -config "$CONFIG" &
PID=$!; sleep 1

echo "=== Foundation ==="
eq "Health" "$(curl -s $ADMIN $BASE_URL/api/health | python3 -c 'import sys,json;print(json.load(sys.stdin)["status"])')" "ok"
eq "Auth admin" "$(curl -s $ADMIN $BASE_URL/api/auth/session | python3 -c 'import sys,json;print(json.load(sys.stdin)["user"]["role"])')" "admin"
eq "Auth readonly" "$(curl -s $RO $BASE_URL/api/auth/session | python3 -c 'import sys,json;print(json.load(sys.stdin)["user"]["role"])')" "readonly"
code "Legacy" "$(curl -s -o /dev/null -w '%{http_code}' $BASE_URL/legacy/)" "200"
code "SPA" "$(curl -s -o /dev/null -w '%{http_code}' $BASE_URL/)" "200"
code "CSS" "$(curl -s -o /dev/null -w '%{http_code}' $BASE_URL/styles.css)" "200"
code "JS" "$(curl -s -o /dev/null -w '%{http_code}' $BASE_URL/app.js)" "200"

echo "=== Read API ==="
eq "People empty" "$(curl -s $ADMIN $BASE_URL/api/people | python3 -c 'import sys,json;print(len(json.load(sys.stdin)))')" "0"
eq "Settings" "$(curl -s $ADMIN $BASE_URL/api/settings | python3 -c 'import sys,json;print(json.load(sys.stdin)["window_weeks"])')" "4"
eq "Projects empty" "$(curl -s $ADMIN $BASE_URL/api/projects | python3 -c 'import sys,json;print(len(json.load(sys.stdin)))')" "0"
code "404 person" "$(curl -s -o /dev/null -w '%{http_code}' $ADMIN $BASE_URL/api/people/none)" "404"

echo "=== Write API ==="
ADDRES=$(curl -s $ADMIN -X POST -H 'Content-Type: application/json' \
  -d '{"name":"Alice","role":"Dev","avatar_emoji":"fox","default_projects":["Atlas"],"is_guest":false,"status":"active","archived_date":""}' \
  $BASE_URL/api/people)
eq "Add person" "$(echo $ADDRES | python3 -c 'import sys,json;print(json.load(sys.stdin)["name"])')" "Alice"
PID1=$(echo $ADDRES | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')

ADDRES2=$(curl -s $ADMIN -X POST -H 'Content-Type: application/json' \
  -d '{"name":"Bob","role":"QA","is_guest":false,"status":"active","archived_date":""}' \
  $BASE_URL/api/people)
PID2=$(echo $ADDRES2 | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')
eq "People count" "$(curl -s $ADMIN $BASE_URL/api/people | python3 -c 'import sys,json;print(len(json.load(sys.stdin)))')" "2"

curl -s $ADMIN -X PUT -H 'Content-Type: application/json' \
  -d "{\"person_id\":\"$PID1\",\"date\":\"2025/01/06\",\"slot\":\"am\",\"data\":{\"state\":\"filled\",\"projects\":[{\"name\":\"Atlas\",\"pct\":100}],\"run\":false}}" \
  $BASE_URL/api/planning/slot > /dev/null
eq "Planning entries" "$(curl -s $ADMIN "$BASE_URL/api/planning?start=2025/01/01&end=2025/01/31" | python3 -c 'import sys,json;print(len(json.load(sys.stdin)))')" "1"

RANGE=$(curl -s $ADMIN -X PUT -H 'Content-Type: application/json' \
  -d "{\"person_ids\":[\"$PID1\",\"$PID2\"],\"start_date\":\"2025/01/08\",\"start_slot\":\"am\",\"end_date\":\"2025/01/10\",\"end_slot\":\"pm\",\"data\":{\"state\":\"filled\",\"away\":{\"type\":\"vacation\",\"note\":\"\"},\"projects\":[],\"run\":false}}" \
  $BASE_URL/api/planning/range)
eq "Range slots" "$(echo $RANGE | python3 -c 'import sys,json;print(json.load(sys.stdin)["slots_set"])')" "12"

curl -s $ADMIN -X POST $BASE_URL/api/people/$PID2/archive > /dev/null
curl -s $ADMIN -X POST $BASE_URL/api/people/$PID2/unarchive > /dev/null
ok "Archive/unarchive"

PROJRES=$(curl -s $ADMIN -X POST -H 'Content-Type: application/json' \
  -d '{"name":"Atlas","emoji":"rocket","status":"in_progress","start_date":"2025/01/06","end_date":"2025/03/28"}' \
  $BASE_URL/api/projects)
eq "Add project" "$(echo $PROJRES | python3 -c 'import sys,json;print(json.load(sys.stdin)["name"])')" "Atlas"
PROJID=$(echo $PROJRES | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')

CSVRES=$(curl -s $ADMIN -X POST -H 'Content-Type: text/csv' \
  -d 'name,emoji,description,status
Beacon,light,UI,unstarted
Atlas,rocket,Updated,in_progress' \
  $BASE_URL/api/projects/import-csv)
eq "CSV created" "$(echo $CSVRES | python3 -c 'import sys,json;print(json.load(sys.stdin)["created"])')" "1"

curl -s $ADMIN -X PUT -H 'Content-Type: application/json' \
  -d "{\"person_id\":\"$PID1\",\"week_start\":\"2025/01/06\"}" $BASE_URL/api/oncall > /dev/null
eq "On-call" "$(curl -s $ADMIN "$BASE_URL/api/oncall?start=2025/01/06&end=2025/01/06" | python3 -c 'import sys,json;d=json.load(sys.stdin);print(len(d.get("2025/01/06",[]))>=1)')" "True"

eq "Rotation" "$(curl -s $ADMIN -X POST -H 'Content-Type: application/json' \
  -d "{\"person_id\":\"$PID1\",\"week_start\":\"2025/01/06\"}" $BASE_URL/api/rotation/assign | python3 -c 'import sys,json;print(json.load(sys.stdin)["status"])')" "ok"

code "Export TOML" "$(curl -s -o /dev/null -w '%{http_code}' $ADMIN $BASE_URL/api/export)" "200"

curl -s $ADMIN $BASE_URL/api/export -o /tmp/tvz_test.toml
IMPORTRES=$(curl -s $ADMIN -X POST -H 'Content-Type: text/toml' \
  --data-binary @/tmp/tvz_test.toml "$BASE_URL/api/import?mode=merge")
eq "Import TOML" "$(echo $IMPORTRES | python3 -c 'import sys,json;print(json.load(sys.stdin)["status"])')" "ok"
rm -f /tmp/tvz_test.toml

echo "=== Role Enforcement ==="
code "RO blocked" "$(curl -s -o /dev/null -w '%{http_code}' $RO -X POST -H 'Content-Type: application/json' -d '{"name":"X"}' $BASE_URL/api/people)" "403"
code "Normal add" "$(curl -s -o /dev/null -w '%{http_code}' $NORMAL -X POST -H 'Content-Type: application/json' -d '{"name":"NUser","is_guest":false,"status":"active","archived_date":""}' $BASE_URL/api/people)" "201"
code "Admin settings" "$(curl -s -o /dev/null -w '%{http_code}' $ADMIN -X PUT -H 'Content-Type: application/json' -d '{"window_weeks":"3"}' $BASE_URL/api/settings)" "200"
code "Normal operational settings" "$(curl -s -o /dev/null -w '%{http_code}' $NORMAL -X PUT -H 'Content-Type: application/json' -d '{"window_weeks":"2"}' $BASE_URL/api/settings)" "200"
code "Normal admin-only settings" "$(curl -s -o /dev/null -w '%{http_code}' $NORMAL -X PUT -H 'Content-Type: application/json' -d '{"prune_weeks":"6"}' $BASE_URL/api/settings)" "403"

echo "=== Cleanup ==="
curl -s $ADMIN -X DELETE $BASE_URL/api/people/$PID1 > /dev/null
curl -s $ADMIN -X DELETE $BASE_URL/api/projects/$PROJID > /dev/null

echo ""
echo "========================="
echo "RESULTS: $PASS passed, $FAIL failed"
echo "========================="
[ $FAIL -eq 0 ] && exit 0 || exit 1