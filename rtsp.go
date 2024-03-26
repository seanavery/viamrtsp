package viamrtsp

import (
	"context"
	"image"
	"io"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/bluenviron/gortsplib/v3"
	"github.com/bluenviron/gortsplib/v3/pkg/base"
	"github.com/bluenviron/gortsplib/v3/pkg/formats"
	"github.com/bluenviron/gortsplib/v3/pkg/formats/rtph264"
	"github.com/bluenviron/gortsplib/v3/pkg/formats/rtph265"
	"github.com/bluenviron/gortsplib/v3/pkg/liberrors"
	"github.com/bluenviron/gortsplib/v3/pkg/media"
	"github.com/bluenviron/gortsplib/v3/pkg/url"

	"github.com/pion/rtp"
	"github.com/pkg/errors"
	goutils "go.viam.com/utils"

	"go.viam.com/rdk/components/camera"
	"go.viam.com/rdk/gostream"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/rimage/transform"
)

var family = resource.ModelNamespace("erh").WithFamily("viamrtsp")
var ModelH264 = family.WithModel("rtsp-h264")

func init() {
	resource.RegisterComponent(camera.API, ModelH264, resource.Registration[camera.Camera, *Config]{
		Constructor: newRTSPCamera,
	})
}

// Config are the config attributes for an RTSP camera model.
type Config struct {
	Address          string                             `json:"rtsp_address"`
	IntrinsicParams  *transform.PinholeCameraIntrinsics `json:"intrinsic_parameters,omitempty"`
	DistortionParams *transform.BrownConrady            `json:"distortion_parameters,omitempty"`
}

