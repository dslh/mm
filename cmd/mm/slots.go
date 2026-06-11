package main

import (
	"context"
	"fmt"
	"time"
)

func cmdSlots(ctx context.Context, args []string) error {
	if len(args) != 0 {
		return usageError("mm slots")
	}
	o, done, err := newOps()
	if err != nil {
		return err
	}
	defer done()

	res, err := o.Slots(ctx)
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(res)
	}
	for _, z := range res.DeliveryZones {
		fmt.Printf("%s — shop %s, zone minimum %s\n", z.Name, z.Shop.Name, euro(z.MinOrderAmount))
		selectable := 0
		for _, s := range z.DeliverySlots {
			if !s.Selectable() {
				continue
			}
			selectable++
			from := time.UnixMilli(s.From).In(paris)
			to := time.UnixMilli(s.To).In(paris)
			until := time.UnixMilli(s.OrderUntil).In(paris)
			extra := ""
			if s.ExtraPrice.DutyFree > 0 {
				extra = "  +" + euro(s.ExtraPrice.DutyFree)
			}
			fmt.Printf("  %s %s–%s  order by %s%s  id:%s\n",
				from.Format("Mon 02 Jan"), from.Format("15:04"), to.Format("15:04"),
				until.Format("Mon 02 Jan 15:04"), extra, s.ID)
		}
		fmt.Printf("  (%d selectable of %d)\n", selectable, len(z.DeliverySlots))
	}
	fmt.Println("Slot selection isn't automated — pick the window in the browser (docs/design.md).")
	return nil
}
