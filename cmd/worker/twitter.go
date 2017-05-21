package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/dghubble/go-twitter/twitter"
	"github.com/dghubble/oauth1"
)

var (
	twitterUsername       string
	twitterConsumerKey    string
	twitterConsumerSecret string
	twitterAuthToken      string
	twitterAuthSecret     string

	twitterMentionRegex = regexp.MustCompile("^@\\w+\\s*")
	twitterTextRegex    = regexp.MustCompile("@\\w+|\\s+|.?")
	twitterQuoteRegex   = regexp.MustCompile("https://t\\.co/\\w+$")
	twitterAPIClient    *twitter.Client
	twitterUploadClient *http.Client
)

const (
	maxTweetLen              = 140
	groupThreshold           = 0.8
	twitterUploadURL         = "https://upload.twitter.com/1.1/media/upload.json"
	twitterUploadMetadataURL = "https://upload.twitter.com/1.1/media/metadata/create.json"
)

type twitterPlugin struct{}

func (p twitterPlugin) EnvVariables() []EnvVariable {
	return []EnvVariable{
		{
			Name:     "TWITTER_USERNAME",
			Variable: &twitterUsername,
		},
		{
			Name:     "TWITTER_CONSUMER_KEY",
			Variable: &twitterConsumerKey,
		},
		{
			Name:     "TWITTER_CONSUMER_SECRET",
			Variable: &twitterConsumerSecret,
		},
		{
			Name:     "TWITTER_ACCESS_TOKEN",
			Variable: &twitterAuthToken,
		},
		{
			Name:     "TWITTER_ACCESS_TOKEN_SECRET",
			Variable: &twitterAuthSecret,
		},
	}
}

func (p twitterPlugin) Name() string {
	return "twitter"
}

func NewTwitterPlugin() WorkerPlugin {
	return twitterPlugin{}
}

func (p twitterPlugin) Start(ch chan error) {
	defer close(ch)

	config := oauth1.NewConfig(twitterConsumerKey, twitterConsumerSecret)
	token := oauth1.NewToken(twitterAuthToken, twitterAuthSecret)

	httpClient := config.Client(oauth1.NoContext, token)
	twitterUploadClient = httpClient
	twitterAPIClient = twitter.NewClient(httpClient)

	params := &twitter.StreamUserParams{
		With:          "user",
		StallWarnings: twitter.Bool(true),
	}
	stream, err := twitterAPIClient.Streams.User(params)
	if err != nil {
		ch <- err
		return
	}

	demux := twitter.NewSwitchDemux()
	demux.Tweet = func(tweet *twitter.Tweet) {
		handleTweet(tweet, ch)
	}
	demux.DM = handleDM
	demux.StreamLimit = handleStreamLimit
	demux.StreamDisconnect = handleStreamDisconnect
	demux.Warning = handleWarning
	demux.Other = handleOther

	demux.HandleChan(stream.Messages)
}

func logMessage(msg interface{}, desc string) {
	if msgJSON, err := json.MarshalIndent(msg, "", "  "); err == nil {
		log.Printf("Received %s: %s\n", desc, string(msgJSON[:]))
	} else {
		logMessageStruct(msg, desc)
	}
}

func logMessageStruct(msg interface{}, desc string) {
	log.Printf("Received %s: %+v\n", desc, msg)
}

func trimReply(t string) string {
	for twitterMentionRegex.MatchString(t) {
		t = twitterMentionRegex.ReplaceAllString(t, "")
	}
	return t
}

