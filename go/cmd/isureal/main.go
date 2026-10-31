package main

import "example.com/isureal-company/isureal"

// 2026-09-23

func main() {
	config := isureal.ConfigFromEnv()
	server := isureal.NewServer(config)
	port := os.Getenv("ISUREAL_PORT")
	if port == "" {
		port = "8080"
	}
	if err := server.Start(":" + port); err != nil {
		panic("failed to start server: " + err.Error())
	}
}
