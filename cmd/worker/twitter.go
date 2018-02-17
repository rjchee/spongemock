package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
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

	twitterAPIClient    *twitter.Client
	twitterUploadClient *http.Client

	tweetURLPattern = regexp.MustCompile("^https?://twitter.com/\\w+/status/(?P<tweet_id>\\d+)$")
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

func (p twitterPlugin) Start(ch chan<- error) {
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
		handleTweet(tweet, ch, true)
	}
	demux.DM = func(dm *twitter.DirectMessage) {
		handleDM(dm, ch)
	}
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

func lookupTweet(tweetID int64) (*twitter.Tweet, error) {
	params := twitter.StatusShowParams{
		TweetMode: "extended",
	}
	tweet, resp, err := twitterAPIClient.Statuses.Show(tweetID, &params)
	if err != nil {
		return nil, fmt.Errorf("status lookup error: %s", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("status lookup HTTP status code: %d", resp.StatusCode)
	}
	if tweet == nil {
		return nil, errors.New("number of returned tweets is 0")
	}
	return tweet, nil
}

func lookupTweetText(tweetID int64) (string, error) {
	tweet, err := lookupTweet(tweetID)
	if err != nil {
		return "", err
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
		return string([]rune(text)[i.Start():i.End()])
	}
	return text
}

func handleTweet(tweet *twitter.Tweet, ch chan<- error, followQuoteRetweet bool) (*twitter.Tweet, error) {
	switch {
	case tweet.User.ScreenName == twitterUsername:
		return nil, errors.New("cannot mock my own tweet")
	case tweet.RetweetedStatus != nil:
		return nil, errors.New("cannot mock a retweet")
	}
	logMessageStruct(tweet, "Tweet")

	mentions := []string{"@" + tweet.User.ScreenName}
	text := extractText(tweet)
	var err error
	if tweet.InReplyToStatusIDStr == "" ||
		!strings.Contains(text, "@"+twitterUsername) {
		// remove twitter username mention
		if strings.HasPrefix(text, "@"+twitterUsername+" ") {
			text = text[len(twitterUsername)+2:]
		}
		if followQuoteRetweet && tweet.QuotedStatus != nil {
			// quote retweets should mock the retweeted person if followQuoteRetweet is true
			text = extractText(tweet.QuotedStatus)
			mentions = append(mentions, "@"+tweet.QuotedStatus.User.ScreenName)
		}
	} else {
		// mock the text the user replied to
		text, err = lookupTweetText(tweet.InReplyToStatusID)
		if err != nil {
			ch <- err
			return nil, err
		}
		if tweet.InReplyToScreenName != twitterUsername {
			mentions = append(mentions, "@"+tweet.InReplyToScreenName)
		}
	}

	log.Println("tweet text:", text)

	finalTweets := finalizeTweet(mentions, text)

	if DEBUG {
		for _, finalTweet := range finalTweets {
			log.Println("tweeting:", finalTweet)
		}
		return nil, errors.New("cannot send a tweet in DEBUG mode")
	} else {
		mediaID, mediaIDStr, cached, err := uploadImage()
		if err != nil {
			err = fmt.Errorf("upload image error: %s", err)
			ch <- err
			return nil, err
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

		var res *twitter.Tweet
		for i, finalTweet := range finalTweets {
			sentTweet, resp, err := twitterAPIClient.Statuses.Update(finalTweet, &params)
			if err != nil {
				err = fmt.Errorf("status update error: %s", err)
				ch <- err
				return nil, err
			}
			resp.Body.Close()

			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				err = fmt.Errorf("response tweet status code: %d", resp.StatusCode)
				ch <- err
				return nil, err
			}
			params.InReplyToStatusID = sentTweet.ID
			if i == 0 {
				res = sentTweet
			}
		}
		return res, nil
	}
}

func extractTweetFromDM(dm *twitter.DirectMessage) (*twitter.Tweet, error) {
	// Is this a link to a tweet?
	if dm.Entities != nil {
		for _, urlEntity := range dm.Entities.Urls {
			if r := tweetURLPattern.FindStringSubmatch(urlEntity.ExpandedURL); r != nil {
				if tweetID, err := strconv.ParseInt(r[1], 10, 64); err == nil {
					// we don't need to check for errors at this point since it cannot be any other kind of message
					return lookupTweet(tweetID)
				} else {
					panic(fmt.Errorf("tweetURLPattern regexp matched a tweet %s with an unparseable tweet ID %d", urlEntity.ExpandedURL, r[1]))
				}
			}
		}
	}
	// is this a tweet ID?
	if tweetID, err := strconv.ParseInt(dm.Text, 10, 64); err == nil {
		if tweet, err := lookupTweet(tweetID); err == nil {
			return tweet, nil
		}
	}

	return nil, errors.New("no tweet found in dm")
}

func sendDM(text string, userID int64) (*twitter.DirectMessage, error) {
	log.Printf("sending a dm to userID %d: %s\n", userID, text)
	dm, resp, err := twitterAPIClient.DirectMessages.New(&twitter.DirectMessageNewParams{
		UserID: userID,
		Text:   text,
	})
	if err != nil {
		return nil, fmt.Errorf("new dm error: %s", err)
	}
	resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("new dm response status code : %d", resp.StatusCode)
	}
	return dm, nil
}

func handleDM(dm *twitter.DirectMessage, ch chan<- error) {
	if dm.RecipientScreenName != twitterUsername {
		// don't react these events
		return
	}
	logMessageStruct(dm, "DM")

	if tweet, err := extractTweetFromDM(dm); err != nil {
		if dm.SenderScreenName != twitterUsername {
			// no tweet found, just mock the user dm'ing the bot
			responseText := transformTwitterText(dm.Text)
			if DEBUG {
				log.Println("dm'ing back:", responseText)
			} else {
				_, err := sendDM(responseText, dm.SenderID)
				if err != nil {
					ch <- err
					return
				}
			}
		} else {
			log.Println("DM'd self with invalid message", dm.Text)
		}
	} else {
		if tweet, err := handleTweet(tweet, ch, false); err != nil {
			ch <- fmt.Errorf("error handling tweet from dm: %s", err)
			_, err := sendDM(transformTwitterText("An error occurred. Please try again"), dm.SenderID)
			if err != nil {
				ch <- err
				return
			}
		} else {
			_, err := sendDM(fmt.Sprintf("https://twitter.com/%s/status/%s", twitterUsername, tweet.IDStr), dm.SenderID)
			if err != nil {
				ch <- err
				return
			}
		}
	}
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
