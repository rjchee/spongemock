package main

import (
	"log"
	"net/http"
)

var (
	slackClientID          string
	slackClientSecret      string
	slackVerificationToken string
)

type slackPlugin struct{}

func (p slackPlugin) EnvVariables() []EnvVariable {
	return []EnvVariable{
		{
			Name:     "SLACK_CLIENT_ID",
			Variable: &slackClientID,
		},
		{
			Name:     "SLACK_CLIENT_SECRET",
			Variable: &slackClientSecret,
		},
		{
			Name:     "SLACK_VERIFICATION_TOKEN",
			Variable: &slackVerificationToken,
		},
	}
}

func (p slackPlugin) RegisterHandles(m *http.ServeMux) {
	err := setupOAuthDB()
	if err != nil {
		log.Printf("error setting up OAuth DB: %s\n", err)
		log.Println("slack integration could not be run")
		return
	}
	m.HandleFunc("/slack", handleSlack)
	m.HandleFunc("/slack/oauth2", handleSlackOAuth)
}

func (p slackPlugin) Name() string {
	return "slack"
}

func NewSlackPlugin() WebPlugin {
	return slackPlugin{}
}
