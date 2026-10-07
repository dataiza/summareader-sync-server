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
func TestOutstandingNoncesAreBounded(t *testing.T) {
	store := newNonceStore(time.Minute, 3)
	for i := 0; i < 3; i++ {
		if _, err := store.issue(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.issue(); err == nil {
		t.Fatal("a fourth nonce was issued past the bound")
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
