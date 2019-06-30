NAME:=nxforwarder
COMMIT := $(shell git log -1 --format=%ct)
DESCRIPTION:="NXDOMAIN forwarder"
MAINTAINER:="Sergey Ignatov <sergey.a.ignatov@gmail.com>"
VERSION:=$(shell sh -c 'git describe --always --tags --abbrev=0| sed -e "s/^v//i"')



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
		--config-files /etc/default/$(NAME) \
		--config-files /lib/systemd/system/$(NAME).service \
		misc/$(NAME).default=/etc/default/$(NAME) \
		misc/$(NAME).service=/lib/systemd/system/$(NAME).service \
		bin/$(NAME)=/usr/bin/$(NAME)

dep:
ifeq ($(shell command -v dep 2> /dev/null),)
	go get -u -v github.com/golang/dep/cmd/dep
endif

deps: dep
	dep ensure -v
