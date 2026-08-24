# syntax=docker/dockerfile:1
# Multi-stage build for English_zoa. No Node/npm stage: web/ is a no-build
# static prototype (React + Babel from CDN) embedded into the Go binary via
# //go:embed, following the MADANG dashboard's pattern on the same platform.

FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
COPY web ./web
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /app .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /app /app
ENV PORT=8080
EXPOSE 8080
ENTRYPOINT ["/app"]
