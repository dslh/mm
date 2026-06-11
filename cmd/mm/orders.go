package main

import (
	"context"
	"fmt"
	"strings"
)

func cmdOrders(ctx context.Context, args []string) error {
	limit, args, err := flagInt(args, "limit", 10)
	if err != nil {
		return err
	}
	if len(args) != 0 {
		return usageError("mm orders [--limit N]")
	}
	o, done, err := newOps()
	if err != nil {
		return err
	}
	defer done()

	res, err := o.API.OrdersPast(ctx)
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(res)
	}
	fmt.Printf("%d past orders\n", res.Count)
	for i, ord := range res.Items {
		if i >= limit {
			fmt.Printf("(… %d more — raise --limit)\n", len(res.Items)-limit)
			break
		}
		var names []string
		for _, p := range ord.Products {
			names = append(names, p.Name)
		}
		preview := strings.Join(names[:min(3, len(names))], ", ")
		if len(names) > 3 {
			preview += fmt.Sprintf(" +%d more", len(names)-3)
		}
		fmt.Printf("  %-16s %-22s %-9s %10s  %s\n",
			ord.ID, slotLabel(ord.Delivery.TimeSlot), ord.State, euro(ord.TotalPrice), preview)
	}
	return nil
}

func cmdOrder(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return usageError("mm order <id>")
	}
	o, done, err := newOps()
	if err != nil {
		return err
	}
	defer done()

	ord, err := o.API.Order(ctx, args[0])
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(ord)
	}
	fmt.Printf("Order %s — %s", ord.ID, ord.State)
	if l := slotLabel(ord.Delivery.TimeSlot); l != "" {
		fmt.Printf(", delivery %s", l)
	}
	fmt.Println()
	for _, p := range ord.Products {
		fmt.Printf("  %3d × %-46s %10s  (%s)\n",
			p.Quotation.Count, trunc(p.Name, 46), euro(float64(p.Quotation.Count)*p.ItemPrice), p.CanonicalID)
	}
	fmt.Printf("Total %s net\n", euro(ord.Price.Quotation.Net))
	return nil
}

func cmdReorder(ctx context.Context, args []string) error {
	dryRun, args := flagBool(args, "dry-run")
	if len(args) != 1 {
		return usageError("mm reorder <id> [--dry-run]")
	}
	o, done, err := newOps()
	if err != nil {
		return err
	}
	defer done()

	order, cart, outcomes, err := o.Reorder(ctx, args[0], dryRun)
	if err != nil {
		if !jsonOut && len(outcomes) > 0 {
			renderOutcomes(outcomes)
		}
		return err
	}
	if jsonOut {
		out := map[string]any{"orderId": order.ID, "dryRun": dryRun, "outcomes": outcomes}
		if cart != nil {
			out["cart"] = viewCart(cart)
		}
		return printJSON(out)
	}
	fmt.Printf("Reorder %s — %d line(s)\n", order.ID, len(order.Products))
	renderOutcomes(outcomes)
	if dryRun {
		fmt.Println("dry run — cart not modified")
	} else {
		cartSummary(cart)
	}
	return nil
}
