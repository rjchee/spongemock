package main

import (
	"fmt"
	"log"
	"os"
	"strings"
)

var (
	allPlugins = []WorkerPlugin{
		NewTwitterPlugin(),
	}
)

type pluginError struct {
	name string
	err  error
}

func (e pluginError) Error() string {
	return fmt.Sprintf("error from %s plugin: %s", e.name, e.err)
}

func main() {
	var plugins []WorkerPlugin
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

	var errChans []chan error

	agg := make(chan error)

	for _, p := range plugins {
		for _, v := range p.EnvVariables() {
			v.Set()
		}

		ch := make(chan error)
		errChans = append(errChans, ch)
		go func(ch chan error) {
			for err := range ch {
				agg <- pluginError{p.Name(), err}
			}
		}(ch)

		go p.Start(ch)
	}

	for err := range agg {
		log.Println(err)
	}
}
