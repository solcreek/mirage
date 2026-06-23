BIN := bin/mirage

.PHONY: build tools-image linux-tools test vet clean

build:
	@mkdir -p bin
	go build -o $(BIN) ./cmd/mirage
	codesign --entitlements entitlements.plist -s - --force $(BIN)

# Build the guest agent tools image (auto-mounts in the guest as /Volumes/MirageTools).
tools-image:
	./scripts/build-tools-image.sh

# Build the Linux guest tools image (ISO9660 with the arm64 agent + installer).
linux-tools:
	./scripts/build-linux-tools.sh

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -rf bin
