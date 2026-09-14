package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase"
	"github.com/spf13/cobra"
)

// The operator's device commands.
//
// # Why these speak HTTP rather than opening the database
//
// `first-device` writes directly, and the console stops the server before
// running it, because two SQLite writers is the one thing this setup cannot
// have. That is the right trade for the one command that must work before a
// server exists — and the wrong one for these, which are used *while* it is
// running. Renaming a device should not mean a restart.
//
// So they ask the running server, on the operator's credential, exactly as the
// console window does. One mechanism, two front ends.
func registerDeviceCommands(app *pocketbase.PocketBase) {
	var addr string
	var token string
	var asJSON bool

	devices := &cobra.Command{
		Use:   "devices",
		Short: "List, rename and stop the devices paired with this server",
	}

	get := func(path string, body any) ([]byte, error) {
		base := strings.TrimSpace(addr)
		if base == "" {
			base = settings.HTTP
		}
		if base == "" {
			base = "127.0.0.1:8090"
		}
		if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
			base = "http://" + base
		}
		want := strings.TrimSpace(token)
		if want == "" {
			want = operatorToken()
		}
		if want == "" {
			return nil, fmt.Errorf(
				"no operator token. Set SUMMAREADER_OPERATOR_TOKEN, or pass --token")
		}

		var req *http.Request
		var err error
		if body == nil {
			req, err = http.NewRequest(http.MethodGet, base+path, nil)
		} else {
			encoded, marshalErr := json.Marshal(body)
			if marshalErr != nil {
				return nil, marshalErr
			}
			req, err = http.NewRequest(http.MethodPost, base+path, bytes.NewReader(encoded))
			if err == nil {
				req.Header.Set("Content-Type", "application/json")
			}
		}
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+want)

		client := &http.Client{Timeout: 5 * time.Second}
		res, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("could not reach the server at %s: %w", base, err)
		}
		defer res.Body.Close()
		payload, err := io.ReadAll(res.Body)
		if err != nil {
			return nil, err
		}
		if res.StatusCode == http.StatusNotFound && path == "/overview" {
			return nil, fmt.Errorf(
				"the server has no operator token set, so these commands are off")
		}
		if res.StatusCode >= 400 {
			return nil, fmt.Errorf("%s: %s", res.Status, strings.TrimSpace(string(payload)))
		}
		return payload, nil
	}

	list := &cobra.Command{
		Use:   "list",
		Short: "Every device paired with this server",
		Run: func(cmd *cobra.Command, _ []string) {
			payload, err := get("/overview", nil)
			if err != nil {
				cmd.PrintErrln(err)
				os.Exit(1)
			}
			if asJSON {
				// Real stdout, for the same reason first-device --json uses
				// it: PocketBase points cobra's writer at stderr, so a piped
				// `| jq` gets nothing.
				fmt.Println(string(payload))
				return
			}
			var out overview
			if err := json.Unmarshal(payload, &out); err != nil {
				cmd.PrintErrln(err)
				os.Exit(1)
			}
			if len(out.Devices) == 0 {
				cmd.Println("No devices are paired with this server.")
				return
			}
			for _, d := range out.Devices {
				label := d.Label
				if strings.TrimSpace(label) == "" {
					label = "(unnamed)"
				}
				seen := d.LastSeen
				if seen == "" {
					seen = "not since this was recorded"
				}
				state := ""
				if d.Revoked {
					state = "  [stopped]"
				}
				cmd.Printf("%s  %-24s  last seen %s%s\n", d.ID, label, seen, state)
			}
		},
	}

	rename := &cobra.Command{
		Use:   "rename <device-id> <name>",
		Short: "Change what a device is called",
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			payload, err := get("/operator/rename", map[string]string{
				"device": args[0],
				"label":  args[1],
			})
			if err != nil {
				cmd.PrintErrln(err)
				os.Exit(1)
			}
			if asJSON {
				fmt.Println(string(payload))
				return
			}
			cmd.Printf("Renamed to %q.\n", args[1])
		},
	}

	revoke := &cobra.Command{
		Use:   "revoke <device-id>",
		Short: "Stop a device syncing",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			payload, err := get("/operator/revoke", map[string]string{
				"device": args[0],
			})
			if err != nil {
				cmd.PrintErrln(err)
				os.Exit(1)
			}
			if asJSON {
				fmt.Println(string(payload))
				return
			}
			// What revocation is and is not, in the one place somebody is
			// doing it from a terminal and has nothing else to read.
			cmd.Println("Stopped. It can no longer sync.")
			cmd.Println("What it already downloaded stays on it — it holds its")
			cmd.Println("own copy of the key, and nothing here can reach that.")
		},
	}

	devices.PersistentFlags().StringVar(&addr, "addr", "",
		"the running server, default the configured bind address")
	devices.PersistentFlags().StringVar(&token, "token", "",
		"the operator token, default $SUMMAREADER_OPERATOR_TOKEN")
	devices.PersistentFlags().BoolVar(&asJSON, "json", false,
		"print the result as JSON")

	devices.AddCommand(list, rename, revoke)
	app.RootCmd.AddCommand(devices)
}
