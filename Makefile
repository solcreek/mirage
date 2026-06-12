BIN := bin/mirage

.PHONY: build tools-image test vet clean

build:
	@mkdir -p bin
	go build -o $(BIN) ./cmd/mirage
	codesign --entitlements entitlements.plist -s - --force $(BIN)

# Build the guest agent tools image (auto-mounts in the guest as /Volumes/MirageTools).
tools-image:
	./scripts/build-tools-image.sh

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -rf bin
