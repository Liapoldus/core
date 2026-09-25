package main

import (
	"database/sql"
	"log"
	"os"

	_ "modernc.org/sqlite"
)

func main() {
	database, err := sql.Open("sqlite", os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TRIGGER reject_audit_insert BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT, 'audit insert rejected by test fixture'); END`); err != nil {
		log.Fatal(err)
	}
}
