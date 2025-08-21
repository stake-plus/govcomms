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

# Binary names
FEEDBACK_BIN = bin$(SEP)feedback-bot$(BINARY_EXT)
AIQA_BIN = bin$(SEP)ai-qa-bot$(BINARY_EXT)
POLKADOT_BIN = bin$(SEP)polkadot-test$(BINARY_EXT)

.PHONY: all build clean feedback ai-qa polkadot-go run-feedback run-ai-qa install test

all: build

build: feedback ai-qa

feedback:
	$(MKDIR) bin
	go build -o $(FEEDBACK_BIN) ./src/feedback

ai-qa:
	$(MKDIR) bin
	go build -o $(AIQA_BIN) ./src/ai-qa

polkadot-go:
	$(MKDIR) bin
	go build -o $(POLKADOT_BIN) ./src/polkadot-go

clean:
ifeq ($(OS),Windows_NT)
	if exist bin $(RM) bin$(SEP)*
else
	$(RM) bin/
endif

run-feedback:
	$(FEEDBACK_BIN)

run-ai-qa:
	$(AIQA_BIN)

install:
	go mod download

test:
	go test ./...

# Build all binaries for both platforms
build-all: build-linux build-windows

build-linux:
	GOOS=linux GOARCH=amd64 go build -o bin/feedback-bot-linux ./src/feedback
	GOOS=linux GOARCH=amd64 go build -o bin/ai-qa-bot-linux ./src/ai-qa

build-windows:
	GOOS=windows GOARCH=amd64 go build -o bin/feedback-bot.exe ./src/feedback
	GOOS=windows GOARCH=amd64 go build -o bin/ai-qa-bot.exe ./src/ai-qa