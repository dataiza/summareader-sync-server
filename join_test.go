package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Card 713: a join proof bound to a nonce the server issued, so a body
// somebody captured cannot be sent again.

func TestANonceIsGoodOnce(t *testing.T) {
	store := newNonceStore(time.Minute, 10)
	nonce, err := store.issue()
	if err != nil {
		t.Fatal(err)
	}
	if !store.take(nonce) {
		t.Fatal("a fresh nonce was refused")
	}
	if store.take(nonce) {
		t.Fatal("a nonce was accepted twice")
	}
	if store.take("never-issued") {
		t.Fatal("a nonce nobody issued was accepted")
	}
}

func TestANonceExpires(t *testing.T) {
	now := time.Now()
	store := newNonceStore(time.Minute, 10)
	store.now = func() time.Time { return now }
	nonce, err := store.issue()
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(61 * time.Second)
	if store.take(nonce) {
		t.Fatal("an expired nonce was accepted")
	}
}

// The endpoint takes no credential, so it must not be a way to fill memory.
// Full, it drops the nonce nearest its end rather than refusing, so a flood
// cannot keep a real join out for the whole two minutes. Card 1030.
func TestOutstandingNoncesAreBounded(t *testing.T) {
	now := time.Now()
	store := newNonceStore(time.Minute, 3)
	store.now = func() time.Time { return now }
	issued := []string{}
	for i := 0; i < 3; i++ {
		nonce, err := store.issue()
		if err != nil {
			t.Fatal(err)
		}
		issued = append(issued, nonce)
		now = now.Add(time.Second)
	}
	if _, err := store.issue(); err != nil {
		t.Fatalf("a full store refused a new nonce: %v", err)
	}
	if len(store.issued) != 3 {
		t.Fatalf("%d nonces held, want the bound of 3", len(store.issued))
	}
	if store.take(issued[0]) {
		t.Fatal("the nonce nearest its end was kept")
	}
	if !store.take(issued[1]) || !store.take(issued[2]) {
		t.Fatal("a later nonce was dropped instead of the oldest")
	}
}

// joinKey stands in for what the app derives from a recovery code.
func joinKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	seed := sha256.Sum256([]byte("a recovery code, near enough"))
	return ed25519.NewKeyFromSeed(seed[:])
}

func signedVerifier(key ed25519.PrivateKey) string {
	return signedVerifierPrefix +
		base64.RawURLEncoding.EncodeToString(key.Public().(ed25519.PublicKey))
}

