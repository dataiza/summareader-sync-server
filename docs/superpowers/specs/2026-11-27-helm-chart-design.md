# Helm chart, pairing page, announced address — design

## Why

The sync server should run on the owner's RKE2 cluster (ArgoCD, Cilium LB-IPAM, ceph-rbd,
OpenBao via External Secrets), not on a desktop. A pod has no desktop console, so pairing needs
another way in: a small password-protected web page that shows the pairing QR code. Discovery on
the LAN should keep working across VLANs via the EdgeRouter's mDNS repeater.

## Goals

- A Helm chart in this repo, installed by ArgoCD from `homenet`, same layout as `daikin-onecta-exporter`.
- One stable address for every device: a LoadBalancer IP with DNS name `summareader-sync`.
- A pairing page in the server binary: password, then a QR code to scan.
- mDNS announcement that names the stable address, not the node the pod happens to be on.

## Non-goals

- Publishing the image or the chart publicly (GHCR, chart repo).
- More than one replica. SQLite on one ReadWriteOnce volume.
- TLS termination anywhere but the pod. The app pins the server's own certificate; a proxy
  certificate would break pairing.

## Approaches considered (picked: A)

- **A. LoadBalancer Service with a fixed IP, `hostNetwork` only for announcing** — picked. Address
  is stable whichever node runs the pod; matches mqtt, syncthing, lnd here. Costs one server
  setting so the announcement names the LB IP.
- B. `hostNetwork` plus `nodeSelector` pinning to one worker. No server change, but sync stops when
  that worker is down.
- C. Both. More values for no current need.

## Server changes

1. **`SUMMAREADER_ANNOUNCE_IP`** (also `--announce-ip`, key `announce_ip`, same precedence as the
   rest). When set, `announce.go` uses `zeroconf.RegisterProxy` with that IP instead of
   `zeroconf.Register`, so the advertised A record is the LB IP. Unset: behaviour unchanged.
2. **`SUMMAREADER_PUBLIC_URL`** (`--public-url`, `public_url`). The address a QR code names. A pod
   cannot know how a phone reaches it. Unset: the page uses the request's `Host`.
3. **Pairing page at `/pair`**, embedded with `go:embed`.
   - Off (404) unless the operator token is set, like `/metrics`. Password = the existing
     `SUMMAREADER_OPERATOR_TOKEN`; no new secret. Checked with the existing constant-time compare.
   - Page asks for the password, keeps it in page memory only (not storage, not a cookie), and
     sends it as `Authorization: Bearer` to the operator routes.
   - No library yet → button *Create first device* → new `POST /operator/first-device`, which calls
     the same code as the `first-device` CLI command. Refused once any library exists, so the page
     cannot create a second library by accident (RUNNING.md's warning).
   - A library exists → *Add a device* → existing `POST /operator/enroll`.
   - Shows the QR (payload exactly as `console/lib/src/pairing.dart`: `version 2`, `server`,
     `device_token`, `server_fingerprint`), the same payload as text, and the fingerprint.
   - QR rendered server-side as SVG by `github.com/skip2/go-qrcode` (my call: no JS from a CDN on a
     page that holds a live token).
   - A `Cache-Control: no-store` header on every response from these routes.
4. Tests: announce-IP config resolution, `/pair` 404 without token, 401 with wrong token,
   first-device refused when a library exists, payload field names match the console's.

## Chart (`helm/summareader-sync/`)

Chart `version` == `appVersion` == server version == image tag, like daikin.

| value | default | notes |
|---|---|---|
| `image.repository` | `10.34.1.1:30095/summareader-sync` | tag empty = appVersion |
| `hostNetwork` | `true` | true: announce on, `dnsPolicy: ClusterFirstWithHostNet`; false: `no_announce` |
| `service.loadBalancerIP` | `""` | rendered as `lbipam.cilium.io/ips` |
| `service.hostname` | `summareader-sync` | rendered as `coredns.io/hostname` for k8s-gateway |
| `publicURL` | `""` | → `SUMMAREADER_PUBLIC_URL` |
| `operatorToken.secretName` | `""` (required) | existing Secret, key `operator-token`; chart never creates it |
| `metricsToken.secretName` | `""` | optional, key `metrics-token` |
| `persistence.size` / `.storageClass` | `1Gi` / `""` | ceph-rbd by default |
| `resources` | small requests, memory limit | |

Templates: Deployment (1 replica, `Recreate`, uid/gid 10001, `fsGroup`, read-only root with an
`emptyDir` at `/tmp`, probes `GET /instance` over HTTPS), Service (LoadBalancer, port 8099),
PVC, `_helpers.tpl`. `SUMMAREADER_ANNOUNCE_IP` is set from `service.loadBalancerIP`.

Image push: `scripts/push-image.sh`, version read from `helm/summareader-sync/Chart.yaml`
(same as daikin's `image:push`, in this repo's `scripts/` convention).

## homenet (`kubernetes/apps/summareader-sync/`)

- `application.yaml`: chart at the release tag, values from homenet, namespace `summareader-sync`,
  **`pod-security.kubernetes.io/enforce: privileged`** (baseline forbids `hostNetwork`).
- `values.yaml`: `service.loadBalancerIP: 10.39.20.12` (next free in the pool, my call),
  `publicURL: https://summareader-sync.test.home.p4ulie.net:8099`, secret names.
- `manifests/externalsecret.yaml`: OpenBao `kv/summareader-sync`, property `operator-token`.
  The owner writes it first: `openssl rand -hex 24`.

## Owner steps outside the repos

- EdgeRouter: `set service mdns repeater interface <LAN VLAN>` and `<cluster VLAN>`.
- Firewall: allow TCP 8099 from the device VLANs to 10.39.20.12.

## Left to the builder

- Exact page styling (plain, readable on a phone).
- Resource numbers.

## Assumption to check

- Phones resolve `*.test.home.p4ulie.net` (Pi-hole forwarding to k8s-gateway). Checked from the
  owner's desktop: `k8s-gateway.k8s-gateway.test.home.p4ulie.net` → `10.39.20.2`. If a phone does
  not, `publicURL` uses the IP.
