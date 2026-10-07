package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Card 715: the devices on an account hear about a new device, a new recovery
// code and a wipe, whichever of them did it.

func askAs(mux http.Handler, token, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = fromLAN
	request.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	return recorder
}

type eventsAnswer struct {
	Events []AccountEvent `json:"events"`
	Self   string         `json:"self"`
}

func eventsFor(t *testing.T, mux http.Handler, token, since string) eventsAnswer {
	t.Helper()
	path := "/events"
	if since != "" {
		path += "?since=" + since
	}
	res := askAs(mux, token, http.MethodGet, path, "")
	if res.Code != http.StatusOK {
		t.Fatalf("/events answered %d: %s", res.Code, res.Body.String())
	}
	var out eventsAnswer
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestEnrollingWritesANotice(t *testing.T) {
	app, _ := newTestApp(t)
	desktop, err := createAccount(app, "Mine", "Desktop")
	if err != nil {
		t.Fatal(err)
	}
	ipad, err := enrollDevice(app, desktop.AccountID, "iPad")
	if err != nil {
		t.Fatal(err)
	}
	mux := builtinsMux(t, app)

	res := askAs(mux, desktop.Token, http.MethodPost, "/enroll", `{"label":"Stranger"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("/enroll answered %d", res.Code)
	}
	var minted Device
	_ = json.Unmarshal(res.Body.Bytes(), &minted)

	heard := eventsFor(t, mux, ipad.Token, "")
	if len(heard.Events) != 1 {
		t.Fatalf("the iPad heard %d events, want 1", len(heard.Events))
	}
	event := heard.Events[0]
	if event.Kind != eventEnrolled || event.Device != minted.DeviceID ||
		event.Label != "Stranger" || event.Actor != desktop.DeviceID {
		t.Fatalf("heard %+v", event)
	}
	if heard.Self != ipad.DeviceID {
		t.Fatalf("self is %q", heard.Self)
	}

	// Nothing again once it has been seen.
	if again := eventsFor(t, mux, ipad.Token, event.At); len(again.Events) != 0 {
		t.Fatalf("an event already seen came back: %+v", again.Events)
	}
}

func TestAJoinWritesANotice(t *testing.T) {
	app, _ := newTestApp(t)
	desktop, err := createAccount(app, "Mine", "Desktop")
	if err != nil {
		t.Fatal(err)
	}
	key := joinKey(t)
	if err := setJoinVerifier(app, desktop.AccountID, signedVerifier(key)); err != nil {
		t.Fatal(err)
	}
	mux := builtinsMux(t, app)
	if res := ask(mux, http.MethodPost, "/join", fromLAN, signedJoinBody(key, fetchNonce(t, mux))); res.Code != http.StatusOK {
		t.Fatalf("join answered %d", res.Code)
	}
	heard := eventsFor(t, mux, desktop.Token, "")
	if len(heard.Events) != 1 || heard.Events[0].Kind != eventJoined ||
		heard.Events[0].Label != "Restored phone" {
		t.Fatalf("heard %+v", heard.Events)
	}
}

func TestANewRecoveryCodeWritesANotice(t *testing.T) {
	app, _ := newTestApp(t)
	desktop, err := createAccount(app, "Mine", "Desktop")
	if err != nil {
		t.Fatal(err)
	}
	mux := builtinsMux(t, app)
	if res := askAs(mux, desktop.Token, http.MethodPost, "/recovery", `{"verifier":"x"}`); res.Code != http.StatusOK {
		t.Fatalf("/recovery answered %d", res.Code)
	}
	heard := eventsFor(t, mux, desktop.Token, "")
	if len(heard.Events) != 1 || heard.Events[0].Kind != eventRecovery ||
		heard.Events[0].Device != desktop.DeviceID || heard.Events[0].Label != "Desktop" {
		t.Fatalf("heard %+v", heard.Events)
	}
}

// The notice outlives the wipe it reports, which deletes everything else.
func TestAWipeWritesANoticeThatSurvivesIt(t *testing.T) {
	app, _ := newTestApp(t)
	desktop, err := createAccount(app, "Mine", "Desktop")
	if err != nil {
		t.Fatal(err)
	}
	mux := builtinsMux(t, app)
	if res := askAs(mux, desktop.Token, http.MethodPost, "/wipe", `{"device_name":"whatever it likes"}`); res.Code != http.StatusOK {
		t.Fatalf("/wipe answered %d: %s", res.Code, res.Body.String())
	}
	heard := eventsFor(t, mux, desktop.Token, "")
	// The label from the device's own row, not the name it sent: a stolen
	// token could call itself anything.
	if len(heard.Events) != 1 || heard.Events[0].Kind != eventWiped ||
		heard.Events[0].Label != "Desktop" {
		t.Fatalf("heard %+v", heard.Events)
	}
}

func TestEventsAreScopedToTheAccount(t *testing.T) {
	app, _ := newTestApp(t)
	mine, err := createAccount(app, "Mine", "Desktop")
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := createAccount(app, "Theirs", "Laptop")
	if err != nil {
		t.Fatal(err)
	}
	mux := builtinsMux(t, app)
	if res := askAs(mux, theirs.Token, http.MethodPost, "/enroll", `{"label":"Theirs too"}`); res.Code != http.StatusOK {
		t.Fatalf("/enroll answered %d", res.Code)
	}
	if heard := eventsFor(t, mux, mine.Token, ""); len(heard.Events) != 0 {
		t.Fatalf("another account's enrolment was heard: %+v", heard.Events)
	}
}

func TestEnrollingIsRateLimitedPerAccount(t *testing.T) {
	app, _ := newTestApp(t)
	desktop, err := createAccount(app, "Mine", "Desktop")
	if err != nil {
		t.Fatal(err)
	}
	other, err := createAccount(app, "Theirs", "Laptop")
	if err != nil {
		t.Fatal(err)
	}
	saved := enrolLimit
	enrolLimit = newRateLimit(3, time.Hour)
	t.Cleanup(func() { enrolLimit = saved })
	mux := builtinsMux(t, app)

	for i := 0; i < 3; i++ {
		if res := askAs(mux, desktop.Token, http.MethodPost, "/enroll", `{}`); res.Code != http.StatusOK {
			t.Fatalf("enrolment %d answered %d", i+1, res.Code)
		}
	}
	res := askAs(mux, desktop.Token, http.MethodPost, "/enroll", `{}`)
	if res.Code != http.StatusTooManyRequests {
		t.Fatalf("the fourth enrolment answered %d", res.Code)
	}
	if !strings.Contains(res.Body.String(), "error") {
		t.Fatal("refused without saying why")
	}
	// Another account's allowance is its own.
	if res := askAs(mux, other.Token, http.MethodPost, "/enroll", `{}`); res.Code != http.StatusOK {
		t.Fatalf("another account was refused: %d", res.Code)
	}
}

func TestTheRateLimitForgetsAfterItsWindow(t *testing.T) {
	now := time.Now()
	limit := newRateLimit(1, time.Hour)
	limit.now = func() time.Time { return now }
	if !limit.allow("a") {
		t.Fatal("the first was refused")
	}
	if limit.allow("a") {
		t.Fatal("the second inside the window was allowed")
	}
	now = now.Add(time.Hour + time.Second)
	if !limit.allow("a") {
		t.Fatal("still refused after the window")
	}
}
