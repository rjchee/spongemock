package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"

	_ "github.com/lib/pq"
)

var (
	AppURL  string
	IconURL string
	MemeURL string
	DB      *sql.DB
	DEBUG   bool
)

const (
	iconPath       = "static/icon.png"
	memePath       = "static/spongemock.jpg"
	groupThreshold = 0.8
)

type EnvVariable struct {
	Name     string
	Variable *string
}

type WorkerPlugin interface {
	Name() string
	EnvVariables() []EnvVariable
	Start(chan error)
}

func init() {
	SetEnvVariable("APP_URL", &AppURL)

	u, err := url.Parse(AppURL)
	if err != nil {
		log.Fatal("invalid $APP_URL %s", AppURL)
	}
	icon, _ := url.Parse(iconPath)
	IconURL = u.ResolveReference(icon).String()
	meme, _ := url.Parse(memePath)
	MemeURL = u.ResolveReference(meme).String()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL != "" {
		DB, err = sql.Open("postgres", dbURL)
		if err != nil {
			log.Println("DATABASE_URL is not set")
			DB = nil
		} else if err = DB.Ping(); err != nil {
			log.Println("error pinging the database:", err)
			log.Println("closing database connection:", DB.Close())
			DB = nil
		}
	}

	DEBUG = strings.ToLower(os.Getenv("DEBUG")) != "false"
}

func SetEnvVariable(name string, value *string) {
	*value = os.Getenv(name)
	if *value == "" {
		log.Fatal(fmt.Errorf("$%s must be set!", name))
	}
}

func (v EnvVariable) Set() {
	SetEnvVariable(v.Name, v.Variable)
}
