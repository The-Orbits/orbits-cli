# make build     the CLI for this machine, in ./orbits
# make test      gofmt, vet and the tests (with the race detector)
# make e2e       the live test (needs ORBITS_E2E_TOKEN and ORBITS_E2E_TEAM)
# make dist      every platform's archive and checksums.txt in ./dist, named
#                as the GitHub release has them (releases themselves are made
#                by GoReleaser, see .goreleaser.yaml)
# make npm       the npm packages (@useorbits/cli and its platform packages)
#                from ./dist in ./dist/npm, and a run of this machine's
#                through the launcher (after make dist)

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/The-Orbits/orbits-cli/internal/cli.Version=$(VERSION)
PLATFORMS := darwin_arm64 darwin_amd64 linux_amd64 linux_arm64

.PHONY: build test e2e dist npm clean

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o orbits ./cmd/orbits

test:
	@test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)
	go vet ./...
	go test -race ./...

e2e:
	go test -v -run TestLive ./e2e

dist: clean
	@mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%_*}; arch=$${p#*_}; dir=$$(mktemp -d); \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o $$dir/orbits ./cmd/orbits || exit 1; \
		cp LICENSE README.md CHANGELOG.md $$dir/; \
		tar -czf dist/orbits_$$p.tar.gz -C $$dir orbits LICENSE README.md CHANGELOG.md; \
		rm -r "$$dir"; \
		echo "built orbits_$$p.tar.gz"; \
	done
	@cd dist && shasum -a 256 *.tar.gz > checksums.txt

npm:
	node npm/build.mjs --version $(if $(filter v%,$(VERSION)),$(VERSION),v0.0.0-$(VERSION)) --dist dist --out dist/npm
	npm/smoke.sh dist/npm

clean:
	rm -f orbits
	rm -fr dist
