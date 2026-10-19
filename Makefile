BINARY := socat-webui
PKG := ./cmd/socat-webui
LDFLAGS := -s -w

.PHONY: build build-web build-linux-amd64 build-linux-arm64 test dev clean

build: build-web
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) $(PKG)

build-web:
	pnpm --dir web install
	pnpm --dir web build

build-linux-amd64: build-web
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/linux_amd64/$(BINARY) $(PKG)

build-linux-arm64: build-web
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/linux_arm64/$(BINARY) $(PKG)

test:
	go test ./...

dev:
	pnpm --dir web dev

clean:
	rm -rf bin
