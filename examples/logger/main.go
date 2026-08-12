package main

import (
	"log"

	acamole "github.com/darioalvarezma90/go-acamole/logger"
)

func main() {
	applicationLogger, err := acamole.NewLogger("example")
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := applicationLogger.Close(); err != nil {
			log.Printf("close logger: %v", err)
		}
	}()

	applicationLogger.Info("example started", "component", "logger")
}
