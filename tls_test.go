package main

import (
	"bufio"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/pem"
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
	ln := secureListener(raw, func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return &cert, nil }, insecure)
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

// A pair the way a Kubernetes Secret volume holds one: the names everybody
// reads are symlinks through ..data, and a renewal points ..data at a new
// directory rather than writing the files in place.
type mountedPair struct {
	dir      string
	versions int
	CertPath string
	KeyPath  string
}

func newMountedPair(t *testing.T) *mountedPair {
	t.Helper()
	m := &mountedPair{dir: t.TempDir()}
	m.CertPath = filepath.Join(m.dir, "tls.crt")
	m.KeyPath = filepath.Join(m.dir, "tls.key")
	m.publish(t, m.fresh(t))
	for _, name := range []string{"tls.crt", "tls.key"} {
		if err := os.Symlink(filepath.Join("..data", name), filepath.Join(m.dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	return m
}

// fresh makes a new self-signed pair in a version directory of its own and
// returns that directory, not yet published.
func (m *mountedPair) fresh(t *testing.T) string {
	t.Helper()
	m.versions++
	version := filepath.Join(m.dir, "..v"+string(rune('0'+m.versions)))
	if err := writeCertificate(filepath.Join(version, "tls.crt"), filepath.Join(version, "tls.key")); err != nil {
		t.Fatal(err)
	}
	// Later than anything before it, so the check cannot miss it on a
	// filesystem whose clock is coarser than a test runs.
	later := time.Now().Add(time.Duration(m.versions) * time.Minute)
	for _, name := range []string{"tls.crt", "tls.key"} {
		if err := os.Chtimes(filepath.Join(version, name), later, later); err != nil {
			t.Fatal(err)
		}
	}
	return version
}

// publish swaps ..data to version in one rename, as the kubelet does.
func (m *mountedPair) publish(t *testing.T, version string) {
	t.Helper()
	tmp := filepath.Join(m.dir, "..data_tmp")
	if err := os.Symlink(filepath.Base(version), tmp); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, filepath.Join(m.dir, "..data")); err != nil {
		t.Fatal(err)
	}
}

func derIn(t *testing.T, certPath string) []byte {
	t.Helper()
	raw, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		t.Fatalf("%s holds no certificate", certPath)
	}
	return block.Bytes
}

// servedDER is what a device sees: one handshake through the listener serve
// builds, and the certificate that came back.
func servedDER(t *testing.T, source *certSource) []byte {
	t.Helper()
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := secureListener(raw, source.GetCertificate, false)
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.(*tls.Conn).Handshake()
	}()
	conn, err := tls.Dial("tcp", raw.Addr().String(), &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	return conn.ConnectionState().PeerCertificates[0].Raw
}

func checkEveryHandshake(t *testing.T) {
	saved := certCheckEvery
	certCheckEvery = 0
	t.Cleanup(func() { certCheckEvery = saved })
}

func TestConfiguredCertificateIsServed(t *testing.T) {
	m := newMountedPair(t)
	source, err := newCertSource(t.TempDir(), m.CertPath, m.KeyPath)
	if err != nil {
		t.Fatal(err)
	}
	want := derIn(t, m.CertPath)
	if got := servedDER(t, source); string(got) != string(want) {
		t.Fatal("the handshake did not carry the configured certificate")
	}
	if source.Fingerprint() != fingerprintOf(want) {
		t.Fatal("the fingerprint is not the configured certificate's")
	}
}

func TestConfiguredCertificateMustBeAPair(t *testing.T) {
	m := newMountedPair(t)
	for _, paths := range [][2]string{{m.CertPath, ""}, {"", m.KeyPath}} {
		_, err := newCertSource(t.TempDir(), paths[0], paths[1])
		if err == nil {
			t.Fatalf("half a pair %q was accepted", paths)
		}
		if !strings.Contains(err.Error(), "tls_cert") || !strings.Contains(err.Error(), "tls_key") {
			t.Fatalf("the error does not name both settings: %v", err)
		}
	}
}

// No fallback: a server told to serve a certificate it cannot find stops,
// rather than quietly minting one nobody asked for.
func TestConfiguredCertificateMissingIsAnError(t *testing.T) {
	dataDir := t.TempDir()
	missing := filepath.Join(t.TempDir(), "nope")
	if _, err := newCertSource(dataDir, missing+".crt", missing+".key"); err == nil {
		t.Fatal("a missing pair was accepted")
	}
	if _, err := os.Stat(filepath.Join(dataDir, certFile)); !os.IsNotExist(err) {
		t.Fatal("a self-signed certificate was made in its place")
	}
}

func TestConfiguredCertificateIsReloaded(t *testing.T) {
	checkEveryHandshake(t)
	m := newMountedPair(t)
	source, err := newCertSource(t.TempDir(), m.CertPath, m.KeyPath)
	if err != nil {
		t.Fatal(err)
	}
	first := servedDER(t, source)

	m.publish(t, m.fresh(t))
	want := derIn(t, m.CertPath)
	if string(want) == string(first) {
		t.Fatal("the test did not change the certificate")
	}
	if got := servedDER(t, source); string(got) != string(want) {
		t.Fatal("the renewed certificate was not picked up")
	}
	if source.Fingerprint() != fingerprintOf(want) {
		t.Fatal("the fingerprint did not follow the renewal")
	}
}

// Written one file after the other, a renewal is briefly a new certificate
// beside the old key. That moment must serve the old pair, and the moment
// after it the new one.
func TestHalfRenewedCertificateKeepsTheOld(t *testing.T) {
	checkEveryHandshake(t)
	m := newMountedPair(t)
	source, err := newCertSource(t.TempDir(), m.CertPath, m.KeyPath)
	if err != nil {
		t.Fatal(err)
	}
	old := servedDER(t, source)

	renewed := m.fresh(t)
	oldKey, err := os.ReadFile(m.KeyPath)
	if err != nil {
		t.Fatal(err)
	}
	newKey, err := os.ReadFile(filepath.Join(renewed, "tls.key"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(renewed, "tls.key"), oldKey, 0o600); err != nil {
		t.Fatal(err)
	}
	m.publish(t, renewed)
	if got := servedDER(t, source); string(got) != string(old) {
		t.Fatal("a certificate without its key replaced the working one")
	}

	if err := os.WriteFile(filepath.Join(renewed, "tls.key"), newKey, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := servedDER(t, source); string(got) != string(derIn(t, m.CertPath)) {
		t.Fatal("the matched pair was not picked up once the key arrived")
	}
}

func TestSelfSignedIsUnchangedWithoutSettings(t *testing.T) {
	dir := t.TempDir()
	source, err := newCertSource(dir, "", "")
	if err != nil {
		t.Fatal(err)
	}
	_, fp, err := loadOrCreateCertificate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if source.Fingerprint() != fp {
		t.Fatalf("got %s, want the data directory's %s", source.Fingerprint(), fp)
	}
	if got := servedDER(t, source); fingerprintOf(got) != fp {
		t.Fatal("the handshake did not carry the self-signed certificate")
	}
}

// The devices command does not start the server, so it pins what the settings
// name, read from the file on each run.
func TestDevicesPinsTheConfiguredCertificate(t *testing.T) {
	savedCert, savedKey := tlsCert, tlsKey
	t.Cleanup(func() { tlsCert, tlsKey = savedCert, savedKey })
	m := newMountedPair(t)
	dataDir := t.TempDir()
	if _, _, err := loadOrCreateCertificate(dataDir); err != nil {
		t.Fatal(err)
	}

	tlsCert, tlsKey = m.CertPath, m.KeyPath
	got, err := pinnedFingerprint(dataDir)
	if err != nil || got != fingerprintOf(derIn(t, m.CertPath)) {
		t.Fatalf("pinned %q, %v: not the configured certificate", got, err)
	}

	tlsCert, tlsKey = "", ""
	own, _ := localFingerprint(dataDir)
	if got, err := pinnedFingerprint(dataDir); err != nil || got != own {
		t.Fatalf("pinned %q, %v: not the data directory's", got, err)
	}
}

func TestConfiguredKeyMissingIsAnError(t *testing.T) {
	m := newMountedPair(t)
	_, err := newCertSource(t.TempDir(), m.CertPath, filepath.Join(t.TempDir(), "nope.key"))
	if err == nil {
		t.Fatal("a certificate without its key was accepted")
	}
}

func TestConfiguredMismatchedPairIsAnError(t *testing.T) {
	a, b := newMountedPair(t), newMountedPair(t)
	if _, err := newCertSource(t.TempDir(), a.CertPath, b.KeyPath); err == nil {
		t.Fatal("a certificate beside another pair's key was accepted")
	}
}
