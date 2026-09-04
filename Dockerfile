FROM golang:1.24-bookworm AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/spacey ./cmd/spacey

FROM ubuntu:24.04
RUN apt-get update && apt-get install -y --no-install-recommends zfsutils-linux ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/spacey /usr/local/bin/spacey
WORKDIR /app
COPY migrations /app/migrations
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["spacey"]
