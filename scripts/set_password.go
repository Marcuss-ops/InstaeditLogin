package main

import (
	"database/sql"
	"fmt"
	"log"

	_ "github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	hash, err := bcrypt.GenerateFromPassword([]byte("admin123"), bcrypt.DefaultCost)
	if err != nil {
		log.Fatal(err)
	}
	db, err := sql.Open("postgres", "postgresql://instaedit:instaedit_dev_pwd@localhost:5432/instaedit_login?sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec("UPDATE users SET password_hash = $1 WHERE email = $2", hash, "dev@example.com")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Password set successfully to 'admin123' for dev@example.com")
}
