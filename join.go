package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// Joining by recovery code, bound to a nonce. Card 713.
//
// /join used to take the proof itself, so a body captured once could be sent
// again for ever — until somebody happened to make a new code. The proof
// cannot simply be mixed with a nonce: the server holds a hash of it and
// never the proof, which is the property card 714 exists to keep, so the
// server has nothing to check such a mix against. Keying a MAC with the
// stored hash instead would make the database itself a way in.
//
// So a code now stands for a signing key. The app derives an Ed25519 key from
// the typed code, the server stores only its public half, and a join is a
// signature over a nonce this server issued a moment ago and will accept once.
// A captured body names a nonce that is already spent, and a copy of the
// database yields a public key, which opens nothing.
//
// Codes made before this hold a hash of the proof. Those still join the old
// way, because the app cannot change what an existing code is; the app swaps
// the verifier for a signed one as soon as such a join lets it in.

// What a signed verifier starts with. A legacy verifier is unpadded base64 of
// a SHA-256, which can never contain a colon, so the two cannot be confused.
const signedVerifierPrefix = "ed25519:"

// Long enough to type nothing in between, which is all a nonce has to cover:
// the app fetches one and signs it in the same breath.
const nonceTTL = 2 * time.Minute

var joinNonces = newNonceStore(nonceTTL, 1024)

// nonceStore holds nonces that have been issued and not yet used.
//
// In memory, because a restart losing them costs a join that is retried in a
// second, and a database row per unauthenticated request is a way to fill a
// disk. Bounded for the same reason: the endpoint takes no credential.
type nonceStore struct {
	mu     sync.Mutex
	issued map[string]time.Time
	ttl    time.Duration
	max    int
	now    func() time.Time
}

func newNonceStore(ttl time.Duration, max int) *nonceStore {
	return &nonceStore{issued: map[string]time.Time{}, ttl: ttl, max: max, now: time.Now}
}

var errTooManyNonces = errors.New("too many joins in progress")

func (s *nonceStore) issue() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	nonce := base64.RawURLEncoding.EncodeToString(buf)

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if len(s.issued) >= s.max {
		for n, expires := range s.issued {
			if now.After(expires) {
				delete(s.issued, n)
			}
		}
	}
	if len(s.issued) >= s.max {
		return "", errTooManyNonces
	}
	s.issued[nonce] = now.Add(s.ttl)
	return nonce, nil
}

// take spends a nonce, and says whether it was one worth spending. Spent
// whatever comes next, so a failed attempt cannot try again with the same one.
func (s *nonceStore) take(nonce string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	expires, ok := s.issued[nonce]
	if !ok {
		return false
	}
	delete(s.issued, nonce)
	return !s.now().After(expires)
}

func handleJoinNonce(e *core.RequestEvent) error {
	nonce, err := joinNonces.issue()
	if err != nil {
		return e.JSON(http.StatusServiceUnavailable, map[string]string{
			"error": "too many joins in progress — try again in a minute",
		})
	}
	return e.JSON(http.StatusOK, map[string]any{
		"nonce":      nonce,
		"expires_in": int(nonceTTL.Seconds()),
	})
}

// joinDeviceSigned issues a token to a device that signed a fresh nonce with
// the key its recovery code stands for.
//
// The nonce is spent before anything else is looked at, so a wrong signature
// costs the caller the nonce as well, and nothing is created on any failure.
func joinDeviceSigned(app core.App, nonce, key, signature, label string) (*Device, error) {
	if !joinNonces.take(nonce) {
		return nil, ErrNoAccount
	}
	public, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(key))
	if err != nil || len(public) != ed25519.PublicKeySize {
		return nil, ErrNoAccount
	}
	sig, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(signature))
	if err != nil || !ed25519.Verify(public, []byte(nonce), sig) {
		return nil, ErrNoAccount
	}
	record, err := app.FindFirstRecordByFilter(
		collAccounts,
		"join_verifier = {:verifier}",
		dbx.Params{"verifier": signedVerifierPrefix + base64.RawURLEncoding.EncodeToString(public)},
	)
	if err != nil || record == nil {
		return nil, ErrNoAccount
	}
	return joinAccount(app, record.Id, label)
}

// joinAccount is what both ways of joining end in: an ordinary enrolment,
// counted against the account's allowance and told to its other devices.
func joinAccount(app core.App, accountID, label string) (*Device, error) {
	if !enrolLimit.allow(accountID) {
		return nil, ErrRateLimited
	}
	// Made free rather than refused, since a device joining from a typed code
	// has even less of a person watching than one being paired.
	free, err := freeLabel(app, accountID, label)
	if err != nil {
		return nil, err
	}
	return enrollAndTell(app, accountID, free, eventJoined, "")
}
