package main

import (
	"context"
	"fmt"
	"time"
)

func cmdAuth(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return usageError("mm auth status|login")
	}
	switch args[0] {
	case "status":
		return authStatus(ctx)
	case "login":
		return authLogin()
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

	if jsonOut {
		if err := printJSON(map[string]any{
			"valid":     probeErr == nil,
			"expiresAt": exp.Format(time.RFC3339),
			"daysLeft":  days,
		}); err != nil {
			return err
		}
		return probeErr
	}
	fmt.Printf("session cookie: expires %s (~%d days; sliding 60-day window)\n", exp.Format("2006-01-02"), days)
	if probeErr == nil {
		fmt.Println("live probe: OK — session is valid")
		return nil
	}
	return probeErr
}

func authLogin() error {
	fmt.Printf(`Login is manual — mm never handles the password.

  1. playwright-cli open https://www.mon-marche.fr
  2. Log in through the site UI (Doug types the credentials).
  3. playwright-cli state-save %s
  4. mm auth status   # verify

`, statePath())
	return nil
}