// Validate checks to see if the attributes of the model are valid.
func (conf *Config) Validate(path string) ([]string, error) {
	_, err := url.Parse(conf.Address)
	if err != nil {
		return nil, err
	}
	if conf.IntrinsicParams != nil {
		if err := conf.IntrinsicParams.CheckValid(); err != nil {
			return nil, err
		}
	}
	if conf.DistortionParams != nil {
		if err := conf.DistortionParams.CheckValid(); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

// rtspCamera contains the rtsp client, and the reader function that fulfills the camera interface.
type rtspCamera struct {
	gostream.VideoReader
	u *url.URL

	client     *gortsplib.Client
	rawDecoder *decoder

	cancelCtx  context.Context
	cancelFunc context.CancelFunc

	activeBackgroundWorkers sync.WaitGroup

	latestFrame atomic.Pointer[image.Image]

	logger logging.Logger
}

// Close closes the camera. It always returns nil, but because of Close() interface, it needs to return an error.
func (rc *rtspCamera) Close(ctx context.Context) error {
	rc.cancelFunc()
	rc.activeBackgroundWorkers.Wait()
	return rc.closeConnection()
}

// clientReconnectBackgroundWorker checks every 5 sec to see if the client is connected to the server, and reconnects if not.
func (rc *rtspCamera) clientReconnectBackgroundWorker() {
	rc.activeBackgroundWorkers.Add(1)
	goutils.ManagedGo(func() {
		for goutils.SelectContextOrWait(rc.cancelCtx, 5*time.Second) {
			badState := false

			// use an OPTIONS request to see if the server is still responding to requests
			if rc.client == nil {
				badState = true
			} else {
				res, err := rc.client.Options(rc.u)
				if err != nil && (errors.Is(err, liberrors.ErrClientTerminated{}) ||
					errors.Is(err, io.EOF) ||
					errors.Is(err, syscall.EPIPE) ||
					errors.Is(err, syscall.ECONNREFUSED)) {
					rc.logger.Warnw("The rtsp client encountered an error, trying to reconnect", "url", rc.u, "error", err)
					badState = true
				} else if res != nil && res.StatusCode != base.StatusOK {
					rc.logger.Warnw("The rtsp server responded with non-OK status", "url", rc.u, "status code", res.StatusCode)
					badState = true
				}
			}

			if badState {
				if err := rc.reconnectClient(); err != nil {
					rc.logger.Warnw("cannot reconnect to rtsp server", "error", err)
				} else {
					rc.logger.Infow("reconnected to rtsp server", "url", rc.u)
				}
			}
		}
	}, rc.activeBackgroundWorkers.Done)
}

func (rc *rtspCamera) closeConnection() error {
	var err error
	if rc.client != nil {
		err = rc.client.Close()
		rc.client = nil
	}
	if rc.rawDecoder != nil {
		rc.rawDecoder.close()
		rc.rawDecoder = nil
	}
	return err
}

// reconnectClient reconnects the RTSP client to the streaming server by closing the old one and starting a new one.
func (rc *rtspCamera) reconnectClient() (err error) {
	rc.logger.Warnf("reconnectClient called")

	if rc == nil {
		return errors.New("rtspCamera is nil")
	}

	err = rc.closeConnection()
	if err != nil {
		rc.logger.Debugw("error while closing rtsp client:", "error", err)
	}

	// replace the client with a new one, but close it if setup is not successful
	rc.client = &gortsplib.Client{}

	var clientSuccessful bool
	defer func() {
		if !clientSuccessful {
			rc.closeConnection()
		}
	}()

	err = rc.client.Start(rc.u.Scheme, rc.u.Host)
	if err != nil {
		return err
	}

	tracks, baseURL, _, err := rc.client.Describe(rc.u)
	if err != nil {
		return err
	}

	codecInfo, err := getStreamInfo(rc.u.String())
	if err != nil {
		return err
	}

	switch codecInfo {
	case H264:
		rc.logger.Infof("setting up H264 decoder")
		err = rc.initH264(tracks, baseURL)
	case H265:
		rc.logger.Infof("setting up H265 decoder")
		err = rc.initH265(tracks, baseURL)
	default:
		return errors.Errorf("codec not supported %v", codecInfo)
	}
	if err != nil {
		return err
	}

	_, err = rc.client.Play(nil)
	if err != nil {
		return err
	}
	clientSuccessful = true

	return nil
}

// initH264 initializes the H264 decoder and sets up the client to receive H264 packets.
func (rc *rtspCamera) initH264(tracks media.Medias, baseURL *url.URL) (err error) {
	// setup RTP/H264 -> H264 decoder
	var format *formats.H264

	track := tracks.FindFormat(&format)
	if track == nil {
		rc.logger.Warn("tracks available")
		for _, x := range tracks {
			rc.logger.Warnf("\t %v", x)
		}
		return errors.New("h264 track not found")
	}

	_, err = rc.client.Setup(track, baseURL, 0, 0)
	if err != nil {
		return err
	}

	// setup RTP/H264 -> H264 decoder
	rtpDec, err := format.CreateDecoder2()
	if err != nil {
		rc.logger.Errorf("error creating H264 decoder %v", err)
		return err
	}

	// setup H264 -> raw frames decoder
	rc.rawDecoder, err = newH264Decoder()
	if err != nil {
		return err
	}

	// if SPS and PPS are present into the SDP, send them to the decoder
	if format.SPS != nil {
		rc.rawDecoder.decode(format.SPS)
	} else {
		rc.logger.Warn("no SPS found in H264 format")
	}
	if format.PPS != nil {
		rc.rawDecoder.decode(format.PPS)
	} else {
		rc.logger.Warn("no PPS found in H264 format")
	}

	// On packet retreival, turn it into an image, and store it in shared memory
	rc.client.OnPacketRTP(track, format, func(pkt *rtp.Packet) {
		// extract access units from RTP packets
		au, _, err := rtpDec.Decode(pkt)
		if err != nil {
			if err != rtph264.ErrNonStartingPacketAndNoPrevious && err != rtph264.ErrMorePacketsNeeded {
				rc.logger.Errorf("error decoding(1) h264 rstp stream %v", err)
			}
			return
		}

		for _, nalu := range au {

			if len(nalu) < 20 {
				// TODO(ERH): this is probably wrong, but fixes a spam issue with "no frame!"
				continue
			}

			// convert NALUs into RGBA frames
			lastImage, err := rc.rawDecoder.decode(nalu)

			if err != nil {
				rc.logger.Error("error decoding(2) h264 rtsp stream  %v", err)
				return
			}

			if lastImage != nil {
				rc.latestFrame.Store(&lastImage)
			}
		}
	})

	return nil
}

// initH265 initializes the H265 decoder and sets up the client to receive H265 packets.
func (rc *rtspCamera) initH265(tracks media.Medias, baseURL *url.URL) (err error) {
	var format *formats.H265

	track := tracks.FindFormat(&format)
	if track == nil {
		rc.logger.Warn("tracks available")
		for _, x := range tracks {
			rc.logger.Warnf("\t %v", x)
		}
		return errors.New("h265 track not found")
	}

	_, err = rc.client.Setup(track, baseURL, 0, 0)
	if err != nil {
		return err
	}

	rtpDec, err := format.CreateDecoder2()
	if err != nil {
		rc.logger.Errorf("error creating H265 decoder %v", err)
		return err
	}

	rc.rawDecoder, err = newH265Decoder()
	if err != nil {
		return err
	}

	// For H.265, handle VPS, SPS, and PPS
	if format.VPS != nil {
		rc.rawDecoder.decode(format.VPS)
	} else {
		rc.logger.Warn("no VPS found in H265 format")
	}

	if format.SPS != nil {
		rc.rawDecoder.decode(format.SPS)
	} else {
		rc.logger.Warn("no SPS found in H265 format")
	}

	if format.PPS != nil {
		rc.rawDecoder.decode(format.PPS)
	} else {
		rc.logger.Warnf("no PPS found in H265 format")
	}

	// On packet retreival, turn it into an image, and store it in shared memory
	rc.client.OnPacketRTP(track, format, func(pkt *rtp.Packet) {
		// Extract access units from RTP packets
		au, _, err := rtpDec.Decode(pkt)
		if err != nil {
			if err != rtph265.ErrNonStartingPacketAndNoPrevious && err != rtph265.ErrMorePacketsNeeded {
				rc.logger.Errorf("error decoding(1) h265 rstp stream %v", err)
			}
			return
		}

		for _, nalu := range au {
			lastImage, err := rc.rawDecoder.decode(nalu)
			if err != nil {
				rc.logger.Error("error decoding(2) h265 rtsp stream  %v", err)
				return
			}

			if lastImage != nil {
				rc.latestFrame.Store(&lastImage)
			}
		}
	})

	return nil
}

func newRTSPCamera(ctx context.Context, _ resource.Dependencies, conf resource.Config, logger logging.Logger) (camera.Camera, error) {
	newConf, err := resource.NativeConfig[*Config](conf)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(newConf.Address)
	if err != nil {
		return nil, err
	}
	rtspCam := &rtspCamera{
		u:      u,
		logger: logger,
	}
	err = rtspCam.reconnectClient()
	if err != nil {
		return nil, err
	}
	cancelCtx, cancel := context.WithCancel(context.Background())
	reader := gostream.VideoReaderFunc(func(ctx context.Context) (image.Image, func(), error) {
		latest := rtspCam.latestFrame.Load()
		if latest == nil {
			return nil, func() {}, errors.New("no frame yet")
		}
		return *latest, func() {}, nil
	})
	rtspCam.VideoReader = reader
	rtspCam.cancelCtx = cancelCtx
	rtspCam.cancelFunc = cancel
	cameraModel := camera.NewPinholeModelWithBrownConradyDistortion(newConf.IntrinsicParams, newConf.DistortionParams)
	rtspCam.clientReconnectBackgroundWorker()
	src, err := camera.NewVideoSourceFromReader(ctx, rtspCam, &cameraModel, camera.ColorStream)
	if err != nil {
		return nil, err
	}

	return camera.FromVideoSource(conf.ResourceName(), src, logger), nil
}
