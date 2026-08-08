// Package main is the Velocity command-line entrypoint.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Hunter2030ZeRo/velocity/internal/command"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if executeErr := command.Execute(ctx); executeErr != nil {
		if _, printErr := fmt.Fprintln(os.Stderr, executeErr); printErr != nil {
			os.Exit(1)
		}
		os.Exit(1)
	}
}
