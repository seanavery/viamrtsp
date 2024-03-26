UNAME_S ?= $(shell uname -s)
UNAME_M ?= $(shell uname -m)
FFMPEG_PREFIX ?= $(shell pwd)/ffmpeg/$(UNAME_S)-$(UNAME_M)
FFMPEG_OPTS ?= --prefix=$(FFMPEG_PREFIX) \
               --enable-static \
               --disable-shared \
               --disable-programs \
               --disable-doc \
               --disable-everything \
               --enable-decoder=h264 \
               --enable-decoder=hevc \
               --enable-swscale
ifeq ($(UNAME_M),x86_64)
    FFMPEG_OPTS += --disable-x86asm
endif
ifeq ($(UNAME_S),Linux)
    CGO_LDFLAGS := "-L$(FFMPEG_PREFIX)/lib -l:libjpeg.a"
else
    CGO_LDFLAGS := -L$(FFMPEG_PREFIX)/lib
endif

.PHONY: build-ffmpeg test lint updaterdk module

bin/viamrtsp: build-ffmpeg *.go cmd/module/*.go
	PKG_CONFIG_PATH=$(FFMPEG_PREFIX)/lib/pkgconfig \
		CGO_CFLAGS=-I$(FFMPEG_PREFIX)/include \
		CGO_LDFLAGS=$(CGO_LDFLAGS) \
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
