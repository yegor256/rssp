// SPDX-FileCopyrightText: Copyright (c) 2025 Yegor Bugayenko
// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/transform"
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

func TestPrintItemToStdout(t *testing.T) {
	originalOutputFile := outputFile
	defer func() { outputFile = originalOutputFile }()

	outputFile = os.Stdout

	item := &Item{
		Title:       "Test Item",
		Link:        "https://example.com/item",
		Description: "Test Description",
		PubDate:     "Mon, 01 Jan 2024 00:00:00 GMT",
		GUID:        "test-guid",
	}

	printItem("https://example.com/feed", item, "Test Channel")
}

func TestPrintItemToFile(t *testing.T) {
	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "test_output.txt")

	file, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	originalOutputFile := outputFile
	originalFullOutput := fullOutput
	defer func() {
		outputFile = originalOutputFile
		fullOutput = originalFullOutput
		file.Close()
	}()

	outputFile = file
	fullOutput = true

	item := &Item{
		Title:       "Test Item",
		Link:        "https://example.com/item",
		Description: "Test Description",
		PubDate:     "Mon, 01 Jan 2024 00:00:00 GMT",
		GUID:        "test-guid",
	}

	printItem("https://example.com/feed", item, "Test Channel")
	file.Close()

	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, "Test Item") {
		t.Error("output file should contain item title")
	}
	if !strings.Contains(contentStr, "https://example.com/item") {
		t.Error("output file should contain item link")
	}
	if !strings.Contains(contentStr, "Test Description") {
		t.Error("output file should contain item description")
	}
	if !strings.Contains(contentStr, "https://example.com/feed") {
		t.Error("output file should contain feed URL")
	}
}

func TestPrintItemAppendMode(t *testing.T) {
	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "test_append.txt")

	err := os.WriteFile(outputPath, []byte("Initial content\n"), 0644)
	if err != nil {
		t.Fatalf("failed to create initial file: %v", err)
	}

	file, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatalf("failed to open file in append mode: %v", err)
	}

	originalOutputFile := outputFile
	originalFullOutput := fullOutput
	defer func() {
		outputFile = originalOutputFile
		fullOutput = originalFullOutput
		file.Close()
	}()

	outputFile = file
	fullOutput = true

	item := &Item{
		Title: "Appended Item",
		Link:  "https://example.com/appended",
		GUID:  "append-guid",
	}

	printItem("https://example.com/feed", item, "Test Channel")
	file.Close()

	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, "Initial content") {
		t.Error("output file should preserve initial content")
	}
	if !strings.Contains(contentStr, "Appended Item") {
		t.Error("output file should contain appended item")
	}
}

func TestPrintItemConcurrentWrites(t *testing.T) {
	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "test_concurrent.txt")

	file, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	originalOutputFile := outputFile
	originalFullOutput := fullOutput
	defer func() {
		outputFile = originalOutputFile
		fullOutput = originalFullOutput
		file.Close()
	}()

	outputFile = file
	fullOutput = true

	var wg sync.WaitGroup
	numGoroutines := 10

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			item := &Item{
				Title: fmt.Sprintf("Item %d", id),
				Link:  fmt.Sprintf("https://example.com/item%d", id),
				GUID:  fmt.Sprintf("guid-%d", id),
			}

			printItem("https://example.com/feed", item, "Test Channel")
		}(i)
	}

	wg.Wait()
	file.Close()

	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}

	contentStr := string(content)
	for i := 0; i < numGoroutines; i++ {
		expectedTitle := fmt.Sprintf("Item %d", i)
		if !strings.Contains(contentStr, expectedTitle) {
			t.Errorf("output file should contain '%s'", expectedTitle)
		}
	}
}

func TestPrintItemWithMinimalFields(t *testing.T) {
	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "test_minimal.txt")

	file, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	originalOutputFile := outputFile
	originalFullOutput := fullOutput
	defer func() {
		outputFile = originalOutputFile
		fullOutput = originalFullOutput
		file.Close()
	}()

	outputFile = file
	fullOutput = true

	item := &Item{
		Title: "Minimal Item",
		Link:  "https://example.com/minimal",
	}

	printItem("https://example.com/feed", item, "Test Channel")
	file.Close()

	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, "Minimal Item") {
		t.Error("output should contain title")
	}
	if !strings.Contains(contentStr, "https://example.com/minimal") {
		t.Error("output should contain link")
	}
	if strings.Contains(contentStr, "Description:") {
		t.Error("output should not contain description field when empty")
	}
	if strings.Contains(contentStr, "Published:") {
		t.Error("output should not contain published field when empty")
	}
}

