package main

import (
	"context"
	"fmt"
	"os"

	"zzz/internal/app"
)

func main() {
	if err := app.Run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, app.Usage())
		os.Exit(1)
	}
}
