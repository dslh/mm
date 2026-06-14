// mm is a personal shopping assistant CLI for mon-marché.fr.
// Scope ends at the cart: checkout and payment always happen in the browser.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/dslh/mm/internal/api"
	"github.com/dslh/mm/internal/ops"
)

const defaultStatePath = ".auth/state.json"

var jsonOut bool

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	var rest []string
	for _, a := range args {
		if a == "--json" {
			jsonOut = true
			continue
		}
		rest = append(rest, a)
	}
	if len(rest) == 0 {
		usage()
		return 2
	}
	cmd, cmdArgs := rest[0], rest[1:]
	ctx := context.Background()

	var err error
	switch cmd {
	case "auth":
		err = cmdAuth(ctx, cmdArgs)
	case "search":
		err = cmdSearch(ctx, cmdArgs)
	case "browse":
		err = cmdBrowse(ctx, cmdArgs)
	case "product":
		err = cmdProduct(ctx, cmdArgs)
	case "cart":
		err = cmdCart(ctx, cmdArgs)
	case "orders":
		err = cmdOrders(ctx, cmdArgs)
	case "order":
		err = cmdOrder(ctx, cmdArgs)
	case "reorder":
		err = cmdReorder(ctx, cmdArgs)
	case "slots":
		err = cmdSlots(ctx, cmdArgs)
	case "mcp":
		err = cmdMCP(ctx, cmdArgs)
	case "help", "-h", "--help":
		usage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage()
		return 2
	}
	if err == nil {
		return 0
	}

	var ue usageError
	if errors.As(err, &ue) {
		fmt.Fprintln(os.Stderr, "usage:", string(ue))
		return 2
	}
	fmt.Fprintln(os.Stderr, "mm:", err)
	var ae *api.APIError
	if errors.As(err, &ae) && ae.IsAuth() {
		fmt.Fprintln(os.Stderr, "session expired — run `mm auth login`")
		return 3
	}
	var de *api.DriftError
	if errors.As(err, &de) {
		fmt.Fprintln(os.Stderr, "the private API may have changed — re-verify against fresh browser traffic (docs/api.md)")
	}
	return 1
}

type usageError string

func (u usageError) Error() string { return string(u) }

func statePath() string {
	if p := os.Getenv("MM_STATE"); p != "" {
		return p
	}
	return defaultStatePath
}

func newOps() (*ops.Ops, func(), error) {
	c, err := api.New(statePath())
	if err != nil {
		return nil, nil, err
	}
	done := func() {
		if err := c.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "mm: saving session state:", err)
		}
	}
	return &ops.Ops{API: c}, done, nil
}

// flagBool removes -name/--name from args, reporting whether it was present.
func flagBool(args []string, name string) (bool, []string) {
	found := false
	var rest []string
	for _, a := range args {
		if a == "-"+name || a == "--"+name {
			found = true
			continue
		}
		rest = append(rest, a)
	}
	return found, rest
}

// flagInt extracts "-name N", "--name N" or "--name=N" from anywhere in args,
// so flags work both before and after positional arguments.
func flagInt(args []string, name string, def int) (int, []string, error) {
	val := def
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-"+name || a == "--"+name {
			if i+1 >= len(args) {
				return 0, nil, usageError(fmt.Sprintf("flag %s needs a value", a))
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil {
				return 0, nil, usageError(fmt.Sprintf("flag %s: not a number: %q", a, args[i+1]))
			}
			val = n
			i++
			continue
		}
		if v, ok := strings.CutPrefix(a, "--"+name+"="); ok {
			n, err := strconv.Atoi(v)
			if err != nil {
				return 0, nil, usageError(fmt.Sprintf("flag --%s: not a number: %q", name, v))
			}
			val = n
			continue
		}
		rest = append(rest, a)
	}
	return val, rest, nil
}

func usage() {
	fmt.Fprint(os.Stderr, `mm — mon-marché shopping assistant (cart only; checkout stays in the browser)

  mm auth status                   session validity and expiry
  mm auth login                    how to (re-)create the session (manual browser login)

  mm search <query> [--all]        product search; --all follows all result pages
  mm browse [slug]                 category tree, or one category's contents
  mm product <slug>                single product detail

  mm cart                          show cart, totals, free-shipping distance
  mm cart add <item> [-n N]        increment; <item> is a search query or id:CANONICALID
  mm cart set <canonicalId> <n>    absolute quantity; 0 removes
  mm cart apply [file|-]           batch JSON lines: {"query":"tomate","n":2} / {"id":"…","set":0}

  mm orders [--limit N]            past orders
  mm order <id>                    one order, with quantities
  mm reorder <id> [--dry-run]      add a past order's lines to the cart

  mm slots                         available delivery windows
  mm slots select <slotId>         set the cart's delivery window (checkout stays in the browser)

  mm mcp                           run the MCP server over stdio (tools for Claude)

flags: --json   machine-readable output
env:   MM_STATE auth state path (default .auth/state.json)
`)
}
