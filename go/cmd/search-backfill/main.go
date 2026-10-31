// Command search-backfill fills the normalized search columns while preventing
// concurrent user writes. Stop the application before running it.
package main

import (
	"context"
	"log"

	"example.com/isureal-company/isureal"
	"github.com/jmoiron/sqlx"
)

type userSearchText struct {
	ID          string `db:"id"`
	Username    string `db:"username"`
	DisplayName string `db:"display_name"`
}

func main() {
	config := isureal.ConfigFromEnv()
	db, err := sqlx.Connect("pgx", config.DatabaseDSN())
	if err != nil {
		log.Fatal("connect database failed")
	}
	defer db.Close()

	tx, err := db.BeginTxx(context.Background(), nil)
	if err != nil {
		log.Fatal("begin backfill failed")
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`LOCK TABLE users, initial_users IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		log.Fatal("lock search tables failed")
	}
	for _, table := range []string{"users", "initial_users"} {
		if _, err := tx.Exec(`ALTER TABLE ` + table + ` ADD COLUMN IF NOT EXISTS search_username text`); err != nil {
			log.Fatal("add username search column failed")
		}
		if _, err := tx.Exec(`ALTER TABLE ` + table + ` ADD COLUMN IF NOT EXISTS search_display_name text`); err != nil {
			log.Fatal("add display name search column failed")
		}
	}
	for _, table := range []string{"users", "initial_users"} {
		var rows []userSearchText
		if err := tx.Select(&rows, `SELECT id, username, display_name FROM `+table); err != nil {
			log.Fatal("read search source failed")
		}
		for _, row := range rows {
			if _, err := tx.Exec(`UPDATE `+table+` SET search_username=$2, search_display_name=$3 WHERE id=$1`,
				row.ID, isureal.NormalizeSearchText(row.Username), isureal.NormalizeSearchText(row.DisplayName)); err != nil {
				log.Fatal("write normalized search text failed")
			}
		}
	}
	if err := tx.Commit(); err != nil {
		log.Fatal("commit backfill failed")
	}
}
