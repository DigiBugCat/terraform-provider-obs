default: build

build:
	go build -v .

install: build
	go install -v .

test:
	go test ./tfprovider/ ./internal/... -count=1

vet:
	go vet ./tfprovider/ ./internal/... ./cmd/... .

fmt:
	gofmt -w tfprovider internal cmd main.go

docs:
	go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs@v0.25.0 generate

# Lifecycle check against a running OBS; see README.md (Lab tools).
lab-inventory:
	go run ./cmd/obs-inventory

.PHONY: build install test vet fmt docs lab-inventory
