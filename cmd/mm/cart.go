package main

import (
	"context"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/dslh/mm/internal/api"
	"github.com/dslh/mm/internal/ops"
)

func cmdCart(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return cartShow(ctx)
	}
	switch args[0] {
	case "show":
		return cartShow(ctx)
	case "add":
		return cartAdd(ctx, args[1:])
	case "set":
		return cartSet(ctx, args[1:])
	case "apply":
		return cartApply(ctx, args[1:])
	}
	return usageError("mm cart [add <item> [-n N] | set <canonicalId> <n> | apply [file|-]]")
}

func cartShow(ctx context.Context) error {
	o, done, err := newOps()
	if err != nil {
		return err
	}
	defer done()

	cart, err := o.GetCart(ctx)
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(viewCart(cart))
	}
	renderCart(cart)
	return nil
}

func cartAdd(ctx context.Context, args []string) error {
	n, args, err := flagInt(args, "n", 1)
	if err != nil {
		return err
	}
	item := strings.TrimSpace(strings.Join(args, " "))
	if item == "" {
		return usageError("mm cart add <query|id:CANONICALID> [-n N]")
	}
	o, done, err := newOps()
	if err != nil {
		return err
	}
	defer done()

	cart, err := o.GetCart(ctx)
	if err != nil {
		return err
	}
	var oc ops.ItemOutcome
	if id, ok := strings.CutPrefix(item, "id:"); ok {
		cart, oc, err = o.AddByID(ctx, cart, id, n)
	} else {
		cart, oc, err = o.AddByQuery(ctx, cart, item, n)
	}
	if err != nil {
		return err
	}
	return finishMutation(cart, []ops.ItemOutcome{oc})
}

func cartSet(ctx context.Context, args []string) error {
	if len(args) != 2 {
		return usageError("mm cart set <canonicalId> <quantity>")
	}
	qty, err := strconv.Atoi(args[1])
	if err != nil || qty < 0 {
		return usageError("mm cart set <canonicalId> <quantity ≥ 0>")
	}
	o, done, err := newOps()
	if err != nil {
		return err
	}
	defer done()

	cart, err := o.GetCart(ctx)
	if err != nil {
		return err
	}
	cart, oc, err := o.SetByID(ctx, cart, args[0], qty)
	if err != nil {
		return err
	}
	return finishMutation(cart, []ops.ItemOutcome{oc})
}

func cartApply(ctx context.Context, args []string) error {
	var r io.Reader
	switch {
	case len(args) == 0 || (len(args) == 1 && args[0] == "-"):
		r = os.Stdin
	case len(args) == 1:
		f, err := os.Open(args[0])
		if err != nil {
			return err
		}
		defer f.Close()
		r = f
	default:
		return usageError("mm cart apply [file|-]")
	}
	lines, err := ops.ParseApplyLines(r)
	if err != nil {
		return err
	}
	o, done, err := newOps()
	if err != nil {
		return err
	}
	defer done()

	cart, outcomes, err := o.Apply(ctx, lines)
	if err != nil {
		// render partial progress before failing, so completed lines aren't lost
		if !jsonOut && len(outcomes) > 0 {
			renderOutcomes(outcomes)
		}
		return err
	}
	return finishMutation(cart, outcomes)
}

func finishMutation(cart *api.Cart, outcomes []ops.ItemOutcome) error {
	if jsonOut {
		return printJSON(map[string]any{"outcomes": outcomes, "cart": viewCart(cart)})
	}
	renderOutcomes(outcomes)
	cartSummary(cart)
	return nil
}
