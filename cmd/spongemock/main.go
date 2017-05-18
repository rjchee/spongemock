package main

import (
	"log"
	"net/http"
	"os"
	"strings"
)

var (
	plugins []Plugin
	port    string

	allPlugins = map[string]Plugin{
		"slack": NewSlackPlugin(),
	}
)

type mainPlugin struct{}

func (p mainPlugin) EnvVariables() []EnvVariable {
	return []EnvVariable{
		{
			Name:     "PORT",
			Variable: &port,
		},
	}
}

func (p mainPlugin) RegisterHandles(m *http.ServeMux) {
	fs := http.FileServer(http.Dir("static"))
	m.Handle("/static/", http.StripPrefix("/static/", fs))
}

func main() {
	plugins = append(plugins, mainPlugin{})

	whitelist := os.Getenv("PLUGINS")
	if whitelist == "" {
		for _, v := range allPlugins {
			plugins = append(plugins, v)
		}
	} else {
		for _, v := range strings.Split(whitelist, ",") {
			plugins = append(plugins, allPlugins[strings.ToLower(v)])
		}
	}

	mux := http.DefaultServeMux

	for _, p := range plugins {
		for _, v := range p.EnvVariables() {
			v.Set()
		}

		p.RegisterHandles(mux)
	}

	log.Fatal(http.ListenAndServe(":"+port, nil))
}