func fetchNonce(t *testing.T, mux http.Handler) string {
	t.Helper()
	res := ask(mux, http.MethodGet, "/join/nonce", fromLAN, "")
	if res.Code != http.StatusOK {
		t.Fatalf("/join/nonce answered %d: %s", res.Code, res.Body.String())
	}
	var body struct {
		Nonce string `json:"nonce"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil || body.Nonce == "" {
		t.Fatalf("no nonce in %s", res.Body.String())
	}
	return body.Nonce
}

func signedJoinBody(key ed25519.PrivateKey, nonce string) string {
	encoded, _ := json.Marshal(map[string]string{
		"nonce":     nonce,
		"key":       base64.RawURLEncoding.EncodeToString(key.Public().(ed25519.PublicKey)),
		"signature": base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, []byte(nonce))),
		"label":     "Restored phone",
	})
	return string(encoded)
}

func TestASignedJoinIssuesAToken(t *testing.T) {
	app, _ := newTestApp(t)
	account, err := createAccount(app, "Mine", "Desktop")
	if err != nil {
		t.Fatal(err)
	}
	key := joinKey(t)
	if err := setJoinVerifier(app, account.AccountID, signedVerifier(key)); err != nil {
		t.Fatal(err)
	}
	mux := builtinsMux(t, app)

	res := ask(mux, http.MethodPost, "/join", fromLAN, signedJoinBody(key, fetchNonce(t, mux)))
	if res.Code != http.StatusOK {
		t.Fatalf("signed join answered %d: %s", res.Code, res.Body.String())
	}
	var device Device
	if err := json.Unmarshal(res.Body.Bytes(), &device); err != nil {
		t.Fatal(err)
	}
	if device.AccountID != account.AccountID || device.Token == "" {
		t.Fatalf("joined %+v", device)
	}
}

func TestAReplayedJoinIsRefused(t *testing.T) {
	app, _ := newTestApp(t)
	account, err := createAccount(app, "Mine", "Desktop")
	if err != nil {
		t.Fatal(err)
	}
	key := joinKey(t)
	if err := setJoinVerifier(app, account.AccountID, signedVerifier(key)); err != nil {
		t.Fatal(err)
	}
	mux := builtinsMux(t, app)

	captured := signedJoinBody(key, fetchNonce(t, mux))
	if res := ask(mux, http.MethodPost, "/join", fromLAN, captured); res.Code != http.StatusOK {
		t.Fatalf("the first join answered %d", res.Code)
	}
	before := countAllDevices(t, app)
	if res := ask(mux, http.MethodPost, "/join", fromLAN, captured); res.Code != http.StatusUnauthorized {
		t.Fatalf("the same body sent again answered %d", res.Code)
	}
	if countAllDevices(t, app) != before {
		t.Fatal("a replayed join made a device")
	}
}

func TestASignatureOverAnotherNonceIsRefused(t *testing.T) {
	app, _ := newTestApp(t)
	account, err := createAccount(app, "Mine", "Desktop")
	if err != nil {
		t.Fatal(err)
	}
	key := joinKey(t)
	if err := setJoinVerifier(app, account.AccountID, signedVerifier(key)); err != nil {
		t.Fatal(err)
	}
	mux := builtinsMux(t, app)

	// A valid signature, over a nonce that is not the one in the body.
	var body map[string]string
	_ = json.Unmarshal([]byte(signedJoinBody(key, fetchNonce(t, mux))), &body)
	body["nonce"] = fetchNonce(t, mux)
	encoded, _ := json.Marshal(body)
	if res := ask(mux, http.MethodPost, "/join", fromLAN, string(encoded)); res.Code != http.StatusUnauthorized {
		t.Fatalf("a signature over another nonce answered %d", res.Code)
	}
}

// Codes made before this card hold a hash of the proof, and the proof is all
// their owner's app can send. They keep working, until the app replaces the
// verifier with a signed one.
func TestALegacyProofStillJoinsALegacyVerifier(t *testing.T) {
	app, _ := newTestApp(t)
	account, err := createAccount(app, "Mine", "Desktop")
	if err != nil {
		t.Fatal(err)
	}
	if err := setJoinVerifier(app, account.AccountID, verifierFor("the-proof")); err != nil {
		t.Fatal(err)
	}
	mux := builtinsMux(t, app)
	res := ask(mux, http.MethodPost, "/join", fromLAN, `{"proof":"the-proof","label":"Old app"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("a legacy join answered %d", res.Code)
	}
}

// And a signed verifier is never matched by a bare proof, whatever it is.
func TestABareProofCannotJoinASignedVerifier(t *testing.T) {
	app, _ := newTestApp(t)
	account, err := createAccount(app, "Mine", "Desktop")
	if err != nil {
		t.Fatal(err)
	}
	verifier := signedVerifier(joinKey(t))
	if err := setJoinVerifier(app, account.AccountID, verifier); err != nil {
		t.Fatal(err)
	}
	mux := builtinsMux(t, app)
	body := `{"proof":"` + strings.TrimPrefix(verifier, signedVerifierPrefix) + `"}`
	if res := ask(mux, http.MethodPost, "/join", fromLAN, body); res.Code != http.StatusUnauthorized {
		t.Fatalf("a bare proof against a signed verifier answered %d", res.Code)
	}
}

// Card 1030: the nonce route takes no credential, so one address asking for
// nonces as fast as it can is limited, and somebody else still gets one.
func TestNonceRequestsAreLimitedPerAddress(t *testing.T) {
	app, _ := newTestApp(t)
	mux := builtinsMux(t, app)
	limited := false
	for i := 0; i < 30 && !limited; i++ {
		limited = ask(mux, http.MethodGet, "/join/nonce", fromLAN, "").Code == http.StatusTooManyRequests
	}
	if !limited {
		t.Fatal("thirty nonces in a row from one address and none was refused")
	}
	if code := ask(mux, http.MethodGet, "/join/nonce", "192.0.2.99:40000", "").Code; code != http.StatusOK {
		t.Fatalf("another address answered %d after the first was limited", code)
	}
}

// signV2 is what an app that knows the server signs. Card 1034.
func signV2(key ed25519.PrivateKey, instance, nonce string) string {
	message := "summareader-join-v2\n" + instance + "\n" + nonce
	return base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, []byte(message)))
}

