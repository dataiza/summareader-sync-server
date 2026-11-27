# Helm chart, pairing page, announced address — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Run the sync server on the owner's RKE2 cluster from a Helm chart, with a password-protected `/pair` page that shows the pairing QR code and an mDNS announcement naming the LoadBalancer IP.

**Architecture:** Two new config settings (`announce_ip`, `public_url`) in the existing flag → env → file chain. A `/pair` HTML page embedded in the binary, plus one operator route pair (`GET`/`POST /operator/pairing`) behind the existing operator token. A chart in `helm/summareader-sync/` modelled on `~/Work/Personal/daikin_onecta_exporter/helm/daikin-onecta-exporter/`, and an ArgoCD app in `~/Work/Personal/homenet/kubernetes/apps/summareader-sync/`.

**Tech Stack:** Go 1.26, PocketBase, `github.com/grandcat/zeroconf` (present), `github.com/skip2/go-qrcode` (new), Helm 3, ArgoCD, External Secrets (OpenBao), Cilium LB-IPAM, k8s-gateway.

**Spec:** `docs/superpowers/specs/2026-11-27-helm-chart-design.md`

## Global Constraints

- Work on branch `helm-chart` off `main` in `summareader-sync-server`; branch `summareader-sync` off `main` in `homenet`. Commit, do not push, do not tag.
- Config precedence everywhere: flag > environment > config file > default (`config.go:resolveConfig`).
- Chart `version` == `appVersion` == `version` const in `version.go` (currently `1.5.1`) == image tag.
- Image: `10.34.1.1:30095/summareader-sync`. Container port `8099`, uid/gid `10001`, data at `/data`.
- QR payload exactly as `console/lib/src/pairing.dart`: `{"version":2,"server":…,"device_token":…,"server_fingerprint":…}`; fingerprint is `fingerprintOf` output (lowercase hex SHA-256 of the DER), the same format the console uses.
- Operator token is the password: `SUMMAREADER_OPERATOR_TOKEN`, checked by existing `operatorOK` (404 when unset, 401 when wrong).
- No JavaScript or CSS from outside the binary on `/pair`. `Cache-Control: no-store` on `/pair` and `/operator/pairing`.
- No TLS termination outside the pod.
- Comments follow the repo's style: say *why*, cite the card/spec where it applies.
- Run before every commit in this repo: `gofmt -l .` (empty), `go vet ./...`, `go test -race ./...`.

## Deviation from the spec (decided while planning)

The spec names `POST /operator/first-device` plus the existing `POST /operator/enroll`. This plan uses one route, `POST /operator/pairing`, which creates the first library when there are none, enrolls into the one library when there is one, and refuses with 409 when there are several. Same behaviour — a second library can still never be made from the page — with one endpoint for the page to call and the QR built in one place.

## Review Focus

1. **Wrong password typed on a phone** — the page must say "wrong password" and stay on the form, not show an empty QR. Test in Task 2 (`GET /operator/pairing` → 401 with wrong token).
2. **Two browsers press *Create first device* at once** — must not create two libraries. Test in Task 2 (second POST after the first enrolls instead of creating).
3. **`public_url` with a trailing slash or a path** — the QR must carry the URL as given, trimmed of a trailing `/`, since the app appends paths. Test in Task 2.
4. **`publicURL` unset behind the LoadBalancer** — falls back to `https://` + request `Host`, which includes the port. Test in Task 2.
5. **Chart rendered with `hostNetwork: false`** — must set `SUMMAREADER_NO_ANNOUNCE=1` and drop `dnsPolicy`. Asserted in Task 3's render check.

---

### Task 1: `announce_ip` and `public_url` settings; announce the given IP

**Files:**
- Modify: `config.go` (struct `Config`, `resolveConfig`)
- Modify: `announce.go` (`announce`)
- Modify: `main.go:112` (call site), persistent flags block near `main.go:166`
- Modify: `summareader-sync.example.json`, `RUNNING.md` (configuration table)
- Test: `config_test.go`