func TestPrintItemCompactOutput(t *testing.T) {
	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "test_compact.txt")

	file, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	originalOutputFile := outputFile
	originalFullOutput := fullOutput
	defer func() {
		outputFile = originalOutputFile
		fullOutput = originalFullOutput
		file.Close()
	}()

	outputFile = file
	fullOutput = false

	item := &Item{
		Title:       "Test Item",
		Link:        "https://example.com/item",
		Description: "Test Description",
		PubDate:     "Mon, 15 Mar 2023 10:30:00 GMT",
		GUID:        "test-guid",
	}

	printItem("https://example.com/feed", item, "Test Channel")
	file.Close()

	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, "15-03-2023") {
		t.Error("output should contain formatted date")
	}
	if !strings.Contains(contentStr, "Test Description") {
		t.Error("output should contain description")
	}
	if strings.Contains(contentStr, "Title:") {
		t.Error("compact output should not contain Title: prefix")
	}
	if strings.Contains(contentStr, "Link:") {
		t.Error("compact output should not contain Link: prefix")
	}
	if strings.Contains(contentStr, "Published:") {
		t.Error("compact output should not contain Published: prefix")
	}
	if strings.Contains(contentStr, "https://example.com/feed") {
		t.Error("compact output should not contain feed URL")
	}
	if !strings.Contains(contentStr, "[Test Channel]") {
		t.Error("compact output should contain channel name in brackets")
	}
}

func TestPrintItemFullOutput(t *testing.T) {
	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "test_full.txt")

	file, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	originalOutputFile := outputFile
	originalFullOutput := fullOutput
	defer func() {
		outputFile = originalOutputFile
		fullOutput = originalFullOutput
		file.Close()
	}()

	outputFile = file
	fullOutput = true

	item := &Item{
		Title:       "Test Item",
		Link:        "https://example.com/item",
		Description: "Test Description",
		PubDate:     "Mon, 15 Mar 2023 10:30:00 GMT",
		GUID:        "test-guid",
	}

	printItem("https://example.com/feed", item, "Test Channel")
	file.Close()

	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, "Title: Test Item") {
		t.Error("full output should contain title with prefix")
	}
	if !strings.Contains(contentStr, "Link: https://example.com/item") {
		t.Error("full output should contain link with prefix")
	}
	if !strings.Contains(contentStr, "Description: Test Description") {
		t.Error("full output should contain description with prefix")
	}
	if !strings.Contains(contentStr, "Published: Mon, 15 Mar 2023 10:30:00 GMT") {
		t.Error("full output should contain original publish date")
	}
	if !strings.Contains(contentStr, "https://example.com/feed") {
		t.Error("full output should contain feed URL")
	}
}

func TestPrintItemCompactOutputNoDescription(t *testing.T) {
	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "test_compact_no_desc.txt")

	file, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	originalOutputFile := outputFile
	originalFullOutput := fullOutput
	defer func() {
		outputFile = originalOutputFile
		fullOutput = originalFullOutput
		file.Close()
	}()

	outputFile = file
	fullOutput = false

	item := &Item{
		Title:   "Test Item",
		Link:    "https://example.com/item",
		PubDate: "Mon, 15 Mar 2023 10:30:00 GMT",
		GUID:    "test-guid",
	}

	printItem("https://example.com/feed", item, "Test Channel")
	file.Close()

	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, "15-03-2023") {
		t.Error("output should contain formatted date")
	}
	if !strings.Contains(contentStr, "[Test Channel]") {
		t.Error("compact output should contain channel name in brackets")
	}
	lines := strings.Split(strings.TrimSpace(contentStr), "\n")
	if len(lines) != 1 {
		t.Errorf("expected 1 line, got %d", len(lines))
	}
	if strings.TrimSpace(lines[0]) != "15-03-2023 [Test Channel]" {
		t.Errorf("expected date with channel name, got '%s'", lines[0])
	}
}

func TestPrintItemCompactOutputNoDate(t *testing.T) {
	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "test_compact_no_date.txt")

	file, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	originalOutputFile := outputFile
	originalFullOutput := fullOutput
	defer func() {
		outputFile = originalOutputFile
		fullOutput = originalFullOutput
		file.Close()
	}()

	outputFile = file
	fullOutput = false

	item := &Item{
		Title:       "Test Item",
		Link:        "https://example.com/item",
		Description: "Test Description without date",
		GUID:        "test-guid",
	}

	printItem("https://example.com/feed", item, "Test Channel")
	file.Close()

	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, "Test Description without date") {
		t.Error("output should contain description")
	}
	lines := strings.Split(strings.TrimSpace(contentStr), "\n")
	if len(lines) != 1 {
		t.Errorf("expected 1 line, got %d", len(lines))
	}
	if strings.TrimSpace(lines[0]) != "Test Description without date [Test Channel]" {
		t.Errorf("expected description with channel name, got '%s'", lines[0])
	}
}

