package main

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/nlopes/slack"
)

func setupOAuthDB() error {
	if DB == nil {
		return errors.New("database required to store OAuth tokens")
	}
	err := createTable("slack_oauth", "(user_id text PRIMARY KEY, token text NOT NULL)")
	if err != nil {
		return fmt.Errorf("error creating oauth table: %s", err)
	}
	return nil
}

func getPublicOAuthLink() string {
	return fmt.Sprintf("https://slack.com/oauth/authorize?&client_id=%s&scope=commands,channels:history,chat:write:bot,groups:history,im:history,mpim:history", slackClientID)
}

func handleSlackOAuth(w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil {
		log.Printf("invalid form data: %s\n", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if denial := r.FormValue("error"); denial != "" {
		// don't handle user permission denials
		w.WriteHeader(http.StatusOK)
		return
	}
	code := r.FormValue("code")
	if code == "" {
		log.Println("no oauth code given")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	oAuthResponse, err := slack.GetOAuthResponse(slackClientID, slackClientSecret, code, AppURL+"/slack/oauth2", false)
	if err != nil {
		log.Printf("error occurred when sending an oauth response: %s", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	err = storeSlackOAuthToken(oAuthResponse.UserID, oAuthResponse.AccessToken)

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// go back to slack after authenticating
	http.Redirect(w, r, "https://my.slack.com", http.StatusFound)
}

func storeSlackOAuthToken(userID, token string) error {
	_, err := DB.Exec("INSERT INTO slack_oauth (user_id, token) VALUES ($1, $2) ON CONFLICT (user_id) DO UPDATE SET token=$2;", userID, token)
	if err != nil {
		return fmt.Errorf("error adding oauth token to database: %s", err)
	}
	return nil
}

func lookupSlackOAuthToken(userID string) (string, error) {
	row := DB.QueryRow("SELECT token FROM slack_oauth WHERE user_id=$1;", userID)
	var token string
	err := row.Scan(&token)
	switch {
	case err == sql.ErrNoRows:
		// return empty string and no error if the user is not in the database
		return "", nil
	case err != nil:
		return "", fmt.Errorf("error looking up oauth token: %s", err)
	default:
		return token, nil
	}
}
