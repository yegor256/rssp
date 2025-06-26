# SPDX-FileCopyrightText: 2025 The Authors
# SPDX-License-Identifier: MIT

.PHONY: test
test:
	go test -v

.PHONY: test-race
test-race:
	go test -v -race

.PHONY: test-coverage
test-coverage:
	go test -cover -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html

.PHONY: build
build:
	go build -o rssp

.PHONY: clean
clean:
	rm -f rssp coverage.out coverage.html
