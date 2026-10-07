package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Card 713. The certificate is the server's identity on the wire from here
// on: a device pins it, so a second one generated on the next start would be
// a server that every paired device refuses.
func TestTheCertificateIsMadeOnceAndReused(t *testing.T) {
	dir := t.TempDir()

	first, fp, err := loadOrCreateCertificate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(fp) != 64 {
		t.Fatalf("fingerprint %q is not 64 hex characters", fp)
	}
	again, fpAgain, err := loadOrCreateCertificate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fp != fpAgain {
		t.Fatalf("the fingerprint moved between starts: %s then %s", fp, fpAgain)
	}
	if string(first.Certificate[0]) != string(again.Certificate[0]) {
		t.Fatal("a second certificate was made instead of the first being read")
	}

	// The SHA-256 of the DER, which is what a device computes from the
	// handshake. Anything else and the two would never agree.
	sum := sha256.Sum256(first.Certificate[0])
	if hex.EncodeToString(sum[:]) != fp {
		t.Fatal("the fingerprint is not the SHA-256 of the certificate")
	}

	// The key is a secret and the card says 0600; the certificate is not,
	// but nothing else is meant to read the data directory either.
	for _, name := range []string{certFile, keyFile} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s is %v, want 0600", name, info.Mode().Perm())
		}
	}
}

func TestTheCertificateIsLongLived(t *testing.T) {
	cert, _, err := loadOrCreateCertificate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if cert.Leaf == nil {
		t.Fatal("no parsed leaf")
	}
	if cert.Leaf.NotAfter.Before(time.Now().AddDate(50, 0, 0)) {
		t.Fatalf("expires %v; a pinned certificate that expires strands every device",
			cert.Leaf.NotAfter)
	}
}

// A half-written pair is refused rather than replaced: replacing it would
// change the fingerprint under every paired device without a word.
func TestAHalfWrittenPairIsNotQuietlyReplaced(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := loadOrCreateCertificate(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, keyFile)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadOrCreateCertificate(dir); err == nil {
		t.Fatal("a certificate without its key was replaced silently")
	}
}

func TestTheFingerprintReadsInPairs(t *testing.T) {
	got := displayFingerprint(strings.Repeat("ab", 32))
	if got != strings.TrimSuffix(strings.Repeat("AB:", 32), ":") {
		t.Fatalf("displayed as %q", got)
	}
}

// listenFor starts an HTTP server on the listener serve would build.
func listenFor(t *testing.T, insecure bool) (string, string) {
	t.Helper()
	cert, fp, err := loadOrCreateCertificate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := secureListener(raw, cert, insecure)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "hello")
	})}
	go server.Serve(ln)
	t.Cleanup(func() { server.Close() })
	return raw.Addr().String(), fp
}

// pinnedGet does what a device does: trusts no authority, and accepts the
// one certificate whose fingerprint it was given.
func pinnedGet(addr, fp string) (string, error) {
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{
		TLSClientConfig: pinnedTLS(fp),
	}}
	res, err := client.Get("https://" + addr + "/")
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	return string(body), err
}

func TestTLSIsServed(t *testing.T) {
	addr, fp := listenFor(t, false)
	body, err := pinnedGet(addr, fp)
	if err != nil {
		t.Fatal(err)
	}
	if body != "hello" {
		t.Fatalf("got %q", body)
	}

	// The wrong pin is refused before anything is sent, which is the whole
	// of what pinning is for.
	if _, err := pinnedGet(addr, strings.Repeat("0", 64)); err == nil {
		t.Fatal("a connection with the wrong pin went through")
	}
}

func TestPlainHTTPIsRefusedWithoutTheFlag(t *testing.T) {
	addr, _ := listenFor(t, false)
	status, err := plainStatus(addr)
	if err == nil && status == http.StatusOK {
		t.Fatal("plain HTTP was answered without --insecure-http")
	}
}

func TestInsecureHTTPAnswersBothOnOnePort(t *testing.T) {
	addr, fp := listenFor(t, true)
	status, err := plainStatus(addr)
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK {
		t.Fatalf("plain HTTP answered %d with --insecure-http", status)
	}
	if body, err := pinnedGet(addr, fp); err != nil || body != "hello" {
		t.Fatalf("TLS beside it: %q, %v", body, err)
	}
}

func plainStatus(addr string) (int, error) {
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n"); err != nil {
		return 0, err
	}
	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return 0, err
	}
	res.Body.Close()
	return res.StatusCode, nil
}

// The client half the devices command uses, checked against the server half.
func TestThePinnedConfigTrustsNoAuthority(t *testing.T) {
	cfg := pinnedTLS("x")
	if !cfg.InsecureSkipVerify || cfg.VerifyPeerCertificate == nil {
		t.Fatal("pinnedTLS must replace chain verification, not add to it")
	}
	if err := cfg.VerifyPeerCertificate([][]byte{[]byte("not it")}, nil); err == nil {
		t.Fatal("a certificate with another fingerprint was accepted")
	}
}

// The devices command checks the server against the file, without the key and
// without making a certificate where there is none.
func TestTheLocalFingerprintIsTheServedOne(t *testing.T) {
	dir := t.TempDir()
	if _, err := localFingerprint(dir); err == nil {
		t.Fatal("a fingerprint was read where there is no certificate")
	}
	if _, err := os.Stat(filepath.Join(dir, certFile)); !os.IsNotExist(err) {
		t.Fatal("reading the fingerprint made a certificate")
	}
	_, fp, err := loadOrCreateCertificate(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := localFingerprint(dir)
	if err != nil || got != fp {
		t.Fatalf("read %q, %v; want %q", got, err, fp)
	}
}
