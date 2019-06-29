NAME:=nxforwarder
COMMIT := $(shell git log -1 --format=%ct)
DESCRIPTION:="NXDOMAIN forwarder"
MAINTAINER:="Sergey Ignatov <sergey.a.ignatov@gmail.com>"
VERSION ?= 0



all: bin/$(NAME)


bin/$(NAME): deps
	@go build -ldflags "-X main.revision=$(VERSION)" -o bin/$(NAME)
test:
	@go test ./...
clean:
	rm -rf bin
deb: bin/$(NAME)
	fpm -s dir -t deb -n $(NAME) -v $(VERSION) \
		--deb-priority optional --category admin \
		--force \
		--url https://github.com/sergeyignatov/$(NAME) \
		--description $(DESCRIPTION) \
		-m $(MAINTAINER) \
		--license "MIT" \
		-a x86_64 \
		misc/nxforwarder.service=/lib/systemd/system/nxforwarder.service

dep:
ifeq ($(shell command -v dep 2> /dev/null),)
	go get -u -v github.com/golang/dep/cmd/dep
endif

deps: dep
	dep ensure -v
