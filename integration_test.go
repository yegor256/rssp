// SPDX-FileCopyrightText: Copyright (c) 2025 Yegor Bugayenko
// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	originalAuthored := authored
	defer func() {
		outputFile = originalOutputFile
		fullOutput = originalFullOutput
		authored = originalAuthored
		file.Close()
	}()

	outputFile = file
	fullOutput = false
	authored = true

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

	allLines := strings.Split(strings.TrimSpace(string(content)), "\n")
	var lines []string
	for _, line := range allLines {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
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

func TestExtractContentWithSuccessfulFetch(t *testing.T) {
	originalMaxLength := maxLength
	maxLength = 2000
	defer func() { maxLength = originalMaxLength }()
	os.Unsetenv("DIFFBOT_TOKEN")
	mockClient := &mockHTTPClient{
		responses: map[string]*http.Response{
			"https://example.com/article": {
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`<html><body><article>Test article content</article></body></html>`)),
			},
		},
	}
	result := extractContent("https://example.com/article", mockClient)
	expected := "Test article content"
	if result != expected {
		t.Errorf("extractContent failed: got %q, want %q", result, expected)
	}
}

func TestExtractContentWithHTTPError(t *testing.T) {
	mockClient := &mockHTTPClient{
		responses: map[string]*http.Response{
			"https://example.com/error": {
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader("")),
			},
		},
	}
	result := extractContent("https://example.com/error", mockClient)
	if result != "" {
		t.Errorf("extractContent should return empty string on HTTP error: got %q", result)
	}
}

func TestExtractContentWithNetworkError(t *testing.T) {
	mockClient := &mockHTTPClient{
		errors: map[string]error{
			"https://example.com/network-error": errors.New("network error"),
		},
	}
	result := extractContent("https://example.com/network-error", mockClient)
	if result != "" {
		t.Errorf("extractContent should return empty string on network error: got %q", result)
	}
}
