package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

// The database framework's own API and dashboard, as somebody on the network
// sees them. Card 712.
//
// Driven through PocketBase's real router rather than a handler, because the
// thing under test is which of its routes answer and to whom.

const (
	fromLAN      = "192.0.2.10:40000"
	fromThisHost = "127.0.0.1:40000"
)

// builtinsMux is the router `serve` builds, with this server's routes on it.
//
// The dashboard is registered by apis.Serve and not by NewRouter, so a
// stand-in takes its path here: what is being tested is who reaches it, not
// what it draws.
func builtinsMux(t *testing.T, app core.App) http.Handler {
	t.Helper()
	router, err := apis.NewRouter(app)
	if err != nil {
		t.Fatal(err)
	}
	router.GET("/_/{path...}", func(e *core.RequestEvent) error {
		return e.String(http.StatusOK, "dashboard")
	})
	registerRoutes(&core.ServeEvent{App: app, Router: router})
	mux, err := router.BuildMux()
	if err != nil {
		t.Fatal(err)
	}
	return mux
}

func ask(mux http.Handler, method, path, remote, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = remote
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	return recorder
}

const signup = `{"email":"someone@example.com","password":"1234567890","passwordConfirm":"1234567890"}`

func TestTheNetworkCannotSignUp(t *testing.T) {
	app, _ := newTestApp(t)
	mux := builtinsMux(t, app)

	if code := ask(mux, http.MethodPost, "/api/collections/users/records", fromLAN, signup).Code; code != http.StatusForbidden {
		t.Fatalf("a signup from the network answered %d, want 403", code)
	}
	// Refused on this machine too, by the collection's own rule rather than by
	// the address: nothing here uses PocketBase's users at all.
	if code := ask(mux, http.MethodPost, "/api/collections/users/records", fromThisHost, signup).Code; code < 400 {
		t.Fatalf("a signup from this machine answered %d, want a refusal", code)
	}
	if n, _ := app.CountRecords("users"); n != 0 {
		t.Fatalf("%d users were created", n)
	}
}

func TestTheDashboardIsOnlyForThisMachine(t *testing.T) {
	app, _ := newTestApp(t)
	mux := builtinsMux(t, app)

	if code := ask(mux, http.MethodGet, "/_/", fromLAN, "").Code; code != http.StatusForbidden {
		t.Fatalf("the dashboard answered the network with %d, want 403", code)
	}
	if code := ask(mux, http.MethodGet, "/api/health", fromLAN, "").Code; code != http.StatusForbidden {
		t.Fatalf("the database API answered the network with %d, want 403", code)
	}
	if code := ask(mux, http.MethodGet, "/_/", fromThisHost, "").Code; code != http.StatusOK {
		t.Fatalf("the dashboard answered this machine with %d, want 200", code)
	}
	if code := ask(mux, http.MethodGet, "/_/", "[::1]:40000", "").Code; code != http.StatusOK {
		t.Fatalf("the dashboard answered ::1 with %d, want 200", code)
	}
}

// A reverse proxy on the same machine makes every request look local. The
// header it adds is what gives the visitor away.
func TestAProxiedRequestIsNotLocal(t *testing.T) {
	app, _ := newTestApp(t)
	mux := builtinsMux(t, app)

	for _, header := range []string{"X-Forwarded-For", "Forwarded", "X-Real-Ip"} {
		request := httptest.NewRequest(http.MethodGet, "/_/", nil)
		request.RemoteAddr = fromThisHost
		request.Header.Set(header, "198.51.100.7")
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusForbidden {
			t.Errorf("a request carrying %s answered %d, want 403", header, recorder.Code)
		}
	}
}

// A server bound to one LAN address does not listen on loopback, so a browser
// on the same machine arrives from that address. Its own address on both ends
// of the connection is only possible from here.
func TestThisMachineOnItsOwnLANAddressIsLocal(t *testing.T) {
	app, _ := newTestApp(t)
	mux := builtinsMux(t, app)

	request := httptest.NewRequest(http.MethodGet, "/_/", nil)
	request.RemoteAddr = "192.0.2.5:40000"
	request = request.WithContext(context.WithValue(request.Context(),
		http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("192.0.2.5"), Port: 8099}))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("this machine on its own LAN address answered %d, want 200", recorder.Code)
	}
}

func TestTheAppsOwnRoutesStillAnswerTheNetwork(t *testing.T) {
	app, _ := newTestApp(t)
	mux := builtinsMux(t, app)

	if code := ask(mux, http.MethodGet, "/instance", fromLAN, "").Code; code != http.StatusOK {
		t.Fatalf("/instance answered the network with %d, want 200", code)
	}
	// Refused for the token, not for the address.
	if code := ask(mux, http.MethodGet, "/devices", fromLAN, "").Code; code != http.StatusUnauthorized {
		t.Fatalf("/devices answered the network with %d, want 401", code)
	}
}

func TestAdministratorLoginIsRateLimited(t *testing.T) {
	app, _ := newTestApp(t)
	mux := builtinsMux(t, app)

	login := `{"identity":"nobody@example.com","password":"wrong-password"}`
	limited := false
	for i := 0; i < 20 && !limited; i++ {
		code := ask(mux, http.MethodPost, "/api/collections/_superusers/auth-with-password", fromThisHost, login).Code
		limited = code == http.StatusTooManyRequests
	}
	if !limited {
		t.Fatal("twenty wrong passwords in a row and none was refused for the rate")
	}
}

// An operator who changed the limits in the dashboard keeps their other rules;
// the one for logging in is put back.
func TestTheLockdownIsIdempotent(t *testing.T) {
	app, _ := newTestApp(t)
	if err := ensureSchema(app); err != nil {
		t.Fatal(err)
	}
	auth := 0
	for _, rule := range app.Settings().RateLimits.Rules {
		if rule.Label == authRateLimit.Label {
			auth++
		}
	}
	if auth != 1 {
		t.Fatalf("%d rules for %s after two boots, want 1", auth, authRateLimit.Label)
	}
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal(err)
	}
	if users.CreateRule != nil || users.ListRule != nil || users.ViewRule != nil {
		t.Fatal("the users collection is still open")
	}
}
