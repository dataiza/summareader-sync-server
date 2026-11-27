package main

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
)

// callPairing drives one of the pairing handlers the way callDevices drives
// /devices. A handler that refuses returns its ApiError rather than writing
// it, so that comes back too.
func callPairing(t *testing.T, app core.App, handler func(*core.RequestEvent) error,
	method, host, token, body string) (*httptest.ResponseRecorder, error) {
	t.Helper()
	request := httptest.NewRequest(method, "/operator/pairing", strings.NewReader(body))
	request.Host = host
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.TLS = &tls.ConnectionState{}
	recorder := httptest.NewRecorder()
	event := &core.RequestEvent{}
	event.App = app
	event.Request = request
	event.Response = recorder
	return recorder, handler(event)
}

// newTestApp comes with a library already; the pairing tests start before one.
func emptyTestApp(t *testing.T) core.App {
	t.Helper()
	app, account := newTestApp(t)
	record, err := app.FindRecordById(collAccounts, account)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Delete(record); err != nil {
		t.Fatal(err)
	}
	return app
}

func apiStatus(err error) int {
	var apiErr *router.ApiError
	if errors.As(err, &apiErr) {
		return apiErr.Status
	}
	return 0
}

func pairingCode(t *testing.T, app core.App, label string) pairingResponse {
	t.Helper()
	recorder, err := callPairing(t, app, handlePairingCode, http.MethodPost,
		"127.0.0.1:8099", "op", `{"label":"`+label+`"}`)
	if err != nil || recorder.Code != http.StatusOK {
		t.Fatalf("pairing: %v %d %s", err, recorder.Code, recorder.Body)
	}
	var answer pairingResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &answer); err != nil {
		t.Fatal(err)
	}
	return answer
}

func TestPairPageIsOffWithoutAToken(t *testing.T) {
	app := emptyTestApp(t)
	t.Setenv("SUMMAREADER_OPERATOR_TOKEN", "")
	_, err := callPairing(t, app, handlePairPage, http.MethodGet, "h", "", "")
	if apiStatus(err) != http.StatusNotFound {
		t.Fatalf("got %v, want a 404", err)
	}
}

func TestPairPageIsServedWithAToken(t *testing.T) {
	app := emptyTestApp(t)
	t.Setenv("SUMMAREADER_OPERATOR_TOKEN", "op")
	recorder, err := callPairing(t, app, handlePairPage, http.MethodGet, "h", "", "")
	if err != nil || !strings.Contains(recorder.Body.String(), "<html") {
		t.Fatalf("page not served: %v", err)
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("the page may be cached")
	}
	want := "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"
	if got := recorder.Header().Get("Content-Security-Policy"); got != want {
		t.Fatalf("CSP = %q", got)
	}
}

// The operator token must never cross plain HTTP (--insecure-http).
func TestPairingRoutesRefusePlainHTTP(t *testing.T) {
	app := emptyTestApp(t)
	t.Setenv("SUMMAREADER_OPERATOR_TOKEN", "op")
	for _, h := range []func(*core.RequestEvent) error{handlePairPage, handlePairingState, handlePairingCode} {
		event := &core.RequestEvent{App: app}
		event.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}"))
		event.Request.Header.Set("Authorization", "Bearer op")
		event.Response = httptest.NewRecorder()
		if apiStatus(h(event)) != http.StatusNotFound {
			t.Fatal("plain HTTP was served")
		}
	}
}

// Review Focus 1: the page turns this 401 into "Wrong password".
func TestPairingWantsThePassword(t *testing.T) {
	app := emptyTestApp(t)
	t.Setenv("SUMMAREADER_OPERATOR_TOKEN", "op")
	_, err := callPairing(t, app, handlePairingState, http.MethodGet, "h", "wrong", "")
	if apiStatus(err) != http.StatusUnauthorized {
		t.Fatalf("got %v, want a 401", err)
	}
	_, err = callPairing(t, app, handlePairingCode, http.MethodPost, "h", "wrong", `{}`)
	if apiStatus(err) != http.StatusUnauthorized {
		t.Fatalf("POST got %v, want a 401", err)
	}
}

func TestPairingStateCountsLibraries(t *testing.T) {
	app := emptyTestApp(t)
	t.Setenv("SUMMAREADER_OPERATOR_TOKEN", "op")
	_, fingerprint, err := loadOrCreateCertificate(app.DataDir())
	if err != nil {
		t.Fatal(err)
	}
	recorder, err := callPairing(t, app, handlePairingState, http.MethodGet, "h", "op", "")
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Libraries   int    `json:"libraries"`
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Libraries != 0 || state.Fingerprint != fingerprint {
		t.Fatalf("state is %+v", state)
	}
}

