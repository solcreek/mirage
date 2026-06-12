BIN := bin/mirage

.PHONY: build sign test clean

build:
	@mkdir -p bin
	go build -o $(BIN) ./cmd/mirage
	codesign --entitlements entitlements.plist -s - --force $(BIN)

test:
	go test ./...

clean:
	rm -rf bin
