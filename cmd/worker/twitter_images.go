package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

var (
	uploadExpireTime time.Time
	lastMediaID      int64
	lastMediaIDStr   string
)

const (
	// how much time to subtract from the media upload time in seconds
	mediaUploadBuffer        = 10
	twitterUploadURL         = "https://upload.twitter.com/1.1/media/upload.json"
	twitterUploadMetadataURL = "https://upload.twitter.com/1.1/media/metadata/create.json"
)

func uploadImage() (int64, string, bool, error) {
	if time.Now().Before(uploadExpireTime) {
		log.Println("retrieving cached values", lastMediaIDStr)
		return lastMediaID, lastMediaIDStr, true, nil
	}
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	memeFile, err := os.Open(memePath)
	if err != nil {
		return 0, "", false, fmt.Errorf("opening meme image file error: %s", err)
	}
	defer memeFile.Close()

	fw, err := w.CreateFormFile("media", filepath.Base(memePath))
	if err != nil {
		return 0, "", false, fmt.Errorf("creating multipart form file header error: %s", err)
	}
	if _, err = io.Copy(fw, memeFile); err != nil {
		return 0, "", false, fmt.Errorf("io copy error: %s", err)
	}
	w.Close()

	req, err := http.NewRequest("POST", twitterUploadURL, &b)
	if err != nil {
		return 0, "", false, fmt.Errorf("creating POST request error: %s", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	res, err := twitterUploadClient.Do(req)
	if err != nil {
		return 0, "", false, fmt.Errorf("sending POST request error: %s", err)
	}

	id, idStr, err := parseUploadResponse(res)
	if err != nil {
		return 0, "", false, err
	}

	return id, idStr, false, nil
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

	if expDur := resp.ExpiresAfterSecs - mediaUploadBuffer; expDur > 0 {
		log.Println("expire duration:", expDur)
		uploadExpireTime = time.Now().Add(time.Duration(expDur) * time.Second)
		lastMediaID = resp.MediaID
		lastMediaIDStr = resp.MediaIDStr
	}
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
