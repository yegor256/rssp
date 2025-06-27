// SPDX-FileCopyrightText: Copyright (c) 2025 Yegor Bugayenko
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/transform"
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

type HTTPClient interface {
	Get(url string) (*http.Response, error)
}

var (
	client HTTPClient = http.DefaultClient
	outputFile *os.File
	outputMutex sync.Mutex
	logger *log.Logger
	fullOutput bool
	authored bool
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "RSS Stream Processor (rssp)\n\n")
		fmt.Fprintf(os.Stderr, "Usage: %s [options] uri1 uri2 ...\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  %s https://example.com/rss.xml https://another.com/feed.xml\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s --output feed.txt https://example.com/rss.xml\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s --full https://example.com/rss.xml\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s --authored https://example.com/rss.xml\n", os.Args[0])
	}

	help := flag.Bool("help", false, "Show help message")
	output := flag.String("output", "", "Output file for RSS items (default: stdout)")
	full := flag.Bool("full", false, "Show full item details (title, link, description, date)")
	auth := flag.Bool("authored", false, "Include channel name in output")
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

	if *output != "" {
		var err error
		outputFile, err = os.OpenFile(*output, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening output file: %v\n", err)
			os.Exit(1)
		}
		defer outputFile.Close()
		fmt.Printf("Output will be written to: %s\n", *output)
	} else {
		outputFile = os.Stdout
	}

	fullOutput = *full
	authored = *auth

	states := make([]*FeedState, len(uris))
	for i, uri := range uris {
		states[i] = &FeedState{
			url:   uri,
			items: make(map[string]bool),
		}
	}

	logger = log.New(os.Stderr, "[RSSP] ", log.LstdFlags)
	logger.Printf("Starting RSS Stream Processor for %d feeds", len(uris))
	for i, uri := range uris {
		logger.Printf("Feed %d: %s", i+1, uri)
	}
	if *output != "" {
		logger.Printf("Output destination: %s (append mode)", *output)
	} else {
		logger.Printf("Output destination: stdout")
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
		logger.Printf("Checking feed: %s", state.url)
		feed, err := fetchFeed(state.url)
		if err != nil {
			logger.Printf("Error fetching %s: %v - retrying in 30 seconds", state.url, err)
			time.Sleep(30 * time.Second)
			continue
		}

		logger.Printf("Successfully fetched %s - found %d total items", state.url, len(feed.Channel.Items))
		if feed.Channel.Title != "" {
			logger.Printf("Feed title: %s", feed.Channel.Title)
		}

		newItemsCount := 0
		state.mutex.Lock()
		for _, item := range feed.Channel.Items {
			id := getItemID(&item)

			if !state.items[id] {
				state.items[id] = true
				if !firstRun {
					newItemsCount++
					logger.Printf("New item found: '%s' from %s", item.Title, state.url)
					printItem(state.url, &item, feed.Channel.Title)
				}
			}
		}
		state.mutex.Unlock()

		if firstRun {
			logger.Printf("Initial load completed for %s - loaded %d existing items", state.url, len(feed.Channel.Items))
		} else if newItemsCount > 0 {
			logger.Printf("Found %d new items from %s", newItemsCount, state.url)
		} else {
			logger.Printf("No new items found in %s", state.url)
		}

		firstRun = false
		logger.Printf("Sleeping for 30 seconds before next check of %s", state.url)
		time.Sleep(30 * time.Second)
	}
}

func getItemID(item *Item) string {
	if item.GUID != "" {
		return item.GUID
	}
	return item.Link
}

