package main

import (
	"fmt"
	"os"
	"strings"
)

// cmdHelp is one node in the help tree. The same tree backs both `mm help <path>`
// and `mm <path> --help`; each node renders a detail page and lists its children.
type cmdHelp struct {
	name    string     // command word, e.g. "add"
	args    string     // argument spec appended after the path, e.g. "<query> [--all]"
	summary string     // one-line description (also shown in a parent's subcommand list)
	long    string     // full description; printed verbatim, may span lines
	flags   string     // optional flags/notes block, printed verbatim
	sample  string     // optional sample output, printed verbatim
	subs    []*cmdHelp // subcommands
}

// helpTree is the source of truth for per-command help. The top-level overview in
// usage() is curated separately (it groups commands); keep summaries here in sync.
var helpTree = []*cmdHelp{
	{
		name:    "auth",
		args:    "<status|login>",
		summary: "session status, or how to (re-)create it",
		long: "Inspect or renew the login session. Auth is just the `session` cookie,\n" +
			"persisted to .auth/state.json (or $MM_STATE). The password is read without\n" +
			"echo, sent only to the signin endpoint, and never logged or stored — only the\n" +
			"resulting cookie is saved.",
		subs: []*cmdHelp{
			{
				name:    "status",
				summary: "session validity and expiry",
				long: "Reports the cookie's expiry and runs a live probe against the API to\n" +
					"confirm the session still works. A successful probe rolls the sliding\n" +
					"60-day window forward.",
				sample: "session cookie: expires 2026-08-13 (~60 days; sliding 60-day window)\n" +
					"live probe: OK — session is valid",
			},
			{
				name:    "login",
				summary: "sign in and save a fresh session",
				long: "Prompts for your mon-marché email and password, signs in via\n" +
					"POST /auth/signin, writes the session cookie to .auth/state.json, and\n" +
					"verifies it with a live probe. The password is read without echo and is\n" +
					"never logged or stored. When stdin is piped it reads two lines (email,\n" +
					"then password) so it can be scripted from a password manager.",
				sample: "mon-marché email: you@example.com\n" +
					"password (hidden):\n" +
					"logged in — session saved to .auth/state.json (expires 2026-08-16)\n" +
					"live probe: OK — session is valid",
			},
		},
	},
	{
		name:    "search",
		args:    "<query> [--all]",
		summary: "product search",
		long: "Full-text product search. Prints one line per result with canonical id,\n" +
			"name, price, stock, and slug. By default shows the first page only.",
		flags: "--all   follow every result page (capped at 20 pages ≈ 1000 items)",
		sample: `42 results for "tomate" — showing 20, use --all for the rest
  AB12CD34     Tomate grappe                                       3,90 €/kg  stock 42   slug:tomate-grappe
  EF56GH78     Tomate cerise barquette 250g                        2,50 €     stock 18   slug:tomate-cerise-250g  [promo -15%]`,
	},
	{
		name:    "browse",
		args:    "[slug]",
		summary: "category tree, or one category's contents",
		long: "With no argument, prints the full category navigation tree. With a category\n" +
			"slug, prints that category: its subcategories and any products it lists.",
		sample: `Fruits & Légumes
  Légumes (legumes)
    Tomates (tomates)
  Fruits (fruits)`,
	},
	{
		name:    "product",
		args:    "<slug>",
		summary: "single product detail",
		long: "Full detail for one product by slug: price, ids, stock, origin, promo,\n" +
			"and descriptions.",
		sample: `Tomate grappe — 3,90 €/kg
id: AB12CD34  sku: 100234  slug: tomate-grappe
stock: 42  origin: France
Tomates grappe cultivées en France.`,
	},
	{
		name:    "cart",
		args:    "[show|add|set|apply]",
		summary: "show or modify the cart",
		long: "Read and mutate the shopping cart. With no subcommand, shows the cart.\n" +
			"Checkout and payment are never touched — those stay in the browser.",
		subs: []*cmdHelp{
			{
				name:    "show",
				summary: "show cart, totals, free-shipping distance",
				long: "Prints each line with quantity and price, the net total with any\n" +
					"shipping/preparation/promo adjustments, and progress toward free shipping.\n" +
					"This is also what `mm cart` with no subcommand prints.",
				sample: `Cart — 2 line(s)
    2 × Tomate grappe                                7,80 €  (AB12CD34, 3,90 € each)
    1 × Banane bio                                   2,30 €  (EF56GH78, 2,30 € each)
Total 10,10 € net (shipping 5,90 €)
Free shipping from 50,00 € — 39,90 € to go`,
			},
			{
				name:    "add",
				args:    "<item> [-n N]",
				summary: "increment a line; <item> is a search query or id:CANONICALID",
				long: "Add N (default 1) of an item to the cart. <item> is either a search query\n" +
					"(the best match is picked) or id:CANONICALID for an exact product. Quantity\n" +
					"is clamped to available stock, and the result is reported per line.",
				flags: "-n N   quantity to add (default 1)",
				sample: `✓ Tomate grappe (AB12CD34): quantity now 2 — 3,90 € each
Cart: 2 line(s), total 10,10 € net
Free shipping from 50,00 € — 39,90 € to go`,
			},
			{
				name:    "set",
				args:    "<canonicalId> <quantity>",
				summary: "set an absolute quantity; 0 removes",
				long: "Set the exact quantity for a product by canonical id. A quantity of 0\n" +
					"removes the line. Quantity is clamped to available stock.",
				sample: "✓ Tomate grappe (AB12CD34): quantity now 3 — 3,90 € each",
			},
			{
				name:    "apply",
				args:    "[file|-]",
				summary: "apply a batch of changes from JSON lines",
				long: "Apply many changes at once from JSON-lines (one object per line), read from\n" +
					"a file or stdin (- or no argument). Each line is either an add by query or\n" +
					"an absolute set by id:\n" +
					`  {"query":"tomate","n":2}\n` +
					`  {"id":"AB12CD34","set":0}`,
				sample: `✓ Tomate grappe (AB12CD34): quantity now 2 — 3,90 € each
✓ Banane bio (EF56GH78): removed
Cart: 1 line(s), total 7,80 € net`,
			},
		},
	},
	{
		name:    "orders",
		args:    "[--limit N]",
		summary: "past orders",
		long: "List past orders, most recent first, with delivery slot, state, total, and a\n" +
			"preview of the items. Defaults to 10; raise with --limit.",
		flags: "--limit N   how many orders to show (default 10)",
		sample: `12 past orders
  ORDER-10234      Tue 03 Jun 10:00–12:00 DELIVERED     78,45 €  Tomate grappe, Banane bio, Lait +9 more`,
	},
	{
		name:    "order",
		args:    "<id>",
		summary: "one order, with quantities",
		long: "Show a single past order in full: every line with quantity and price, the\n" +
			"delivery slot, and the net total.",
		sample: `Order ORDER-10234 — DELIVERED, delivery Tue 03 Jun 10:00–12:00
    2 × Tomate grappe                                7,80 €  (AB12CD34)
Total 78,45 € net`,
	},
	{
		name:    "reorder",
		args:    "<id> [--dry-run]",
		summary: "add a past order's lines to the cart",
		long: "Add every line of a past order to the current cart, clamping to stock and\n" +
			"reporting each line. Use --dry-run to preview what would be added without\n" +
			"touching the cart.",
		flags: "--dry-run   show what would be added; leave the cart unchanged",
		sample: `Reorder ORDER-10234 — 11 line(s)
· 2 × Tomate grappe (AB12CD34) — 3,90 € each
dry run — cart not modified`,
	},
	{
		name:    "slots",
		args:    "[select <slotId>]",
		summary: "delivery windows; select one for the cart",
		long: "With no subcommand, lists available delivery windows per zone with their\n" +
			"order-by deadlines and any surcharge. Checkout stays in the browser.",
		subs: []*cmdHelp{
			{
				name:    "select",
				args:    "<slotId>",
				summary: "set the cart's delivery window",
				long: "Set the cart's delivery window to <slotId> (from `mm slots`). Final review,\n" +
					"checkout, and payment stay in the browser. Delivery address/notes are passed\n" +
					"through opaquely and never logged or stored.",
				sample: "Delivery window set: Mon 16 Jun 10:00–12:00 (id slot_abc)\n" +
					"Cart: 2 line(s), total 10,10 € net",
			},
		},
	},
	{
		name:    "mcp",
		summary: "run the MCP server over stdio (tools for Claude)",
		long: "Run the Model Context Protocol server over stdio, exposing the same\n" +
			"operations as tools for an MCP client. Cart mutation goes through a single\n" +
			"cart_apply tool. Register with:\n" +
			"  claude mcp add mm -- /abs/path/to/bin/mm mcp\n" +
			"The server reads .auth/state.json from its working directory, or $MM_STATE.",
	},
	{
		name:    "version",
		summary: "print version, commit, and build date",
		long: "Print the build version, git commit, and build date. Release builds embed\n" +
			"these via -ldflags; `go build`/`go install` builds report \"dev\".\n" +
			"`mm --version` (or -v) is a shortcut for this command.",
		sample: "mm version 1.0.0\ncommit: a1b2c3d\nbuilt:  2026-06-17T09:00:00Z",
	},
	{
		name:    "help",
		args:    "[command [subcommand]]",
		summary: "show this overview, or detailed help for a command",
		long: "With no argument, prints the command overview. With a command (and optional\n" +
			"subcommand), prints its detailed help. Equivalent to passing --help to that\n" +
			"command, e.g. `mm help cart add` == `mm cart add --help`.",
	},
}

