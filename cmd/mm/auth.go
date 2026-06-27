package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/dslh/mm/internal/api"
)

func cmdAuth(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return usageError("mm auth status|login")
	}
	switch args[0] {
	case "status":
		return authStatus(ctx)
	case "login":
		return authLogin(ctx)
	}
	return usageError("mm auth status|login")
}

func authStatus(ctx context.Context) error {
	o, done, err := newOps()
	if err != nil {
		return err
	}
	defer done()

	probeErr := o.API.ProbeAuth(ctx)
	exp := o.API.SessionExpires() // read after the probe: a valid probe rolls it forward
	days := int(time.Until(exp).Hours() / 24)

	credPath := credentialsPath()

	if jsonOut {
		if err := printJSON(map[string]any{
			"valid":       probeErr == nil,
			"expiresAt":   exp.Format(time.RFC3339),
			"daysLeft":    days,
			"credentials": credPath,
		}); err != nil {
			return err
		}
		return probeErr
	}
	fmt.Printf("credentials:    %s\n", credPath)
	fmt.Printf("session cookie: expires %s (~%d days; sliding 60-day window)\n", exp.Format("2006-01-02"), days)
	if probeErr == nil {
		fmt.Println("live probe: OK — session is valid")
		return nil
	}
	return probeErr
}

// authLogin signs in directly (POST /auth/signin) and writes .auth/state.json.
// The password is read without echo and handed straight to the API client; mm
// never logs, stores, or transmits it anywhere but the signin request.
func authLogin(ctx context.Context) error {
	email, password, err := readCredentials()
	if err != nil {
		return err
	}

	sess, err := api.Login(ctx, statePath(), email, password)
	if err != nil {
		if errors.Is(err, api.ErrBadCredentials) {
			return errors.New("login failed: the email or password was not accepted")
		}
		return err
	}
	fmt.Printf("logged in — session saved to %s (expires %s)\n", statePath(), sess.Expires().Format("2006-01-02"))

	// Verify the freshly saved cookie against a live probe, so anything subtly
	// wrong fails here rather than on the next command.
	c, err := api.New(statePath())
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.ProbeAuth(ctx); err != nil {
		return fmt.Errorf("session saved but live probe failed: %w", err)
	}
	fmt.Println("live probe: OK — session is valid")
	return nil
}

// readCredentials prompts for the email and password. On a terminal the password
// is read without echo; when stdin is piped it reads two lines (email, password)
// so the command can be scripted (e.g. from a password manager).
func readCredentials() (email, password string, err error) {
	fd := int(os.Stdin.Fd())
	interactive := term.IsTerminal(fd)

	if interactive {
		fmt.Fprint(os.Stderr, "mon-marché email: ")
	}
	email, err = readLineRaw()
	if err != nil {
		return "", "", err
	}
	email = strings.TrimSpace(email)

	if interactive {
		fmt.Fprint(os.Stderr, "password (hidden): ")
		pw, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", "", err
		}
		password = string(pw)
	} else {
		password, err = readLineRaw()
		if err != nil {
			return "", "", err
		}
	}

	if email == "" || password == "" {
		return "", "", errors.New("email and password are both required")
	}
	return email, password, nil
}

// readLineRaw reads one line from stdin a byte at a time, so it never buffers
// past the newline into the password that term.ReadPassword reads next.
func readLineRaw() (string, error) {
	var b []byte
	buf := make([]byte, 1)
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				break
			}
			if buf[0] != '\r' {
				b = append(b, buf[0])
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return "", err
		}
	}
	return string(b), nil
}
