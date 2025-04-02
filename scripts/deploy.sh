#!/bin/bash
set -e

# Change to the root directory of the project
cd "$(dirname "$0")/.."
PROJECT_ROOT=$(pwd)

function run_tests {
    echo "Running tests with coverage..."
    go test -v $(go list ./... | grep -v vendor) --count 1 -race \
      -coverprofile="${PROJECT_ROOT}/coverage.txt" -covermode=atomic
}

function run_tests_ci {
    echo "Running tests in CI environment..."
    export CIRCLECI="true"
    run_tests
}

function perform_release {
    tag=$1
    if [ -z "$tag" ]; then
        echo "Error: Missing tag name. Usage: $0 release <tag>"
        exit 1
    fi

    echo "Creating and pushing tag: $tag"
    git tag -a "$tag" -m "Release $tag"
    git push origin "$tag"

    echo "Cleaning previous release artifacts"
    rm -rf "${PROJECT_ROOT}/dist"

    echo "Running goreleaser"
    goreleaser release --rm-dist
}

function test_release {
    echo "Cleaning previous release artifacts"
    rm -rf "${PROJECT_ROOT}/dist"

    echo "Running goreleaser in snapshot mode"
    goreleaser release --snapshot --clean
}

# Execute the specified command
cmd=$1
shift
case "$cmd" in
    test)
        run_tests "$@"
        ;;
    test_ci)
        run_tests_ci "$@"
        ;;
    release)
        perform_release "$@"
        ;;
    release_test)
        test_release "$@"
        ;;
    *)
        echo "Unknown command: $cmd"
        echo "Available commands: test, test_ci, release, release_test"
        exit 1
        ;;
esac
