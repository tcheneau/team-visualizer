package auth

import (
	"html/template"
	"net/http"
)

// accessDeniedPage carries the data rendered into the access-denied page.
type accessDeniedPage struct {
	Username         string
	ReceivedGroups   []string // groups found in the token (may be empty)
	RecognizedGroups []string // groups configured to grant access
}

// accessDeniedTmpl is rendered when a user authenticates successfully at the
// OIDC provider but their token holds none of the groups that grant access to
// the application (fail-closed role mapping). It is a standalone page: no app
// session is created, and it offers sign-out (ends the provider SSO session
// view) and retry actions.
var accessDeniedTmpl = template.Must(template.New("access-denied").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Access denied &mdash; Team Visualizer</title>
<style>
  :root { color-scheme: light dark; }
  * { box-sizing: border-box; }
  body {
    margin: 0; min-height: 100vh; display: grid; place-items: center;
    font-family: system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
    background: #eef1f6; color: #1d2530; padding: 1.5rem;
  }
  .card {
    max-width: 34rem; width: 100%; background: #ffffff; border-radius: 14px;
    padding: 2.25rem 2.5rem; box-shadow: 0 12px 30px rgba(20, 30, 50, .12);
  }
  .icon { font-size: 1.9rem; line-height: 1; }
  h1 { font-size: 1.35rem; margin: .9rem 0 .55rem; }
  p { margin: .5rem 0; line-height: 1.55; font-size: .95rem; }
  .muted { color: #66707f; }
  .chip {
    display: inline-block; padding: .16rem .6rem; margin: .18rem .25rem .18rem 0;
    border-radius: 999px; background: #e9edf4; font-size: .82rem;
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  }
  code {
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: .88em;
    background: #e9edf3; padding: .08rem .4rem; border-radius: 5px;
  }
  .actions { display: flex; gap: .6rem; margin-top: 1.6rem; }
  .btn {
    display: inline-block; padding: .5rem 1.05rem; border-radius: 8px;
    text-decoration: none; font-size: .92rem; font-weight: 500;
    border: 1px solid transparent;
  }
  .btn-primary { background: #3454d1; color: #fff; }
  .btn-primary:hover { background: #2b46b4; }
  .btn-ghost { border-color: #c7ced9; color: inherit; }
  @media (prefers-color-scheme: dark) {
    body { background: #12161c; color: #e7eaf0; }
    .card { box-shadow: 0 0 0 1px #2a313b, 0 12px 30px rgba(0, 0, 0, .35); }
    .muted { color: #99a3b2; }
    .chip, code { background: #222a35; }
    .btn-ghost { border-color: #4a5563; }
  }
</style>
</head>
<body>
<main class="card">
  <div class="icon">&#9940;</div>
  <h1>Access denied</h1>
  <p>
    You signed in as <strong>{{.Username}}</strong>, but this account is not a
    member of any group that grants access to Team Visualizer. No application
    session was created.
  </p>
  <p class="muted">Access is granted by membership in one of:</p>
  <p>{{range .RecognizedGroups}}<span class="chip">{{.}}</span>{{end}}</p>
  <p class="muted">Groups received in your sign-in token:
    {{if .ReceivedGroups -}}
      {{range .ReceivedGroups}}<code>{{.}}</code> {{end}}
    {{- else -}}
      <em>none received</em> &mdash; the OIDC client may be missing its
      <code>groups</code> mapper.
    {{- end}}
  </p>
  <p class="muted">If you believe you should have access, please contact your
     administrator.</p>
  <div class="actions">
    <a class="btn btn-ghost" href="/auth/logout">Sign out</a>
    <a class="btn btn-primary" href="/auth/login">Try again</a>
  </div>
</main>
</body>
</html>
`))

// renderAccessDenied writes the standalone access-denied page (HTTP 403).
func (a *AuthService) renderAccessDenied(w http.ResponseWriter, username string, receivedGroups []string) {
	page := accessDeniedPage{
		Username:         username,
		ReceivedGroups:   receivedGroups,
		RecognizedGroups: []string{a.cfg.AdminGroup, a.cfg.NormalGroup, a.cfg.ReadonlyGroup},
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_ = accessDeniedTmpl.Execute(w, page)
}
