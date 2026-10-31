package main

import (
	"net/http"
	_ "net/http/pprof"
	"os"

	"example.com/isureal-company/isureal"
)

// 2026-09-23

func main() {
	config := isureal.ConfigFromEnv()
	if addr := os.Getenv("ISUREAL_PPROF"); addr != "" {
		go http.ListenAndServe(addr, nil)
	}
	server := isureal.NewServer(config)
	port := os.Getenv("ISUREAL_PORT")
	if port == "" {
		port = "8080"
	}
	if err := server.Start(":" + port); err != nil {
		panic("failed to start server: " + err.Error())
	}
}
