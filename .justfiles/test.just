# Test recipes for agent-coordinator

set working-directory := '..'

@default:
    just --list --justfile {{source_file()}}

# Run all tests with race detector
all:
    go test -race ./...

# Run unit tests only (skip integration)
unit:
    go test -short ./...

# Run unit tests with verbose output
unit-verbose:
    go test -short -v ./...

# Show coverage summary
coverage:
    go test -race -coverprofile=coverage.out ./...
    go tool cover -func=coverage.out
    @rm -f coverage.out

# Generate HTML coverage report
coverage-html:
    go test -race -coverprofile=coverage.out ./...
    go tool cover -html=coverage.out -o coverage.html
    @echo "Coverage report: coverage.html"

# Run benchmarks
bench:
    go test -bench=. -benchmem ./...

# Run tests matching a pattern
pattern PATTERN:
    go test -race -run {{PATTERN}} ./...

# Test a specific package
pkg PKG:
    go test -race {{PKG}}