**Interfaces:**
- Produces: `Config.AnnounceIP string` (json `announce_ip`, env `SUMMAREADER_ANNOUNCE_IP`, flag `--announce-ip`); `Config.PublicURL string` (json `public_url`, env `SUMMAREADER_PUBLIC_URL`, flag `--public-url`); package var `publicURL string` holding the resolved value for Task 2; `announce(addr, instance, ip string) func()`.

- [ ] **Step 1: Write the failing test** in `config_test.go`:

```go
func TestAnnounceIPAndPublicURL(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `{"announce_ip":"10.0.0.9","public_url":"https://file:1"}`)
	cfg, err := resolveConfig([]string{"x", "serve", "--dir=" + dir}, envOf(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AnnounceIP != "10.0.0.9" || cfg.PublicURL != "https://file:1" {
		t.Fatalf("file was not read: %+v", cfg)
	}
	cfg, _ = resolveConfig([]string{"x", "serve", "--dir=" + dir}, envOf(map[string]string{
		"SUMMAREADER_ANNOUNCE_IP": " 10.39.20.12 ",
		"SUMMAREADER_PUBLIC_URL":  "https://summareader-sync.test.home.p4ulie.net:8099",
	}))
	if cfg.AnnounceIP != "10.39.20.12" || cfg.PublicURL != "https://summareader-sync.test.home.p4ulie.net:8099" {
		t.Fatalf("environment did not win: %+v", cfg)
	}
}
```

