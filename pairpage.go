package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	qrcode "github.com/skip2/go-qrcode"
)

// Pairing from a browser, for a server with no desktop console beside it — a
// pod in Kubernetes, a box in a cupboard. The page draws the same code the
// console draws (console/lib/src/pairing.dart is the contract), behind the
// operator token, which the page asks for as a password.
//
// The page itself is not behind the token: it holds nothing until the
// password is typed, and keeps that in a JS variable, never in storage. It is
// still off when no token is set, like every operator route.

//go:embed pairpage.html
var pairPage string

// Nothing on the page or behind it may come from anywhere else, or sit in a
// cache: what it shows is a live device token.
func noStore(e *core.RequestEvent) {
	e.Response.Header().Set("Cache-Control", "no-store")
}

func handlePairPage(e *core.RequestEvent) error {
	if operatorToken() == "" {
		return e.NotFoundError("", nil)
	}
	noStore(e)
	// Inline style and script only; fetches to this server only. The QR is an
	// inline SVG for the same reason.
	e.Response.Header().Set("Content-Security-Policy",
		"default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'")
	return e.HTML(http.StatusOK, pairPage)
}

// The page's first question after the password: which button to show, and the
// fingerprint to compare with the server log before trusting anything.
func handlePairingState(e *core.RequestEvent) error {
	noStore(e)
	if ok, err := operatorOK(e); !ok {
		return err
	}
	libraries, err := e.App.CountRecords(collAccounts)
	if err != nil {
		return e.JSON(http.StatusInternalServerError, map[string]string{"error": "unavailable"})
	}
	_, fingerprint, err := loadOrCreateCertificate(e.App.DataDir())
	if err != nil {
		return e.JSON(http.StatusInternalServerError, map[string]string{"error": "unavailable"})
	}
	return e.JSON(http.StatusOK, map[string]any{
		"libraries":   libraries,
		"fingerprint": fingerprint,
	})
}

type pairingResponse struct {
	First       bool   `json:"first"`
	Server      string `json:"server"`
	Payload     string `json:"payload"`
	QRSVG       string `json:"qr_svg"`
	Fingerprint string `json:"fingerprint"`
}

var errSeveralLibraries = errors.New("several libraries")

// One route for both buttons, where the spec named two (first-device and
// enroll): no library → make it, one → a token into it, more → refuse, so a
// second library can never be made from here.
func handlePairingCode(e *core.RequestEvent) error {
	noStore(e)
	if ok, err := operatorOK(e); !ok {
		return err
	}

	var body struct {
		Label string `json:"label"`
	}
	_ = e.BindBody(&body)
	label := strings.TrimSpace(body.Label)
	if label == "" {
		label = "A new device"
	}

	var device *Device
	first := false
	// Count and create in one transaction, so two browsers pressing *Create
	// first device* at once make one library and enrol into it, not two.
	err := e.App.RunInTransaction(func(tx core.App) error {
		accounts := []*core.Record{}
		if err := tx.RecordQuery(collAccounts).Limit(2).All(&accounts); err != nil {
			return err
		}
		var err error
		switch len(accounts) {
		case 0:
			first = true
			device, err = createAccount(tx, "My library", label)
		case 1:
			var free string
			if free, err = freeLabel(tx, accounts[0].Id, label); err == nil {
				// Told to the devices, as handleOperatorEnroll does. Card 715.
				device, err = enrollAndTell(tx, accounts[0].Id, free, eventEnrolled, "operator")
			}
		default:
			err = errSeveralLibraries
		}
		return err
	})
	if errors.Is(err, errSeveralLibraries) {
		return e.JSON(http.StatusConflict, map[string]string{
			"error": "more than one library here — use the console",
		})
	}
	if err != nil {
		return e.JSON(http.StatusInternalServerError, map[string]string{"error": "could not pair"})
	}

	_, fingerprint, err := loadOrCreateCertificate(e.App.DataDir())
	if err != nil {
		return e.JSON(http.StatusInternalServerError, map[string]string{"error": "unavailable"})
	}
	server := pairingServerURL(e)
	payload := pairingPayload(server, device.Token, fingerprint)
	svg, err := qrSVG(payload)
	if err != nil {
		return e.JSON(http.StatusInternalServerError, map[string]string{"error": "could not draw the code"})
	}
	return e.JSON(http.StatusOK, pairingResponse{
		First:       first,
		Server:      server,
		Payload:     payload,
		QRSVG:       svg,
		Fingerprint: fingerprint,
	})
}

// The address the code carries. --public-url as given, less a trailing slash
// because the app appends paths; else the address this request came in on,
// which behind a LoadBalancer is the one the browser — and so the phone —
// can reach. Host includes the port.
func pairingServerURL(e *core.RequestEvent) string {
	if publicURL != "" {
		return strings.TrimRight(publicURL, "/")
	}
	return "https://" + e.Request.Host
}

// The console's payload, field for field and in its order: a renamed field is
// a phone that refuses to scan. See console/lib/src/pairing.dart.
func pairingPayload(server, token, fingerprint string) string {
	out, _ := json.Marshal(struct {
		Version     int    `json:"version"`
		Server      string `json:"server"`
		DeviceToken string `json:"device_token"`
		Fingerprint string `json:"server_fingerprint"`
	}{2, server, token, fingerprint})
	return string(out)
}

// go-qrcode draws PNGs only. One rect per dark module on a white ground;
// Bitmap already carries the 4-module quiet zone.
func qrSVG(payload string) (string, error) {
	code, err := qrcode.New(payload, qrcode.Medium)
	if err != nil {
		return "", err
	}
	bitmap := code.Bitmap()
	var svg strings.Builder
	fmt.Fprintf(&svg, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges">`+
		`<rect width="100%%" height="100%%" fill="#fff"/>`, len(bitmap), len(bitmap))
	for y, row := range bitmap {
		for x, dark := range row {
			if dark {
				fmt.Fprintf(&svg, `<rect x="%d" y="%d" width="1" height="1"/>`, x, y)
			}
		}
	}
	svg.WriteString(`</svg>`)
	return svg.String(), nil
}
