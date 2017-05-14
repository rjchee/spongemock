package main

import (
	"bytes"
	"errors"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/nlopes/slack"
)

const (
	iconPath = "/static/icon.png"
	memePath = "/static/spongemock.jpg"

	username = "Spongebob"
	fallback = "*Spongebob mocking meme*"
)

var (
	atk     = os.Getenv("AUTHENTICATION_TOKEN")
	vtk     = os.Getenv("VERIFICATION_TOKEN")
	appURL  = os.Getenv("APP_URL")
	iconURL string
	memeURL string
	api     = slack.New(atk)

	textRegexp = regexp.MustCompile("&amp;|&lt;|&gt;|.?")
	userRegexp = regexp.MustCompile("^<@(U[0-9A-F]+)\\|.+?>$")
)

func transformText(m string) string {
	var buffer bytes.Buffer
	letters := textRegexp.FindAllString(m, -1)
	for _, ch := range letters {
		// ignore html escaped entities
		if len(ch) > 1 {
			buffer.WriteString(ch)
			continue
		}
		if rand.Int()%2 == 0 {
			ch = strings.ToUpper(ch)
		} else {
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

func getLastSlackMessage(c string, u string) (string, error) {
	if u != "" {
		log.Printf("searching for messages by user %s\n", u)
	}
	h, err := api.GetChannelHistory(c, slack.NewHistoryParameters())
	if err != nil {
		log.Printf("history API request error: %s", err)
		return "", err
	}

	for _, msg := range h.Messages {
		log.Printf("message: %v\n", msg)
		// don't support message subtypes for now
		if msg.SubType != "" || msg.Text == "" {
			continue
		}
		// if a user is supplied, search for the last message by a user
		if u != "" && msg.User != u {
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
	reqText := r.PostFormValue("text")
	log.Printf("command: %s %s\n", r.PostFormValue("command"), reqText)
	var message string
	var err error
	if reqText == "" {
		message, err = getLastSlackMessage(channel, "")
		if err != nil {
			status = http.StatusInternalServerError
			return
		}
	} else if userRegexp.MatchString(reqText) {
		message, err = getLastSlackMessage(channel, userRegexp.FindStringSubmatch(reqText)[1])
		if err != nil {
			status = http.StatusInternalServerError
			return
		}
	} else {
		status = http.StatusBadRequest
		log.Println(len(reqText), reqText)
		return
	}

	mockedText := transformText(message)
	if mockedText == "" {
		status = http.StatusInternalServerError
		return
	}
	params := slack.NewPostMessageParameters()
	params.Username = username
	params.Attachments = []slack.Attachment{{
		Text:     mockedText,
		Fallback: fallback,
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
	u, err := url.Parse(appURL)
	if err != nil {
		log.Fatal("invalid $APP_URL %s", appURL)
	}
	icon, _ := url.Parse(iconPath)
	iconURL = u.ResolveReference(icon).String()
	meme, _ := url.Parse(memePath)
	memeURL = u.ResolveReference(meme).String()

	fs := http.FileServer(http.Dir("static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))
	http.HandleFunc("/slack", handleSlack)

	log.Fatal(http.ListenAndServe(":"+port, nil))
}
