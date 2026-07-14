.PHONY: build build-linux build-all clean

APP := smart-alert-aggregator
DIST := dist
LDFLAGS := -s -w

build:
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o $(DIST)/$(APP) ./cmd

build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(DIST)/$(APP)-linux-amd64 ./cmd
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o $(DIST)/$(APP)-linux-arm64 ./cmd

build-all: build-linux
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o $(DIST)/$(APP)-darwin-arm64 ./cmd
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(DIST)/$(APP)-darwin-amd64 ./cmd

clean:
	rm -rf $(DIST)
