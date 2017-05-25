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
	twitterRetweetRegex  = regexp.MustCompile("^RT @\\w{1,15}:")
	twitterMentionRegex  = regexp.MustCompile("^@\\w{1,15}\\s*")
	twitterTextRegex     = regexp.MustCompile("@\\w{1,15}|\\s+|.?")
	twitterQuoteRegex    = regexp.MustCompile("https?://t\\.co/\\w+$")
	twitterTextTrimRegex = regexp.MustCompile(" ?@\\w{1,15}(\\s+|$)|.")
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

func finalizeTweet(mentions, text string) string {
	if len(mentions)+len(text) > maxTweetLen {
		// first strategy to reduce tweet length is to trim out the unnecessary
		// replies and links
		text = trimReply(text)

		if len(mentions)+len(text) > maxTweetLen {
			// last strategy to reduce tweet length is to randomly remove
			// letters from the text until it fits. The algorithm below tries
			// to avoid other user mentions

			indices := twitterTextTrimRegex.FindAllStringIndex(text, -1)
			n := len(text)
			for len(mentions)+n > maxTweetLen {
				// avoid removing the first character because it would be
				// obvious if it's missing
				idx := rand.Intn(len(indices)-1) + 1
				original := idx
				if indices[idx][1]-indices[idx][0] > 1 {
					idx = (idx + 1) % len(indices)
					if idx == 0 {
						idx = 1
					}
					for idx != original && indices[idx][1]-indices[idx][0] > 1 {
						idx = (idx + 1) % len(indices)
						if idx == 0 {
							idx = 1
						}
					}
				}
				i := indices[idx]
				indices = append(indices[:idx], indices[idx+1:]...)
				n -= i[1] - i[0]
			}

			var buf bytes.Buffer
			for _, i := range indices {
				buf.WriteString(text[i[0]:i[1]])
			}
			text = buf.String()
		}
	}

	return mentions + transformTwitterText(text)
}

func trimReply(t string) string {
	t = trimQuotesAndRT(t)
	for twitterMentionRegex.MatchString(t) {
		t = twitterMentionRegex.ReplaceAllString(t, "")
	}
	return t
}

func trimQuotesAndRT(t string) string {
	for twitterRetweetRegex.MatchString(t) {
		t = twitterRetweetRegex.ReplaceAllString(t, "")
	}
	for twitterQuoteRegex.MatchString(t) {
		t = twitterQuoteRegex.ReplaceAllString(t, "")
	}
	return t
}
