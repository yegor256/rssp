# SPDX-FileCopyrightText: Copyright (c) 2025 Yegor Bugayenko
# SPDX-License-Identifier: MIT

FROM golang:1.24-alpine

WORKDIR /rssp
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o rssp

CMD ["./rssp"]
