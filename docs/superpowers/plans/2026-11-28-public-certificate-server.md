# Public certificate: server and chart Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The sync server can serve a configured, renewing certificate. Its web page lists and manages devices. The chart gets the certificate from cert-manager and drops the separate pair Ingress.

**Architecture:**
- `tls.go` gains a certificate source that is either the self-signed pair (unchanged) or a configured pair, re-read on change. `secureListener` takes the source through `tls.Config.GetCertificate`.
- One package-level `publicCertificate bool` decides whether codes and pages carry a fingerprint.
- The page (`pairpage.html`, `pairpage.go`) gains a device list over a new `GET /operator/devices` and the existing `/operator/*` writes.
- The chart replaces `pairIngress` with `tls.certManager`.

**Tech Stack:** Go 1.26, PocketBase, `crypto/tls`, `grandcat/zeroconf`, Helm 3, cert-manager `Certificate` (cert-manager.io/v1).

**Spec:** `docs/superpowers/specs/2026-11-28-single-address-design.md`

**Base:** branch `public-cert` from `origin/main` (1.7.0), worktree `../summareader-sync-server-public`.

## Global Constraints

- **Settings:** `tls_cert` / `tls_key` in the config file, env `SUMMAREADER_TLS_CERT` / `SUMMAREADER_TLS_KEY` (read with `TrimSpace`), flags `--tls-cert` / `--tls-key` on the root's persistent flags. Precedence flag > env > file > default, as `AnnounceIP` and `PublicURL` do in `config.go`.
- **Checked at start, all as errors that stop the server:**
  - exactly one of the two set;
  - either file missing or unreadable;
  - the pair not loading (`tls.LoadX509KeyPair`);
  - a configured pair with an empty `publicURL`.
- **No fallback** to the self-signed pair when a configured pair is set.
- **Reload:** a configured pair is re-read when the cert file's mtime or size changes, checked at most once every 30 s from inside `GetCertificate`. A failed re-read keeps the certificate already loaded and logs `certificate reload failed: <err>`. A successful one logs `certificate reloaded; expires <NotAfter RFC3339>`.
- **Start log line** with a configured pair: `serving https on <addr>; certificate from <cert path>, expires <NotAfter RFC3339>`. It replaces the fingerprint line, which stays for self-signed.
- **When `publicCertificate` is true:**
  - `GET /operator/pairing` omits `fingerprint`;
  - `POST /operator/pairing` omits `fingerprint` from its answer and `server_fingerprint` from the payload;
  - `first-device` prints no fingerprint lines, and its `--json` output omits `fingerprint`;
  - the page hides both fingerprint lines.
- **The payload field order stays** `version, server, device_token, server_fingerprint`, with `server_fingerprint` as `omitempty` (console contract, `console/lib/src/pairing.dart`).
- **`devices` CLI** (`devices_cmd.go`): with a configured pair, it pins the fingerprint of the configured cert file, read on each run. Otherwise `localFingerprint` as today.
- **mDNS TXT** gains `url=<publicURL>` when `publicURL` is non-empty, after `scheme=https`.
- **`GET /operator/devices`:**
  - Behind `operatorOK`: 404 without a token, 401 with a wrong one. Refused over plain HTTP like the other page routes (`e.Request.TLS == nil` → 404). `Cache-Control: no-store`.
  - Body: `{"devices": [...]}`, the `overviewDevice` rows from `collectOverview`, unchanged in shape.
- **Page:** every server value is inserted with `textContent`, as today. Remove asks `confirm()` first. The CSP header is unchanged except that `connect-src 'self'` must cover the new calls, which it already does.
- **Chart:**
  - `tls.certManager.enabled` (default `false`), `.clusterIssuer` (required when enabled), `.secretName` (default `"<chart name>-tls"`).
  - Enabled without `publicURL` fails the render with `publicURL is required with tls.certManager: the certificate is issued for its host`.
  - `pairIngress`, `templates/ingress.yaml` and their tests are removed. `service.annotations` stays.
