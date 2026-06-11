package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/dslh/mm/internal/api"
)

const maxSearchPages = 20 // --all backstop: 20 pages ≈ 1000 items

func cmdSearch(ctx context.Context, args []string) error {
	all, args := flagBool(args, "all")
	query := strings.TrimSpace(strings.Join(args, " "))
	if query == "" {
		return usageError("mm search <query> [--all]")
	}
	o, done, err := newOps()
	if err != nil {
		return err
	}
	defer done()

	var res *api.SearchResponse
	if all {
		res, err = o.API.SearchAll(ctx, query, maxSearchPages)
	} else {
		res, err = o.API.Search(ctx, query, "")
	}
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(res)
	}
	fmt.Printf("%d results for %q", res.Count, query)
	if res.Next != "" {
		if all {
			fmt.Printf(" — stopped at %d items (page cap)", len(res.Items))
		} else {
			fmt.Printf(" — showing %d, use --all for the rest", len(res.Items))
		}
	}
	fmt.Println()
	for i := range res.Items {
		fmt.Println(productLine(&res.Items[i]))
	}
	return nil
}

func cmdBrowse(ctx context.Context, args []string) error {
	o, done, err := newOps()
	if err != nil {
		return err
	}
	defer done()

	if len(args) == 0 {
		nav, err := o.API.Navigation(ctx)
		if err != nil {
			return err
		}
		if jsonOut {
			return printJSON(nav)
		}
		for _, f := range nav.Families {
			fmt.Println(f.Name)
			for _, c := range f.Categories {
				printNavCategory(c, 1)
			}
		}
		return nil
	}
	if len(args) != 1 {
		return usageError("mm browse [slug]")
	}

	cat, err := o.API.Category(ctx, args[0])
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(cat)
	}
	fmt.Printf("%s (%s)", cat.Name, cat.Slug)
	if cat.Parent != nil {
		fmt.Printf(" — parent: %s", cat.Parent.Slug)
	}
	fmt.Println()
	for _, sc := range cat.Subcategories {
		fmt.Printf("  %s (%s) — %d items\n", sc.Title, sc.Slug, sc.ItemCount)
	}
	products := cat.Products()
	for i := range products {
		fmt.Println(productLine(&products[i]))
	}
	if len(cat.Subcategories) == 0 && len(products) == 0 {
		fmt.Println("(empty category)")
	}
	return nil
}

func printNavCategory(c api.NavCategory, depth int) {
	fmt.Printf("%s%s (%s)\n", strings.Repeat("  ", depth), c.Name, c.Slug)
	for _, ch := range c.Children {
		printNavCategory(ch, depth+1)
	}
}

func cmdProduct(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return usageError("mm product <slug>")
	}
	o, done, err := newOps()
	if err != nil {
		return err
	}
	defer done()

	d, err := o.API.ArticleBySlug(ctx, args[0])
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(d)
	}
	fmt.Printf("%s — %s%s\n", d.Name, euro(d.ItemPrice), d.PriceUnit())
	fmt.Printf("id: %s  sku: %s  slug: %s\n", d.CanonicalID, d.SKU, d.Slug)
	fmt.Printf("stock: %d", d.AvailableQuantity)
	if d.Origin != "" {
		fmt.Printf("  origin: %s", d.Origin)
	}
	fmt.Println()
	if d.Promo != nil {
		fmt.Println("promo:", promoTag(d.Promo))
	}
	if !d.Enabled {
		fmt.Println("⚠ product is disabled")
	}
	if s := strings.TrimSpace(d.ShortDescription); s != "" {
		fmt.Println(s)
	}
	if s := strings.TrimSpace(d.Description); s != "" {
		fmt.Println(s)
	}
	return nil
}
