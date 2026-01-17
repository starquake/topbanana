# Congratulations! You are the Top Banana!
Top Banana! is a self-hosted quiz service built with Go, focussing on simplicity and easy deployment.

[![Go Report Card](https://goreportcard.com/badge/github.com/starquake/topbanana)](https://goreportcard.com/report/github.com/starquake/topbanana)

This repository also serves as a showcase for my software engineering and Go skills. Learn more about the [Technical Stack & Techniques](#technical-stack--techniques).

## Demo

### Game
![Image](https://github.com/user-attachments/assets/ebf9de5c-47b6-410f-b8b8-7d29200fa193)

### Admin interface
![Image](https://github.com/user-attachments/assets/77d8a814-df67-449b-a7af-15814fc21333)

A link to a live demo will be provided soon.

## Features
- **Quiz Creation**: Create and edit quizzes with questions and answers.
- **Gameplay**: Play quizzes solo or with friends (soon&trade;).
- **Self-Hosted**: Run directly or with Docker.

## Demo

A demo will be provided soon.

## Technical Stack & Techniques

### Backend & Architecture
- **Modern Go Implementation**: Build using the latest language features and standard library enhancements.
- **Enhanced Routing**: Uses the Go 1.22+ `http.ServeMux` for pattern-based routing, reducing reliance on third-party web frameworks.
- **Modular Monolith**: Using packages to separate concerns into logical domains (API, game logic, storage), following the [standard Go server project layout](https://go.dev/doc/modules/layout#server-project).   
- **Service Layer Pattern**: Business logic is decoupled from both transport (HTTP) and persistence (SQL) layers which should make it easier to maintain and extend in the future.
- **Dependency Injection**: Explicitly managed dependencies through constructor injection and interfaces providing loose coupling and better testability.
- **Graceful Shutdown**: Handling of SIGTERM signals to gracefully shutdown the server, handling in-flight requests, and closing database connections.
- **Structured Logging**: Using `log/slog` for consistent machine-readable structured logging.

### Persistence & Data Modeling
- **Type-Safe SQL with [sqlc](https://github.com/sqlc-dev/sqlc)**: SQL-first development that generates type-safe Go code from raw queries, providing compile-time guarantees for database interactions.
- **CGO-Free SQLite**: Uses modernc.org/sqlite to keep the project portable and cross-platform, avoiding the need for CGO and any C dependencies.
- **Automated Migrations**: Schema versiong and evolution managed by [goose](https://github.com/pressly/goose).
- **Global Unique IDs**: Uses [xid](https://github.com/rs/xid) for generating globally unique, sortable and URL-friendly identifiers.
- **Database Transactions**: Ensuring atomicity of database operations by wrapping them in a transaction.

### Quality Assurance
- **Thorough Testing Suite**
  - **Unit Testing**: Targeted tests for individual packages and functions.
  - **Integration Testing**: Full-cycle tests against a real database instance.
- **Concurrency Safety**: Tests run with Go race detector (`-race`) and parrallel test execution for every test.
- **Static Analysis & Linting**: Integration of `golangci-lint` and `sqlc vet` into the development workflow to maintain high code quality and SQL correctness.
- **Test Coverage**: Automated coverage checks in CI using `go-test-coverage` to maintain high testing standards.
- **Dependency Updates**: Periodic dependency checks with Dependabot to ensure up-to-date dependencies.

### CI/CD DevOps
- **GitHub Actions**: Automated pipelines for:
  - **Continuous Integration**: Linting and testing on every pull request.
  - **Docker Automation**: Multi-stage Docker builds pushed to GitHub Container Registry (GHCR).
  - **Automated Deployment**: Staging and production deployments triggered by successful builds.
- **Secure Containerization**:
  - **Distroless Images**: Uses Google's `distroless` images to minimize attack surface and image size.
  - **Non-Root Execution**: Runs as a non-root user with limited privileges.
- **Supply Chain Security**: Docker images are signed using `cosign` to ensure image integrity.
- **Environment Variables**: Configuration via environment variables with support for .env files.

### Development Experience (DX)
- **Makefile Automation**: A unified interface for common tasks: `make check`, `make test`, `make build`.
- **Server-Side Rendering (SSR)**: Admin interface built with Go's `html/template`.
- **Minimalist Frontend**: A clean client interface using modern JavaScript and CSS without the overhead of a framework.
- **Hot Reloading**: Changes in HTML or JavaScript files are immediately reflected in the browser on a page reload.    

## Project Structure

Following the official guidelines for a [standard Go server project layout](https://go.dev/doc/modules/layout#server-project).

### Folders
- `cmd/topbanana`: Application entrypoint.
- `deployments`: Docker compose configuration for the Demo. Can be used as an example.
- `docs`: Documentation for the project.
- `internal/`: Private library code, including domain logic, database operations, HTTP handlers.
  - `admin`: Business Logic for the admin interface.
  - `client`: Client interface using Javascript and CSS.
  - `clientapi`: API used by the client.
  - `config`: Configuration management.
  - `database`: Database connection and utilities.
  - `db`: Database operations and models generated by `sqlc`.
  - `dbtest`: Helpers for testing database operations.
  - `game`: Business logic for gameplay.
  - `health`: Health check endpoint.
  - `httputil`: HTTP utilities for parsing query parameters, encoding and decoding JSON, and more.
  - `migrations`: Database migrations.
  - `queries`: SQL queries used by the `sqlc` for the application.
  - `quiz`: Business logic for quiz creation and management.
  - `server`: HTTP server, routes, and middleware.
  - `store`: Database storage layer for quizzes and games.
  - `testutil`: Helpers for testing the application.
  - `web`: Admin interface using `html/template`.
- `test`: Integration tests for the application.

### Files
- `.env.example`: Example environment variables file.
- `.golangci.yaml`: Configuration for `golangci-lint`.
- `docker-compose.yml`: Docker compose configuration for development.
- `Dockerfile`: Dockerfile for building the application.
- `sqlc.yaml`: Configuration for `sqlc`.
- `Makefile`: Makefile for common tasks.

## Installation

Instructions will be provided soon.

## Development

### Code Organization
- All packages are organized into packages based on functionality.
- Package have names that reflect their purpose.
- Packages are focused on a single responsibility.

### Code Style
This project uses conventions used by the standard library and the following style guides:
- [Go Style Guide](https://google.github.io/styleguide/go/)
- [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments)

### Running tests
- To run unit tests: `make test`
- To run integration tests: `make test-integration`
- To run all tests: `make test-all`
- To check test coverage for all packages: `make test-coverage`
- To view test coverage in your browser: `make test-coverage-html`

### Pre-commit check
Run `make check` to run linters, build the project, and run all tests with coverage.
