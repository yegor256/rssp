// SPDX-FileCopyrightText: 2025 The Authors
// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type mockHTTPClient struct {
	responses map[string]*http.Response
	errors    map[string]error
}

func (m *mockHTTPClient) Get(url string) (*http.Response, error) {
	if err, ok := m.errors[url]; ok {
		return nil, err
	}
	if resp, ok := m.responses[url]; ok {
		return resp, nil
	}
	return nil, errors.New("unexpected URL")
}

func TestParseFeedWithValidRSS(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
	<channel>
		<title>Test Feed</title>
		<link>https://example.com</link>
		<description>Test Description</description>
		<item>
			<title>Test Item</title>
			<link>https://example.com/item1</link>
			<description>Item Description</description>
			<pubDate>Mon, 01 Jan 2024 00:00:00 GMT</pubDate>
			<guid>unique-guid-1</guid>
		</item>
	</channel>
</rss>`

	feed, err := parseFeed([]byte(xml))
	if err != nil {
		t.Fatalf("parseFeed returned error: %v", err)
	}

	if feed.Channel.Title != "Test Feed" {
		t.Errorf("expected title 'Test Feed', got '%s'", feed.Channel.Title)
	}

	if len(feed.Channel.Items) != 1 {
		t.Errorf("expected 1 item, got %d", len(feed.Channel.Items))
	}

	item := feed.Channel.Items[0]
	if item.Title != "Test Item" {
		t.Errorf("expected item title 'Test Item', got '%s'", item.Title)
	}
}

func TestParseFeedWithInvalidXML(t *testing.T) {
	xml := `not valid xml`

	_, err := parseFeed([]byte(xml))
	if err == nil {
		t.Error("expected error for invalid XML, got nil")
	}
}

func TestParseFeedWithEmptyChannel(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
	<channel>
		<title>Empty Feed</title>
		<link>https://example.com</link>
		<description>No items</description>
	</channel>
</rss>`

	feed, err := parseFeed([]byte(xml))
	if err != nil {
		t.Fatalf("parseFeed returned error: %v", err)
	}

	if len(feed.Channel.Items) != 0 {
		t.Errorf("expected 0 items, got %d", len(feed.Channel.Items))
	}
}

func TestGetItemIDWithGUID(t *testing.T) {
	item := &Item{
		GUID: "test-guid",
		Link: "https://example.com",
	}

	id := getItemID(item)
	if id != "test-guid" {
		t.Errorf("expected 'test-guid', got '%s'", id)
	}
}

func TestGetItemIDWithoutGUID(t *testing.T) {
	item := &Item{
		GUID: "",
		Link: "https://example.com/item",
	}

	id := getItemID(item)
	if id != "https://example.com/item" {
		t.Errorf("expected 'https://example.com/item', got '%s'", id)
	}
}

