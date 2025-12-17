package main

import(
	"log"
	"vestri-worker/internal/http"
)

func main() {
	log.Println("starting worker")
	err := http.Start(":8031")
	if err != nil {
		log.Fatal(err)
	}
}