func TestCharsetReaderUTF8(t *testing.T) {
	input := strings.NewReader("test content")
	reader, err := charsetReader("utf-8", input)
	if err != nil {
		t.Fatalf("charsetReader returned error for UTF-8: %v", err)
	}
	if reader != input {
		t.Error("charsetReader should return original reader for UTF-8")
	}
}

func TestCharsetReaderEmptyCharset(t *testing.T) {
	input := strings.NewReader("test content")
	reader, err := charsetReader("", input)
	if err != nil {
		t.Fatalf("charsetReader returned error for empty charset: %v", err)
	}
	if reader != input {
		t.Error("charsetReader should return original reader for empty charset")
	}
}

func TestCharsetReaderWindows1251(t *testing.T) {
	input := strings.NewReader("test content")
	reader, err := charsetReader("windows-1251", input)
	if err != nil {
		t.Fatalf("charsetReader returned error for windows-1251: %v", err)
	}
	if reader == input {
		t.Error("charsetReader should return transformed reader for windows-1251")
	}
}

func TestCharsetReaderUnsupportedCharset(t *testing.T) {
	input := strings.NewReader("test content")
	_, err := charsetReader("unsupported-charset", input)
	if err == nil {
		t.Error("charsetReader should return error for unsupported charset")
	}
	if !strings.Contains(err.Error(), "unsupported charset") {
		t.Errorf("expected 'unsupported charset' in error, got: %v", err)
	}
}

func TestParseFeedWithWindows1251Encoding(t *testing.T) {
	originalText := "Тест на русском языке"

	encoder := charmap.Windows1251.NewEncoder()
	encoded, _, err := transform.String(encoder, originalText)
	if err != nil {
		t.Fatalf("failed to encode text: %v", err)
	}

	xml := `<?xml version="1.0" encoding="windows-1251"?>
<rss version="2.0">
	<channel>
		<title>` + encoded + `</title>
		<item>
			<title>` + encoded + `</title>
			<link>https://example.com/item</link>
		</item>
	</channel>
</rss>`

	feed, err := parseFeed([]byte(xml))
	if err != nil {
		t.Fatalf("parseFeed returned error for windows-1251: %v", err)
	}

	if feed.Channel.Title != originalText {
		t.Errorf("expected title '%s', got '%s'", originalText, feed.Channel.Title)
	}

	if len(feed.Channel.Items) != 1 {
		t.Errorf("expected 1 item, got %d", len(feed.Channel.Items))
	}

	if feed.Channel.Items[0].Title != originalText {
		t.Errorf("expected item title '%s', got '%s'", originalText, feed.Channel.Items[0].Title)
	}
}

func TestParseFeedWithISO88591Encoding(t *testing.T) {
	originalText := "Café français"

	encoder := charmap.ISO8859_1.NewEncoder()
	encoded, _, err := transform.String(encoder, originalText)
	if err != nil {
		t.Fatalf("failed to encode text: %v", err)
	}

	xml := `<?xml version="1.0" encoding="iso-8859-1"?>
<rss version="2.0">
	<channel>
		<title>` + encoded + `</title>
		<item>
			<title>` + encoded + `</title>
			<link>https://example.com/item</link>
		</item>
	</channel>
</rss>`

	feed, err := parseFeed([]byte(xml))
	if err != nil {
		t.Fatalf("parseFeed returned error for iso-8859-1: %v", err)
	}

	if feed.Channel.Title != originalText {
		t.Errorf("expected title '%s', got '%s'", originalText, feed.Channel.Title)
	}
}

func TestCharsetReaderCaseInsensitive(t *testing.T) {
	testCases := []string{
		"WINDOWS-1251",
		"Windows-1251",
		"windows-1251",
		"CP1251",
		"cp1251",
	}

	for _, charset := range testCases {
		input := strings.NewReader("test")
		reader, err := charsetReader(charset, input)
		if err != nil {
			t.Errorf("charsetReader failed for charset '%s': %v", charset, err)
		}
		if reader == input {
			t.Errorf("charsetReader should transform for charset '%s'", charset)
		}
	}
}

func TestParseFeedWithUnsupportedCharset(t *testing.T) {
	xml := `<?xml version="1.0" encoding="unsupported-encoding"?>
<rss version="2.0">
	<channel>
		<title>Test</title>
	</channel>
</rss>`

	_, err := parseFeed([]byte(xml))
	if err == nil {
		t.Error("parseFeed should return error for unsupported charset")
	}
	if !strings.Contains(err.Error(), "unsupported charset") {
		t.Errorf("expected 'unsupported charset' in error, got: %v", err)
	}
}