// Review Focus 2: a second press after the first enrolls, never creates.
func TestFirstPairingCreatesTheLibraryThenEnrolls(t *testing.T) {
	app := emptyTestApp(t)
	t.Setenv("SUMMAREADER_OPERATOR_TOKEN", "op")

	first := pairingCode(t, app, "Phone")
	if !first.First {
		t.Fatal("the first pairing did not say it made the library")
	}
	second := pairingCode(t, app, "Tablet")
	if second.First {
		t.Fatal("the second pairing made another library")
	}

	total, err := app.CountRecords(collAccounts)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("%d libraries", total)
	}

	var payload struct {
		DeviceToken string `json:"device_token"`
	}
	if err := json.Unmarshal([]byte(second.Payload), &payload); err != nil {
		t.Fatal(err)
	}
	account, _, err := accountForToken(app, payload.DeviceToken)
	if err != nil {
		t.Fatalf("the issued token does not resolve: %v", err)
	}
	devices, err := listDevices(app, account)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 2 {
		t.Fatalf("%d devices", len(devices))
	}
	if !strings.Contains(second.QRSVG, "<svg") {
		t.Fatal("no QR drawn")
	}
}

func TestPairingRefusesWithTwoLibraries(t *testing.T) {
	app := emptyTestApp(t)
	t.Setenv("SUMMAREADER_OPERATOR_TOKEN", "op")
	for _, name := range []string{"One", "Two"} {
		if _, err := createAccount(app, name, "Desktop"); err != nil {
			t.Fatal(err)
		}
	}
	recorder, err := callPairing(t, app, handlePairingCode, http.MethodPost, "h", "op", `{}`)
	if err != nil || recorder.Code != http.StatusConflict {
		t.Fatalf("got %v %d, want a 409", err, recorder.Code)
	}
}

func TestPairingPayloadMatchesTheConsole(t *testing.T) {
	payload := pairingPayload("https://h:1", "t", "ab")
	if !strings.HasPrefix(payload, `{"version":2,"server":`) {
		t.Fatalf("not in the console's order: %s", payload)
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(payload), &fields); err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	want := []string{"device_token", "server", "server_fingerprint", "version"}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("keys are %v", keys)
	}
	if fields["version"] != float64(2) || fields["server"] != "https://h:1" ||
		fields["device_token"] != "t" || fields["server_fingerprint"] != "ab" {
		t.Fatalf("payload is %v", fields)
	}
}

// Review Focus 3 and 4.
func TestPairingServerURL(t *testing.T) {
	saved := publicURL
	t.Cleanup(func() { publicURL = saved })
	event := &core.RequestEvent{}
	event.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	event.Request.Host = "10.39.20.12:8099"

	publicURL = "https://x:8099/"
	if got := pairingServerURL(event); got != "https://x:8099" {
		t.Fatalf("got %q", got)
	}
	publicURL = "https://x/sync/"
	if got := pairingServerURL(event); got != "https://x/sync" {
		t.Fatalf("got %q, the path was lost", got)
	}
	publicURL = ""
	if got := pairingServerURL(event); got != "https://10.39.20.12:8099" {
		t.Fatalf("got %q", got)
	}
}

func TestPairingIsNotCached(t *testing.T) {
	app := emptyTestApp(t)
	t.Setenv("SUMMAREADER_OPERATOR_TOKEN", "op")
	recorder, err := callPairing(t, app, handlePairingCode, http.MethodPost, "h", "op", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("a live token may be cached")
	}
	recorder, err = callPairing(t, app, handlePairingState, http.MethodGet, "h", "op", "")
	if err != nil || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("the state may be cached")
	}
}

// Review Focus 2, as it happens: two first presses at once.
func TestTwoFirstPressesMakeOneLibrary(t *testing.T) {
	app := emptyTestApp(t)
	t.Setenv("SUMMAREADER_OPERATOR_TOKEN", "op")
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			recorder, err := callPairing(t, app, handlePairingCode, http.MethodPost, "h", "op", `{}`)
			if err == nil {
				codes[i] = recorder.Code
			}
		}()
	}
	wg.Wait()
	// Both answered, so neither press lost to a busy database.
	if codes[0] != http.StatusOK || codes[1] != http.StatusOK {
		t.Fatalf("answers were %v", codes)
	}
	accounts := []*core.Record{}
	if err := app.RecordQuery(collAccounts).All(&accounts); err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 {
		t.Fatalf("%d libraries", len(accounts))
	}
	devices, err := listDevices(app, accounts[0].Id)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 2 {
		t.Fatalf("%d devices", len(devices))
	}
}
