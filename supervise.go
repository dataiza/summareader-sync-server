package main

import (
	"errors"
	"log"
	"os"
	"runtime"
	"syscall"
	"time"
)

// How often the supervisor is looked in on. Cheap enough to do often, and the
// gap is the window in which the address stays taken after the console that
// held it has gone — the same two seconds the console's own status poll uses.
const superviseEvery = 2 * time.Second

// superviseParent ends this server when the process that started it is gone.
//
// The console starts the server as a child and stops it again when its window
// closes. A console that is killed outright, or whose session simply ends, gets
// no chance to: the child is re-parented and carries on serving. One was found
// that way two days after its window had gone, holding the address the next
// console then could not bind — and with no window anywhere to say so.
//
// Polling a pid rather than reading an inherited pipe until EOF, which is the
// classic answer and needs no ticker: the console hands its child the same
// stdio it holds itself, so the server's log lands where the console's does,
// and there is no spare descriptor to be had without giving that up.
//
// Leaving by interrupt and not by exiting, because that is the signal
// PocketBase waits for before it closes the database — the same one the console
// sends when it stops the child politely. An orphan traded for a database cut
// off mid-write is not a fix.
func superviseParent(pid int) {
	ticker := time.NewTicker(superviseEvery)
	defer ticker.Stop()
	for range ticker.C {
		if !processGone(pid) {
			continue
		}
		log.Printf("the console that started this server (pid %d) is gone; stopping", pid)
		interruptSelf()
		return
	}
}

// processGone reports whether pid no longer belongs to a live process.
//
// Signal 0 is the portable way to ask: it is delivered to nobody and only the
// error says whether there was anybody to deliver it to. A pid owned by another
// user answers "not permitted", which is still an answer that it is there.
//
// A pid can be reused, and a server whose supervisor's number has been handed
// to something else keeps running — which is exactly what it does today, so the
// worst case of being wrong here is the behaviour this replaces.
func processGone(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return true
	}
	// On Windows FindProcess opens a handle and its failure above is the whole
	// of the answer; there is no signal to send afterwards.
	if runtime.GOOS == "windows" {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err != nil && !errors.Is(err, os.ErrPermission)
}

// interruptSelf asks this process to shut down the way Ctrl-C would.
//
// Windows cannot: there is no way to hand your own process the interrupt
// PocketBase is waiting for, and terminating it instead would be the mid-write
// cut this exists to avoid. Nothing is done there, which leaves a Windows
// console exactly where it was — its own signal handling still covers every
// exit short of a hard kill.
func interruptSelf() {
	if runtime.GOOS == "windows" {
		log.Print("no way to interrupt this process on this platform; carrying on")
		return
	}
	process, err := os.FindProcess(os.Getpid())
	if err != nil {
		log.Printf("could not signal this process: %v", err)
		return
	}
	if err := process.Signal(os.Interrupt); err != nil {
		log.Printf("could not interrupt this process: %v", err)
	}
}
