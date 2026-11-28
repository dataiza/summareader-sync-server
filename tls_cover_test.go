package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"strings"
	"testing"
	"time"
)

func certFor(t *testing.T, host string) *tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
}

func TestCertificateMustCoverPublicURL(t *testing.T) {
	c := certFor(t, "sync.example")
	if err := certificateCovers(c, "https://sync.example:8443"); err != nil {
		t.Fatalf("matching host refused: %v", err)
	}
	if err := certificateCovers(c, "https://other.example"); err == nil {
		t.Fatal("a certificate for another host was accepted")
	}
}

func TestHalfACertificatePairIsAnError(t *testing.T) {
	savedCert, savedKey := tlsCert, tlsKey
	t.Cleanup(func() { tlsCert, tlsKey = savedCert, savedKey })
	tlsCert, tlsKey = "/x/tls.crt", ""
	if err := checkCertificateSettings(); err == nil || !strings.Contains(err.Error(), "tls_key") {
		t.Fatalf("got %v", err)
	}
	tlsCert, tlsKey = "", ""
	if err := checkCertificateSettings(); err != nil {
		t.Fatal(err)
	}
}
