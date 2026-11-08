package config_test

import (
	"context"
	"fmt"
	"os"

	config "github.com/mas-bandwidth/nova-tools/v2/modules/config"
)

// Read the friends nova-config keeps, as nova-config friend list does.
func ExampleStore_Friends() {
	ctx := context.Background()
	st, err := config.Open(ctx, os.Getenv("NOVA_PG_DSN"), os.Getenv("NOVA_PG_PASSWORD"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	defer st.Close()
	friends, err := st.Friends(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	for _, f := range friends {
		fmt.Println(f.Name, f.Harness, f.Width, f.Tiers)
	}
}
