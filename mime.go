package viamrtsp

/*
#cgo pkg-config: libavutil libswscale
#include <libavutil/frame.h>
#include <libswscale/swscale.h>
*/
import "C"

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"

	"go.viam.com/rdk/components/camera"
	rutils "go.viam.com/rdk/utils"
)

func encodeToJPEG(img image.Image) ([]byte, camera.ImageMetadata, error) {
	buf := new(bytes.Buffer)
	if err := jpeg.Encode(buf, img, nil); err != nil {
		return nil, camera.ImageMetadata{}, err
	}
	return buf.Bytes(), camera.ImageMetadata{
		MimeType: rutils.MimeTypeJPEG,
	}, nil
}

// takes in a avframe and converts it to a YUYV422 format using swscale
func convertYUV420toYUYV422(src *C.AVFrame) (*C.AVFrame, error) {
	// initialze the destination frame
	dst := C.av_frame_alloc()
	if dst == nil {
		return nil, errors.New("failed to allocate destination frame")
	}

	// set the destination frame format
	dst.format = C.AV_PIX_FMT_YUYV422
	// hardcode width and height for now
	dst.width = src.width
	dst.height = src.height

	// allocate the destination frame
	// res := C.av_frame_get_buffer(dst, 1)
	res := C.av_frame_get_buffer(dst, 32)
	if res < 0 {
		return nil, errors.New("failed to allocate destination frame buffer")
	}

	// initialize the sws context
	swsCtx := C.sws_getContext(
		src.width, src.height, C.AV_PIX_FMT_YUV420P,
		dst.width, dst.height, C.AV_PIX_FMT_YUYV422,
		C.SWS_BICUBIC, nil, nil, nil,
	)

	if swsCtx == nil {
		return nil, errors.New("failed to initialize sws context")
	}

	// convert the frame
	res = C.sws_scale(swsCtx, frameData(src), frameLineSize(src),
		0, src.height, frameData(dst), frameLineSize(dst))

	if res < 0 {
		return nil, errors.New("failed to convert frame")
	}

	// free the sws context
	C.sws_freeContext(swsCtx)

	return dst, nil
}
