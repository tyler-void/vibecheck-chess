.PHONY: build run test test-race lint tailwind tailwind-watch perf-smoke perf-soak clean help

BIN          := bin/kisschess
TAILWIND     := bin/tailwindcss
TAILWIND_URL := https://github.com/tailwindlabs/tailwindcss/releases/latest/download/tailwindcss-linux-x64
INPUT_CSS    := web/input.css
OUTPUT_CSS   := web/output.css
PERF_BIN     := bin/k6
PERF_HOST    ?= http://localhost:8080

help:
	@echo 'Targets:'
	@echo '  build           — go build -o $(BIN) ./cmd/server'
	@echo '  run             — go run ./cmd/server (dev)'
	@echo '  test            — go test ./...'
	@echo '  test-race       — go test -race ./... (mandatory before commit)'
	@echo '  lint            — gofmt -d + go vet + staticcheck'
	@echo '  tailwind        — compile $(INPUT_CSS) -> $(OUTPUT_CSS) (downloads CLI on first run)'
	@echo '  tailwind-watch  — recompile on change (dev)'
	@echo '  perf-smoke      — k6 run tests/perf/scenarios/move-throughput.js'
	@echo '  perf-soak       — k6 run tests/perf/scenarios/capacity-soak.js (1h)'
	@echo '  clean           — rm -rf bin $(OUTPUT_CSS)'

$(TAILWIND):
	@mkdir -p bin
	curl -fsSL -o $@ $(TAILWIND_URL)
	chmod +x $@

build:
	@mkdir -p bin
	go build -o $(BIN) ./cmd/server

run:
	go run ./cmd/server

test:
	go test ./...

test-race:
	go test -race ./...

lint:
	gofmt -d .
	go vet ./...
	@command -v staticcheck >/dev/null || (echo 'staticcheck not installed; go install honnef.co/go/tools/cmd/staticcheck@latest' && exit 1)
	staticcheck ./...

tailwind: $(TAILWIND)
	$(TAILWIND) -i $(INPUT_CSS) -o $(OUTPUT_CSS) --minify

tailwind-watch: $(TAILWIND)
	$(TAILWIND) -i $(INPUT_CSS) -o $(OUTPUT_CSS) --watch

# k6 with xk6-sse must be built once: xk6 build --with github.com/phymbert/xk6-sse
$(PERF_BIN):
	@command -v xk6 >/dev/null || (echo 'xk6 not installed; go install go.k6.io/xk6/cmd/xk6@latest' && exit 1)
	@mkdir -p bin
	xk6 build --with github.com/phymbert/xk6-sse@latest --output $@

perf-smoke: $(PERF_BIN)
	BASE_URL=$(PERF_HOST) $(PERF_BIN) run tests/perf/scenarios/move-throughput.js

perf-soak: $(PERF_BIN)
	BASE_URL=$(PERF_HOST) $(PERF_BIN) run tests/perf/scenarios/capacity-soak.js

clean:
	rm -rf bin $(OUTPUT_CSS)
