// Command allreader-sync-server is the one sync implementation.
//
// Self-hosted for free and hosted as the paid tier are the same binary with a
// different URL — there is no "cloud edition". That is only sustainable
// because the server is deliberately incapable: it stores opaque ciphertext
// against sequence numbers and never learns what any of it means.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/spf13/cobra"
)

// Set by --no-announce. Some networks would rather nothing multicast at all,
// and a hosted instance has no reason to shout on the network it happens to
// sit in.
var noAnnounce bool

// Held for the life of the process. See the comment in OnServe.
var stopAnnouncing = func() {}

func main() {
	app := pocketbase.New()

	app.OnBootstrap().BindFunc(func(e *core.BootstrapEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		return ensureSchema(e.App)
	})

	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		registerRoutes(e)

		// Findable on a local network, so self-hosting does not begin with
		// reading an IP address off a router. Only ever a convenience: the
		// address still works, and a server that could not announce itself
		// says so and carries on.
		//
		// Kept in a variable that outlives this function rather than deferred
		// into it. OnServe fires *before* serving starts and returns straight
		// away, so a deferred shutdown here unregistered the announcement
		// about a millisecond after making it — which looked exactly like
		// success in the log and was invisible to every browser on the
		// network. The process exiting is what withdraws it.
		if !noAnnounce {
			stopAnnouncing = announce(e.Server.Addr, e.App.Settings().Meta.AppName)
		}

		return e.Next()
	})

	registerCommands(app)

	// PocketBase owns `serve`; this adds one flag to it rather than wrapping
	// the command, so `serve --help` still lists everything it always did.
	if serve, _, err := app.RootCmd.Find([]string{"serve"}); err == nil {
		serve.Flags().BoolVar(&noAnnounce, "no-announce", false,
			"do not advertise this server on the local network")
	}

	if err := app.Start(); err != nil {
		log.Fatal(err)
		os.Exit(1)
	}
}

// The five operations, frozen.
//
// append, readFrom, putBlob, getBlob, subscribe. Nothing else is offered, and
// that is the point: "query by tag" or "count unread" would double the work
// permanently and mean building a database twice, once per backend. Anything
// the client needs beyond these five, it computes locally from what it has
// already decrypted.
func registerRoutes(e *core.ServeEvent) {
	e.Router.POST("/append", handleAppend)
	e.Router.GET("/from/{seq}", handleReadFrom)
	e.Router.PUT("/blob/{name}", handlePutBlob)
	e.Router.GET("/blob/{name}", handleGetBlob)
	e.Router.GET("/subscribe", handleSubscribe)

	// Deletion is a separate, deliberate operation — never folded into
	// switching servers and never into closing an account.
	e.Router.POST("/wipe", handleWipe)

	// Provisioning. Not part of the five-operation sync contract — these are
	// about who may sync at all, not about moving data.
	e.Router.POST("/enroll", handleEnroll)
	e.Router.GET("/devices", handleListDevices)
	e.Router.POST("/revoke", handleRevoke)

	// Not part of the contract — it exists so a client can tell "wrong URL"
	// from "right URL, no data", which §9.3 turns into four different prompts.
	e.Router.GET("/instance", handleInstance)
}

func authenticate(e *core.RequestEvent) (accountID, deviceID string, ok bool) {
	token := e.Request.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(token) > len(prefix) && token[:len(prefix)] == prefix {
		token = token[len(prefix):]
	}

	accountID, deviceID, err := accountForToken(e.App, token)
	if err != nil {
		return "", "", false
	}
	return accountID, deviceID, true
}

type appendRequest struct {
	Payload string `json:"payload"`
}

type appendResponse struct {
	Seq int64 `json:"seq"`
}

func handleAppend(e *core.RequestEvent) error {
	accountID, deviceID, ok := authenticate(e)
	if !ok {
		return e.JSON(http.StatusUnauthorized, map[string]string{
			"error": "unknown or revoked device",
		})
	}

	var body appendRequest
	if err := e.BindBody(&body); err != nil || body.Payload == "" {
		return e.JSON(http.StatusBadRequest, map[string]string{
			"error": "payload required",
		})
	}

	seq, err := appendEntry(e.App, accountID, deviceID, body.Payload)
	if err != nil {
		if errors.Is(err, ErrNoAccount) {
			return e.JSON(http.StatusUnauthorized, map[string]string{
				"error": "unknown account",
			})
		}
		return e.JSON(http.StatusInternalServerError, map[string]string{
			"error": "append failed",
		})
	}

	return e.JSON(http.StatusOK, appendResponse{Seq: seq})
}

type readResponse struct {
	Entries []LogEntry `json:"entries"`
}