func TestFetchFeedWithSuccess(t *testing.T) {
	oldClient := client
	defer func() { client = oldClient }()

	mockClient := &mockHTTPClient{
		responses: map[string]*http.Response{
			"https://test.com/feed.xml": {
				StatusCode: 200,
				Body: io.NopCloser(strings.NewReader(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
	<channel>
		<title>Test</title>
		<item>
			<title>Item 1</title>
			<link>https://test.com/1</link>
		</item>
	</channel>
</rss>`)),
			},
		},
		errors: make(map[string]error),
	}
	client = mockClient

	feed, err := fetchFeed("https://test.com/feed.xml")
	if err != nil {
		t.Fatalf("fetchFeed returned error: %v", err)
	}

	if feed.Channel.Title != "Test" {
		t.Errorf("expected title 'Test', got '%s'", feed.Channel.Title)
	}
}

func TestFetchFeedWithHTTPError(t *testing.T) {
	oldClient := client
	defer func() { client = oldClient }()

	mockClient := &mockHTTPClient{
		responses: map[string]*http.Response{
			"https://test.com/404": {
				StatusCode: 404,
				Status:     "404 Not Found",
				Body:       io.NopCloser(strings.NewReader("")),
			},
		},
		errors: make(map[string]error),
	}
	client = mockClient

	_, err := fetchFeed("https://test.com/404")
	if err == nil {
		t.Error("expected error for 404 response, got nil")
	}

	if !strings.Contains(err.Error(), "HTTP error") {
		t.Errorf("expected 'HTTP error' in error message, got: %v", err)
	}
}

func TestFetchFeedWithNetworkError(t *testing.T) {
	oldClient := client
	defer func() { client = oldClient }()

	mockClient := &mockHTTPClient{
		responses: make(map[string]*http.Response),
		errors: map[string]error{
			"https://test.com/error": errors.New("network error"),
		},
	}
	client = mockClient

	_, err := fetchFeed("https://test.com/error")
	if err == nil {
		t.Error("expected network error, got nil")
	}
}

func TestFeedStateDeduplication(t *testing.T) {
	state := &FeedState{
		url:   "https://test.com",
		items: make(map[string]bool),
		mutex: sync.Mutex{},
	}

	item1 := &Item{
		Title: "Item 1",
		Link:  "https://test.com/1",
		GUID:  "guid-1",
	}

	item2 := &Item{
		Title: "Item 2",
		Link:  "https://test.com/2",
		GUID:  "guid-2",
	}

	state.mutex.Lock()
	id1 := getItemID(item1)
	if state.items[id1] {
		t.Error("item1 should not be in state initially")
	}
	state.items[id1] = true
	state.mutex.Unlock()

	state.mutex.Lock()
	if !state.items[id1] {
		t.Error("item1 should be in state after adding")
	}

	id2 := getItemID(item2)
	if state.items[id2] {
		t.Error("item2 should not be in state")
	}
	state.mutex.Unlock()
}

func TestMultipleItemsInFeed(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
	<channel>
		<title>Multi Item Feed</title>
		<item>
			<title>Item 1</title>
			<link>https://example.com/1</link>
			<guid>guid-1</guid>
		</item>
		<item>
			<title>Item 2</title>
			<link>https://example.com/2</link>
			<guid>guid-2</guid>
		</item>
		<item>
			<title>Item 3</title>
			<link>https://example.com/3</link>
			<guid>guid-3</guid>
		</item>
	</channel>
</rss>`

	feed, err := parseFeed([]byte(xml))
	if err != nil {
		t.Fatalf("parseFeed returned error: %v", err)
	}

	if len(feed.Channel.Items) != 3 {
		t.Errorf("expected 3 items, got %d", len(feed.Channel.Items))
	}

	expectedTitles := []string{"Item 1", "Item 2", "Item 3"}
	for i, item := range feed.Channel.Items {
		if item.Title != expectedTitles[i] {
			t.Errorf("expected title '%s', got '%s'", expectedTitles[i], item.Title)
		}
	}
}

func TestFeedWithSpecialCharacters(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
	<channel>
		<title>Feed with Special &amp; Characters</title>
		<item>
			<title>Item with &lt;HTML&gt; tags</title>
			<link>https://example.com/special?param=1&amp;other=2</link>
			<description>Description with "quotes" and 'apostrophes'</description>
		</item>
	</channel>
</rss>`

	feed, err := parseFeed([]byte(xml))
	if err != nil {
		t.Fatalf("parseFeed returned error: %v", err)
	}

	if !strings.Contains(feed.Channel.Title, "&") {
		t.Error("expected ampersand in title")
	}

	item := feed.Channel.Items[0]
	if !strings.Contains(item.Title, "<HTML>") {
		t.Error("expected HTML tags in item title")
	}
}

func TestFeedWithNonASCII(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
	<channel>
		<title>Feed with 中文 and émojis 🎉</title>
		<item>
			<title>Статья на русском</title>
			<link>https://example.com/世界</link>
			<description>مرحبا بالعالم</description>
		</item>
	</channel>
</rss>`

	feed, err := parseFeed([]byte(xml))
	if err != nil {
		t.Fatalf("parseFeed returned error: %v", err)
	}

	if !strings.Contains(feed.Channel.Title, "中文") {
		t.Error("expected Chinese characters in title")
	}

	if !strings.Contains(feed.Channel.Title, "🎉") {
		t.Error("expected emoji in title")
	}
}

func TestFeedStateRaceCondition(t *testing.T) {
	state := &FeedState{
		url:   "https://test.com",
		items: make(map[string]bool),
		mutex: sync.Mutex{},
	}

	var wg sync.WaitGroup
	iterations := 100

	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			state.mutex.Lock()
			itemID := string(rune(id))
			state.items[itemID] = true
			state.mutex.Unlock()
		}(i)
	}

	wg.Wait()

	if len(state.items) != iterations {
		t.Errorf("expected %d items, got %d", iterations, len(state.items))
	}
}

func TestParseFeedWithMalformedItem(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
	<channel>
		<title>Feed with Malformed Item</title>
		<item>
			<title>Good Item</title>
			<link>https://example.com/good</link>
		</item>
		<item>
			<title></title>
			<link></link>
		</item>
	</channel>
</rss>`

	feed, err := parseFeed([]byte(xml))
	if err != nil {
		t.Fatalf("parseFeed returned error: %v", err)
	}

	if len(feed.Channel.Items) != 2 {
		t.Errorf("expected 2 items including empty one, got %d", len(feed.Channel.Items))
	}
}

func TestFetchFeedWithInvalidResponseBody(t *testing.T) {
	oldClient := client
	defer func() { client = oldClient }()

	mockClient := &mockHTTPClient{
		responses: map[string]*http.Response{
			"https://test.com/invalid": {
				StatusCode: 200,
				Body:       io.NopCloser(&errorReader{}),
			},
		},
		errors: make(map[string]error),
	}
	client = mockClient

	_, err := fetchFeed("https://test.com/invalid")
	if err == nil {
		t.Error("expected error for invalid response body, got nil")
	}
}

type errorReader struct{}

func (e *errorReader) Read(p []byte) (n int, err error) {
	return 0, errors.New("read error")
}

func TestConcurrentFeedStateMutations(t *testing.T) {
	state := &FeedState{
		url:   "https://test.com",
		items: make(map[string]bool),
		mutex: sync.Mutex{},
	}

	var wg sync.WaitGroup
	readers := 50
	writers := 50

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			state.mutex.Lock()
			state.items[string(rune(id))] = true
			state.mutex.Unlock()
		}(i)
	}

	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			state.mutex.Lock()
			_ = len(state.items)
			state.mutex.Unlock()
		}()
	}

	wg.Wait()

	if len(state.items) != writers {
		t.Errorf("expected %d items after concurrent access, got %d", writers, len(state.items))
	}
}

func TestFeedWithMissingRequiredFields(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
	<channel>
		<item>
			<title>Item without link</title>
		</item>
	</channel>
</rss>`

	feed, err := parseFeed([]byte(xml))
	if err != nil {
		t.Fatalf("parseFeed returned error: %v", err)
	}

	if len(feed.Channel.Items) != 1 {
		t.Error("should parse item even without link")
	}

	item := feed.Channel.Items[0]
	if item.Link != "" {
		t.Errorf("expected empty link, got '%s'", item.Link)
	}
}

func TestFetchFeedBodyReadError(t *testing.T) {
	oldClient := client
	defer func() { client = oldClient }()

	body := &closeErrorBody{
		Reader: strings.NewReader("test"),
	}

	mockClient := &mockHTTPClient{
		responses: map[string]*http.Response{
			"https://test.com/close-error": {
				StatusCode: 200,
				Body:       body,
			},
		},
		errors: make(map[string]error),
	}
	client = mockClient

	_, err := fetchFeed("https://test.com/close-error")
	if err == nil {
		t.Error("expected error when body read fails")
	}
}

type closeErrorBody struct {
	*strings.Reader
	closed bool
}

func (c *closeErrorBody) Close() error {
	c.closed = true
	return nil
}

func (c *closeErrorBody) Read(p []byte) (n int, err error) {
	if c.closed {
		return 0, errors.New("already closed")
	}
	return 0, errors.New("read error")
}
