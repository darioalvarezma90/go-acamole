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
	if dsn == "" {
		log.Print("set POSTGRESQL_DSN to run this example")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := acamole.NewClient(ctx, dsn, acamole.WithApplicationName("go-acamole-example"))
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
