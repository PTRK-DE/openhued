package main

import (
	"flag"
	"fmt"
	"net"
	"github.com/PTRK-DE/openhued/deamon"
	"github.com/PTRK-DE/openhued/deamon/config"
	"os"
)

func sendCommand(socketPath, cmd string) error {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return fmt.Errorf("connect daemon: %w", err)
	}
	defer conn.Close()

	if _, err := fmt.Fprintf(conn, "%s\n", cmd); err != nil {
		return fmt.Errorf("write command: %w", err)
	}

	buf := make([]byte, 4096)
	n, _ := conn.Read(buf)
	fmt.Print(string(buf[:n]))
	return nil
}

func usage() {
	fmt.Fprintf(os.Stderr, `Usage:
  openhued serve [-config path] [-socket path]
  openhued toggle
  openhued up
  openhued down

Commands:
  serve     Run as daemon (background service)
  toggle    Toggle light on/off via daemon
  up        Increase brightness
  down      Decrease brightness
  status    Get Brightness in Percent
`)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}

	switch os.Args[1] {
	case "serve":
		serveCmd := flag.NewFlagSet("serve", flag.ExitOnError)
		configPath := serveCmd.String("config", "", "Path to config file")
		socketPath := serveCmd.String("socket", deamon.DefaultSocketPath(), "Unix socket path")
		if err := serveCmd.Parse(os.Args[2:]); err != nil {
			os.Exit(2)
		}

		cfg, err := config.Load(*configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "load config: %v\n", err)
			os.Exit(1)
		}

		d, err := deamon.NewDaemon(cfg, *socketPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "start daemon: %v\n", err)
			os.Exit(1)
		}

		if err := d.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "daemon error: %v\n", err)
			os.Exit(1)
		}

	case "toggle", "up", "down", "status":
		socketPath := deamon.DefaultSocketPath()
		if err := sendCommand(socketPath, os.Args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "openhued: %v\n", err)
			os.Exit(1)
		}

	default:
		usage()
	}
}
