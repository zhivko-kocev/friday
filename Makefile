VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
PKG       := github.com/zhivko-kocev/friday/cmd/friday
LDFLAGS   := -X main.version=$(VERSION)

# Append .exe on Windows so the binary has a recognised extension; without it
# Windows can't pick a handler and double-clicking shows "Choose an app".
ifeq ($(OS),Windows_NT)
  EXE := .exe
else
  EXE :=
endif
BIN       := friday$(EXE)

.PHONY: build install clean test lint tidy

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/friday

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/friday

test:
	go test ./...

lint:
	go vet ./...

tidy:
	go mod tidy

# `-` prefix lets Make swallow rm's exit code on hosts where it's absent
# (Windows cmd.exe). On Git Bash / WSL / Unix it works as expected.
clean:
	go clean ./...
	-rm -f $(BIN) $(BIN).exe
	-rm -rf dist/

# Cross-compile for all release targets
release-local:
	GOOS=linux   GOARCH=amd64   go build -ldflags "$(LDFLAGS)" -o dist/$(BIN)_linux_amd64   ./cmd/friday
	GOOS=linux   GOARCH=arm64   go build -ldflags "$(LDFLAGS)" -o dist/$(BIN)_linux_arm64   ./cmd/friday
	GOOS=darwin  GOARCH=amd64   go build -ldflags "$(LDFLAGS)" -o dist/$(BIN)_darwin_amd64  ./cmd/friday
	GOOS=darwin  GOARCH=arm64   go build -ldflags "$(LDFLAGS)" -o dist/$(BIN)_darwin_arm64  ./cmd/friday
	GOOS=windows GOARCH=amd64   go build -ldflags "$(LDFLAGS)" -o dist/$(BIN)_windows_amd64.exe ./cmd/friday
