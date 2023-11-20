GOVERSION=$(shell go version)
THIS_GOOS=$(word 1,$(subst /, ,$(lastword $(GOVERSION))))
THIS_GOARCH=$(word 2,$(subst /, ,$(lastword $(GOVERSION))))
GOOS?=$(THIS_GOOS)
GOARCH?=$(THIS_GOARCH)
DIR_BUILD=build

default: build

.PHONY: \
    build \
	.build-docker \
	build-debug \
	build-linux-amd64 \
	build-linux-386 \
	test \

build:
	go build -race ./...

.PHONY: lint
lint:
	go vet ./...
	golangci-lint run

.PHONY: check-example
check-example:
	./scripts/diff-example-doc.sh 10 95 ./examples/connect-intdash/main.go
	./scripts/diff-example-doc.sh 101 156 ./examples/hello-world/upstream/main.go
	./scripts/diff-example-doc.sh 165 231 ./examples/hello-world/downstream/main.go

TEST_COUNT?=1
TEST_TIMEOUT?=120s
.PHONY: test-unit
test-unit:
	go test -cover -coverprofile=cover.out ./... -timeout $(TEST_TIMEOUT) -count $(TEST_COUNT)
	go tool cover -func=cover.out

clean:
	rm -rf build

$(DIR_BUILD)/$(GOOS)_$(GOARCH):
	mkdir -p $@

CREDITS: go.sum
	cp -p go.sum go.sum.bak
	cat go.sum.bak \
	  | grep -v github.com \
	  | grep -v github.com/golangci/gofmt \
	  > go.sum
	gocredits -w -skip-missing
	cp -p go.sum.bak go.sum
	rm go.sum.bak

serve-go-doc:
	go run golang.org/x/tools/cmd/godoc@latest -http=:6060 > /dev/null

DIR_BUILD=build
.PHONY: go-doc
go-doc: ${DIR_BUILD}/doc/godoc
	./scripts/gen-static-godoc.sh

.PHONY: gen-message-proto
gen-message-proto:
	go generate ./encoding
	go generate ./wire/wireproto

${DIR_BUILD}/doc/godoc:
	mkdir -p $@
