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
func announce(addr, instance string) func() {
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

	server, err := zeroconf.Register(
		instance,
		serviceType,
		"local.",
		port,
		// What a client learns before connecting. Deliberately nothing about
		// the data: this is broadcast to every machine on the network, so it
		// carries what is needed to reach the server and not one field more.
		[]string{"software=summareader-sync-server"},
		nil,
	)
	if err != nil {
		log.Printf("not announcing on the network: %v", err)
		return func() {}
	}

	log.Printf("announcing %q on the local network as %s", instance, serviceType)
	return server.Shutdown
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
