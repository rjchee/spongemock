package main

import (
	"net/http"
	"net/url"
	"os"
)

func main() {
	appURL, _ := url.Parse(os.Getenv("APP_URL"))
	path, _ := url.Parse("static/icon.png")
	http.Get(appURL.ResolveReference(path).String())
}
