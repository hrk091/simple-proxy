IMAGE_NAME ?= "simple-proxy"

.PHONY: build
build:
	docker build -t $(IMAGE_NAME) .
