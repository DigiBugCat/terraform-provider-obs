.PHONY: fetch build check clean

fetch:
	./scripts/fetch-sdk.sh

build:
	./scripts/build-macos.sh

check: build
	./scripts/check-offline.sh
	./scripts/analyze.sh
	./descriptor/build.sh

clean:
	find build -mindepth 1 -maxdepth 1 ! -name deps -exec rm -rf -- {} +
