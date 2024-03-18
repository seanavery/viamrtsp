FFMPEG_PREFIX ?= $(shell pwd)/ffmpeg/$(shell uname -s)-$(shell uname -m)
FFMPEG_OPTS ?= --prefix=$(FFMPEG_PREFIX) \
               --enable-static \
               --disable-shared \
               --disable-programs \
               --disable-doc \
               --disable-everything \
               --enable-decoder=h264 \
               --enable-decoder=hevc \
               --enable-swscale

.PHONY: build-ffmpeg

bin/viamrtsp: build-ffmpeg *.go cmd/module/*.go
	CGO_CFLAGS=-I$(FFMPEG_PREFIX)/include \
		CGO_LDFLAGS="-L$(FFMPEG_PREFIX)/lib -l:libjpeg.a" \
		go build -o bin/viamrtsp cmd/module/cmd.go

test:
	go test

lint:
	gofmt -w -s .

updaterdk:
	go get go.viam.com/rdk@latest
	go mod tidy

FFmpeg:
	git clone https://github.com/FFmpeg/FFmpeg.git --depth 1 --branch release/6.1

build-ffmpeg: FFmpeg
	cd FFmpeg && ./configure $(FFMPEG_OPTS) && make -j$(shell nproc) && make install

module: bin/viamrtsp
	tar czf module.tar.gz bin/viamrtsp
