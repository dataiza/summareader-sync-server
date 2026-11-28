package main

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The server's own certificate. Card 713.
//
// Everything a device sends this server — the token that can wipe the
// library, a join proof, the ciphertext — used to cross the network in the
// clear, so anybody on the same network could take the token and use it. A
// certificate from an authority is not an option for a server on a home
// network with no name, and is not needed either: every device already gets
// here by being handed something by its owner, and that something now carries
// the certificate's fingerprint. The device trusts that one certificate and
// nothing else, which is a stronger promise than an authority makes.
//
// Generated once, on first start, beside the database. Losing it is losing
// the server's identity: every paired device will then refuse the new one
// and ask, loudly, whether to trust it — which is the point.
const (
	certFile = "tls-cert.pem"
	keyFile  = "tls-key.pem"
)

// Set by --insecure-http. See secureListener.
var insecureHTTP bool

// loadOrCreateCertificate returns the certificate in dir, making one the first
// time, with the SHA-256 of its DER in lowercase hex.
//
// A pair where one half is missing is refused rather than replaced. Replacing
// it would change the fingerprint under every paired device without a word,
// and a server that will not start says what is wrong far more plainly.
func loadOrCreateCertificate(dir string) (tls.Certificate, string, error) {
	certPath := filepath.Join(dir, certFile)
	keyPath := filepath.Join(dir, keyFile)

	_, certErr := os.Stat(certPath)
	_, keyErr := os.Stat(keyPath)
	switch {
	case certErr == nil && keyErr == nil:
		return readCertificate(certPath, keyPath)
	case os.IsNotExist(certErr) && os.IsNotExist(keyErr):
		if err := writeCertificate(certPath, keyPath); err != nil {
			return tls.Certificate{}, "", err
		}
		return readCertificate(certPath, keyPath)
	default:
		return tls.Certificate{}, "", fmt.Errorf(
			"%s and %s must both be there or both be gone; one of them is "+
				"missing, and making a new pair would change this server's "+
				"fingerprint under every paired device", certPath, keyPath)
	}
}

// Set when serve is given a certificate of its own through tls_cert and
// tls_key. A device then trusts it through an authority, as a browser does,
// and a pairing code has no fingerprint to carry.
var publicCertificate bool

// How often a configured certificate's file is looked at for a renewal. A
// stat per handshake would cost nothing much, but a renewal comes every few
// weeks, and half a minute late is not late.
var certCheckEvery = 30 * time.Second

// certSource is the certificate serve hands to every handshake.
//
// The self-signed pair never changes, so for it this only holds the one
// certificate. A configured pair is replaced on disk when it is renewed, by
// cert-manager through a Secret volume, and is read again when the
// certificate file changes; a server that needed restarting for that would
// stop being trusted on the day the old certificate expired.
type certSource struct {
	certPath, keyPath string

	mu      sync.Mutex
	cert    *tls.Certificate
	stamp   fileStamp
	checked time.Time
}

// fileStamp is what a renewal changes. Read with os.Stat, which follows a
// symlink: a Secret volume renews by pointing ..data at a new directory, and
// the link itself never changes.
type fileStamp struct {
	modified time.Time
	size     int64
}

func stampOf(path string) (fileStamp, error) {
	info, err := os.Stat(path)
	if err != nil {
		return fileStamp{}, err
	}
	return fileStamp{info.ModTime(), info.Size()}, nil
}

