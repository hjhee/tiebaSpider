.DEFAULT_GOAL := build

GITVER = `git rev-parse HEAD`

build:
	@go build -ldflags "-X main.version=${GITVER}"

clean:
	@go clean

.PHONY: git-tree-check
git-tree-check:
ifneq ($(git diff --stat),)
	$(warning "git tree is not clean")
endif

win: git-tree-check
	@echo ver: ${GITVER}
	@GOOS="windows" go build -ldflags "-X main.version=${GITVER}"
	@zip win64.zip template/*.html tiebaSpider.exe LICENSE README.md url.txt