func joinBodyWith(key ed25519.PrivateKey, nonce, signature, sign string) string {
	fields := map[string]string{
		"nonce":     nonce,
		"key":       base64.RawURLEncoding.EncodeToString(key.Public().(ed25519.PublicKey)),
		"signature": signature,
		"label":     "Restored phone",
	}
	if sign != "" {
		fields["sign"] = sign
	}
	encoded, _ := json.Marshal(fields)
	return string(encoded)
}

// Card 1034: a v2 signature names the server it was made for, so one relayed
// from another server does not open this one, and a bare v1 signature cannot
// be passed off as v2.
func TestAV2JoinIsBoundToThisServer(t *testing.T) {
	app, _ := newTestApp(t)
	account, err := createAccount(app, "Mine", "Desktop")
	if err != nil {
		t.Fatal(err)
	}
	key := joinKey(t)
	if err := setJoinVerifier(app, account.AccountID, signedVerifier(key)); err != nil {
		t.Fatal(err)
	}
	mux := builtinsMux(t, app)
	instance := instanceId(app)
	if instance == "" {
		t.Fatal("the test server has no instance id")
	}

	res := ask(mux, http.MethodGet, "/join/nonce", fromLAN, "")
	var offer struct {
		Sign string `json:"sign"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &offer); err != nil || offer.Sign != "v2" {
		t.Fatalf("the nonce reply does not offer v2: %s", res.Body.String())
	}

	nonce := fetchNonce(t, mux)
	body := joinBodyWith(key, nonce, signV2(key, "another-server", nonce), "v2")
	if res := ask(mux, http.MethodPost, "/join", fromLAN, body); res.Code != http.StatusUnauthorized {
		t.Fatalf("a v2 signature for another server answered %d", res.Code)
	}

	nonce = fetchNonce(t, mux)
	v1 := base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, []byte(nonce)))
	if res := ask(mux, http.MethodPost, "/join", fromLAN, joinBodyWith(key, nonce, v1, "v2")); res.Code != http.StatusUnauthorized {
		t.Fatalf("a v1 signature sent as v2 answered %d", res.Code)
	}

	nonce = fetchNonce(t, mux)
	body = joinBodyWith(key, nonce, signV2(key, instance, nonce), "v2")
	if res := ask(mux, http.MethodPost, "/join", fromLAN, body); res.Code != http.StatusOK {
		t.Fatalf("a v2 signature for this server answered %d: %s", res.Code, res.Body.String())
	}

	// And an app that only knows v1 still gets in.
	nonce = fetchNonce(t, mux)
	if res := ask(mux, http.MethodPost, "/join", fromLAN, joinBodyWith(key, nonce, v1Sig(key, nonce), "")); res.Code != http.StatusOK {
		t.Fatalf("a v1 join without the field answered %d", res.Code)
	}
}

func v1Sig(key ed25519.PrivateKey, nonce string) string {
	return base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, []byte(nonce)))
}

// Card 1035: a legacy proof opens the account once. The verifier goes with
// the join, so a proof somebody saw on its way cannot be sent again; the app
// sets a signed verifier with the token it just got.
func TestALegacyProofJoinsOnlyOnce(t *testing.T) {
	app, _ := newTestApp(t)
	account, err := createAccount(app, "Mine", "Desktop")
	if err != nil {
		t.Fatal(err)
	}
	if err := setJoinVerifier(app, account.AccountID, verifierFor("the-proof")); err != nil {
		t.Fatal(err)
	}
	mux := builtinsMux(t, app)
	body := `{"proof":"the-proof","label":"Old app"}`
	if res := ask(mux, http.MethodPost, "/join", fromLAN, body); res.Code != http.StatusOK {
		t.Fatalf("a legacy join answered %d", res.Code)
	}
	if res := ask(mux, http.MethodPost, "/join", fromLAN, body); res.Code != http.StatusUnauthorized {
		t.Fatalf("the same proof joined twice: %d", res.Code)
	}
}
