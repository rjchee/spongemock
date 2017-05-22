package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"regexp"
	"strings"

	"github.com/nlopes/slack"
)

var (
	slackAuthToken         string
	slackVerificationToken string
	slackAPI               *slack.Client

	slackTextRegex = regexp.MustCompile("&amp;|&lt;|&gt;|<.+?>|\\s+|.?")
	slackUserRegex = regexp.MustCompile("^<@(U\\w+)\\|.+?>$")
)

type slackPlugin struct{}

func (p slackPlugin) EnvVariables() []EnvVariable {
	return []EnvVariable{
		{
			Name:     "SLACK_OAUTH_TOKEN",
			Variable: &slackAuthToken,
		},
		{
			Name:     "SLACK_VERIFICATION_TOKEN",
			Variable: &slackVerificationToken,
		},
	}
}

func (p slackPlugin) RegisterHandles(m *http.ServeMux) {
	slackAPI = slack.New(slackAuthToken)
	m.HandleFunc("/slack", handleSlack)
}

func (p slackPlugin) Name() string {
	return "slack"
}

func NewSlackPlugin() WebPlugin {
	return slackPlugin{}
}

const (
	slackUsername = "Spongebob"
	slackFallback = "*Spongebob mocking meme*"
)

func transformSlackText(m string) string {
	var buffer bytes.Buffer
	letters := slackTextRegex.FindAllString(m, -1)
	trFuncs := []func(string) string{
		strings.ToUpper,
		strings.ToLower,
	}
	idx := rand.Intn(2)
	groupSize := rand.Intn(2) + 1
	for _, ch := range letters {
		// ignore html escaped entities
		if len(ch) == 1 && strings.TrimSpace(ch) != "" {
			ch = trFuncs[idx](ch)
			groupSize--
			if groupSize == 0 {
				idx = (idx + 1) % 2
				groupSize = 1
				if rand.Float64() > groupThreshold {
					groupSize++
				}
			}
		}
		buffer.WriteString(ch)
	}
	return buffer.String()
}

type slackResponseType string

const (
	inChannel slackResponseType = "in_channel"
	ephemeral slackResponseType = "ephemeral"
)

type slackSlashResponse struct {
	ResponseType slackResponseType `json:"response_type,omitempty"`
	Text         string            `json:"text"`
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
	if tk := r.PostFormValue("token"); tk != slackVerificationToken {
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
	histParams := slack.NewHistoryParameters()
	var h *slack.History
	var err error
	if c[0] == 'C' {
		h, err = slackAPI.GetChannelHistory(c, histParams)
	} else if c[0] == 'G' {
		// handle private channels
		h, err = slackAPI.GetGroupHistory(c, histParams)
	} else if c[0] == 'D' {
		// handle direct messages
		h, err = slackAPI.GetIMHistory(c, histParams)
	} else {
		err = fmt.Errorf("unknown channel type, channel_id = %s", c)
	}
	if err != nil {
		log.Printf("history API request error: %s\n", err)
		return "", err
	}

	for _, msg := range h.Messages {
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
	response := slackSlashResponse{}
	defer func() {
		if response != (slackSlashResponse{}) {
			output, err := json.Marshal(response)
			if err != nil {
				status = http.StatusInternalServerError
				log.Printf("error marshalling response json: %s\n", err)
			} else {
				w.Header().Add("Content-type", "application/json")
				defer w.Write(output)
			}
		}
		w.WriteHeader(status)
	}()
	if !isValidSlackRequest(r) {
		status = http.StatusBadRequest
		return
	}
	channel := r.PostFormValue("channel_id")
	reqText := r.PostFormValue("text")
	log.Printf("incoming command: %s %s\n", r.PostFormValue("command"), reqText)
	var message string
	var err error
	if reqText == "" {
		message, err = getLastSlackMessage(channel, "")
		if err != nil {
			status = http.StatusInternalServerError
			return
		}
	} else if slackUserRegex.MatchString(reqText) {
		message, err = getLastSlackMessage(channel, slackUserRegex.FindStringSubmatch(reqText)[1])
		if err != nil {
			status = http.StatusInternalServerError
			return
		}
	} else if reqText == "help" {
		response.ResponseType = ephemeral
		response.Text = strings.Join([]string{
			"`/spongemock` will mock the last message in the channel",
			"`/spongemock @user` will mock the last message from that user",
			"`/spongemock text` will mock the given text",
		}, "\n")
		return
	} else {
		message = reqText
	}

	mockedText := transformSlackText(message)
	if mockedText == "" {
		status = http.StatusInternalServerError
		return
	}
	params := slack.NewPostMessageParameters()
	params.Username = slackUsername
	params.Attachments = []slack.Attachment{{
		Text:     mockedText,
		Fallback: slackFallback,
		ImageURL: MemeURL,
	}}
	params.IconURL = IconURL
	_, _, err = slackAPI.PostMessage(channel, "", params)
	if err != nil {
		status = http.StatusInternalServerError
	}
}