- [ ] **Step 2: Run it, expect FAIL** — `go test -run TestAnnounceIPAndPublicURL .` → `cfg.AnnounceIP undefined`.
- [ ] **Step 3: Add the two fields and their env reads** (`strings.TrimSpace`, like `SUMMAREADER_NAME`). Add `--announce-ip` and `--public-url` as root persistent `StringVar` flags defaulting to the resolved settings, following the `--no-announce` pattern and its comment about why it is on the root. `publicURL` is the package var the flag writes into.
- [ ] **Step 4: Change `announce`** to take `ip string`. Empty: unchanged `zeroconf.Register`. Set: `zeroconf.RegisterProxy(instance, serviceType, "local.", port, "summareader-sync", []string{ip}, txt, nil)` with the same TXT records; log `announcing %q on the local network as %s at %s`. Comment why: on `hostNetwork` the pod's own addresses are a node's, which change when it moves; the LB IP does not. Pass the flag's value at `main.go:112`.
- [ ] **Step 5: Run** `go test -race ./...` → PASS; `go vet ./...`; `gofmt -l .` → empty.
- [ ] **Step 6: Document** both keys in `summareader-sync.example.json` (with `_announce_ip` / `_public_url` notes in the file's style) and two rows in the RUNNING.md configuration table.
- [ ] **Step 7: Commit** — `Announce a given IP, and name the address a pairing code carries`.

### Task 2: `/pair` page and `/operator/pairing`

**Files:**
- Create: `pairpage.go`, `pairpage.html`, `pairpage_test.go`
- Modify: `main.go:registerRoutes` (three routes), `go.mod`/`go.sum` (`go get github.com/skip2/go-qrcode`)
- Modify: `RUNNING.md` (new section "Pairing from a browser", linked from the contents list)

**Interfaces:**
- Consumes: `publicURL` (Task 1), `operatorOK`, `createAccount`, `enrollAndTell(app, account, label, eventEnrolled, "operator")`, `loadOrCreateCertificate(dir) (tls.Certificate, string, error)`, `freeLabel`.
- Produces:
  - `GET /pair` → `handlePairPage`: the embedded HTML; 404 when `operatorToken() == ""`; no auth otherwise (the page holds nothing until the password is typed).
  - `GET /operator/pairing` → `handlePairingState`: `{"libraries": int, "fingerprint": string}`; operator auth.
  - `POST /operator/pairing` body `{"label": string}` → `handlePairingCode`: `{"first": bool, "server": string, "payload": string, "qr_svg": string, "fingerprint": string}`; 0 libraries → `createAccount(app, "My library", label)`, 1 → enroll into it, >1 → 409 `{"error":"more than one library here — use the console"}`. Label defaults to `"A new device"`.
  - `pairingServerURL(e *core.RequestEvent) string`: `strings.TrimRight(publicURL, "/")` if set, else `"https://" + e.Request.Host`.
  - `pairingPayload(server, token, fingerprint string) string`: JSON with exactly the four keys, in the console's order.

- [ ] **Step 1: Write the failing tests** in `pairpage_test.go`, using `newTestApp(t)` and a `RequestEvent` built the way `callDevices` in `append_test.go` builds one; set the token with `t.Setenv("SUMMAREADER_OPERATOR_TOKEN", "op")`:
  - `TestPairPageIsOffWithoutAToken` — no env → `handlePairPage` returns a 404 error.
  - `TestPairingWantsThePassword` — `GET /operator/pairing` with `Bearer wrong` → 401.
  - `TestFirstPairingCreatesTheLibraryThenEnrolls` — first POST → `first == true`, one account; second POST → `first == false`, still one account, two devices (`listDevices`).
  - `TestPairingRefusesWithTwoLibraries` — two `createAccount` calls, POST → 409.
  - `TestPairingPayloadMatchesTheConsole` — `json.Unmarshal(pairingPayload("https://h:1","t","ab"))` has exactly keys `version` (2), `server`, `device_token`, `server_fingerprint`.
  - `TestPairingServerURL` — `publicURL = "https://x:8099/"` → `"https://x:8099"`; `publicURL = ""`, request Host `10.39.20.12:8099` → `"https://10.39.20.12:8099"`.
  - `TestPairingIsNotCached` — the POST response has `Cache-Control: no-store`.
  - The issued token resolves: `accountForToken(app, token)` succeeds (as in `operator_test.go:238`).
- [ ] **Step 2: Run, expect FAIL** — `go test -run 'Pair' .` → undefined handlers.
- [ ] **Step 3: Implement `pairpage.go`.** `//go:embed pairpage.html`. QR: `qrcode.New(payload, qrcode.Medium)` and write an SVG from its `Bitmap()` (one `<rect>` per dark module, white background with a 4-module quiet zone) — go-qrcode has no SVG writer, and an inline SVG keeps the page free of images and scripts from elsewhere. Count libraries with the same `RecordQuery(collAccounts)` as `handleOperatorEnroll`. Do the count-and-create inside `e.App.RunInTransaction` so two simultaneous first presses cannot both create (Review Focus 2).
- [ ] **Step 4: Implement `pairpage.html`.** One file, inline CSS and JS, readable at phone width, light/dark via `prefers-color-scheme`. Flow: password field → `GET /operator/pairing` (401 → "Wrong password", stays) → button *Create first device* when `libraries == 0`, else *Add a device*, with a device-name field → `POST` → show `qr_svg`, the fingerprint in groups (`ab:cd:…`), the payload as copyable text, and the warning that the code is a live device token, shown once. Password held in a JS variable only. Lead text: the browser warns once about the certificate; compare the fingerprint shown here with the one in the server log.
- [ ] **Step 5: Register routes** in `registerRoutes` next to the other operator routes, with one comment line each.
- [ ] **Step 6: Run** `go test -race ./...` → PASS; vet; gofmt.
- [ ] **Step 7: Smoke it** — `SUMMAREADER_OPERATOR_TOKEN=op go run . serve --http=127.0.0.1:18099 --dir=$(mktemp -d) --no-announce`, then `curl -sk -X POST https://127.0.0.1:18099/operator/pairing -H 'Authorization: Bearer op' -d '{"label":"Phone"}'` → JSON with `"first":true` and an `<svg` in `qr_svg`; `curl -sk -o /dev/null -w '%{http_code}' https://127.0.0.1:18099/pair` → `200`.
- [ ] **Step 8: Document** in RUNNING.md, then **commit** — `Pair a device from a browser: /pair, behind the operator token`.

### Task 3: Helm chart and image push script

**Files:**
- Create: `helm/summareader-sync/{Chart.yaml,values.yaml,.helmignore}`, `helm/summareader-sync/templates/{_helpers.tpl,deployment.yaml,service.yaml,pvc.yaml}`
- Create: `scripts/push-image.sh`, `chart_test.go`
- Modify: `RUNNING.md` (new section "Kubernetes")

**Interfaces:**
- Consumes: env names from Tasks 1–2 and the existing `SUMMAREADER_OPERATOR_TOKEN`, `SUMMAREADER_METRICS_TOKEN`, `SUMMAREADER_NO_ANNOUNCE`.
- Produces (values homenet sets in Task 4): `image.repository`, `image.tag`, `hostNetwork`, `service.loadBalancerIP`, `service.hostname`, `publicURL`, `operatorToken.secretName` (key `operator-token`), `metricsToken.secretName` (key `metrics-token`), `persistence.size`, `persistence.storageClass`, `resources`.

- [ ] **Step 1: Write the failing test** `chart_test.go`:
  - `TestChartVersionMatchesTheServer` — reads `helm/summareader-sync/Chart.yaml` with `os.ReadFile` and line matching (no YAML dependency); `version` and `appVersion` both equal the `version` const.
  - `TestChartRenders` — `t.Skip` when `exec.LookPath("helm")` fails. `helm template t helm/summareader-sync --set operatorToken.secretName=s --set service.loadBalancerIP=10.39.20.12` output contains `hostNetwork: true`, `dnsPolicy: ClusterFirstWithHostNet`, `lbipam.cilium.io/ips: 10.39.20.12`, `coredns.io/hostname: summareader-sync`, `SUMMAREADER_ANNOUNCE_IP`, `type: Recreate`; with `--set hostNetwork=false` it contains `SUMMAREADER_NO_ANNOUNCE` and not `hostNetwork: true` (Review Focus 5); without `operatorToken.secretName` `helm template` fails with `operatorToken.secretName is required`.
- [ ] **Step 2: Run, expect FAIL** — `go test -run Chart .` → no Chart.yaml.
- [ ] **Step 3: Write the chart**, copying the structure and comment style of the daikin chart. Values defaults as in the spec table. Deployment: 1 replica, `Recreate`, `enableServiceLinks: false`, pod `securityContext` `runAsNonRoot`, uid/gid/fsGroup `10001`, `seccompProfile: RuntimeDefault`; container `allowPrivilegeEscalation: false`, `readOnlyRootFilesystem: true`, drop ALL; `emptyDir` at `/tmp`; command `summareader-sync serve --http=0.0.0.0:8099 --dir=/data`; env `SUMMAREADER_OPERATOR_TOKEN` from the secret, `SUMMAREADER_METRICS_TOKEN` only when its secret is set, `SUMMAREADER_PUBLIC_URL` when `publicURL` set, `SUMMAREADER_ANNOUNCE_IP` = `service.loadBalancerIP` when `hostNetwork`, else `SUMMAREADER_NO_ANNOUNCE: "1"`; readiness and liveness `httpGet /instance` port 8099 `scheme: HTTPS`, liveness `initialDelaySeconds: 30` (first start migrates the schema). Service: `LoadBalancer`, port/targetPort 8099, annotations `lbipam.cilium.io/ips` (when set) and `coredns.io/hostname`. PVC: ReadWriteOnce, `persistence.size`, storage class only when set.
- [ ] **Step 4: Write `scripts/push-image.sh`** — `set -eu`; `registry=${REGISTRY:-10.34.1.1:30095}`; version from `Chart.yaml` like daikin's `image:push` task; `docker build -t "$registry/summareader-sync:$v" . && docker push …`. `chmod +x`.
- [ ] **Step 5: Run** `go test -race ./...` → PASS (chart test runs; helm is at `/usr/bin/helm`); `helm lint helm/summareader-sync --set operatorToken.secretName=s` → `0 chart(s) failed`.
- [ ] **Step 6: Document** in RUNNING.md: the chart, the values, `hostNetwork` and why the namespace needs `privileged`, the EdgeRouter `set service mdns repeater interface …` lines, the firewall rule (TCP 8099), first pairing via `/pair`.
- [ ] **Step 7: README.md** (owner's request) — under `## Use`, before `## Licence`, add `### Kubernetes` (two lines: the chart's path, link to RUNNING.md's section) and `### Server and devices on different networks`. That section, written for any user, not only this cluster: mDNS reaches one network segment only, so a server on another VLAN or subnet is not discovered unless the router repeats mDNS between them; the address still works when typed or scanned. Give the repeater setting for: EdgeRouter/EdgeOS (`set service mdns repeater interface <iface>` per network, `commit ; save`), UniFi (Settings → Networks → Multicast DNS), OPNsense/pfSense (the `mdns-repeater` / Avahi package, both interfaces), and a Linux router (`avahi-daemon` with `enable-reflector=yes`). Then what else must hold: firewall allows UDP 5353 between the networks and TCP 8099 from devices to the server; in Kubernetes the pod needs `hostNetwork: true` and `announce_ip` set to the LoadBalancer IP (the chart does both); in Docker the container needs `network_mode: host` for the announcement to leave the host. Link each to RUNNING.md's "Finding it on a local network".
- [ ] **Step 8: Commit** — `Helm chart for the cluster, and a script to push its image`.

### Task 4: homenet ArgoCD app

**Repo:** `~/Work/Personal/homenet`, branch `summareader-sync`.

**Files:**
- Create: `kubernetes/apps/summareader-sync/application.yaml`, `kubernetes/apps/summareader-sync/values.yaml`, `kubernetes/apps/summareader-sync/manifests/externalsecret.yaml`

**Interfaces:**
- Consumes: Task 3's values.

- [ ] **Step 1: Write `application.yaml`** copying `kubernetes/apps/daikin-onecta-exporter/application.yaml`: header comment in its style; source `https://github.com/dataiza/summareader-sync-server.git` (public, so no Argo credentials needed), `path: helm/summareader-sync`, `targetRevision: v1.5.2` (the next release; Argo cannot sync until that tag and its image exist — say so in the header comment); values and manifests from homenet with `"@branch@"`; namespace `summareader-sync`; `managedNamespaceMetadata.labels` `pod-security.kubernetes.io/enforce: privileged` with a comment that baseline forbids `hostNetwork`.
- [ ] **Step 2: Write `values.yaml`** — `service.loadBalancerIP: 10.39.20.12`, `publicURL: https://summareader-sync.test.home.p4ulie.net:8099`, `operatorToken.secretName: summareader-sync`.
- [ ] **Step 3: Write `manifests/externalsecret.yaml`** copying daikin's, every defaulted field spelled out: target Secret `summareader-sync`, one key `operator-token` ← OpenBao key `summareader-sync`, property `operator-token`. Header: the owner writes it first (`openssl rand -hex 24`).
- [ ] **Step 4: Verify** — `kubectl apply --dry-run=client -f kubernetes/apps/summareader-sync/application.yaml` and `-f kubernetes/apps/summareader-sync/manifests/externalsecret.yaml` → `created (dry run)`; `helm template t <sync-server>/helm/summareader-sync -f kubernetes/apps/summareader-sync/values.yaml` renders. Confirm `10.39.20.12` is unused: `grep -rn '10\.39\.20\.12' kubernetes` empty and absent from `kubectl get svc -A`.
- [ ] **Step 5: Commit** — `summareader-sync: ArgoCD app, values and operator token`.