func handleReadFrom(e *core.RequestEvent) error {
	accountID, _, ok := authenticate(e)
	if !ok {
		return e.JSON(http.StatusUnauthorized, map[string]string{
			"error": "unknown or revoked device",
		})
	}

	after := parseInt(e.Request.PathValue("seq"))
	entries, err := readFrom(e.App, accountID, after, 500)
	if err != nil {
		return e.JSON(http.StatusInternalServerError, map[string]string{
			"error": "read failed",
		})
	}

	// An empty list is a completely ordinary answer and must never read as an
	// error — a client that treats "nothing new" as a failure would either
	// spin or, far worse, conclude the account was wiped.
	return e.JSON(http.StatusOK, readResponse{Entries: entries})
}

type blobRequest struct {
	Payload string `json:"payload"`
}

func handlePutBlob(e *core.RequestEvent) error {
	accountID, _, ok := authenticate(e)
	if !ok {
		return e.JSON(http.StatusUnauthorized, map[string]string{
			"error": "unknown or revoked device",
		})
	}

	name := e.Request.PathValue("name")
	if name == "" {
		return e.JSON(http.StatusBadRequest, map[string]string{
			"error": "name required",
		})
	}

	var body blobRequest
	if err := e.BindBody(&body); err != nil || body.Payload == "" {
		return e.JSON(http.StatusBadRequest, map[string]string{
			"error": "payload required",
		})
	}

	if err := putBlob(e.App, accountID, name, body.Payload); err != nil {
		return e.JSON(http.StatusInternalServerError, map[string]string{
			"error": "store failed",
		})
	}
	return e.JSON(http.StatusOK, map[string]string{"name": name})
}

func handleGetBlob(e *core.RequestEvent) error {
	accountID, _, ok := authenticate(e)
	if !ok {
		return e.JSON(http.StatusUnauthorized, map[string]string{
			"error": "unknown or revoked device",
		})
	}

	payload, err := getBlob(e.App, accountID, e.Request.PathValue("name"))
	if err != nil {
		return e.JSON(http.StatusNotFound, map[string]string{
			"error": "no such blob",
		})
	}
	return e.JSON(http.StatusOK, map[string]string{"payload": payload})
}

// handleSubscribe is a change *hint*, nothing more.
//
// It says "there is something new, come and get it" and carries no payload and
// no count. A count would tell whoever carries the message how much this user
// reads and when, which is exactly what the encryption is for.
func handleSubscribe(e *core.RequestEvent) error {
	accountID, _, ok := authenticate(e)
	if !ok {
		return e.JSON(http.StatusUnauthorized, map[string]string{
			"error": "unknown or revoked device",
		})
	}

	account, err := e.App.FindRecordById(collAccounts, accountID)
	if err != nil {
		return e.JSON(http.StatusInternalServerError, map[string]string{
			"error": "unavailable",
		})
	}

	// The receipt rides along, so a device that finds an empty log learns why
	// in the same round trip that told it the log is empty.
	receipt, _ := receiptFor(e.App, accountID)

	// Long-polling rather than a websocket for now: the client already polls
	// on resume, and a socket is a reconnection state machine to maintain for
	// a message that says "poll now".
	return e.JSON(http.StatusOK, map[string]any{
		"head":    account.GetInt("seq"),
		"receipt": receipt,
	})
}

type wipeRequest struct {
	DeviceName  string `json:"device_name"`
	Replacement bool   `json:"replacement"`
}

func handleWipe(e *core.RequestEvent) error {
	accountID, _, ok := authenticate(e)
	if !ok {
		return e.JSON(http.StatusUnauthorized, map[string]string{
			"error": "unknown or revoked device",
		})
	}

	var body wipeRequest
	_ = e.BindBody(&body)
	if body.DeviceName == "" {
		body.DeviceName = "another device"
	}

	result, err := wipeAccount(e.App, accountID, body.DeviceName, body.Replacement)
	if err != nil {
		// Reports the partial result alongside the failure rather than a bare
		// error: "we removed some of it" is actionable, "it failed" is not.
		return e.JSON(http.StatusInternalServerError, map[string]any{
			"error":  err.Error(),
			"result": result,
		})
	}
	return e.JSON(http.StatusOK, result)
}

// handleInstance identifies this server so a client can distinguish an empty
// account from a different server. Without it, pointing at a fresh instance
// looks identical to "everything was deleted".
func handleInstance(e *core.RequestEvent) error {
	settings := e.App.Settings()
	return e.JSON(http.StatusOK, map[string]string{
		"instance": settings.Meta.AppName,
		"software": "allreader-sync-server",
	})
}

