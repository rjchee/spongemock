package main

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
)

var (
	AppURL  string
	IconURL string
	MemeURL string
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

type WebPlugin interface {
	Name() string
	EnvVariables() []EnvVariable
	RegisterHandles(*http.ServeMux)
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
