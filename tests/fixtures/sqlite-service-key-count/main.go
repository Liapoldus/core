package main

import (
	"database/sql"
	"encoding/json"
	"os"

	_ "modernc.org/sqlite"
)

func main() {
	database, err := sql.Open("sqlite", os.Args[1])
	if err != nil {
		panic(err)
	}
	defer database.Close()
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM service_keys").Scan(&count); err != nil {
		panic(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]int{"count": count}); err != nil {
		panic(err)
	}
}
