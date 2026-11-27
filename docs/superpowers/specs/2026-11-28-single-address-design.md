# One address for the web page and sync, with a public certificate

Approved 2026-11-28.

## Intent

The cluster deployment has two addresses:

- `summareader-sync.test.home.p4ulie.net`: the LoadBalancer, 10.39.20.12, ports 443 and 8099, self-signed certificate, which devices pin.
- `summareader-pair.test.home.p4ulie.net`: a Traefik Ingress with a Let's Encrypt certificate, for the `/pair` page only.

The owner wants one address for the web page and for sync. They also want the web page to show the synced devices and manage them, as the desktop console does. The cluster has no desktop console, so today it has no way to see or manage its devices at all.

## Goals

- One address, `summareader-sync.test.home.p4ulie.net`, on 443 (and 8099), serving both sync and the web page. The certificate is publicly valid, so a browser shows no warning.
- The web page shows the pairing code, as `/pair` does today, and a device list with rename, revoke, resume and remove.
- Paired devices keep syncing through the switch and through every certificate renewal, with no question asked of the owner.
- Docker and the AppImage are unchanged: a self-signed certificate made by the server, pinned by devices.

## Non-goals

- **Server configuration on the web page.** The owner ruled it out on 2026-11-28. On Kubernetes, the Helm values are the configuration.
- Moving sync to a different port. Every paired device's address has no port, so 443 stays sync.
- A public certificate for Docker or the AppImage. They keep self-signed; a person who wants one there can put the server behind their own reverse proxy, as today.

## Anti-goals

- Any device that keeps a pin breaking at every Let's Encrypt renewal, which is roughly every 60 days.
- A device trusting a public certificate for an address other than the one it was paired with.
- The server falling back to a self-signed certificate when the configured one is missing. Devices would see a certificate nobody meant them to trust.

## Approaches considered

1. **The server serves the Let's Encrypt certificate itself. Chosen.** cert-manager issues it, the chart mounts it into the pod, and the server re-reads it when it changes. The LoadBalancer, the IP and mDNS stay as they are; the pair Ingress goes away. Costs a certificate-reload path in the server.
2. **Everything goes through Traefik.** The sync DNS name points at Traefik, which terminates TLS and forwards to the pod. There is no certificate code in the server, but every sync request goes through Traefik: blob uploads, `/subscribe` streams, and Traefik's body-size and timeout limits. It is one more hop that can break sync. Rejected.
3. **One address, self-signed.** The browser warns on the page. The owner already rejected that on 2026-11-27, which is why the pair Ingress exists.

## How trust works today (app, `summareader`)

- `packages/summareader_core/lib/src/pinning.dart`: `pinnedHttpClient(fp)` trusts no roots and accepts exactly one certificate. With no pin stored, the app uses a normal client with system roots.
- `lib/src/sync/sync_service.dart:684`, `certificateQuestion()`:
  - A pin is stored: no question; the pinned client refuses anything else.
  - No pin: it asks `offeredCertificate()`. That returns null for a certificate an authority signed, so there is no question. Otherwise it returns the fingerprint, which the owner must approve.
- `lib/src/crypto/device_identity.dart:505`: a pairing code's `server_fingerprint` is optional. It is refused only when present and malformed.
- **Consequence:** a device paired from a code without a fingerprint, at a DNS name with a public certificate, already works with today's app. Only devices that already hold a pin, and discovery, need app changes.

## Design

### Server (`summareader-sync-server`)

- **New settings `tls_cert` and `tls_key`**, following the existing pattern: flag `--tls-cert`/`--tls-key`, env `SUMMAREADER_TLS_CERT`/`SUMMAREADER_TLS_KEY`, config file, default empty. Precedence is flag > env > file > default, as in `config.go`.
  - When both are set, the server serves that pair instead of `loadOrCreateCertificate` (`tls.go:53`). It serves it through `tls.Config.GetCertificate`, re-reading the files when they change on disk. cert-manager replaces the Secret, and the mounted files change in place.
  - When the files are missing or unreadable at start, the server exits with an error. No fallback to self-signed.
  - A failed reload after start keeps serving the certificate it has and logs the error.
  - Setting only one of the two is a configuration error at start.
  - `--insecure-http` is untouched.
- **Fingerprint in pairing codes:**
  - When a configured certificate is in use, codes drawn by the server (`/operator/pairing`) leave out `server_fingerprint`, and the server address is `publicURL`. Starting with a configured certificate and no `publicURL` is an error at start, so a code never names an address the certificate does not cover.
  - With self-signed, codes are unchanged.
- **mDNS:** the announcement's TXT record gains `url=<publicURL>` when `publicURL` is set (`announce.go`). Everything else in the announcement is unchanged.
- **The web page** (`pairpage.html`, `pairpage.go`):
  - Still at `/pair`, behind the operator token exactly as today.
  - Adds a device list. It uses a new `GET /operator/devices`, behind the operator token, that returns the same device rows `/overview` builds (`overview.go`, `collectOverview`).
  - Rename, revoke, resume and remove call the existing `/operator/rename`, `/operator/revoke`, `/operator/resume` and `/operator/remove`.
  - Removing a device asks for confirmation on the page first.
  - `/overview` stays as it is, behind the metrics token.
