package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/digineo/go-dhclient"
	"github.com/google/gopacket/layers"
)

const (
	TargetAddr = "192.168.0.223"
)

func getInterface() (*net.Interface, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	for _, iface := range interfaces {
		if (iface.Flags&net.FlagUp) == 0 || (iface.Flags&net.FlagLoopback) != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			return nil, fmt.Errorf("unable to get addrs for iface %s: %w", iface.Name, err)
		}

		if len(addrs) == 0 {
			continue
		}

		return &iface, nil
	}

	return nil, errors.New("unable to find interface")
}

func getLogger() *slog.Logger {
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(handler)
}

func main() {
	logger := getLogger()

	iface, err := getInterface()
	if err != nil {
		logger.Error("Unable to get interface to bind to", "err", err)
		os.Exit(1)
	}

	logger.Info("Using interface", "iface", iface.Name)

	client := dhclient.Client{
		Iface:  iface,
		Logger: logger,
		OnBound: func(lease *dhclient.Lease) {
			logger.Info("Got lease", "addr", lease.FixedAddress, "ttl", time.Until(lease.Expire))
		},
		OnExpire: func(lease *dhclient.Lease) {
			logger.Info("Lease expired, re-requesting")
		},
	}

	for _, param := range dhclient.DefaultParamsRequestList {
		logger.Info("Requesting default option", "param", param)
		client.AddParamRequest(layers.DHCPOpt(param))
	}

	logger.Info("Adding option to request ip", "ip", TargetAddr)
	client.AddOption(
		layers.DHCPOptRequestIP, net.ParseIP(TargetAddr).To4(),
	)

	logger.Info("Starting dhcp client")
	client.Start()
	defer client.Stop()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL)
	for {
		sig := <-c
		logger.Info("Received signal, exiting", "signal", sig)
		return
	}
}
