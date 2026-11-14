default: build

build:
	go build -v .

install: build
	go install -v .

test:
	go test ./tfprovider/ ./internal/... ./tools/easel/ -count=1

vet:
	go vet ./tfprovider/ ./internal/... ./cmd/... .

fmt:
	gofmt -w tfprovider internal cmd main.go

# make docs renders README.md, docs/ and the generated examples from the canvases under
# tools/easel/canvas. Those files are never edited by hand: text is laid stroke by stroke
# with the easel (go run ./tools/easel help), which checks every name against the provider.
docs:
	go run ./tools/easel render

# make docs-check fails when a rendered file differs from its canvas or a page is incomplete.
docs-check:
	go run ./tools/easel verify
	go run ./tools/easel critique

# Lifecycle check against a running OBS; see README.md (Lab tools).
lab-inventory:
	go run ./cmd/obs-inventory

.PHONY: build install test vet fmt docs docs-check lab-inventory
