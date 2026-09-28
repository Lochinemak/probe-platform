# probe-platform build helpers
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  = -s -w -X probe-platform/internal/buildinfo.Version=$(VERSION)
GOFLAGS  = -trimpath
BIN      = bin

.PHONY: all web server agent build agents-all docker run-server run-agent test lint clean geoip-db

all: build

## web: build the Vue dashboard into web/dist (embedded into the server binary)
web:
	cd web && npm install --no-fund --no-audit && npm run build

## server: build the server for the current platform (requires `make web` first)
server:
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN)/probe-server ./cmd/server

## agent: build the agent for the current platform
agent:
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN)/probe-agent ./cmd/agent

build: web server agent

## agents-all: cross-compile agents for every NAS / server architecture
agents-all:
	@mkdir -p $(BIN)
	@for t in linux/amd64 linux/arm64 linux/arm/7 linux/arm/6 linux/386 linux/mipsle linux/mips linux/mips64le linux/riscv64 darwin/arm64 darwin/amd64 windows/amd64 windows/arm64 windows/386 freebsd/amd64; do \
		os=$${t%%/*}; rest=$${t#*/}; arch=$${rest%%/*}; arm=""; suffix=""; \
		case $$rest in */*) arm=$${rest#*/}; suffix=v$$arm;; esac; \
		ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		out=$(BIN)/probe-agent-$$os-$$arch$$suffix$$ext; \
		echo "  -> $$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch GOARM=$$arm GOMIPS=softfloat go build $(GOFLAGS) -ldflags "$(LDFLAGS) -X probe-platform/internal/buildinfo.Variant=$$suffix" -o $$out ./cmd/agent || exit 1; \
	done
	@for t in linux/amd64 linux/arm64; do \
		os=$${t%%/*}; arch=$${t#*/}; \
		out=$(BIN)/probe-server-$$os-$$arch; echo "  -> $$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $$out ./cmd/server || exit 1; \
	done

## docker: build multi-arch images locally (needs docker buildx)
IMAGE_PREFIX ?= probe-platform
docker:
	docker buildx build --platform linux/amd64,linux/arm64,linux/arm/v7 -f deploy/Dockerfile.agent  -t $(IMAGE_PREFIX)/probe-agent:$(VERSION)  --build-arg VERSION=$(VERSION) .
	docker buildx build --platform linux/amd64,linux/arm64                -f deploy/Dockerfile.server -t $(IMAGE_PREFIX)/probe-server:$(VERSION) --build-arg VERSION=$(VERSION) .

## run-server / run-agent: local development
run-server:
	go run ./cmd/server --data ./data --log-level debug --agents-dir ./bin

run-agent:
	go run ./cmd/agent --server http://localhost:8080 --token "$$(cat data/agent_token)" --name dev-local --location 本机 --isp 开发 --log-level debug

test:
	go test ./...

lint:
	go vet ./...
	gofmt -l ./cmd ./internal

## geoip-db: download the ip2region offline database used to label MTR hops
geoip-db:
	@mkdir -p data
	curl -fL -o data/ip2region.xdb https://raw.githubusercontent.com/lionsoul2014/ip2region/master/data/ip2region_v4.xdb
	@echo "saved data/ip2region.xdb (restart probe-server to load it)"

clean:
	rm -rf $(BIN) web/dist
