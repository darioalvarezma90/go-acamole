package main

import (
	"context"
	"log"
	"os"
	"time"

	acamole "github.com/darioalvarezma90/go-acamole/postgresql"
)

func main() {
	dsn := os.Getenv("POSTGRESQL_DSN")
	// Sin POSTGRESQL_DSN se utilizan PGHOST, PGPORT, PGUSER, PGPASSWORD,
	// PGDATABASE y los valores predeterminados de pgx.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := acamole.NewClient(ctx,
		acamole.WithConnectionString(dsn),
		acamole.WithApplicationName("go-acamole-example"),
		acamole.WithMaxConnections(10),
		acamole.WithMinConnections(0),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()
	var value int
	if err := client.Driver().QueryRow(ctx, "select 1").Scan(&value); err != nil {
		log.Fatal(err)
	}
	log.Printf("select 1 = %d", value)
}
