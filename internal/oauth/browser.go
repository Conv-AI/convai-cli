package oauth

import (
	"errors"
	"net/url"
	"os/exec"
	"runtime"
)

// OpenBrowser asks the operating system to open an HTTPS URL in the user's
// default browser. The URL is passed as one argument, never through a shell.
func OpenBrowser(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" {
		return errors.New("refusing to open a non-HTTPS sign-in URL")
	}
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", raw)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", raw)
	default:
		command = exec.Command("xdg-open", raw)
	}
	if err := command.Start(); err != nil {
		return err
	}
	// Launchers may outlive the CLI's wait; do not block sign-in on them.
	go func() { _ = command.Wait() }()
	return nil
}
