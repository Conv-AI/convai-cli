package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/Conv-AI/convai-cli/internal/app"
)

var version = "dev"

func main() {
	root, err := app.New(app.Options{
		Version: version,
		Stdin:   os.Stdin,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
	})
	if err != nil {
		os.Exit(app.ReportError(os.Stderr, "table", err))
	}
	// Interrupts cancel the command context so browser login and credential
	// rotation can revoke anything they issued before the process exits.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err = root.ExecuteContext(ctx)
	stop()
	if err != nil {
		format, _ := root.PersistentFlags().GetString("output")
		os.Exit(app.ReportError(os.Stderr, format, err))
	}
}
