package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

const chartDir = "helm/summareader-sync"

// The chart's version, appVersion and the image tag are one number; the image
// push script tags with the chart version, so a drift ships the wrong image.
func TestChartVersionMatchesTheServer(t *testing.T) {
	b, err := os.ReadFile(chartDir + "/Chart.yaml")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, l := range strings.Split(string(b), "\n") {
		for _, k := range []string{"version", "appVersion"} {
			if v, ok := strings.CutPrefix(l, k+":"); ok {
				got[k] = strings.Trim(strings.TrimSpace(v), `"`)
			}
		}
	}
	for _, k := range []string{"version", "appVersion"} {
		if got[k] != version {
			t.Errorf("Chart.yaml %s = %q, want %q", k, got[k], version)
		}
	}
}

func helmTemplate(t *testing.T, args ...string) (string, error) {
	t.Helper()
	a := append([]string{"template", "t", chartDir}, args...)
	out, err := exec.Command("helm", a...).CombinedOutput()
	return string(out), err
}

func TestChartRenders(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not installed")
	}
	base := []string{"--set", "operatorToken.secretName=s", "--set", "service.loadBalancerIP=10.39.20.12"}

	out, err := helmTemplate(t, base...)
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, out)
	}
	for _, want := range []string{
		"hostNetwork: true", "dnsPolicy: ClusterFirstWithHostNet",
		"lbipam.cilium.io/ips: 10.39.20.12", "coredns.io/hostname: summareader-sync",
		"SUMMAREADER_ANNOUNCE_IP", "type: Recreate", "port: 443", "port: 8099",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("default render lacks %q", want)
		}
	}

	out, err = helmTemplate(t, append(base, "--set", "metricsToken.secretName=m",
		"--set", "publicURL=https://x", "--set", "name=N")...)
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, out)
	}
	for _, want := range []string{"SUMMAREADER_METRICS_TOKEN", "SUMMAREADER_PUBLIC_URL", "SUMMAREADER_NAME"} {
		if !strings.Contains(out, want) {
			t.Errorf("optional-values render lacks %q", want)
		}
	}

	// A public certificate is off by default: no Certificate, no volume, no variables.
	for _, bad := range []string{"kind: Certificate", "SUMMAREADER_TLS_CERT", "/tls", "kind: Ingress"} {
		if strings.Contains(out, bad) {
			t.Errorf("default render has %q", bad)
		}
	}

	// cert-manager: the Certificate is issued for publicURL's host, and the pod reads it from the Secret.
	cm := append(base, "--set", "tls.certManager.enabled=true", "--set", "tls.certManager.clusterIssuer=le")
	out, err = helmTemplate(t, append(cm, "--set", "publicURL=https://sync.example:8443")...)
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, out)
	}
	for _, want := range []string{
		"kind: Certificate", "- sync.example\n", "kind: ClusterIssuer", "name: le",
		"secretName: summareader-sync-tls", "defaultMode: 0440", "mountPath: /tls", "readOnly: true",
		"fsGroup: 10001", "value: /tls/tls.crt", "value: /tls/tls.key",
		"SUMMAREADER_TLS_CERT", "SUMMAREADER_TLS_KEY",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("certManager render lacks %q\n%s", want, out)
		}
	}
	if strings.Contains(out[strings.Index(out, "dnsNames"):], "8443") {
		t.Error("dnsNames keeps the port")
	}
	if strings.Contains(out, "kind: Ingress") {
		t.Error("certManager render has an Ingress")
	}
	out, err = helmTemplate(t, append(cm, "--set", "publicURL=https://sync.example", "--set", "tls.certManager.secretName=mine")...)
	if err != nil || !strings.Contains(out, "secretName: mine") {
		t.Errorf("tls.certManager.secretName not used, err=%v", err)
	}
	for _, url := range []string{"", " "} {
		out, err := helmTemplate(t, append(cm, "--set", "publicURL="+url)...)
		if err == nil || !strings.Contains(out, "publicURL is required with tls.certManager: the certificate is issued for its host") {
			t.Errorf("certManager with publicURL %q should fail with the required message, err=%v\n%s", url, err, out)
		}
	}
	if out, err := helmTemplate(t, append(cm, "--set", "publicURL=sync.example")...); err == nil || !strings.Contains(out, "has no host") {
		t.Errorf("certManager with a schemeless publicURL should fail with a no-host message, err=%v\n%s", err, out)
	}
	if out, err := helmTemplate(t, append(base, "--set", "tls.certManager.enabled=true", "--set", "publicURL=https://sync.example")...); err == nil {
		t.Errorf("certManager without clusterIssuer rendered:\n%s", out)
	}
	// The removed pairIngress values are ignored, not an error, and render no Ingress.
	out, err = helmTemplate(t, append(base, "--set", "pairIngress.enabled=true", "--set", "pairIngress.host=p.example")...)
	if err != nil || strings.Contains(out, "kind: Ingress") {
		t.Errorf("pairIngress should be ignored, err=%v", err)
	}

	out, err = helmTemplate(t, append(base, "--set", "service.labels.pool=static")...)
	if err != nil || !strings.Contains(out, "pool: static") {
		t.Errorf("service.labels not rendered, err=%v", err)
	}

	// Without host networking the announcement cannot leave the pod, so it is
	// switched off and the host-network DNS policy must not be left behind.
	out, err = helmTemplate(t, append(base, "--set", "hostNetwork=false")...)
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, out)
	}
	if !strings.Contains(out, "SUMMAREADER_NO_ANNOUNCE") {
		t.Error("hostNetwork=false should set SUMMAREADER_NO_ANNOUNCE")
	}
	for _, bad := range []string{"hostNetwork: true", "dnsPolicy", "SUMMAREADER_ANNOUNCE_IP"} {
		if strings.Contains(out, bad) {
			t.Errorf("hostNetwork=false render still has %q", bad)
		}
	}

	// No LB IP: omit the variable, so the pod announces the node's own address.
	out, err = helmTemplate(t, "--set", "operatorToken.secretName=s")
	if err != nil || strings.Contains(out, "SUMMAREADER_ANNOUNCE_IP") {
		t.Errorf("hostNetwork with no loadBalancerIP should omit SUMMAREADER_ANNOUNCE_IP, err=%v", err)
	}

	out, err = helmTemplate(t)
	if err == nil || !strings.Contains(out, "operatorToken.secretName is required") {
		t.Errorf("missing secret name should fail with the required message, got err=%v\n%s", err, out)
	}
}