func parseInt(raw string) int64 {
	var n int64
	for _, c := range raw {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int64(c-'0')
	}
	return n
}

// registerCommands adds the provisioning CLI.
//
// The first device has to be created from a shell, because until one exists
// there is nobody to authorise the request. Everything after that is enrolled
// by a device that is already paired.
func registerCommands(app *pocketbase.PocketBase) {
	var asJSON bool

	pair := &cobra.Command{
		Use:   "pair [account-label] [device-label]",
		Short: "Create an account and issue a token for its first device",
		Run: func(cmd *cobra.Command, args []string) {
			accountLabel := "My library"
			deviceLabel := "First device"
			if len(args) > 0 {
				accountLabel = args[0]
			}
			if len(args) > 1 {
				deviceLabel = args[1]
			}

			if err := ensureSchema(app); err != nil {
				log.Fatal(err)
			}

			device, err := createAccount(app, accountLabel, deviceLabel)
			if err != nil {
				log.Fatal(err)
			}

			if asJSON {
				encoded, _ := json.Marshal(device)
				// Straight to stdout, not through cobra: PocketBase points the
				// command's writer at stderr, so `$(... --json)` in a script
				// would capture nothing at all. --json exists for scripts, so
				// it has to land where a script looks.
				fmt.Println(string(encoded))
				return
			}

			// Labelled lines rather than a JSON blob, because bootstrapping
			// prints a wall of migration DDL first and a person has to find
			// their token in it. `--json` is there for scripts.
			cmd.Println()
			cmd.Println("──────────────────────────────────────────────")
			cmd.Println("Account: " + device.AccountID)
			cmd.Println("Device:  " + device.DeviceID + "  (" + device.Label + ")")
			cmd.Println("Token:   " + device.Token)
			cmd.Println("──────────────────────────────────────────────")
			cmd.Println()
			cmd.Println("Paste the token into AllReader on this device.")
			cmd.Println("It is shown once and is not recoverable — the server")
			cmd.Println("keeps it only to compare against.")
			cmd.Println()
			cmd.Println("Other devices do not need this command: pair them")
			cmd.Println("from one that is already set up.")
		},
	}

	// RootCmd is a field, not a method — an interface assertion for it
	// compiles and then silently never fires, which is exactly what happened
	// the first time and why this is wired concretely.
	pair.Flags().BoolVar(&asJSON, "json", false, "print the result as JSON")

	app.RootCmd.AddCommand(pair)
}

type enrollRequest struct {
	Label string `json:"label"`
}

func handleEnroll(e *core.RequestEvent) error {
	accountID, _, ok := authenticate(e)
	if !ok {
		return e.JSON(http.StatusUnauthorized, map[string]string{
			"error": "unknown or revoked device",
		})
	}

	var body enrollRequest
	_ = e.BindBody(&body)
	if body.Label == "" {
		body.Label = "A new device"
	}

	device, err := enrollDevice(e.App, accountID, body.Label)
	if err != nil {
		return e.JSON(http.StatusInternalServerError, map[string]string{
			"error": "could not enrol",
		})
	}
	return e.JSON(http.StatusOK, device)
}

func handleListDevices(e *core.RequestEvent) error {
	accountID, _, ok := authenticate(e)
	if !ok {
		return e.JSON(http.StatusUnauthorized, map[string]string{
			"error": "unknown or revoked device",
		})
	}
	devices, err := listDevices(e.App, accountID)
	if err != nil {
		return e.JSON(http.StatusInternalServerError, map[string]string{
			"error": "unavailable",
		})
	}
	return e.JSON(http.StatusOK, map[string]any{"devices": devices})
}

type revokeRequest struct {
	DeviceID string `json:"device_id"`
}

func handleRevoke(e *core.RequestEvent) error {
	accountID, callerID, ok := authenticate(e)
	if !ok {
		return e.JSON(http.StatusUnauthorized, map[string]string{
			"error": "unknown or revoked device",
		})
	}

	var body revokeRequest
	if err := e.BindBody(&body); err != nil || body.DeviceID == "" {
		return e.JSON(http.StatusBadRequest, map[string]string{
			"error": "device_id required",
		})
	}

	// Revoking the device you are holding would lock you out of the account
	// with no way back except the CLI. Refuse, and say why.
	if body.DeviceID == callerID {
		return e.JSON(http.StatusBadRequest, map[string]string{
			"error": "that is this device — remove the account here instead",
		})
	}

	if err := revokeDevice(e.App, accountID, body.DeviceID); err != nil {
		return e.JSON(http.StatusNotFound, map[string]string{
			"error": "no such device on this account",
		})
	}
	return e.JSON(http.StatusOK, map[string]string{"revoked": body.DeviceID})
}
