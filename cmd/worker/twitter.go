package main

import (
	"bytes"
	"database/sql"
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
	twitterQuoteRegex   = regexp.MustCompile("https?://t\\.co/\\w+$")
	twitterAPIClient    *twitter.Client
	twitterUploadClient *http.Client
)

const (
	maxTweetLen              = 140
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

	handleOfflineActivity(ch)

	stream, err := twitterAPIClient.Streams.User(&twitter.StreamUserParams{
		With:          "user",
		StallWarnings: twitter.Bool(true),
	})
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

func handleOfflineActivity(ch chan error) {
	id, err := queryLastMentionID()
	if err != nil {
		ch <- err
		return
	}
	twitterSinceID := id

	mentions := getMentionTimelineStream(id, ch)
	// make the done channel non-blocking
	done := make(chan struct{}, 2)
	defer close(done)
	tweets := getUserTimelineStream(id, ch, done)

	var lastTweetID int64
	lastTweet, open := <-tweets
	if open {
		lastTweetID = lastTweet.InReplyToStatusID
		if lastTweetID > twitterSinceID {
			// after responding to all previous tweets, newly added tweets can
			// only be later than this id
			twitterSinceID = lastTweetID
		}
	}

	for mention := range mentions {
		for open && (lastTweetID > mention.ID || lastTweetID == 0) {
			lastTweet, open = <-tweets
			if open {
				lastTweetID = lastTweet.InReplyToStatusID
				if lastTweetID > twitterSinceID {
					twitterSinceID = lastTweetID
				}
			}
		}
		if lastTweetID != 0 && lastTweetID != mention.ID {
			// mention hasn't been responded to
			handleTweet(&mention, ch)
		}
	}
	// close the user timeline stream if necesary
	if open {
		// send done message to the tweet channel if it's not done
		done <- struct{}{}
		// unblock the user timeline channel
		<-tweets
	}

	if DEBUG {
		log.Println("twitterSinceID:", twitterSinceID)
	} else {
		// store next id in db
		if id == 0 {
			// we never stored the id into the db
			_, err := DB.Exec("INSERT INTO tw_timeline_ids (name, tid) VALUES ($1, $2);", "mentions", twitterSinceID)
			if err != nil {
				ch <- fmt.Errorf("error inserting since id into db: %s", err)
				return
			}
		} else {
			_, err := DB.Exec("UPDATE tw_timeline_ids SET tid=$1 WHERE name=$2", twitterSinceID, "mentions")
			if err != nil {
				ch <- fmt.Errorf("error updating db: %s", err)
				return
			}
		}
	}
}

func queryLastMentionID() (int64, error) {
	if DB == nil {
		// query from the start of time
		return 0, nil
	}
	row := DB.QueryRow("SELECT EXISTS(SELECT * FROM information_schema.tables WHERE table_name=$1);", "tw_timeline_ids")
	var tableExists bool
	err := row.Scan(&tableExists)
	if err != nil {
		return 0, err
	}
	if !tableExists {
		_, err := DB.Exec("CREATE TABLE tw_timeline_ids (id serial PRIMARY KEY, name text NOT NULL UNIQUE, tid bigint NOT NULL);")
		if err != nil {
			return 0, err
		}
	}
	row = DB.QueryRow("SELECT tid FROM tw_timeline_ids WHERE name=$1", "mentions")
	var mentionID int64
	err = row.Scan(&mentionID)
	if err == sql.ErrNoRows {
		return 0, nil
	} else if err != nil {
		return 0, err
	}

	return mentionID, nil
}

func getUserTimelineStream(sinceID int64, ch chan error, done chan struct{}) chan twitter.Tweet {
	tweetCh := make(chan twitter.Tweet, 20)
	go func() {
		defer close(tweetCh)
		params := twitter.UserTimelineParams{
			ScreenName:     twitterUsername,
			SinceID:        sinceID,
			TrimUser:       twitter.Bool(true),
			ExcludeReplies: twitter.Bool(false),
		}

		for {
			tweets, resp, err := twitterAPIClient.Timelines.UserTimeline(&params)
			if err != nil {
				ch <- fmt.Errorf("error getting user timeline: %s", err)
				return
			}
			resp.Body.Close()
			if len(tweets) == 0 {
				return
			}
			for _, tweet := range tweets {
				select {
				case <-done:
					return
				default:
					tweetCh <- tweet
				}
			}
			params.MaxID = tweets[len(tweets)-1].ID - 1
		}
	}()
	return tweetCh
}

func getMentionTimelineStream(sinceID int64, ch chan error) chan twitter.Tweet {
	tweetCh := make(chan twitter.Tweet, 20)
	go func() {
		defer close(tweetCh)
		params := twitter.MentionTimelineParams{
			SinceID: sinceID,
		}

		for {
			tweets, resp, err := twitterAPIClient.Timelines.MentionTimeline(&params)
			if err != nil {
				ch <- fmt.Errorf("error getting mention timeline: %s", err)
				return
			}
			resp.Body.Close()
			if len(tweets) == 0 {
				return
			}
			for _, tweet := range tweets {
				tweetCh <- tweet
			}
			params.MaxID = tweets[len(tweets)-1].ID - 1
		}
	}()
	return tweetCh
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
	t = trimQuotes(t)
	return t
}

func trimQuotes(t string) string {
	for twitterQuoteRegex.MatchString(t) {
		t = twitterQuoteRegex.ReplaceAllString(t, "")
	}
	return t
}

func transformTwitterText(t string) string {
	t = trimQuotes(t)
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
	params := twitter.StatusShowParams{
		IncludeEntities: twitter.Bool(false),
	}
	tweet, resp, err := twitterAPIClient.Statuses.Show(tweetID, &params)
	if err != nil {
		return "", fmt.Errorf("status lookup error: %s", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("status lookup HTTP status code: %d", resp.StatusCode)
	}
	if tweet == nil {
		return "", errors.New("number of returned tweets is 0")
	}
	return fmt.Sprintf("@%s %s", tweet.User.ScreenName, trimReply(tweet.Text)), nil
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
	if res.StatusCode < 200 || res.StatusCode >= 300 {
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

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("metadata upload returned status code %d", res.StatusCode)
	}

	return nil
}

func handleTweet(tweet *twitter.Tweet, ch chan error) {
	if tweet.User.ScreenName == twitterUsername {
		return
	}
	logMessageStruct(tweet, "Tweet")

	var tt string
	var err error
	if tweet.InReplyToStatusIDStr == "" ||
		(tweet.InReplyToScreenName == twitterUsername &&
			tweet.Text != fmt.Sprintf("@%s", twitterUsername)) {
		if tweet.QuotedStatus != nil {
			// quote retweets should mock the retweeted person
			tt = tweet.QuotedStatus.Text
		} else {
			// case where someone tweets @ the bot
			tt = trimReply(tweet.Text)
		}
	} else {
		// mock the text the user replied to
		tt, err = lookupTweetText(tweet.InReplyToStatusID)
		if err != nil {
			ch <- err
			return
		}
	}

	rt := fmt.Sprintf("@%s %s", tweet.User.ScreenName, transformTwitterText(tt))
	if len(rt) > maxTweetLen {
		log.Println("Exceeded max tweet length:", len(rt), rt)
		rt = fmt.Sprintf("@%s %s", tweet.User.ScreenName, transformTwitterText(trimReply(tt)))
	}

	if DEBUG {
		log.Println("tweeting:", rt)
	} else {
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

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			ch <- fmt.Errorf("response tweet status code: %d", resp.StatusCode)
			return
		}
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
