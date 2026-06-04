# Build stage
FROM golang:1.25 AS build

WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build stamp (#663). The release version is read from the committed
# VERSION file; commit + date arrive as build args because .git is
# dockerignored, so runtime/debug.ReadBuildInfo() cannot recover them
# inside the image build. CI passes COMMIT + DATE; a bare `docker build`
# leaves them empty and the binary falls back to "unknown"/"".
ARG COMMIT=""
ARG DATE=""

# Build the application
# CGO_ENABLED=0 for a static binary
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-X github.com/starquake/topbanana/internal/version.Version=$(cat VERSION) \
              -X github.com/starquake/topbanana/internal/version.Commit=${COMMIT} \
              -X github.com/starquake/topbanana/internal/version.Date=${DATE}" \
    -o /server ./cmd/server/main.go

# Create data directory in build stage
RUN mkdir -p /data

# Final stage
FROM gcr.io/distroless/static-debian13:nonroot

WORKDIR /home/nonroot

# Copy the binary from the build stage
COPY --from=build --chown=nonroot:nonroot /server /home/nonroot/server
# Copy the data directory with correct ownership
COPY --from=build --chown=nonroot:nonroot /data /home/nonroot/data

# Expose the port the app runs on
EXPOSE 8080

# Use the nonroot user
USER nonroot

# Default environment variables
ENV APP_ENV=production
ENV HOST=0.0.0.0
ENV PORT=8080
ENV DB_URI="file:data/topbanana.sqlite?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"

# The image is distroless (no shell, no wget/curl) so the healthcheck
# reuses the server binary itself with -healthcheck -- does an HTTP
# GET against 127.0.0.1:$PORT/healthz and exits 0/1 (#344).
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD ["/home/nonroot/server", "-healthcheck"]

# Run the server
ENTRYPOINT ["/home/nonroot/server"]
