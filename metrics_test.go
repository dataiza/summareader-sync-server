package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// The server counts what it holds and never what it holds *is*.
//
// It stores ciphertext and cannot read an entry; this endpoint must not become
// the reason it starts wanting to. Everything here is a count, a byte total or
// an age.

func scrape(t *testing.T, app core.App, token string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	event := &core.RequestEvent{}
	event.App = app
	event.Request = request
	event.Response = recorder
	if err := handleMetrics(event); err != nil {
		// The error helpers write their own status, so an error here is the
		// refusal path rather than a failure of the test.
		t.Logf("handler returned: %v", err)
	}
	return recorder
}

func TestMetricsAreOffWithoutAToken(t *testing.T) {
	app, _ := newTestApp(t)
	t.Setenv("SUMMAREADER_METRICS_TOKEN", "")

	if body := scrape(t, app, "").Body.String(); strings.Contains(body, "summareader_server") {
		t.Fatalf("metrics served with no token configured: %q", body)
	}
}

func TestMetricsRefuseTheWrongToken(t *testing.T) {
	app, _ := newTestApp(t)
	t.Setenv("SUMMAREADER_METRICS_TOKEN", "sesame")

	if body := scrape(t, app, "not-it").Body.String(); strings.Contains(body, "summareader_server") {
		t.Fatal("metrics served to a wrong token")
	}
}

func TestMetricsCountWhatIsHeld(t *testing.T) {
	app, account := newTestApp(t)
	t.Setenv("SUMMAREADER_METRICS_TOKEN", "sesame")

	if _, err := appendEntry(app, account, "device-1", "ciphertext"); err != nil {
		t.Fatal(err)
	}
	if _, err := appendEntry(app, account, "device-1", "more ciphertext"); err != nil {
		t.Fatal(err)
	}

	body := scrape(t, app, "sesame").Body.String()

	for _, want := range []string{
		"summareader_server_log_entries 2",
		"summareader_server_accounts 1",
		"# TYPE summareader_server_stored_bytes gauge",
		"process_start_time_seconds",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
}

func TestMetricsSayNothingAboutContent(t *testing.T) {
	// The one rule this endpoint has to keep: the server holds ciphertext, and
	// a metric that leaked a payload would be the only place it ever appeared
	// in the clear.
	app, account := newTestApp(t)
	t.Setenv("SUMMAREADER_METRICS_TOKEN", "sesame")

	const secret = "an-entry-nobody-should-see"
	if _, err := appendEntry(app, account, "device-1", secret); err != nil {
		t.Fatal(err)
	}

	if body := scrape(t, app, "sesame").Body.String(); strings.Contains(body, secret) {
		t.Fatal("a payload appeared in the metrics")
	}
}

func TestMetricsPerAccount(t *testing.T) {
	app, account := newTestApp(t)
	t.Setenv("SUMMAREADER_METRICS_TOKEN", "sesame")
	if _, err := appendEntry(app, account, "device-1", "x"); err != nil {
		t.Fatal(err)
	}

	body := scrape(t, app, "sesame").Body.String()

	// "The server is full" is never the useful form of the question.
	if !strings.Contains(body, `summareader_server_account_entries{account="`+account+`"} 1`) {
		t.Errorf("no per-account entry count in:\n%s", body)
	}
}

func TestMetricsTokenIsTrimmed(t *testing.T) {
	// A token pasted out of a file arrives with a newline on it more often
	// than not, and "the token is right but the scrape 401s" is a bad hour.
	app, _ := newTestApp(t)
	os.Setenv("SUMMAREADER_METRICS_TOKEN", "  sesame\n")
	t.Cleanup(func() { os.Unsetenv("SUMMAREADER_METRICS_TOKEN") })

	if !strings.Contains(scrape(t, app, "sesame").Body.String(), "summareader_server_accounts") {
		t.Fatal("a padded token in the environment refused the right one")
	}
}