func TestParseDateWithRFC1123Format(t *testing.T) {
	input := "Mon, 02 Jan 2006 15:04:05 MST"
	result := parseDate(input)
	if result != "02-01-2006" {
		t.Errorf("expected '02-01-2006', got '%s'", result)
	}
}

func TestParseDateWithRFC822Format(t *testing.T) {
	input := "02 Jan 06 15:04 MST"
	result := parseDate(input)
	if result != "02-01-2006" {
		t.Errorf("expected '02-01-2006', got '%s'", result)
	}
}

func TestParseDateWithISOFormat(t *testing.T) {
	input := "2006-01-02T15:04:05Z"
	result := parseDate(input)
	if result != "02-01-2006" {
		t.Errorf("expected '02-01-2006', got '%s'", result)
	}
}

func TestParseDateWithEmptyString(t *testing.T) {
	result := parseDate("")
	if result != "" {
		t.Errorf("expected empty string, got '%s'", result)
	}
}

func TestParseDateWithInvalidFormat(t *testing.T) {
	input := "not a valid date"
	result := parseDate(input)
	if result != input {
		t.Errorf("expected original string '%s', got '%s'", input, result)
	}
}

func TestParseDateWithVariousFormats(t *testing.T) {
	testCases := []struct {
		input    string
		expected string
	}{
		{"Mon, 15 Mar 2023 10:30:00 GMT", "15-03-2023"},
		{"Wed, 31 Dec 2025 23:59:59 +0000", "31-12-2025"},
		{"2023-06-15T14:30:00Z", "15-06-2023"},
		{"2023-06-15T14:30:00+02:00", "15-06-2023"},
		{"2023-06-15 14:30:00", "15-06-2023"},
		{"Thu, 1 Jan 2015 00:00:00 GMT", "01-01-2015"},
	}

	for _, tc := range testCases {
		result := parseDate(tc.input)
		if result != tc.expected {
			t.Errorf("for input '%s', expected '%s', got '%s'", tc.input, tc.expected, result)
		}
	}
}

func TestIntegrationCompactOutputWithXMLFile(t *testing.T) {
	tempDir := t.TempDir()
	feedPath := filepath.Join(tempDir, "test_feed.xml")
	outputPath := filepath.Join(tempDir, "output.txt")

	feedContent := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Tech News Feed</title>
    <link>https://example.com</link>
    <description>Latest technology news</description>
    <item>
      <title>Breaking: New AI Model Released</title>
      <link>https://example.com/ai-news</link>
      <description>Revolutionary AI model achieves breakthrough performance</description>
      <pubDate>Mon, 15 Mar 2023 10:30:00 GMT</pubDate>
      <guid>ai-news-001</guid>
    </item>
    <item>
      <title>Security Update Available</title>
      <link>https://example.com/security</link>
      <description>Critical security patch for popular software</description>
      <pubDate>Tue, 16 Mar 2023 14:00:00 GMT</pubDate>
      <guid>security-002</guid>
    </item>
    <item>
      <title>No Description Item</title>
      <link>https://example.com/no-desc</link>
      <pubDate>Wed, 17 Mar 2023 09:00:00 GMT</pubDate>
      <guid>no-desc-003</guid>
    </item>
  </channel>
</rss>`

	err := os.WriteFile(feedPath, []byte(feedContent), 0644)
	if err != nil {
		t.Fatalf("failed to write test feed: %v", err)
	}

	file, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatalf("failed to create output file: %v", err)
	}

	originalOutputFile := outputFile
	originalFullOutput := fullOutput
	defer func() {
		outputFile = originalOutputFile
		fullOutput = originalFullOutput
		file.Close()
	}()

	outputFile = file
	fullOutput = false

	data, err := os.ReadFile(feedPath)
	if err != nil {
		t.Fatalf("failed to read feed file: %v", err)
	}

	feed, err := parseFeed(data)
	if err != nil {
		t.Fatalf("failed to parse feed: %v", err)
	}

	for _, item := range feed.Channel.Items {
		printItem("file://"+feedPath, &item, feed.Channel.Title)
	}

	file.Close()

	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 3 {
		t.Errorf("expected 3 lines, got %d", len(lines))
	}

	expectedLines := []string{
		"15-03-2023 Revolutionary AI model achieves breakthrough performance [Tech News Feed]",
		"16-03-2023 Critical security patch for popular software [Tech News Feed]",
		"17-03-2023 [Tech News Feed]",
	}

	for i, expected := range expectedLines {
		if i < len(lines) && lines[i] != expected {
			t.Errorf("line %d: got %q, want %q", i+1, lines[i], expected)
		}
	}
}
