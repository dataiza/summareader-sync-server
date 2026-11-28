package main

import (
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/grandcat/zeroconf"
)

// The service type this server answers to on a local network.
//
// Registered under the convention for a private protocol: a name nobody else
// uses, over TCP. Clients browse for exactly this and nothing else.
const serviceType = "_summareader-sync._tcp"

// announce publishes this server on the local network over mDNS.
//
// Typing an IP address is the least pleasant part of self-hosting, and the
// address is the one thing a user cannot guess. This makes "the server on my
// network" findable, which is the only case that matters: a server on the
// internet is reached by a name somebody already knows.
//
// Returns a shutdown function, and never an error worth stopping for — a
// server that failed to announce still syncs perfectly well for anyone who
// knows its address, and refusing to start because a multicast socket could
// not be opened would trade the whole feature for the convenience.
//
// A non-empty ip is announced in place of the machine's own addresses. On a
// host-networked pod those belong to whichever node it landed on and change
// when it moves; a load balancer's IP does not.
func announce(addr, instance, ip, url string) func() {
	port, err := portOf(addr)
	if err != nil {
		log.Printf("not announcing on the network: %v", err)
		return func() {}
	}

	if instance == "" {
		host, _ := os.Hostname()
		if host == "" {
			host = "SummaReader sync"
		}
		instance = host
	}

	txt := announceTXT(url)

	var server *zeroconf.Server
	if ip == "" {
		server, err = zeroconf.Register(instance, serviceType, "local.", port, txt, nil)
	} else {
		server, err = zeroconf.RegisterProxy(instance, serviceType, "local.", port,
			"summareader-sync", []string{ip}, txt, nil)
	}
	if err != nil {
		log.Printf("not announcing on the network: %v", err)
		return func() {}
	}

	if ip != "" {
		log.Printf("announcing %q on the local network as %s at %s", instance, serviceType, ip)
	} else {
		log.Printf("announcing %q on the local network as %s", instance, serviceType)
	}
	return server.Shutdown
}

// announceTXT is what a client learns before connecting. Deliberately nothing
// about the data: this is broadcast to every machine on the network, so it
// carries what is needed to reach the server and not one field more.
//
// The scheme since card 713: every connection is TLS now, and a device that
// found this server and spoke plain HTTP to it would be refused. Its absence
// is how an older server says http.
//
// The public address is broadcast too, because it is already public (it is in
// DNS), and a device needs the name for the certificate to match. Left out
// when there is none, so a server without one announces exactly as before.
func announceTXT(url string) []string {
	txt := []string{"software=summareader-sync-server", "scheme=https"}
	if url != "" {
		txt = append(txt, "url="+url)
	}
	return txt
}

// portOf pulls the port out of a listen address like "0.0.0.0:8099".
func portOf(addr string) (int, error) {
	idx := strings.LastIndex(addr, ":")
	if idx < 0 {
		return 0, errNoPort{addr}
	}
	return strconv.Atoi(addr[idx+1:])
}

type errNoPort struct{ addr string }

func (e errNoPort) Error() string { return "no port in " + e.addr }
