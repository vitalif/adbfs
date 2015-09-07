/*
Another FUSE filesystem that can mount any device visible to your adb server.
Uses github.com/zach-klippenstein/goadb to interface with the server directly
instead of calling out to the adb client program.

See package fs for the filesystem implementation.
*/
package main

import (
	"errors"
	"flag"
	"fmt"
	stdlog "log"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/hanwen/go-fuse/fuse"
	"github.com/hanwen/go-fuse/fuse/nodefs"
	"github.com/hanwen/go-fuse/fuse/pathfs"
	"github.com/zach-klippenstein/adbfs/fs"
	"github.com/zach-klippenstein/goadb"
)

var (
	deviceSerial = flag.String("device", "", "Device serial number to mount.")
	mountpoint   = flag.String("mountpoint", "", "Directory to mount the device on.")
	adbPort      = flag.Int("port", goadb.AdbPort, "Port to connect to adb server on.")
	logLevel     = flag.String("loglevel", "info", "Detail of logs to show.")
	cacheTtl     = flag.Duration("cachettl", 300*time.Millisecond, "Duration to keep cached file info.")
)

var (
	server *fuse.Server
	log    *logrus.Logger

	// Prevents trying to unmount the server multiple times.
	unmounted fs.AtomicBool
)

const StartTimeout = 5 * time.Second

func main() {
	flag.Parse()
	initializeLogger()

	if *mountpoint == "" {
		log.Fatalln("Mountpoint must be specified. Run with -h.")
	}
	absoluteMountpoint, err := filepath.Abs(*mountpoint)
	if err != nil {
		log.Fatal(err)
	}
	if err = checkValidMountpoint(absoluteMountpoint); err != nil {
		log.Fatal(err)
	}

	cache := initializeCache(*cacheTtl)

	clientConfig := goadb.ClientConfig{
		Dialer: goadb.NewDialer("", *adbPort),
	}

	fs := initializeFileSystem(clientConfig, absoluteMountpoint, cache, handleDeviceDisconnected)

	server, _, err = nodefs.MountRoot(absoluteMountpoint, fs.Root(), nil)
	if err != nil {
		log.Fatal(err)
	}

	serverDone, err := startServer(StartTimeout)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("mounted %s on %s", *deviceSerial, absoluteMountpoint)
	defer unmountServer()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, os.Kill)

	for {
		select {
		case signal := <-signals:
			log.Println("got signal", signal)
			switch signal {
			case os.Kill, os.Interrupt:
				log.Println("exiting...")
				return
			}

		case <-serverDone:
			log.Debugln("server done channel closed.")
			return
		}
	}
}

func initializeLogger() {
	log = logrus.StandardLogger()

	logLevel, err := logrus.ParseLevel(*logLevel)
	if err != nil {
		log.Fatal(err)
	}
	log.Level = logLevel

	log.Formatter = &logrus.TextFormatter{
		FullTimestamp: true,
		// RFC 3339 with milliseconds.
		TimestampFormat: "2006-01-02T15:04:05.000000000Z07:00",
	}

	// Redirect standard logger (used by fuse) to our logger.
	stdlog.SetOutput(log.Writer())
	// Disable standard log timestamps, logrus has its own.
	stdlog.SetFlags(0)

	return
}

func initializeCache(ttl time.Duration) fs.DirEntryCache {
	log.Infoln("stat cache ttl:", ttl)
	return fs.NewDirEntryCache(ttl)
}

func initializeFileSystem(clientConfig goadb.ClientConfig, mountpoint string, cache fs.DirEntryCache, deviceNotFoundHandler func()) *pathfs.PathNodeFs {
	clientFactory := fs.NewCachingDeviceClientFactory(cache,
		fs.NewGoadbDeviceClientFactory(clientConfig, *deviceSerial))

	var fsImpl pathfs.FileSystem
	fsImpl, err := fs.NewAdbFileSystem(fs.Config{
		Mountpoint:    mountpoint,
		ClientFactory: clientFactory,
		Log:           log,
		DeviceNotFoundHandler: deviceNotFoundHandler,
	})
	if err != nil {
		log.Fatal(err)
	}

	return pathfs.NewPathNodeFs(fsImpl, nil)
}

func startServer(startTimeout time.Duration) (<-chan struct{}, error) {
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		server.Serve()
		log.Println("server finished.")
		return
	}()

	// Wait for OS to finish initializing the mount.
	// If server.Serve() fails (e.g. mountpoint doesn't exist), WaitMount() won't
	// ever return. Running it in a separate goroutine allows us to detect that case.
	serverReady := make(chan struct{})
	go func() {
		defer close(serverReady)
		server.WaitMount()
	}()

	select {
	case <-serverReady:
		log.Println("server ready.")
		return serverDone, nil
	case <-serverDone:
		return nil, errors.New("unknown error")
	case <-time.After(startTimeout):
		return nil, errors.New(fmt.Sprint("server failed to start after", startTimeout))
	}
}

func unmountServer() {
	if server == nil {
		panic("attempted to unmount server before creating it")
	}

	if unmounted.CompareAndSwap(false, true) {
		log.Println("unmounting...")
		server.Unmount()
		log.Println("unmounted.")
	}
}

func handleDeviceDisconnected() {
	// Server guaranteed to be non-nil by now, since we can't detect
	// device disconnection without a filesystem op, which can't happen
	// until the server has started.
	go func() {
		if !unmounted.Value() {
			log.Infoln("device disconnected, unmounting...")
			unmountServer()
		}
	}()
}

func checkValidMountpoint(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}

	if !info.IsDir() {
		return errors.New(fmt.Sprint("path is not a directory:", path))
	}

	return nil
}
