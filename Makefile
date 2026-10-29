.PHONY: build test e2e release
build:
	CGO_ENABLED=0 go build -o bin/docsreader ./cmd/docsreader
test:
	go vet ./... && go test ./...
# Browser tests; needs Google Chrome or Chromium installed.
e2e:
	go test -tags e2e ./internal/adapters/web

release:
	@for t in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do \
		os=$${t%/*}; arch=$${t#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -ldflags '-s -w' -o dist/docsreader-$$os-$$arch$$ext ./cmd/docsreader || exit 1; \
	done
