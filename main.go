// SPDX-FileCopyrightText: 2025 The Authors
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

type RSS struct {
	Channel Channel `xml:"channel"`
}

type Channel struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	Items       []Item `xml:"item"`
}

type Item struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
	GUID        string `xml:"guid"`
}

type FeedState struct {
	url   string
	items map[string]bool
	mutex sync.Mutex
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "RSS Stream Processor (rssp)\n\n")
		fmt.Fprintf(os.Stderr, "Usage: %s [options] uri1 uri2 ...\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExample:\n")
		fmt.Fprintf(os.Stderr, "  %s https://example.com/rss.xml https://another.com/feed.xml\n", os.Args[0])
	}

	help := flag.Bool("help", false, "Show help message")
	flag.Parse()

	if *help {
		flag.Usage()
		os.Exit(0)
	}

	uris := flag.Args()
	if len(uris) == 0 {
		fmt.Fprintf(os.Stderr, "Error: No URIs provided\n")
		flag.Usage()
		os.Exit(1)
	}

	states := make([]*FeedState, len(uris))
	for i, uri := range uris {
		states[i] = &FeedState{
			url:   uri,
			items: make(map[string]bool),
		}
	}

	fmt.Printf("Starting RSS Stream Processor for %d feeds\n", len(uris))

	var wg sync.WaitGroup
	for _, state := range states {
		wg.Add(1)
		go func(fs *FeedState) {
			defer wg.Done()
			pollFeed(fs)
		}(state)
	}

	wg.Wait()
}

func pollFeed(state *FeedState) {
	firstRun := true
	for {
		feed, err := fetchFeed(state.url)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error fetching %s: %v\n", state.url, err)
			time.Sleep(30 * time.Second)
			continue
		}

		state.mutex.Lock()
		for _, item := range feed.Channel.Items {
			id := item.GUID
			if id == "" {
				id = item.Link
			}

			if !state.items[id] {
				state.items[id] = true
				if !firstRun {
					printItem(state.url, &item)
				}
			}
		}
		state.mutex.Unlock()

		firstRun = false
		time.Sleep(30 * time.Second)
	}
}

func fetchFeed(url string) (*RSS, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP error: %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var rss RSS
	err = xml.Unmarshal(body, &rss)
	if err != nil {
		return nil, err
	}

	return &rss, nil
}

func printItem(feedURL string, item *Item) {
	fmt.Printf("\n[%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), feedURL)
	fmt.Printf("Title: %s\n", item.Title)
	fmt.Printf("Link: %s\n", item.Link)
	if item.Description != "" {
		fmt.Printf("Description: %s\n", item.Description)
	}
	if item.PubDate != "" {
		fmt.Printf("Published: %s\n", item.PubDate)
	}
	fmt.Println("---")
}