// stripHelpFlag removes -h/--help from args, reporting whether either was present.
func stripHelpFlag(args []string) (rest []string, found bool) {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			found = true
			continue
		}
		rest = append(rest, a)
	}
	return rest, found
}

// printHelp resolves the deepest help node matching the leading words of path and
// prints its detail page. With no path it prints the overview. Returns the process
// exit code.
func printHelp(path []string) int {
	if len(path) == 0 {
		usageTo(os.Stdout)
		return 0
	}
	nodes := helpTree
	var node *cmdHelp
	var matched []string
	for _, word := range path {
		next := findHelp(nodes, word)
		if next == nil {
			break
		}
		node = next
		matched = append(matched, word)
		nodes = next.subs
	}
	if node == nil {
		fmt.Fprintf(os.Stderr, "mm: no help for %q\n\n", strings.Join(path, " "))
		usage()
		return 2
	}
	printHelpNode(node, matched)
	return 0
}

func findHelp(nodes []*cmdHelp, name string) *cmdHelp {
	for _, n := range nodes {
		if n.name == name {
			return n
		}
	}
	return nil
}

func printHelpNode(n *cmdHelp, path []string) {
	usageLine := "mm " + strings.Join(path, " ")
	if n.args != "" {
		usageLine += " " + n.args
	}
	fmt.Println(usageLine)
	if n.long != "" {
		fmt.Printf("\n%s\n", n.long)
	} else if n.summary != "" {
		fmt.Printf("\n%s\n", n.summary)
	}
	if n.flags != "" {
		fmt.Printf("\nFlags:\n%s\n", indent(n.flags))
	}
	if len(n.subs) > 0 {
		fmt.Println("\nSubcommands:")
		w := 0
		for _, s := range n.subs {
			if len(s.name) > w {
				w = len(s.name)
			}
		}
		for _, s := range n.subs {
			fmt.Printf("  %-*s  %s\n", w, s.name, s.summary)
		}
		fmt.Printf("\nRun `mm help %s <subcommand>` for detail.\n", strings.Join(path, " "))
	}
	if n.sample != "" {
		fmt.Printf("\nExample:\n%s\n", indent(n.sample))
	}
}

func indent(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n")
}
