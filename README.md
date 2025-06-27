# RSS Stream Processor

[![CI](https://github.com/yegor256/rssp/actions/workflows/ci.yml/badge.svg)](https://github.com/yegor256/rssp/actions/workflows/ci.yml)

A smart RSS aggregator that helps you stay focused on news that matters. Instead of manually browsing through multiple RSS feeds and filtering irrelevant content, this tool monitors your favorite news sources, uses ChatGPT to intelligently filter and compress articles based on your interests, and outputs only the relevant news to a text file that you can read at your convenience or tail-follow in real-time.

Install it from sources:

```bash
git clone https://github.com/yegor256/rssp.git
cd rssp
go build -o rssp
```

Then, monitor a single RSS feed and save output to a file:

```bash
rssp --output feed.txt https://example.com/rss.xml
```

It is advised to define `DIFFBOT_TOKEN` and `OPENAI_API_KEY` environment
variables.

## How It Works

1. The tool accepts one or more RSS feed URLs as command-line arguments
2. It polls each feed every 30 seconds for new content
3. New items are printed to stdout (or to a file if `--output` is specified) with timestamps
4. Items are deduplicated using their GUID (or link if GUID is not available)
5. When using `--output`, content is appended to the file, preserving existing content
6. The tool runs continuously in the foreground until interrupted

## How to Contribute

```bash
make test           # Run all tests
make test-race      # Run tests with race detector
make test-coverage  # Generate coverage report
make build          # Build the binary
make clean          # Clean build artifacts
```

[Diffbot]: https://www.diffbot.com/
