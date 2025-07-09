package data

import (
	"log"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func MustMySQL(dsn string) *gorm.DB {
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("mysql: %v", err)
	}
	return db
}
