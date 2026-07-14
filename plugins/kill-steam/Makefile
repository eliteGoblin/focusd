# Makefile for plugins/kill-steam
#
# Targets:
#   build   compile the plugin binary into ./build/
#   test    run unit tests with -race
#   bundle  build + copy binary + plugin.json into the platform bundle
#           dir (platform/internal/bundle/data/kill-steam/) so the
#           platform's release pipeline ships the plugin embedded.
#
# Mirrors plugins/skill-protector/Makefile + plugins/network-block/Makefile.

PLUGIN_NAME := kill-steam
BUILD_DIR   := build
OUT_BIN     := $(BUILD_DIR)/$(PLUGIN_NAME)

REPO_ROOT   := $(shell git rev-parse --show-toplevel 2>/dev/null || (cd $(CURDIR)/../.. && pwd))
# HF4 (FEATURE 24): bundle dir + binary use a NEUTRAL codename (not the plugin
# id) so the on-disk install carries no greppable plugin token. Discovery keys
# off plugin.json "id" (unchanged), not the dir name; entrypoint → ./$(BUNDLE_NAME).
BUNDLE_NAME := cedar
BUNDLE_DIR  := $(REPO_ROOT)/platform/internal/bundle/data/$(BUNDLE_NAME)

GO ?= go
LDFLAGS ?= -s -w

.PHONY: all build test bundle clean

all: build

build:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(OUT_BIN) ./cmd

test:
	$(GO) test ./... -race -count=1

bundle: build
	mkdir -p $(BUNDLE_DIR)
	cp plugin.json $(BUNDLE_DIR)/plugin.json
	cp $(OUT_BIN)  $(BUNDLE_DIR)/$(BUNDLE_NAME)
	@echo "bundled $(PLUGIN_NAME) into $(BUNDLE_DIR)"

clean:
	rm -rf $(BUILD_DIR)
