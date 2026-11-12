# Bazel-native build commands. Proto codegen stays with buf.

default:
    @just --list

# Build everything
build:
    bazelisk build //...

# Run all tests (unit, conformance, integration). Needs API keys in env.
test:
    bazelisk test //... \
        --test_env=GOOGLE_API_KEY --test_env=OPENAI_API_KEY --test_env=ANTHROPIC_API_KEY

# Run unit tests only (no API keys needed)
test-unit:
    bazelisk test //...

# Run tests with coverage report
test-cover:
    go test -race -coverprofile=coverage.out -covermode=atomic ./pkg/...
    go tool cover -func=coverage.out
    go tool cover -html=coverage.out -o coverage.html

# Run conformance tests against all cloud providers (requires API keys)
conformancetest:
    go test -tags external_llm ./conformancetest -v -timeout=300s

# Run gazelle to sync BUILD files from go.mod
gazelle:
    bazelisk run //:gazelle

# Update BUILD files after adding/removing Go files or deps
update-build: gazelle

# Generate proto code for the annotations and the test protos (outside Bazel)
generate:
    cd proto && buf generate
    cd pkg/testdata && buf generate
    rm -rf pkg/testdata/gen/go/mcp/
    gofumpt -l -w pkg/testdata/
    find pkg/mcpv1 -name '*.go' | xargs gofumpt -l -w

# Lint code
lint:
    golangci-lint run

# Format code (excludes generated protobuf files)
fmt:
    find . -name '*.go' -not -path './pkg/testdata/gen/*' | xargs gofumpt -l -w
