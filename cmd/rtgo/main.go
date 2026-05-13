package main

import (
	"log"
	"os"
	"github.com/thealanphipps-del/rtgo"
)

func main() {
	connStr := os.Getenv("DATABASE_URL")
	mgr, err := rtgo.NewManager(connStr)
	if err != nil {
		log.Fatalf("Failed to initialize manager: %v", err)
	}

	server := rtgo.NewServer(mgr)
	log.Println("Starting RTGO REST 2.0 API Server on :8080...")
	if err := server.Run(":8080"); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
