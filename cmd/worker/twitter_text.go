package main

import (
	"bytes"
	"math/rand"
	"regexp"
	"strings"
)

const (
	maxTweetLen = 140
)

var (
	twitterTextRegex = regexp.MustCompile("@\\w{1,15}|\\s+|.?")
)

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

func finalizeTweet(mentions []string, text string) []string {
	var tweets []string
	tweet := strings.Join(append(mentions, text), " ")
	if len(tweet) > maxTweetLen {
		tweets = append(tweets, tweet[:maxTweetLen])
		mentions = append([]string{"@" + twitterUsername}, mentions...)
		for {
			tweet = strings.Join(append(mentions, tweet[maxTweetLen:]), " ")
			if len(tweet) > maxTweetLen {
				tweets = append(tweets, tweet[:maxTweetLen])
			} else {
				tweets = append(tweets, tweet)
				break
			}
		}
	} else {
		tweets = append(tweets, tweet)
	}

	return tweets
}
