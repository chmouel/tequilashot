NAME = tequilashot
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

all: build

mkdir:
	mkdir -p bin

build: mkdir
	go build -ldflags "-X github.com/chmouel/tequilashot/internal/gui.Version=$(VERSION)" -o bin/$(NAME) ./cmd/$(NAME)

sanity: lint format test

lint:
	golangci-lint run --fix ./...

format:
	gofumpt -w .

test:
	go test ./...

coverage:
	go test ./... -covermode=count -coverprofile=coverage.out
	go tool cover -func=coverage.out -o=coverage.out

release:
	./scripts/make-release.sh

screenshots:
	python3 hack/screenshots.py

optimize:
	for i in .github/screenshots/*.png;do pngquant --ext .new.png --skip-if-larger --quality 75 -f $$i;t=$${i/.png/.new.png};[[ -e $$t ]] && mv -vf $$t $$i || true;done
.PHONY: all build lint format test coverage sanity mkdir release screenshots
