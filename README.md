<!--
SPDX-FileCopyrightText: Copyright (c) 2025 Yegor Bugayenko
SPDX-License-Identifier: MIT
-->

# RSSP - RSS Stream Processor

A command-line tool that monitors multiple RSS feeds and streams new content to stdout in real-time.

## Features

- Monitor multiple RSS feeds concurrently
- Real-time streaming of new items to stdout
- Automatic deduplication of items
- Simple command-line interface
- Lightweight and efficient

## Installation

### From Source

```bash
go install github.com/yegor256/rssp@latest
```

### Build Locally

```bash
git clone https://github.com/yegor256/rssp.git
cd rssp
go build -o rssp
```

## Usage

```bash
rssp [options] uri1 uri2 ...
```

### Examples

Monitor a single RSS feed:
```bash
rssp https://example.com/rss.xml
```

Monitor multiple RSS feeds:
```bash
rssp https://news.ycombinator.com/rss https://example.com/feed.xml
```

Save output to a file (appends to existing content):
```bash
rssp --output feed.txt https://example.com/rss.xml
```

Show help:
```bash
rssp --help
```

## How It Works

1. The tool accepts one or more RSS feed URLs as command-line arguments
2. It polls each feed every 30 seconds for new content
3. New items are printed to stdout (or to a file if `--output` is specified) with timestamps
4. Items are deduplicated using their GUID (or link if GUID is not available)
5. When using `--output`, content is appended to the file, preserving existing content
6. The tool runs continuously in the foreground until interrupted

## Output Format

New items are printed in the following format:

```
[2024-01-15 10:30:45] https://example.com/rss.xml
Title: Article Title
Link: https://example.com/article
Description: Article description (if available)
Published: Mon, 15 Jan 2024 10:30:00 GMT (if available)
---
```

## Development

### Running Tests

```bash
make test           # Run all tests
make test-race      # Run tests with race detector
make test-coverage  # Generate coverage report
```

### Building

```bash
make build          # Build the binary
make clean          # Clean build artifacts
```

## Architecture

The tool is designed with simplicity and testability in mind:

- `RSS`, `Channel`, and `Item` structs for parsing RSS XML
- `FeedState` struct for tracking seen items per feed
- HTTP client interface for easy mocking in tests
- Concurrent polling with goroutines and proper synchronization

## Testing

The project includes comprehensive unit tests covering:

- RSS XML parsing (valid, invalid, edge cases)
- HTTP fetching with mocked responses
- Concurrent access and race conditions
- Special characters and non-ASCII content
- Error handling scenarios

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSES/MIT.txt) file for details.

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.
