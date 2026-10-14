package main

import (
	"fmt"
	"github.com/ImSingee/TailBuddy/internal/app"
	"github.com/ImSingee/TailBuddy/internal/engine"
	"os"
	"runtime"
)

func main() {
	if handled, code := engine.SupervisorMain(os.Args[1:]); handled {
		os.Exit(code)
	}
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println("TailBuddy " + app.Version)
		return
	}
	if runtime.GOOS != "darwin" {
		fmt.Fprintln(os.Stderr, "TailBuddy is a macOS menu bar application.")
		os.Exit(1)
	}
	if err := app.New().Run(); err != nil {
		fmt.Fprintln(os.Stderr, "TailBuddy could not start its native interface.")
		os.Exit(1)
	}
}
