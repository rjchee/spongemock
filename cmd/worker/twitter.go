package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
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

	twitterAPIClient    *twitter.Client
	twitterUploadClient *http.Client
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

func lookupTweetText(tweetID int64) (string, error) {
	params := twitter.StatusShowParams{
		TweetMode: "extended",
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
	return extractText(tweet), nil
}

func extractText(tweet *twitter.Tweet) string {
	var text string
	if tweet.FullText == "" {
		text = tweet.Text
	} else {
		text = tweet.FullText
	}
	if i := tweet.DisplayTextRange; i.End() > 0 {
		return text[i.Start():i.End()]
	}
	return text
}

func handleTweet(tweet *twitter.Tweet, ch chan error) {
	switch {
	case tweet.User.ScreenName == twitterUsername:
		return
	case tweet.RetweetedStatus != nil:
		return
	}
	logMessageStruct(tweet, "Tweet")

	mentions := []string{"@" + tweet.User.ScreenName}
	text := extractText(tweet)
	var err error
	if tweet.InReplyToStatusIDStr == "" ||
		!strings.Contains(text, "@"+twitterUsername) {
		if tweet.QuotedStatus != nil {
			// quote retweets should mock the retweeted person
			text = extractText(tweet.QuotedStatus)
			mentions = append(mentions, "@"+tweet.QuotedStatus.User.ScreenName)
		}
	} else {
		// mock the text the user replied to
		text, err = lookupTweetText(tweet.InReplyToStatusID)
		if err != nil {
			ch <- err
			return
		}
		mentions = append(mentions, "@"+tweet.InReplyToScreenName)
	}

	finalTweets := finalizeTweet(mentions, text)

	if DEBUG {
		log.Println("tweeting:", finalTweets)
	} else {
		mediaID, mediaIDStr, cached, err := uploadImage()
		if err != nil {
			ch <- fmt.Errorf("upload image error: %s", err)
			return
		}
		if !cached {
			if err = uploadMetadata(mediaIDStr, text); err != nil {
				// we can continue from a metadata upload error
				// because it is not essential
				ch <- fmt.Errorf("metadata upload error: %s", err)
			}
		}

		params := twitter.StatusUpdateParams{
			InReplyToStatusID: tweet.ID,
			TrimUser:          twitter.Bool(true),
			MediaIds:          []int64{mediaID},
		}

		for _, finalTweet := range finalTweets {
			sentTweet, resp, err := twitterAPIClient.Statuses.Update(finalTweet, &params)
			if err != nil {
				ch <- fmt.Errorf("status update error: %s", err)
				return
			}
			resp.Body.Close()

			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				ch <- fmt.Errorf("response tweet status code: %d", resp.StatusCode)
				return
			}
			params.InReplyToStatusID = sentTweet.ID
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
