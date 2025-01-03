package viamrtsp

/*
#cgo pkg-config: libavutil libswscale
#include <libavutil/frame.h>
#include <libavutil/imgutils.h>
#include <libswscale/swscale.h>
*/
import "C"

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"unsafe"

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

func convertYUV420toYUYV422(frame *avFrameWrapper) ([]byte, camera.ImageMetadata, error) {
	// allocate a new avframe
	dst := C.av_frame_alloc()
	defer C.av_frame_free(&dst)
	dst.format = C.AV_PIX_FMT_YUYV422
	dst.width = frame.frame.width
	dst.height = frame.frame.height
	res := C.av_frame_get_buffer(dst, 32)
	if res < 0 {
		return nil, camera.ImageMetadata{}, fmt.Errorf("failed to allocate buffer for new frame: %d", res)
	}
	// convert the frame to YUYV422
	// swsCtx := C.sws_getContext(
	// 	frame.frame.width, frame.frame.height, C.AV_PIX_FMT_YUV420P,
	// 	dst.width, dst.height, C.AV_PIX_FMT_YUYV422,
	// 	C.SWS_BICUBIC, nil, nil, nil,
	// )
	// hack to use RGBA for now instead of YUV420P until we rip out decoder sws conversion
	swsCtx := C.sws_getContext(
		frame.frame.width, frame.frame.height, C.AV_PIX_FMT_RGBA,
		dst.width, dst.height, C.AV_PIX_FMT_YUYV422,
		C.SWS_BICUBIC, nil, nil, nil,
	)
	if swsCtx == nil {
		return nil, camera.ImageMetadata{}, fmt.Errorf("failed to create sws context")
	}
	defer C.sws_freeContext(swsCtx)
	// res = C.sws_scale(swsCtx, frameData(frame.frame), frameLineSize(frame.frame),
	// 	0, frame.frame.height, frameData(dst), frameLineSize(dst))
	res = C.sws_scale(swsCtx, frameData(frame.frame), frameLineSize(frame.frame),
		0, frame.frame.height, frameData(dst), frameLineSize(dst))
	if res < 0 {
		return nil, camera.ImageMetadata{}, fmt.Errorf("failed to scale frame: %d", res)
	}
	// return the new frame as go byte slice
	dstFrameSize := C.av_image_get_buffer_size(C.AV_PIX_FMT_YUYV422, dst.width, dst.height, 32)
	dataGo := C.GoBytes(unsafe.Pointer(dst.data[0]), dstFrameSize)
	return dataGo, camera.ImageMetadata{
		MimeType: "image/vnd.viam.yuyv",
	}, nil
}
