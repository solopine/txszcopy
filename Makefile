SHELL=/usr/bin/env bash

GOCC?=go


deps: $(BUILD_DEPS)
.PHONY: deps

## ldflags -s -w strips binary

#txcartool: $(BUILD_DEPS)
#	rm -f txcartool
#	GOAMD64=v3 $(GOCC) build $(GOFLAGS) -gcflags "all=-N -l" -o txcartool -ldflags " \
#	-X github.com/filecoin-project/curio/build.CurrentCommit=+git_`git log -1 --format=%h_%cI`" \
#	./cmd

txszcopy: $(BUILD_DEPS)
	rm -f txszcopy
	GOAMD64=v3 $(GOCC) build $(GOFLAGS) -o txszcopy -ldflags " -s -w  \
	-X github.com/solopine/txszcopy/build.CurrentCommit=+git_`git log -1 --format=%h_%cI`" \
	./cmd


.PHONY: txszcopy
BINS+=txszcopy



debug: GOFLAGS+=-tags=debug
debug: build



all: build
.PHONY: all

build: txszcopy
	@[[ $$(type -P "txszcopy") ]] && echo "Caution: you have \
an existing txszcopy binary in your PATH. This may cause problems if you don't run 'sudo make install'" || true

.PHONY: build


# TODO move systemd?

buildall: $(BINS)

clean:
	rm -rf $(CLEAN) $(BINS)
.PHONY: clean

dist-clean:
	git clean -xdff
	git submodule deinit --all -f
.PHONY: dist-clean