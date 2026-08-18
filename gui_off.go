//go:build !gui

package main

import (
	"github.com/pocketbase/pocketbase"
	"github.com/spf13/cobra"
)

// The word `gui` exists in every build, only the window does not.
//
// This is the binary that cross-compiles for four platforms from one machine
// because it needs no C toolchain, and a window does. Somebody who has been
// told "run it with gui" and reaches this build has picked the wrong download,
// which is worth saying — an unknown-command error from cobra reads as "that
// feature does not exist" and sends them looking for something else.
func registerGUI(app *pocketbase.PocketBase) {
	app.RootCmd.AddCommand(&cobra.Command{
		Use:   "gui",
		Short: "Open the desktop window (this build has none)",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Println("This build has no desktop window. It is the headless")
			cmd.Println("one — that is what lets it run anywhere without a C")
			cmd.Println("toolchain. The desktop build is on the releases page.")
			cmd.Println()
			cmd.Println("The window is a front end for these two, which work here:")
			cmd.Println("  summareader-sync serve --http=127.0.0.1:8099 --dir=DIR")
			cmd.Println("  summareader-sync pair \"My library\" \"Desktop\" --dir=DIR")
		},
	})
}

// Nothing to open. main calls this unconditionally so that the two builds
// differ in one file and not in main.
func startGUI() {}
