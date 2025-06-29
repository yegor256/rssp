// SPDX-FileCopyrightText: Copyright (c) 2025 Yegor Bugayenko
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"text/template"
	"time"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/transform"
)

//go:embed prompt.txt
var embeddedPrompt string

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

type DiffbotResponse struct {
	Objects []DiffbotArticle `json:"objects"`
}

type DiffbotArticle struct {
	Title string `json:"title"`
	Text  string `json:"text"`
	HTML  string `json:"html"`
	Date  string `json:"date"`
}

type OpenAIRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type OpenAIResponse struct {
	Choices []Choice `json:"choices"`
}

type Choice struct {
	Message Message `json:"message"`
}

var (
	client      HTTPClient = http.DefaultClient
	outputFile  *os.File
	outputMutex sync.Mutex
	logger      *log.Logger
	fullOutput  bool
	authored    bool
	maxLength   int
	focus       string
)

func main() {
	if embeddedPrompt == "" {
		fmt.Fprintf(os.Stderr, "Error: Embedded prompt template is empty. The binary was not built correctly.\n")
		os.Exit(1)
	}

	_, err := template.New("prompt").Parse(embeddedPrompt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to parse embedded prompt template: %v\n", err)
		os.Exit(1)
	}

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
		fmt.Fprintf(os.Stderr, "  %s --focus \"artificial intelligence\" https://example.com/rss.xml\n", os.Args[0])
	}

	help := flag.Bool("help", false, "Show help message")
	version := flag.Bool("version", false, "Show version information")
	output := flag.String("output", "", "Output file for RSS items (default: stdout)")
	full := flag.Bool("full", false, "Show full item details (title, link, description, date)")
	auth := flag.Bool("authored", false, "Include channel name in output")
	maxLen := flag.Int("max-length", 10000, "Maximum length of article text to extract")
	focusFlag := flag.String("focus", "", "Topic focus for OpenAI content filtering (requires OPENAI_API_KEY)")
	flag.Parse()

	if *help {
		flag.Usage()
		os.Exit(0)
	}

	if *version {
		fmt.Println("0.0.0")
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
	maxLength = *maxLen
	focus = *focusFlag

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

func strip(text string) string {
	re := regexp.MustCompile(`<[^>]*>`)
	return strings.TrimSpace(re.ReplaceAllString(text, ""))
}

func extractContent(link string, httpClient HTTPClient) string {
	if httpClient == nil {
		httpClient = client
	}
	token := os.Getenv("DIFFBOT_TOKEN")
	if token == "" {
		if logger != nil {
			logger.Printf("DIFFBOT_TOKEN not set, falling back to basic extraction for %s", link)
		}
		return extractBasicContent(link, httpClient)
	}
	diffbotURL := fmt.Sprintf("https://api.diffbot.com/v3/article?token=%s&url=%s", token, url.QueryEscape(link))
	resp, err := httpClient.Get(diffbotURL)
	if err != nil {
		if logger != nil {
			logger.Printf("Failed to fetch from Diffbot for %s: %v", link, err)
		}
		return extractBasicContent(link, httpClient)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if logger != nil {
			logger.Printf("Diffbot API error %d for %s, falling back to basic extraction", resp.StatusCode, link)
		}
		return extractBasicContent(link, httpClient)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if logger != nil {
			logger.Printf("Failed to read Diffbot response for %s: %v", link, err)
		}
		return extractBasicContent(link, httpClient)
	}
	var diffbotResp DiffbotResponse
	err = json.Unmarshal(body, &diffbotResp)
	if err != nil {
		if logger != nil {
			logger.Printf("Failed to parse Diffbot response for %s: %v", link, err)
		}
		return extractBasicContent(link, httpClient)
	}
	if len(diffbotResp.Objects) == 0 {
		if logger != nil {
			logger.Printf("No objects in Diffbot response for %s, falling back to basic extraction", link)
		}
		return extractBasicContent(link, httpClient)
	}
	article := diffbotResp.Objects[0]
	text := article.Text
	if len(text) > maxLength {
		text = text[:maxLength] + "..."
	}
	if logger != nil {
		logger.Printf("Successfully extracted %d characters via Diffbot API from %s", len(text), link)
	}
	return text
}

func extractBasicContent(link string, httpClient HTTPClient) string {
	resp, err := httpClient.Get(link)
	if err != nil {
		if logger != nil {
			logger.Printf("Failed to fetch %s: %v", link, err)
		}
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if logger != nil {
			logger.Printf("Non-OK status code %d for %s", resp.StatusCode, link)
		}
		return ""
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if logger != nil {
			logger.Printf("Failed to read response body from %s: %v", link, err)
		}
		return ""
	}
	return extractMainText(string(body))
}

func extractMainText(html string) string {
	scriptRe := regexp.MustCompile(`(?s)<script[^>]*>.*?</script>`)
	html = scriptRe.ReplaceAllString(html, "")

	styleRe := regexp.MustCompile(`(?s)<style[^>]*>.*?</style>`)
	html = styleRe.ReplaceAllString(html, "")

	navRe := regexp.MustCompile(`(?s)<nav[^>]*>.*?</nav>`)
	html = navRe.ReplaceAllString(html, "")

	footerRe := regexp.MustCompile(`(?s)<footer[^>]*>.*?</footer>`)
	html = footerRe.ReplaceAllString(html, "")

	headerRe := regexp.MustCompile(`(?s)<header[^>]*>.*?</header>`)
	html = headerRe.ReplaceAllString(html, "")

	articleRe := regexp.MustCompile(`(?s)<article[^>]*>(.*?)</article>`)
	articleMatches := articleRe.FindAllStringSubmatch(html, -1)
	if len(articleMatches) > 0 {
		html = ""
		for _, match := range articleMatches {
			html += match[1] + " "
		}
	} else {
		mainRe := regexp.MustCompile(`(?s)<main[^>]*>(.*?)</main>`)
		mainMatch := mainRe.FindStringSubmatch(html)
		if len(mainMatch) > 1 {
			html = mainMatch[1]
		}
	}

	tagRe := regexp.MustCompile(`<[^>]*>`)
	text := tagRe.ReplaceAllString(html, " ")

	spaceRe := regexp.MustCompile(`\s+`)
	text = spaceRe.ReplaceAllString(text, " ")

	text = strings.TrimSpace(text)

	if len(text) > maxLength {
		text = text[:maxLength] + "..."
	}

	return text
}

func buildPrompt(topic string, content string) (string, error) {
	tmpl, err := template.New("prompt").Parse(embeddedPrompt)
	if err != nil {
		return "", fmt.Errorf("failed to parse prompt template: %w", err)
	}
	data := struct {
		Topic   string
		Content string
	}{
		Topic:   topic,
		Content: content,
	}
	var buf bytes.Buffer
	err = tmpl.Execute(&buf, data)
	if err != nil {
		return "", fmt.Errorf("failed to execute prompt template: %w", err)
	}
	return buf.String(), nil
}

func processWithOpenAI(content string, topic string) (string, bool) {
	token := os.Getenv("OPENAI_API_KEY")
	if token == "" {
		if logger != nil {
			logger.Printf("OPENAI_API_KEY not set, skipping content filtering")
		}
		return content, true
	}

	if logger != nil {
		logger.Printf("Processing content with ChatGPT for topic filtering and compression")
	}

	prompt, err := buildPrompt(topic, content)
	if err != nil {
		if logger != nil {
			logger.Printf("Failed to build prompt: %v", err)
		}
		return content, true
	}

	request := OpenAIRequest{
		Model: "gpt-3.5-turbo",
		Messages: []Message{
			{
				Role:    "user",
				Content: prompt,
			},
		},
	}

	requestBody, err := json.Marshal(request)
	if err != nil {
		if logger != nil {
			logger.Printf("Failed to marshal OpenAI request: %v", err)
		}
		return content, true
	}

	req, err := http.NewRequest("POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(requestBody))
	if err != nil {
		if logger != nil {
			logger.Printf("Failed to create OpenAI request: %v", err)
		}
		return content, true
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	httpClient := &http.Client{}
	resp, err := httpClient.Do(req)
	if err != nil {
		if logger != nil {
			logger.Printf("Failed to send OpenAI request: %v", err)
		}
		return content, true
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if logger != nil {
			logger.Printf("OpenAI API error %d, keeping content", resp.StatusCode)
		}
		return content, true
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if logger != nil {
			logger.Printf("Failed to read OpenAI response: %v", err)
		}
		return content, true
	}

	var openaiResp OpenAIResponse
	err = json.Unmarshal(body, &openaiResp)
	if err != nil {
		if logger != nil {
			logger.Printf("Failed to parse OpenAI response: %v", err)
		}
		return content, true
	}

	if len(openaiResp.Choices) == 0 {
		if logger != nil {
			logger.Printf("No choices in OpenAI response, keeping content")
		}
		return content, true
	}

	response := openaiResp.Choices[0].Message.Content
	if strings.HasPrefix(response, "NOT_RELEVANT") {
		if logger != nil {
			logger.Printf("Content marked as not relevant to topic '%s' by ChatGPT, filtering out", topic)
		}
		return "", false
	}

	if strings.HasPrefix(response, "RELEVANT:") {
		compressed := strings.TrimSpace(strings.TrimPrefix(response, "RELEVANT:"))
		if logger != nil {
			logger.Printf("Content processed and compressed by ChatGPT from %d to %d characters", len(content), len(compressed))
		}
		return compressed, true
	}

	if logger != nil {
		logger.Printf("Unexpected OpenAI response format, keeping original content")
	}
	return content, true
}

func hostname(feedURL string) string {
	u, err := url.Parse(feedURL)
	if err != nil {
		return feedURL
	}
	return u.Host
}

func printItem(feedURL string, item *Item, channelTitle string) {
	outputMutex.Lock()
	defer outputMutex.Unlock()

	if logger != nil {
		logger.Printf("Writing item to output: '%s' (ID: %s)", item.Title, getItemID(item))
	}

	webContent := ""
	if item.Link != "" {
		if logger != nil {
			logger.Printf("Fetching web content from: %s", item.Link)
		}
		webContent = extractContent(item.Link, client)
		if webContent != "" && logger != nil {
			logger.Printf("Successfully extracted %d characters of content from %s", len(webContent), item.Link)
		}
	}

	contentToProcess := ""
	if item.Description != "" {
		contentToProcess = strip(item.Description)
	}
	if webContent != "" {
		if contentToProcess != "" {
			contentToProcess += "\n\n" + webContent
		} else {
			contentToProcess = webContent
		}
	}

	processedContent := ""
	shouldPrint := true
	if focus != "" && contentToProcess != "" {
		processed, relevant := processWithOpenAI(contentToProcess, focus)
		if relevant {
			processedContent = processed
		} else {
			shouldPrint = false
		}
	}

	if !shouldPrint {
		if logger != nil {
			logger.Printf("Item filtered out as not relevant to focus topic '%s'", focus)
		}
		return
	}

	if fullOutput {
		fmt.Fprintf(outputFile, "\n[%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), feedURL)
		fmt.Fprintf(outputFile, "Title: %s\n", strip(item.Title))
		fmt.Fprintf(outputFile, "Link: %s\n", item.Link)
		if processedContent != "" {
			fmt.Fprintf(outputFile, "Content: %s\n", processedContent)
		} else if item.Description != "" {
			fmt.Fprintf(outputFile, "Description: %s\n", strip(item.Description))
		} else if webContent != "" {
			fmt.Fprintf(outputFile, "Content: %s\n", webContent)
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
		if processedContent != "" {
			if hasContent {
				fmt.Fprintf(outputFile, " ")
			}
			fmt.Fprintf(outputFile, "%s", processedContent)
			hasContent = true
		} else if item.Description != "" {
			if hasContent {
				fmt.Fprintf(outputFile, " ")
			}
			fmt.Fprintf(outputFile, "%s", strip(item.Description))
			hasContent = true
		} else if webContent != "" {
			if hasContent {
				fmt.Fprintf(outputFile, " ")
			}
			fmt.Fprintf(outputFile, "%s", webContent)
			hasContent = true
		}
		if authored && channelTitle != "" {
			if hasContent {
				fmt.Fprintf(outputFile, " ")
			}
			displayName := channelTitle
			if strings.Count(channelTitle, " ") > 2 {
				displayName = hostname(feedURL)
			}
			fmt.Fprintf(outputFile, "[%s]", displayName)
			hasContent = true
		}
		if hasContent {
			fmt.Fprintf(outputFile, "\n\n")
		}
	}

	if outputFile != os.Stdout {
		outputFile.Sync()
		if logger != nil {
			logger.Printf("Item written to file and synced")
		}
	}
}
