package main

import (
	"context"
	"log"
	"os"
	"time"

	acamole "github.com/darioalvarezma90/go-acamole/mongodb"
)

func main() {
	uri := os.Getenv("MONGODB_URI")
	if uri == "" {
		log.Print("set MONGODB_URI to run this example")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := acamole.NewClient(ctx, uri, "example")
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		if err := client.Close(closeCtx); err != nil {
			log.Printf("close MongoDB: %v", err)
		}
	}()
	log.Printf("connected to %s", client.Database().Name())
}
