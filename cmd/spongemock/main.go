package main

import (
	"log"
	"net/http"
	"os"
	"strings"
)

var (
	port string

	allPlugins = []WebPlugin{
		NewSlackPlugin(),
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

func (p mainPlugin) Name() string {
	return "main"
}

func main() {
	plugins := []WebPlugin{mainPlugin{}}

	whitelist := os.Getenv("PLUGINS")
	if whitelist == "" {
		for _, p := range allPlugins {
			plugins = append(plugins, p)
		}
	} else {
		pluginSet := make(map[string]struct{})
		for _, v := range strings.Split(whitelist, ",") {
			pluginSet[v] = struct{}{}
		}

		for _, p := range allPlugins {
			if _, ok := pluginSet[p.Name()]; ok {
				plugins = append(plugins, p)
			}
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
