-- ============================================================
-- group_lookup.lua — LDAP group → app role mapping for Apache
-- ============================================================
--
-- Called by mod_lua's LuaHookFixups after Kerberos authentication
-- has set r.user (REMOTE_USER). Queries LDAP for the user's group
-- memberships, maps them to app group names (tvz-admin, tvz-normal,
-- tvz-readonly), and injects the result as the X-Dev-Groups request
-- header. Also sets X-Dev-User from the authenticated username.
--
-- The script reads its configuration from environment variables
-- set in the Apache config (SetEnv directives):
--
--   TVZ_LDAP_URL       — LDAP server URL (e.g. ldap://dc1.example.com:389)
--   TVZ_LDAP_BIND_DN   — Bind DN for the LDAP search
--   TVZ_LDAP_BIND_PW   — Bind password
--   TVZ_LDAP_BASE_DN   — Base DN for user search
--   TVZ_LDAP_USER_ATTR — Attribute matching the username (default: sAMAccountName)
--   TVZ_LDAP_GROUP_MAP — Pipe-separated mapping: app-group=LDAP-DN|app-group=LDAP-DN|...
--   TVZ_LDAP_CACHE_TTL — Cache TTL in seconds (default: 60)
--
-- A simple in-memory cache avoids calling ldapsearch on every request.
-- The cache is per-worker (each Apache process/worker has its own Lua
-- state), so TTL should be short enough to stay reasonably fresh.
--
-- Requires: ldapsearch (openldap-clients / ldap-utils) on PATH
-- ============================================================

local cache = {}  -- username → { groups = "...", ts = 1234567 }

local function env(name, default)
    local v = os.getenv(name)
    if v == nil or v == "" then
        return default
    end
    return v
end

local function parse_group_map(map_str)
    -- "tvz-admin=CN=tvz-admin,...|tvz-normal=CN=tvz-normal,..." →
    -- { {app="tvz-admin", dn="CN=tvz-admin,..."}, {app="tvz-normal", dn="CN=tvz-normal,..."} }
    local entries = {}
    for entry in (map_str or ""):gmatch("[^|]+") do
        local app, dn = entry:match("^([^=]+)=(.+)$")
        if app and dn then
            table.insert(entries, { app = app:match("^%s*(.-)%s*$"), dn = dn:match("^%s*(.-)%s*$") })
        end
    end
    return entries
end

local function ldap_groups(user, cfg)
    -- Build and run ldapsearch. We search for the user object and
    -- request its memberOf attribute, then parse the output.
    local cmd = string.format(
        'ldapsearch -x -LLL -H %s -D "%s" -w "%s" -b "%s" "(%s=%s)" memberOf 2>/dev/null',
        cfg.url, cfg.bind_dn, cfg.bind_pw, cfg.base_dn, cfg.user_attr, user
    )

    local handle = io.popen(cmd)
    if not handle then return nil end
    local output = handle:read("*a")
    handle:close()

    if not output or output == "" then
        return nil
    end

    -- Collect all memberOf DNs from the output
    local dns = {}
    for line in output:gmatch("memberOf:%s*(%S[^\r\n]*)") do
        table.insert(dns, line)
    end

    return dns
end

local function map_groups(dns, group_map)
    -- Map LDAP group DNs to app group names using the group_map.
    -- Returns a comma-separated string of app group names.
    -- Admin takes priority (matches the app's role hierarchy).
    local result = {}
    local seen = {}
    for _, entry in ipairs(group_map) do
        for _, dn in ipairs(dns) do
            -- Case-insensitive DN comparison
            if dn:lower() == entry.dn:lower() then
                if not seen[entry.app] then
                    table.insert(result, entry.app)
                    seen[entry.app] = true
                end
                break
            end
        end
    end

    if #result == 0 then
        return nil
    end
    table.sort(result)  -- deterministic order
    return table.concat(result, ",")
end

function fixup(r)
    -- Get the authenticated username (set by mod_auth_gssapi)
    local user = r.user
    if not user or user == "" then
        return 0  -- DECLINED — no authenticated user, let proxy handle it
    end

    -- Read config from environment
    local cfg = {
        url       = env("TVZ_LDAP_URL", "ldap://localhost:389"),
        bind_dn   = env("TVZ_LDAP_BIND_DN", ""),
        bind_pw   = env("TVZ_LDAP_BIND_PW", ""),
        base_dn   = env("TVZ_LDAP_BASE_DN", ""),
        user_attr = env("TVZ_LDAP_USER_ATTR", "sAMAccountName"),
        cache_ttl = tonumber(env("TVZ_LDAP_CACHE_TTL", "60")) or 60,
    }
    local group_map = parse_group_map(env("TVZ_LDAP_GROUP_MAP", ""))

    -- Always set X-Dev-User from the authenticated username
    r.headers_in["X-Dev-User"] = user

    -- If no group map is configured, set empty groups (readonly role)
    if #group_map == 0 then
        r.headers_in["X-Dev-Groups"] = ""
        return 0
    end

    -- Check cache
    local now = os.time()
    local cached = cache[user]
    if cached and (now - cached.ts) < cfg.cache_ttl then
        r.headers_in["X-Dev-Groups"] = cached.groups
        return 0
    end

    -- Query LDAP
    local dns = ldap_groups(user, cfg)
    if not dns or #dns == 0 then
        -- LDAP lookup failed or user has no groups — give readonly
        r.headers_in["X-Dev-Groups"] = ""
        cache[user] = { groups = "", ts = now }
        return 0
    end

    -- Map DNs to app group names
    local groups_str = map_groups(dns, group_map) or ""
    r.headers_in["X-Dev-Groups"] = groups_str
    cache[user] = { groups = groups_str, ts = now }

    return 0  -- DECLINED — let other handlers (proxy) continue
end