func fetchFeed(url string) (*RSS, error) {
	if logger != nil {
		logger.Printf("Making HTTP request to %s", url)
	}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if logger != nil {
		logger.Printf("HTTP response from %s: %s", url, resp.Status)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP error: %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if logger != nil {
		logger.Printf("Downloaded %d bytes from %s", len(body), url)
	}
	return parseFeed(body)
}

func parseFeed(data []byte) (*RSS, error) {
	if logger != nil {
		logger.Printf("Parsing RSS XML data (%d bytes)", len(data))
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.CharsetReader = charsetReader

	var rss RSS
	err := decoder.Decode(&rss)
	if err != nil {
		return nil, fmt.Errorf("XML parsing failed: %w", err)
	}
	if logger != nil {
		logger.Printf("Successfully parsed RSS feed with %d items", len(rss.Channel.Items))
	}
	return &rss, nil
}

func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	charset = strings.ToLower(charset)
	if logger != nil {
		logger.Printf("Converting charset: %s", charset)
	}

	switch charset {
	case "utf-8", "":
		return input, nil
	case "windows-1251", "cp1251":
		return transform.NewReader(input, charmap.Windows1251.NewDecoder()), nil
	case "windows-1252", "cp1252":
		return transform.NewReader(input, charmap.Windows1252.NewDecoder()), nil
	case "iso-8859-1", "latin1":
		return transform.NewReader(input, charmap.ISO8859_1.NewDecoder()), nil
	case "iso-8859-2", "latin2":
		return transform.NewReader(input, charmap.ISO8859_2.NewDecoder()), nil
	case "iso-8859-5":
		return transform.NewReader(input, charmap.ISO8859_5.NewDecoder()), nil
	case "iso-8859-15":
		return transform.NewReader(input, charmap.ISO8859_15.NewDecoder()), nil
	case "koi8-r":
		return transform.NewReader(input, charmap.KOI8R.NewDecoder()), nil
	case "koi8-u":
		return transform.NewReader(input, charmap.KOI8U.NewDecoder()), nil
	default:
		return nil, fmt.Errorf("unsupported charset: %s", charset)
	}
}

func parseDate(pubDate string) string {
	if pubDate == "" {
		return ""
	}
	layouts := []string{
		time.RFC1123,
		time.RFC1123Z,
		time.RFC822,
		time.RFC822Z,
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02T15:04:05Z",
		"2006-01-02 15:04:05",
		"Mon, 2 Jan 2006 15:04:05 MST",
		"Mon, 2 Jan 2006 15:04:05 -0700",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, pubDate); err == nil {
			return t.Format("02-01-2006")
		}
	}
	return pubDate
}

func printItem(feedURL string, item *Item, channelTitle string) {
	outputMutex.Lock()
	defer outputMutex.Unlock()

	if logger != nil {
		logger.Printf("Writing item to output: '%s' (ID: %s)", item.Title, getItemID(item))
	}
	if fullOutput {
		fmt.Fprintf(outputFile, "\n[%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), feedURL)
		fmt.Fprintf(outputFile, "Title: %s\n", item.Title)
		fmt.Fprintf(outputFile, "Link: %s\n", item.Link)
		if item.Description != "" {
			fmt.Fprintf(outputFile, "Description: %s\n", item.Description)
		}
		if item.PubDate != "" {
			fmt.Fprintf(outputFile, "Published: %s\n", item.PubDate)
		}
		fmt.Fprintf(outputFile, "---\n\n")
	} else {
		date := parseDate(item.PubDate)
		hasContent := false
		if date != "" {
			fmt.Fprintf(outputFile, "%s", date)
			hasContent = true
		}
		if item.Description != "" {
			if hasContent {
				fmt.Fprintf(outputFile, " ")
			}
			fmt.Fprintf(outputFile, "%s", item.Description)
			hasContent = true
		}
		if authored && channelTitle != "" {
			if hasContent {
				fmt.Fprintf(outputFile, " ")
			}
			fmt.Fprintf(outputFile, "[%s]", channelTitle)
		}
		fmt.Fprintf(outputFile, "\n\n")
	}

	if outputFile != os.Stdout {
		outputFile.Sync()
		if logger != nil {
			logger.Printf("Item written to file and synced")
		}
	}
}
