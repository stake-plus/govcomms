# ---------------------------------------------------------------------------
# Build script for the GovComms API
#
# `substrate-api-rpc/keyring` compiles SR‑25519 code with CGO.  We must:
#   * enable CGO (`CGO_ENABLED=1`)
#   * point Go to a working C compiler (`CC`)
#
# GNU make defines a built‑in variable **CC** whose default value is `cc`.
# Using `?=` (set if empty) therefore never overrides it.  Here we **force**
# the value we need with `:=`, then export it so the Go tool‑chain sees it.
# ---------------------------------------------------------------------------

# Detect host OS
ifeq ($(OS),Windows_NT)
  MKDIRBIN = if not exist $(BIN_DIR) mkdir $(BIN_DIR)
  DELBIN   = if exist     $(BIN_DIR) rmdir /S /Q $(BIN_DIR)
  EXT      = .exe
  CC       := gcc               # <- override built‑in “cc” on Windows
else
  MKDIRBIN = mkdir -p $(BIN_DIR)
  DELBIN   = rm -rf $(BIN_DIR)
  EXT      =
  CC       ?= cc                # use system compiler on *nix
endif

# ---------------------------------------------------------------------------
# General build constants
# ---------------------------------------------------------------------------
BIN_DIR := bin
SEP     := /
APP     := govcomms-api

# Go build flags
GOTAGS          ?= sr25519
GO_LDFLAGS      ?=
GO_BUILD_FLAGS  ?=

# Ensure CGO is enabled and the compiler is exported
export CGO_ENABLED := 1
export CC
# (CXX not needed by Go, but you can `export CXX` similarly if desired)

# ---------------------------------------------------------------------------
# Phony targets
# ---------------------------------------------------------------------------
.PHONY: all clean deps build

# Default workflow: clean → deps → build
all: clean deps build

deps:
	go mod tidy
	go get -u ./...

# Ensure bin/ exists
$(BIN_DIR):
	$(MKDIRBIN)

# Compile the API binary
build: $(BIN_DIR)
	go build $(GO_BUILD_FLAGS) -tags '$(GOTAGS)' -ldflags '$(GO_LDFLAGS)' \
		-o $(BIN_DIR)$(SEP)$(APP)$(EXT) ./src/api

# Remove build artefacts
clean:
	-$(DELBIN)