// newCertSource serves the configured pair when certPath and keyPath are set,
// and the data directory's self-signed one when neither is.
//
// A configured pair that cannot be read is an error, never a reason to fall
// back to the self-signed one. Devices told to trust an authority would refuse
// it, and a server that will not start says why where a fallback would not.
func newCertSource(dataDir, certPath, keyPath string) (*certSource, error) {
	if certPath == "" && keyPath == "" {
		cert, _, err := loadOrCreateCertificate(dataDir)
		if err != nil {
			return nil, err
		}
		return &certSource{cert: &cert}, nil
	}
	if certPath == "" || keyPath == "" {
		return nil, errHalfCertificate
	}
	stamp, err := stampOf(certPath)
	if err != nil {
		return nil, err
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	return &certSource{
		certPath: certPath, keyPath: keyPath,
		cert: &cert, stamp: stamp, checked: time.Now(),
	}, nil
}

// GetCertificate never fails a handshake over a renewal. A pair that does
// not load is most often one caught halfway through being written, the new
// certificate beside the old key, so the one already loaded keeps serving
// and the stamp is left alone for the next check to try again.
func (s *certSource) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.certPath == "" || time.Since(s.checked) < certCheckEvery {
		return s.cert, nil
	}
	s.checked = time.Now()
	stamp, err := stampOf(s.certPath)
	if err == nil && stamp == s.stamp {
		return s.cert, nil
	}
	if err == nil {
		var cert tls.Certificate
		cert, err = tls.LoadX509KeyPair(s.certPath, s.keyPath)
		if err == nil {
			s.cert, s.stamp = &cert, stamp
			log.Printf("certificate reloaded; expires %s",
				cert.Leaf.NotAfter.Format(time.RFC3339))
			return s.cert, nil
		}
	}
	log.Printf("certificate reload failed: %v", err)
	return s.cert, nil
}

// Fingerprint is the SHA-256 of the certificate being served now.
func (s *certSource) Fingerprint() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fingerprintOf(s.cert.Certificate[0])
}

func readCertificate(certPath, keyPath string) (tls.Certificate, string, error) {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	return cert, fingerprintOf(cert.Certificate[0]), nil
}

// writeCertificate makes an ECDSA P-256 key and a self-signed certificate for
// it, and writes both readable by their owner alone.
//
// A hundred years, because a pinned certificate that expires strands every
// device that pinned it, and nothing here checks an expiry date anyway: the
// fingerprint is the whole of the trust. No names in it for the same reason —
// the address a device uses is checked by the device against what its owner
// told it, not against the certificate.
func writeCertificate(certPath, keyPath string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "SummaReader sync server"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(100, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		return err
	}
	// The key first: a certificate on disk with no key beside it is the
	// half-written pair loadOrCreateCertificate refuses, and that is the
	// better failure of the two.
	if err := writePEM(keyPath, "PRIVATE KEY", keyDER); err != nil {
		return err
	}
	return writePEM(certPath, "CERTIFICATE", der)
}

func writePEM(path, kind string, der []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if err := pem.Encode(file, &pem.Block{Type: kind, Bytes: der}); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

// configuredCertificate says whether the settings name a certificate of its
// own. The commands that run beside the server have no listener to ask, so
// they decide from the same settings serve does.
func configuredCertificate() bool { return tlsCert != "" || tlsKey != "" }

var errHalfCertificate = errors.New("tls_cert and tls_key must both be set or both " +
	"be empty; a certificate cannot be served without its key")

// checkCertificateSettings is the both-or-neither rule serve applies, for the
// commands that decide from the same settings: without it one of them would
// treat half a pair as public mode and then read a file named "".
func checkCertificateSettings() error {
	if (tlsCert == "") != (tlsKey == "") {
		return errHalfCertificate
	}
	return nil
}

// certificateCovers refuses a certificate that does not cover the host of
// publicURL: every pairing code names that host and a device would reject the
// handshake, which looks like a dead server rather than a wrong certificate.
func certificateCovers(cert *tls.Certificate, publicURL string) error {
	u, err := url.Parse(publicURL)
	if err != nil || u.Hostname() == "" {
		return fmt.Errorf("public_url %q has no host to check the certificate against", publicURL)
	}
	if cert.Leaf == nil {
		return errors.New("the configured certificate could not be read")
	}
	if err := cert.Leaf.VerifyHostname(u.Hostname()); err != nil {
		return fmt.Errorf("the certificate in tls_cert does not cover public_url's host: %w", err)
	}
	return nil
}

// pinnedFingerprint is the certificate a command should expect of the server
// it talks to: the configured one, read from its file on each run so a
// renewal is followed, else the one in the data directory.
func pinnedFingerprint(dir string) (string, error) {
	if configuredCertificate() {
		return fingerprintOfFile(tlsCert)
	}
	return localFingerprint(dir)
}

// localFingerprint reads the certificate in dir without touching the key and
// without making one: the devices commands only check the server they talk
// to, and a command that quietly minted a certificate would be choosing the
// server's identity for it.
func localFingerprint(dir string) (string, error) {
	return fingerprintOfFile(filepath.Join(dir, certFile))
}

func fingerprintOfFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE" {
		return "", fmt.Errorf("%s holds no certificate", path)
	}
	return fingerprintOf(block.Bytes), nil
}