- **Versions are not bumped here.** The release (skill `release-summareader`) makes it 2.0.0 for the server, the console and the chart together.
- `go test -race ./...` passes after every task. Never print a token in logs or tests' failure messages.

## Review Focus

1. A cert file replaced by a symlink swap, as Kubernetes Secret volumes do (`..data` → new dir): the reload notices, because mtime is read through the symlink with `os.Stat`, not `Lstat`.
2. A renewed certificate whose files are written one after the other (the key not yet replaced): the re-read fails `LoadX509KeyPair`, the old certificate keeps serving, and the next check picks up the matched pair.
3. `GET /operator/devices` with the metrics token but not the operator token: 401. The page's password is the operator token only.
4. A device label containing `<img src=x onerror=…>`: shown as text on the page.
5. A self-signed server (no settings): every existing test passes unchanged, the start log still prints the fingerprint, and pairing codes still carry it.

---

### Task 1: Serve a configured certificate, re-read on renewal

**Files:**
- Modify: `config.go` (fields `TLSCert`, `TLSKey`; env in `resolveConfig`)
- Modify: `tls.go` (certificate source; `secureListener` signature)
- Modify: `main.go` (flags near line 183; the `OnServe` block near line 127; the start log line)
- Modify: `summareader-sync.example.json` (both keys, empty)
- Test: `tls_test.go`, `config_test.go`

**Interfaces:**
- Produces: `type certSource struct{...}` with `func newCertSource(dataDir, certPath, keyPath string) (*certSource, error)` and `func (s *certSource) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error)`.
- Produces: `func (s *certSource) Fingerprint() string`, which is the current DER's SHA-256 and is used by Task 2.
- Produces: package var `publicCertificate bool`, set true when a configured pair is in use.
- Changes: `secureListener(raw net.Listener, get func(*tls.ClientHelloInfo) (*tls.Certificate, error), insecure bool) net.Listener`. The test at `tls_test.go:108` is adjusted to pass `func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return &cert, nil }`.

- [ ] **Step 1: Write the failing tests**

```go
func TestConfiguredCertificateIsServed(t *testing.T)            // handshake sees the configured cert's DER
func TestConfiguredCertificateMustBeAPair(t *testing.T)         // only cert set → error naming both settings
func TestConfiguredCertificateMissingIsAnError(t *testing.T)    // path does not exist → error, no tls-cert.pem made in dataDir
func TestConfiguredCertificateIsReloaded(t *testing.T)          // replace files, advance the check clock, next handshake sees the new DER
func TestHalfRenewedCertificateKeepsTheOld(t *testing.T)        // new cert + old key → old cert still served, no error to the client
func TestSelfSignedIsUnchangedWithoutSettings(t *testing.T)     // newCertSource(dir, "", "") == loadOrCreateCertificate fingerprint
func TestTLSSettingsFromEnv(t *testing.T)                       // SUMMAREADER_TLS_CERT/KEY trimmed into Config
```

Make the 30 s clock a package-level `var certCheckEvery = 30 * time.Second` and set it to 0 in tests. Make the certificates with a helper that writes a self-signed pair to a temp dir.

- [ ] **Step 2: Run them to see them fail**

Run: `go test -run 'Certificate|TLSSettings' ./...`
Expected: FAIL (undefined `newCertSource`).

- [ ] **Step 3: Implement**, as the Global Constraints and Interfaces say. In `OnServe`, a configured pair with an empty `publicURL` returns an error before listening.

- [ ] **Step 4: Run all tests**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 5: Commit** — `Serve a configured certificate and re-read it when it is renewed`.

---

### Task 2: No fingerprint where the certificate is public

