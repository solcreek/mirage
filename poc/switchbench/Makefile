.PHONY: build clean

build:
	go build -o switchbench .
	codesign --entitlements entitlements.plist -s - --force switchbench

clean:
	rm -f switchbench
