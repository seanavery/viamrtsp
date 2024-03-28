package viamrtsp

import (
	"context"

	"go.viam.com/rdk/components/camera"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/robot/client"
)

//lint:ignore U1000 This is a script
func main() {
	logger := logging.NewDebugLogger("client")
	robot, err := client.New(
		context.Background(),
		"localhost:8080",
		logger,
	)
	if err != nil {
		logger.Fatal(err)
	}

	defer robot.Close(context.Background())

	ipCam, err := camera.FromRobot(robot, "ip-cam")
	if err != nil {
		logger.Fatal(err)
	}
	stream, err := ipCam.Stream(context.Background())
	if err != nil {
		logger.Fatal(err)
	}
	_, _, err = stream.Next(context.Background())
	if err != nil {
		logger.Fatal(err)
	}
}
