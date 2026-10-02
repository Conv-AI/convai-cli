package main

import (
	"context"
	"os"

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
	if err := root.ExecuteContext(context.Background()); err != nil {
		format, _ := root.PersistentFlags().GetString("output")
		os.Exit(app.ReportError(os.Stderr, format, err))
	}
}
