package main

import (
	"context"
	"fmt"
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
		fmt.Fprintf(os.Stderr, "convai: %s\n", app.RedactError(err))
		os.Exit(1)
	}
	if err := root.ExecuteContext(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "convai: %s\n", app.RedactError(err))
		os.Exit(1)
	}
}
