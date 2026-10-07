package main

import (
	"net"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
)

// The database framework's own front door. Card 712.
//
// PocketBase serves its API under /api/ and its dashboard under /_/ beside the
// routes in registerRoutes, and out of the box both answer anybody: a public
// signup on the users collection, an administrator login with no limit on
// guesses, and CORS open to every web page. None of that is used by a device.
// The dashboard is still the real administration screen, so it is kept — for
// this machine, which is where somebody running the server already is, or
// at the end of an SSH tunnel.

// What a login to the dashboard may cost a guesser: five tries a minute per
// address. Enough for an administrator who mistyped, and a lifetime for a
// dictionary.
var authRateLimit = core.RateLimitRule{Label: "*:auth", MaxRequests: 5, Duration: 60}

// lockDownBuiltins closes what PocketBase leaves open, on every boot.
//
// Every boot rather than once, because the settings live in the database and
// the dashboard can change them: a limit switched off by accident should not
// stay off. Only the rule for logging in is put back — any other limit an
// operator has added is theirs.
func lockDownBuiltins(app core.App) error {
	// The users collection PocketBase creates for itself. Nothing here signs
	// anybody up — a device is a row in devices with a token, see provision.go
	// — so its rules are all nil, which means superusers only. Left at the
	// default, anyone who could reach the server could create accounts until
	// the disk was full. Tolerated if somebody has deleted it.
	if users, err := app.FindCollectionByNameOrId("users"); err == nil {
		if users.ListRule != nil || users.ViewRule != nil || users.CreateRule != nil ||
			users.UpdateRule != nil || users.DeleteRule != nil {
			users.ListRule, users.ViewRule, users.CreateRule = nil, nil, nil
			users.UpdateRule, users.DeleteRule = nil, nil
			if err := app.Save(users); err != nil {
				return err
			}
		}
	}

	settings := app.Settings()
	limits := &settings.RateLimits
	changed := !limits.Enabled
	limits.Enabled = true
	found := false
	for i, rule := range limits.Rules {
		if rule.Label != authRateLimit.Label {
			continue
		}
		found = true
		if rule != authRateLimit {
			limits.Rules[i] = authRateLimit
			changed = true
		}
	}
	if !found {
		// First, though the order matters less than it looks: PocketBase
		// matches a collection's own labels, then the wildcard ones, then
		// paths, whatever order the list is in.
		limits.Rules = append([]core.RateLimitRule{authRateLimit}, limits.Rules...)
		changed = true
	}
	if !changed {
		return nil
	}
	return app.Save(settings)
}

// bindLocalOnly puts the dashboard and the database API behind fromThisMachine.
//
// Bound on the whole router because PocketBase's routes are registered there
// too, and early — before the rate limiter and before the auth token is read —
// so a guess from the network is turned away without being counted, checked
// or logged in to.
func bindLocalOnly(e *core.ServeEvent) {
	e.Router.Bind(&hook.Handler[*core.RequestEvent]{
		Id:       "summareaderLocalOnly",
		Priority: apis.DefaultPanicRecoverMiddlewarePriority + 1,
		Func: func(re *core.RequestEvent) error {
			path := re.Request.URL.Path
			builtin := path == "/api" || strings.HasPrefix(path, "/api/") ||
				path == "/_" || strings.HasPrefix(path, "/_/")
			if builtin && !fromThisMachine(re.Request) {
				return re.ForbiddenError("The dashboard and the database API answer "+
					"only on the machine the server runs on. Open it there, or "+
					"through an SSH tunnel.", nil)
			}
			return re.Next()
		},
	})
}

// fromThisMachine says whether the connection itself came from this host.
//
// The peer's address, never RealIP: that one believes headers, and a header is
// whatever the client wrote. A loopback peer, or a peer whose address is the
// one it connected to — which over TCP can only be this machine, since the
// reply to anybody pretending would never leave it. The second is a browser
// here talking to a server bound to one LAN address, which has no loopback.
//
// A request carrying a forwarding header is not local, whatever its peer: that
// is a reverse proxy on this machine relaying somebody else, and without this
// putting one in front of the server would open the dashboard to everybody it
// serves.
func fromThisMachine(r *http.Request) bool {
	for _, header := range []string{"X-Forwarded-For", "Forwarded", "X-Real-Ip"} {
		if r.Header.Get(header) != "" {
			return false
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	peer := net.ParseIP(host)
	if peer == nil {
		return false
	}
	if peer.IsLoopback() {
		return true
	}
	local, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok {
		return false
	}
	tcp, ok := local.(*net.TCPAddr)
	return ok && tcp.IP.Equal(peer)
}
