// Application server is the main server for the application
package main

import (
	"context"
	"fmt"
	"os"

	_ "modernc.org/sqlite"

	"github.com/starquake/topbanana/cmd/server/app"
)

func main() {
	ctx := context.Background()
	if err := app.Run(ctx, os.Getenv, os.Stdout); err != nil {
		if _, err2 := fmt.Fprintf(os.Stderr, "error: %v\n", err); err2 != nil {
			panic(err2)
		}

		os.Exit(1)
	}
}