func transformTwitterText(t string) string {
	var buffer bytes.Buffer
	letters := twitterTextRegex.FindAllString(t, -1)
	trFuncs := []func(string) string{
		strings.ToUpper,
		strings.ToLower,
	}
	idx := rand.Intn(2)
	groupSize := rand.Intn(2) + 1
	for _, ch := range letters {
		// ignore twitter usernames
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

func lookupTweetText(tweetID int64) (string, error) {
	params := twitter.StatusLookupParams{
		IncludeEntities: twitter.Bool(false),
	}
	tweets, resp, err := twitterAPIClient.Statuses.Lookup([]int64{tweetID}, &params)
	if err != nil {
		return "", fmt.Errorf("status lookup error: %s", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status lookup HTTP status code: %d", resp.StatusCode)
	}
	if len(tweets) == 0 {
		return "", errors.New("number of returned tweets is 0")
	}
	return fmt.Sprintf("@%s %s", tweets[0].User.ScreenName, trimReply(tweets[0].Text)), nil
}

type twitterImageData struct {
	ImageType string `json:"image_type"`
	Width     int    `json:"w"`
	Height    int    `json:"h"`
}

type twitterUploadResponse struct {
	MediaID          int64             `json:"media_id"`
	MediaIDStr       string            `json:"media_id_string"`
	Size             int               `json:"size"`
	ExpiresAfterSecs int               `json:"expires_after_secs"`
	Image            *twitterImageData `json:"image"`
}

func uploadImage() (int64, string, error) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	memeFile, err := os.Open(memePath)
	if err != nil {
		return 0, "", fmt.Errorf("opening meme image file error: %s", err)
	}
	defer memeFile.Close()

	fw, err := w.CreateFormFile("media", filepath.Base(memePath))
	if err != nil {
		return 0, "", fmt.Errorf("creating multipart form file header error: %s", err)
	}
	if _, err = io.Copy(fw, memeFile); err != nil {
		return 0, "", fmt.Errorf("io copy error: %s", err)
	}
	w.Close()

	req, err := http.NewRequest("POST", twitterUploadURL, &b)
	if err != nil {
		return 0, "", fmt.Errorf("creating POST request error: %s", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	res, err := twitterUploadClient.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("sending POST request error: %s", err)
	}

	id, idStr, err := parseUploadResponse(res)
	if err != nil {
		return 0, "", err
	}

	return id, idStr, nil
}

func parseUploadResponse(res *http.Response) (int64, string, error) {
	if res.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("image upload bad status: %s", res.Status)
	}
	defer res.Body.Close()

	var resBuf bytes.Buffer
	if _, err := resBuf.ReadFrom(res.Body); err != nil {
		return 0, "", fmt.Errorf("reading from http response body error: %s", err)
	}

	resp := twitterUploadResponse{}
	if err := json.Unmarshal(resBuf.Bytes(), &resp); err != nil {
		return 0, "", fmt.Errorf("unmarshalling twitter upload response error: %s", err)
	}

	// TODO: add logic dealing with the expires_after_secs
	return resp.MediaID, resp.MediaIDStr, nil
}

type twitterAltText struct {
	Text string `json:"text"`
}

type twitterImageMetadata struct {
	MediaID string          `json:"media_id"`
	AltText *twitterAltText `json:"alt_text"`
}

func uploadMetadata(mediaID, text string) error {
	md := twitterImageMetadata{
		MediaID: mediaID,
		AltText: &twitterAltText{
			Text: text,
		},
	}
	raw, err := json.Marshal(md)
	if err != nil {
		return fmt.Errorf("json marshal error: %s", err)
	}
	req, err := http.NewRequest("POST", twitterUploadMetadataURL, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("making http request error: %s", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")

	res, err := twitterUploadClient.Do(req)
	if err != nil {
		return fmt.Errorf("sending POST request error: %s", err)
	}

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("metadata upload returned status code %d", res.StatusCode)
	}

	return nil
}

func handleTweet(tweet *twitter.Tweet, ch chan error) {
	logMessageStruct(tweet, "Tweet")

	var tt string
	var err error
	if tweet.InReplyToStatusIDStr == "" {
		// case where someone tweets @ the bot

		if twitterQuoteRegex.MatchString(tweet.Text) {
			// quote retweets should mock the retweeted person
			shortenedURL := twitterQuoteRegex.FindString(tweet.Text)
			resp, err := http.Get(shortenedURL)
			if err != nil {
				ch <- fmt.Errorf("error following shortened url %s: %s", shortenedURL, err)
				return
			}
			tweetURL := resp.Request.URL
			resp.Body.Close()
			tweetIDStr := filepath.Base(tweetURL.Path)
			tweetID, err := strconv.ParseInt(tweetIDStr, 10, 64)
			if err != nil {
				ch <- fmt.Errorf("invalid tweet id %s", tweetIDStr)
				return
			}
			tt, err = lookupTweetText(tweetID)
			if err != nil {
				ch <- err
				return
			}
		} else {
			tt = tweet.Text
		}
	} else if tweet.User.ScreenName != twitterUsername {
		tt, err = lookupTweetText(tweet.InReplyToStatusID)
		if err != nil {
			ch <- err
			return
		}
	} else {
		return
	}

	rt := fmt.Sprintf("@%s %s", tweet.User.ScreenName, transformTwitterText(tt))
	if len(rt) > maxTweetLen {
		log.Println("Exceeded max tweet length:", len(rt), rt)
		rt = fmt.Sprintf("@%s %s", tweet.User.ScreenName, transformTwitterText(trimReply(tt)))
	}
	mediaID, mediaIDStr, err := uploadImage()
	if err != nil {
		ch <- fmt.Errorf("upload image error: %s", err)
		return
	}
	if err = uploadMetadata(mediaIDStr, tt); err != nil {
		// we can continue from a metadata upload error
		// because it is not essential
		ch <- fmt.Errorf("metadata upload error: %s", err)
	}

	params := twitter.StatusUpdateParams{
		InReplyToStatusID: tweet.ID,
		TrimUser:          twitter.Bool(true),
		MediaIds:          []int64{mediaID},
	}
	_, resp, err := twitterAPIClient.Statuses.Update(rt, &params)
	if err != nil {
		ch <- fmt.Errorf("status update error: %s", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		ch <- fmt.Errorf("response tweet status code: %d", resp.StatusCode)
		return
	}
}

func handleDM(dm *twitter.DirectMessage) {
	logMessage(dm, "DM")
}

func handleStreamLimit(sl *twitter.StreamLimit) {
	logMessage(sl, "stream limit message")
}

func handleStreamDisconnect(sd *twitter.StreamDisconnect) {
	logMessage(sd, "stream disconnect message")
}

func handleWarning(w *twitter.StallWarning) {
	logMessage(w, "stall warning")
}

func handleOther(message interface{}) {
	logMessage(message, `"other" message type`)
}
