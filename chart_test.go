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
		"SUMMAREADER_ANNOUNCE_IP", "type: Recreate",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("default render lacks %q", want)
		}
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