**Files:**
- Modify: `pairpage.go` (`handlePairingState`, `handlePairingCode`, `pairingPayload`'s struct tag to `json:"server_fingerprint,omitempty"`)
- Modify: `main.go` (`first-device`, near line 942)
- Modify: `devices_cmd.go` (near line 70)
- Modify: `pairpage.html` (hide `#state-fingerprint`'s and `#fingerprint`'s paragraphs when the field is absent)
- Test: `pairpage_test.go`, `devices_cmd_test.go` if present (otherwise add `TestDevicesPinsTheConfiguredCertificate` to `tls_test.go`)

**Interfaces:**
- Consumes: `publicCertificate`, and `certSource.Fingerprint()` or a function reading the configured cert file's fingerprint for the CLI. The CLI does not start the server, so it reads the file the way `localFingerprint` does, from the configured path.

- [ ] **Step 1: Write the failing tests**

```go
func TestPairingWithPublicCertificateCarriesNoFingerprint(t *testing.T) // publicCertificate=true: GET has no "fingerprint" key; POST payload has no "server_fingerprint"; server == publicURL
func TestPairingPayloadOmitsAnEmptyFingerprint(t *testing.T)           // pairingPayload(s, tok, "") == `{"version":2,"server":"s","device_token":"tok"}`
func TestDevicesPinsTheConfiguredCertificate(t *testing.T)             // with TLS settings, the pin used is the configured file's fingerprint
```

`TestPairingPayloadMatchesTheConsole` stays as it is and must still pass.

- [ ] **Step 2: Run them to see them fail** — `go test -run 'Pairing|Devices' ./...` → FAIL.
- [ ] **Step 3: Implement.** For `first-device` with a configured pair, skip `loadOrCreateCertificate`, so no self-signed pair gets made next to a public one, and print `Certificate: issued by an authority for <publicURL host>` in place of the fingerprint lines.
- [ ] **Step 4: Run all tests** — `go test -race ./...` → PASS.
- [ ] **Step 5: Commit** — `Pairing codes from a server with a public certificate carry no fingerprint`.

---

### Task 3: Announce the public address

**Files:**
- Modify: `announce.go` (the `txt` slice near line 55; the signature gains `url string`)
- Modify: `main.go` (the `announce(...)` call near line 119 passes `strings.TrimRight(publicURL, "/")`)
- Test: `announce_test.go` (create if absent)

**Interfaces:**
- Produces: `func announceTXT(url string) []string`, a pure function used by `announce`.

- [ ] **Step 1: Failing test**

```go
func TestAnnounceTXT(t *testing.T) {
	// "" → [software=summareader-sync-server scheme=https]
	// "https://sync.example" → same plus "url=https://sync.example"
}
```

- [ ] **Step 2: Run** — `go test -run AnnounceTXT ./...` → FAIL.
- [ ] **Step 3: Implement**, and update the comment above `txt`: the address is now part of what is broadcast, because it is already public (it is in DNS), and a device needs the name for the certificate to match.
- [ ] **Step 4: Run all tests** — `go test -race ./...` → PASS.
- [ ] **Step 5: Commit** — `Announce the public address on the network`.

---

### Task 4: The device list on the page

**Files:**
- Modify: `pairpage.go` (new `handleOperatorDevices`)
- Modify: `main.go` (route `GET /operator/devices`, beside the other page routes)
- Modify: `pairpage.html` (a *Devices* section shown after login)
- Test: `pairpage_test.go`

**Interfaces:**
- Consumes: `collectOverview(app)` from `overview.go`, `operatorOK`, and the existing `POST /operator/rename|revoke|resume|remove`. Read each handler's body shape in `main.go` before writing the page's calls.

- [ ] **Step 1: Failing tests**

```go
func TestOperatorDevicesWantsTheOperatorToken(t *testing.T) // no token configured → 404; wrong → 401; metrics token → 401
func TestOperatorDevicesListsEveryDevice(t *testing.T)      // two devices made with createAccount/enroll → both ids in "devices"
func TestOperatorDevicesRefusesPlainHTTP(t *testing.T)      // Request.TLS == nil → 404
func TestOperatorDevicesIsNotCached(t *testing.T)           // Cache-Control: no-store
```

- [ ] **Step 2: Run** — `go test -run OperatorDevices ./...` → FAIL.
- [ ] **Step 3: Implement the handler**, then the page:
  - After login, the page loads `/operator/devices` and lists each device: label, last seen, client build, entry count, and a revoked marker.
  - Each device has *Rename* (a `prompt()` for the new name), *Revoke* or *Resume* depending on its state, and *Remove* (after `confirm()`).
  - The list reloads after each action, and again after a pairing code is made.
  - Errors show in a `.warn` paragraph, inserted with `textContent`.
- [ ] **Step 4: Run all tests** — `go test -race ./...` → PASS. Then start the server locally with `SUMMAREADER_OPERATOR_TOKEN` set, open `https://localhost:8099/pair`, and check that the list, rename, revoke, resume and remove work. Say in the report that this was done.
- [ ] **Step 5: Commit** — `List and manage devices on the server's page`.

---

### Task 5: Chart: certificate from cert-manager, pair Ingress removed; docs

**Files:**
- Create: `helm/summareader-sync/templates/certificate.yaml`
- Delete: `helm/summareader-sync/templates/ingress.yaml`
- Modify: `helm/summareader-sync/values.yaml` (drop the `pairIngress` block and its comments; add `tls.certManager`), `templates/deployment.yaml` (env, volume and mount), `chart_test.go`
- Modify: `RUNNING.md`, `README.md`, `CHANGELOG.md` (unreleased section)

**Interfaces:**
- Consumes: env names from Task 1.

- [ ] **Step 1: Failing chart tests** in `chart_test.go`, skipped without helm as `TestChartRenders` is:

```go
// default render: no Certificate, no tls volume, no SUMMAREADER_TLS_CERT
// tls.certManager.enabled=true, clusterIssuer=le, publicURL=https://sync.example:
//   kind: Certificate; dnsNames: [sync.example]; issuerRef: {kind: ClusterIssuer, name: le};
//   secretName: summareader-sync-tls; volume secret defaultMode 0440 mounted at /tls readOnly;
//   SUMMAREADER_TLS_CERT=/tls/tls.crt, SUMMAREADER_TLS_KEY=/tls/tls.key
// enabled with publicURL "" or " " → render fails with the message in Global Constraints
// enabled without clusterIssuer → render fails
// no Ingress in any render; values with pairIngress set are simply ignored
```

- [ ] **Step 2: Run** — `go test -run Chart ./...` → FAIL.
- [ ] **Step 3: Implement.**
  - The `dnsNames` host comes from `publicURL` with the scheme and any port stripped. Use `urlParse` in the template: `(urlParse .Values.publicURL).host`, then split off `:port`.
  - Docs:
    - RUNNING.md: replace the "pairing page with a public certificate" section and its Traefik/ServersTransport examples with "A public certificate". Cover the two settings, cert-manager through `tls.certManager`, codes without a fingerprint, devices following the server onto it (an app from this release on), and devices stored by IP needing a new code. Update the values table.
    - README.md: the Kubernetes paragraph names the single address.
    - CHANGELOG.md: the unreleased section lists Tasks 1–5. `pairIngress` is removed, so the entry says this is breaking.
- [ ] **Step 4: Run all tests** — `go test -race ./...` → PASS; `helm lint helm/summareader-sync --set tls.certManager.enabled=true --set tls.certManager.clusterIssuer=le --set publicURL=https://sync.example` → no errors.
- [ ] **Step 5: Commit** — `Chart: the certificate from cert-manager; the pair Ingress is gone`.

---

## After the plan (not tasks)

- Release the app first (`public-certificate-app` plan), then this repo as 2.0.0 (`release-summareader`).
- homenet, through the homenet session:
  - enable `tls.certManager` with `letsencrypt-production`;
  - delete the `pairIngress` values, the pair host from the inventory, and the ServersTransport;
  - update the runbook.
- Verify on the cluster:
  - `curl https://summareader-sync.test.home.p4ulie.net/instance` and `/pair` with no `-k`;
  - the iPad's trace shows `server certificate is public; pin dropped`;
  - `avahi-browse -rt _summareader-sync._tcp` shows `url=`.
