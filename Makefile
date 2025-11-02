# Detect OS
ifeq ($(OS),Windows_NT)
    BINARY_EXT = .exe
    RM = del /Q
    MKDIR = mkdir
    SEP = \\
else
    BINARY_EXT =
    RM = rm -rf
    MKDIR = mkdir -p
    SEP = /
endif

# Binary name
GOVCOMMS_BIN = bin$(SEP)govcomms$(BINARY_EXT)

.PHONY: all build clean govcomms run install test build-all build-linux build-windows

all: build

build: govcomms

govcomms:
	$(MKDIR) bin
	go build -o $(GOVCOMMS_BIN) ./src

clean:
ifeq ($(OS),Windows_NT)
	if exist bin $(RM) bin$(SEP)*
else
	$(RM) bin/
endif

run:
	$(GOVCOMMS_BIN)

install:
	go mod download

test:
	go test ./...

# Build all binaries for both platforms
build-all: build-linux build-windows

build-linux:
	GOOS=linux GOARCH=amd64 go build -o bin/govcomms-linux ./src

build-windows:
	GOOS=windows GOARCH=amd64 go build -o bin/govcomms.exe ./src