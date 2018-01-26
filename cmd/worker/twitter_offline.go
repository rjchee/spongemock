package main

import (
	"database/sql"
	"fmt"
	"log"

	"github.com/dghubble/go-twitter/twitter"
)

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
	// close the user timeline stream if necessary
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
			ScreenName:      twitterUsername,
			SinceID:         sinceID,
			TrimUser:        twitter.Bool(true),
			ExcludeReplies:  twitter.Bool(false),
			IncludeRetweets: twitter.Bool(false),
			TweetMode:       "extended",
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
			SinceID:   sinceID,
			TweetMode: "extended",
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
