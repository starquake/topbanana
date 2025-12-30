// Application server is the main server for the application
package main

import (
	"context"
	"fmt"
	"os"

	_ "modernc.org/sqlite"

	"github.com/starquake/topbanana/cmd/server/app"
	"github.com/starquake/topbanana/internal/db"
)

func main() {
	ctx := context.Background()

	db.SetupGoose()

	if err := app.Run(ctx, os.Getenv, os.Stdout, nil); err != nil {
		if _, err2 := fmt.Fprintf(os.Stderr, "error: %v\n", err); err2 != nil {
			panic(err2)
		}

		os.Exit(1)
	}
}
