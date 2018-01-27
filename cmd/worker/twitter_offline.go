package main

import (
	"database/sql"
	"fmt"
	"log"
	"sort"

	"github.com/dghubble/go-twitter/twitter"
)

func handleOfflineActivity(ch chan<- error) {
	err := ensureTimelineTableExists()
	if err != nil {
		ch <- err
		return
	}
	handleOfflineTweets(ch)
	handleOfflineDMs(ch)
}

func handleOfflineTweets(ch chan<- error) {
	id, err := queryLastID("mentions")
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
		if err := updateLastID(id == 0, "mentions", twitterSinceID); err != nil {
			ch <- err
			return
		}
	}
}

type byID []twitter.DirectMessage

func (a byID) Len() int           { return len(a) }
func (a byID) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a byID) Less(i, j int) bool { return a[i].ID < a[j].ID }

func handleOfflineDMs(ch chan<- error) {
	id, err := queryLastID("direct_messages")
	if err != nil {
		ch <- err
		return
	}
	rcvd, d1 := getReceivedDMStream(id, ch)
	sent, d2 := getSentDMStream(id, ch)

	conversations := make(map[int64][]twitter.DirectMessage)
	rcvdDone, sentDone := false, false
	for !rcvdDone || !sentDone {
		select {
		case rcv, ok := <-rcvd:
			if ok && rcv.SenderID != rcv.RecipientID {
				conversations[rcv.SenderID] = append(conversations[rcv.SenderID], rcv)
			}
		case sentDM, ok := <-sent:
			if ok && sentDM.SenderID != sentDM.RecipientID {
				conversations[sentDM.RecipientID] = append(conversations[sentDM.RecipientID], sentDM)
			}
		case <-d1:
			rcvdDone = true
		case <-d2:
			sentDone = true
		}
	}
	var latestDMID int64 = id
	for userID := range conversations {
		sort.Sort(byID(conversations[userID]))
		convo := conversations[userID]
		if latestID := convo[len(convo)-1].ID; latestID > latestDMID {
			latestDMID = latestID
		}

		// reply to all messages after latest dm sent by bot
		var i int
		for i = len(convo) - 1; i >= 0; i-- {
			if convo[i].SenderScreenName == twitterUsername {
				break
			}
		}
		convo = convo[i+1:]
		for _, dm := range convo {
			handleDM(&dm, ch)
		}
	}

	if DEBUG {
		log.Println("latestDMID:", latestDMID)
	} else {
		// store next id in db
		if err := updateLastID(id == 0, "direct_messages", latestDMID); err != nil {
			ch <- err
			return
		}
	}
}

func ensureTimelineTableExists() error {
	row := DB.QueryRow("SELECT EXISTS(SELECT * FROM information_schema.tables WHERE table_name=$1);", "tw_timeline_ids")
	var tableExists bool
	err := row.Scan(&tableExists)
	if err != nil {
		return err
	}
	if !tableExists {
		_, err := DB.Exec("CREATE TABLE tw_timeline_ids (id serial PRIMARY KEY, name text NOT NULL UNIQUE, tid bigint NOT NULL);")
		if err != nil {
			return err
		}
	}
	return nil
}

func queryLastID(key string) (int64, error) {
	if DB == nil {
		// query from the start of time
		return 0, nil
	}
	row := DB.QueryRow("SELECT tid FROM tw_timeline_ids WHERE name=$1", key)
	var id int64
	err := row.Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	} else if err != nil {
		return 0, err
	}

	return id, nil
}

func updateLastID(insert bool, key string, lastID int64) error {
	if insert {
		_, err := DB.Exec("INSERT INTO tw_timeline_ids (name, tid) VALUES ($1, $2);", key, lastID)
		if err != nil {
			return fmt.Errorf("error inserting since id into db: %s", err)
		}
	} else {
		_, err := DB.Exec("UPDATE tw_timeline_ids SET tid=$1 WHERE name=$2", lastID, key)
		if err != nil {
			return fmt.Errorf("error updating db: %s", err)
		}
	}
	return nil
}

func getUserTimelineStream(sinceID int64, ch chan<- error, done <-chan struct{}) <-chan twitter.Tweet {
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

func getMentionTimelineStream(sinceID int64, ch chan<- error) <-chan twitter.Tweet {
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

func getReceivedDMStream(sinceID int64, ch chan<- error) (<-chan twitter.DirectMessage, <-chan struct{}) {
	dmCh := make(chan twitter.DirectMessage)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { done <- struct{}{} }()
		defer close(dmCh)
		params := &twitter.DirectMessageGetParams{
			SinceID:         sinceID,
			Count:           200,
			IncludeEntities: twitter.Bool(true),
			SkipStatus:      twitter.Bool(false),
		}
		for {
			dms, resp, err := twitterAPIClient.DirectMessages.Get(params)
			if err != nil {
				ch <- err
				return
			}
			resp.Body.Close()
			if len(dms) == 0 {
				return
			}
			for _, dm := range dms {
				dmCh <- dm
			}
			params.MaxID = dms[len(dms)-1].ID - 1
		}
	}()
	return dmCh, done
}

func getSentDMStream(sinceID int64, ch chan<- error) (<-chan twitter.DirectMessage, <-chan struct{}) {
	dmCh := make(chan twitter.DirectMessage)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { done <- struct{}{} }()
		defer close(dmCh)
		params := &twitter.DirectMessageSentParams{
			SinceID:         sinceID,
			Count:           200,
			IncludeEntities: twitter.Bool(true),
		}
		for {
			dms, resp, err := twitterAPIClient.DirectMessages.Sent(params)
			if err != nil {
				ch <- err
				return
			}
			resp.Body.Close()
			if len(dms) == 0 {
				return
			}
			for _, dm := range dms {
				dmCh <- dm
			}
			params.MaxID = dms[len(dms)-1].ID - 1
		}
	}()
	return dmCh, done
}