// fingerprintOf is what a device compares: SHA-256 of the DER, lowercase hex.
func fingerprintOf(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// displayFingerprint is the same value for a person to read off a screen and
// compare with another: upper case, in pairs.
func displayFingerprint(fp string) string {
	upper := strings.ToUpper(fp)
	pairs := make([]string, 0, len(upper)/2)
	for i := 0; i+1 < len(upper); i += 2 {
		pairs = append(pairs, upper[i:i+2])
	}
	return strings.Join(pairs, ":")
}

// pinnedTLS trusts exactly one certificate and no authority.
//
// The chain check is switched off and replaced, not supplemented: an authority
// vouching for some other certificate at this address is precisely the case
// a pin exists to refuse.
func pinnedTLS(fp string) *tls.Config {
	want := strings.ToLower(strings.TrimSpace(fp))
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 || fingerprintOf(raw[0]) != want {
				if configuredCertificate() {
					return errors.New("the server's certificate is not the one in the " +
						"configured tls_cert file; a renewal may be in progress, so retry in a minute")
				}
				return errors.New("the server's certificate is not the one this " +
					"machine's data directory holds")
			}
			return nil
		},
	}
}

// secureListener puts TLS on every connection, and with insecure set also
// answers plain HTTP on the same port.
//
// The same port, because every device already knows the port and a second one
// would be a second thing to configure. Which protocol a connection speaks is
// decided by its first byte: a TLS handshake always opens with 0x16, and no
// HTTP method does. Plain HTTP is for one transition release only, for devices
// whose app predates this card and cannot speak anything else.
func secureListener(raw net.Listener, get func(*tls.ClientHelloInfo) (*tls.Certificate, error), insecure bool) net.Listener {
	config := &tls.Config{
		MinVersion:     tls.VersionTLS12,
		GetCertificate: get,
	}
	if !insecure {
		return tls.NewListener(raw, config)
	}
	return &sniffingListener{Listener: raw, config: config}
}

type sniffingListener struct {
	net.Listener
	config *tls.Config
}

// Accept hands the connection over undecided. Peeking here would hold up the
// accept loop for as long as a slow client took to send its first byte; the
// decision is made on the connection's own first read instead, in the
// goroutine that serves it.
func (l *sniffingListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &sniffedConn{Conn: conn, config: l.config}, nil
}

type sniffedConn struct {
	net.Conn
	config *tls.Config
	once   sync.Once
	inner  net.Conn
}

func (c *sniffedConn) decide() {
	c.once.Do(func() {
		buffered := &bufferedConn{Conn: c.Conn, reader: bufio.NewReader(c.Conn)}
		first, err := buffered.reader.Peek(1)
		if err == nil && first[0] == 0x16 {
			c.inner = tls.Server(buffered, c.config)
			return
		}
		c.inner = buffered
	})
}

func (c *sniffedConn) Read(p []byte) (int, error) {
	c.decide()
	return c.inner.Read(p)
}

func (c *sniffedConn) Write(p []byte) (int, error) {
	c.decide()
	return c.inner.Write(p)
}

// Close shuts the socket itself, decided or not. A shutdown can close a
// connection that has not sent its first byte while another goroutine is
// still waiting for it, and the socket is the one thing both can touch safely.
// A TLS connection loses its closing alert this way, which HTTP does not need.
func (c *sniffedConn) Close() error { return c.Conn.Close() }

// bufferedConn gives back the byte the sniff peeked at.
type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
