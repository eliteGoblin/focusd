# Makefile for plugins/skill-protector
#
# Targets:
#   build   compile the plugin binary into ./build/
#   test    run unit tests with -race
#   bundle  build + copy binary + plugin.json into the platform bundle
#           dir (platform/internal/bundle/data/skill-protector/) so the
#           platform's release pipeline can ship the plugin alongside
#           the platform itself.
#
# The hook script (internal/reconciler/data/hook.sh) is the embedded
# canonical script. The rule body inside that script must mirror
# data/rule.md exactly; the `hook.sh` step regenerates it on demand.

PLUGIN_NAME := skill-protector
BUILD_DIR   := build
OUT_BIN     := $(BUILD_DIR)/$(PLUGIN_NAME)

# Resolve REPO_ROOT: prefer git, fall back to walking up from PWD. The
# walk-up handles a tarball checkout (no .git). The plugin lives at
# <repo>/plugins/skill-protector so the walk-up is two levels.
REPO_ROOT   := $(shell git rev-parse --show-toplevel 2>/dev/null || (cd $(CURDIR)/../.. && pwd))
# HF4 (FEATURE 24): bundle dir + binary use a NEUTRAL codename (not the plugin
# id) so the on-disk install carries no greppable plugin token. Discovery keys
# off plugin.json "id" (unchanged), not the dir name; entrypoint → ./$(BUNDLE_NAME).
BUNDLE_NAME := harbor
BUNDLE_DIR  := $(REPO_ROOT)/platform/internal/bundle/data/$(BUNDLE_NAME)

GO ?= go
LDFLAGS ?= -s -w

.PHONY: all build test bundle hook-script clean

all: build

build: hook-script
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(OUT_BIN) ./cmd

test: hook-script
	$(GO) test ./... -race -count=1

# hook-script regenerates internal/reconciler/data/hook.sh from
# internal/reconciler/data/rule.md so the embedded heredoc body is
# always in sync with the rule. The script is committed to the repo
# so a plain `go test` (no Makefile) also works; this target is the
# write-side of that contract.
hook-script:
	@sh ./scripts/regen-hook.sh

bundle: build
	mkdir -p $(BUNDLE_DIR)
	cp plugin.json $(BUNDLE_DIR)/plugin.json
	cp $(OUT_BIN)  $(BUNDLE_DIR)/$(BUNDLE_NAME)
	@echo "bundled $(PLUGIN_NAME) into $(BUNDLE_DIR)"

clean:
	rm -rf $(BUILD_DIR)
