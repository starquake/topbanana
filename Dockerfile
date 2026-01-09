# Build stage
FROM golang:1.25 AS build

WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
# CGO_ENABLED=0 for a static binary
RUN CGO_ENABLED=0 GOOS=linux go build -o /server ./cmd/server/main.go

# Final stage
FROM gcr.io/distroless/static-debian13:nonroot

WORKDIR /home/nonroot

# Copy the binary from the build stage
COPY --from=build --chown=nonroot:nonroot /server /home/nonroot/server

# Expose the port the app runs on
EXPOSE 8080

# Use the nonroot user
USER nonroot

# Default environment variables
ENV APP_ENV=production
ENV HOST=0.0.0.0
ENV PORT=8080
ENV DB_URI="file:topbanana.sqlite?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"

# Run the server
ENTRYPOINT ["/home/nonroot/server"]
