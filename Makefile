VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build test vet fmt check hooks hooks-test scan

# bin/heka 를 빌드한다(버전은 -ldflags 로 주입).
build:
	go build -ldflags "-X main.version=$(VERSION)" -o bin/heka ./cmd/heka

# Go 테스트를 돌린다.
test:
	go test ./...

# go vet 정적 검사를 돌린다.
vet:
	go vet ./...

# gofmt 를 통과하지 못한 파일이 있으면 목록을 보이고 실패한다.
fmt:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt 필요:"; echo "$$out"; exit 1; fi

# 푸시 전에 돌리는 전체 검사.
check: fmt vet test hooks-test

# 이 저장소의 git 훅 경로를 .githooks 로 지정한다.
hooks:
	git config core.hooksPath .githooks

# pre-push 훅 자체의 테스트를 돌린다.
hooks-test:
	bash scripts/test-pre-push.sh

# 훅을 HEAD 에서 닿는 모든 커밋에 대해 수동으로 돌린다.
scan:
	.githooks/pre-push --all
