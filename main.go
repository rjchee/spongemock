package main

import (
	"bytes"
	"errors"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strings"

	"github.com/nlopes/slack"
)

var (
	atk     = os.Getenv("AUTHENTICATION_TOKEN")
	vtk     = os.Getenv("VERIFICATION_TOKEN")
	appURL  = os.Getenv("APP_URL")
	iconURL = appURL + "/static/icon.png"
	memeURL = appURL + "/static/spongemock.jpg"
	api     = slack.New(atk)
)

func transformText(m string) string {
	var buffer bytes.Buffer
	for i := 0; i < len(m); i++ {
		ch := m[i : i+1]
		if rand.Int()%2 == 0 {
			ch = strings.ToLower(ch)
		}
		buffer.WriteString(ch)
	}
	return buffer.String()
}

func isValidSlackRequest(r *http.Request) bool {
	if r.Method != "POST" {
		log.Printf("want method POST, got %s\n", r.Method)
		return false
	}
	err := r.ParseForm()
	if err != nil {
		log.Printf("invalid form data: %s\n", err)
		return false
	}
	if cmd := r.PostFormValue("command"); cmd != "/spongemock" {
		log.Printf("want command /spongemock, got %s\n", cmd)
		return false
	}
	if tk := r.PostFormValue("token"); tk != vtk {
		log.Printf("received invalid token %s\n", tk)
		return false
	}
	if url := r.PostFormValue("response_url"); url == "" {
		log.Println("did not receive response url")
		return false
	}
	return true
}

func getLastSlackMessage(c string) (string, error) {
	h, err := api.GetChannelHistory(c, slack.NewHistoryParameters())
	if err != nil {
		log.Printf("history API request error: %s", err)
		return "", err
	}

	for _, msg := range h.Messages {
		if msg.Text == "" {
			continue
		}
		return msg.Text, nil
	}

	err = errors.New("no last message found")
	log.Println(err)
	return "", err
}

func handleSlack(w http.ResponseWriter, r *http.Request) {
	status := http.StatusOK
	defer func() {
		w.WriteHeader(status)
	}()
	if !isValidSlackRequest(r) {
		status = http.StatusBadRequest
		return
	}
	channel := r.PostFormValue("channel_id")
	lastMessage, err := getLastSlackMessage(channel)
	if err != nil {
		status = http.StatusInternalServerError
		return
	}
	params := slack.NewPostMessageParameters()
	params.Username = "Spongebob"
	params.Attachments = []slack.Attachment{{
		Text:     transformText(lastMessage),
		Fallback: "*Spongebob mocking meme*",
		ImageURL: memeURL,
	}}
	params.IconURL = iconURL
	_, _, err = api.PostMessage(channel, "", params)
	if err != nil {
		status = http.StatusInternalServerError
	}
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		log.Fatal("$PORT must be set!")
	}
	if atk == "" {
		log.Fatal("$AUTHENTICATION_TOKEN must be set!")
	}
	if vtk == "" {
		log.Fatal("$VERIFICATION_TOKEN must be set!")
	}
	if appURL == "" {
		log.Fatal("$APP_URL must be set!")
	}

	http.Handle("/static", http.FileServer(http.Dir("/static")))
	http.HandleFunc("/slack", handleSlack)

	log.Fatal(http.ListenAndServe(":"+port, nil))
}