- **Changelog, RUNNING.md, README:** document the two settings, the public-certificate mode, and that pairing codes then carry no fingerprint.

### App (`summareader`)

- **Pinned devices move to the public certificate.**
  - When the pinned connection to the configured address fails because a different certificate is offered, the app checks the offered certificate with system roots, for the configured host.
  - If that check passes, the stored pin is dropped (`sync.pin → null`) with a trace line, and sync carries on with no question.
  - If it fails, the existing question flow (`assessCertificate`) runs as today.
  - This deliberately narrows card 713's rule: the comment in `pinning.dart` ("an authority vouching for some other certificate at the same address is exactly the case this exists to refuse"). The comment changes with the code and says why: the owner moved their server to a public certificate for the address they paired with.
- **Discovery uses the announced name.**
  - When a found service's TXT record has `url`, that is the candidate address instead of `https://<ip>:<port>`. It is still probed by `probeSyncServer` and confirmed by the person (card 706), as every candidate is.
  - Without `url`, behaviour is unchanged.
- **Release order:** the app ships before the cluster switches. An older app with a pin would ask the owner to approve the new certificate, and ask again at every renewal.

### Chart (`helm/summareader-sync`) and homenet

- **New values `tls.certManager`:** `enabled` (default `false`), `clusterIssuer`, `secretName` (default `<chart>-tls`).
  - When enabled, the chart renders a cert-manager `Certificate` for the host in `publicURL`, mounts the Secret read-only at `/tls`, and sets `SUMMAREADER_TLS_CERT=/tls/tls.crt` and `SUMMAREADER_TLS_KEY=/tls/tls.key`.
  - Enabled without `publicURL` fails the render, as `pairIngress` does today.
- **`pairIngress` and its template are removed.**
  - The chart's major version goes up. Releases from 1.7.0 on keep a matching chart and server version, so the server's version number goes up accordingly; the server change itself is a feature.
  - The ServersTransport manifest in homenet is no longer needed.
- **Probes stay on HTTPS `/instance`:** the kubelet skips verification on HTTPS probes, so the certificate does not matter to them.
- **homenet** (done by the homenet session):
  - Enable `tls.certManager` with `letsencrypt-production` (DNS-01 via Route53).
  - Remove `pairIngress`, the pair host and the ServersTransport.
  - Update `docs/runbooks/summareader-sync.md`.

## Order of work

1. App: pin migration and discovery `url`. Released.
2. Server and chart: certificate files, reload, codes without fingerprint, mDNS `url`, device list on the page, `tls.certManager`, `pairIngress` removed. Released.
3. homenet: switch values, remove the pair Ingress, verify.
4. Verify on the cluster:
   - `curl` with no `-k` to `/instance` and `/pair` on `summareader-sync.test.home.p4ulie.net`.
   - The iPad keeps syncing and its trace shows the pin dropped.
   - `avahi-browse` shows the `url` TXT entry.

## Calls made after the builder check

Decided by Claude on 2026-11-28 and open to correction:

- **Base:** the work starts from `main` (1.7.0). The `sync-app-icon` checkout predates the pairing page and the chart.
- **Every code the server draws follows the same rule:** `/operator/pairing`, `/operator/enroll` and `first-device` (`main.go`). In public-certificate mode, none of them carries a fingerprint, and all of them name `publicURL`. They share one helper, so they cannot drift apart.
- **Devices stored by IP address:**
  - The migration checks the public certificate against the stored host. A Let's Encrypt certificate never covers an IP, so a device stored as `https://10.39.20.12…` gets the existing approve-this-certificate question instead.
  - That device is paired again from a fresh code, once. The app does not rewrite its address, which would break anti-goal 2.
  - The iPad is stored by DNS name (its trace from 2026-11-27 shows `sync.url = https://summareader-sync.test.home.p4ulie.net`). The other devices are checked during the switch.
- **The `devices` CLI** (`devices_cmd.go`) pins the certificate the server is serving: the configured file when there is one, otherwise the self-signed one, read on every run. It talks to localhost, where a public certificate's name would not match.
- **The fingerprint on the page:** in public-certificate mode, `/operator/pairing` leaves out `fingerprint` and the page does not show one. Nothing replaces it.
- **Versions:** the chart and the server both become 2.0.0. Removing `pairIngress` breaks existing values files, and the two versions stay equal.

## Left to the builder

- How `GetCertificate` notices a change: mtime check on each handshake, or a short-interval stat. Either is fine; nothing else in the server watches files.
- The device list's layout and wording on the page, in the page's existing style.
- How the app recognises "a different certificate was offered" in the failing request path, and where the system-roots check lives; `pinning.dart` is the natural home.
