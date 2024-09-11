package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/digineo/go-dhclient"
	"github.com/google/gopacket/layers"
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

func runClient(ctx context.Context, wg *sync.WaitGroup, baseLogger *slog.Logger, iface *net.Interface, targetAddr string) {
	defer wg.Done()

	logger := baseLogger.With("target", targetAddr)
	logger.Info("Will continually request a lease for target addr")

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
		logger.Debug("Adding default option", "param", param)
		client.AddParamRequest(layers.DHCPOpt(param))
	}

	logger.Debug("Adding option to request target address")
	client.AddOption(
		layers.DHCPOptRequestIP, net.ParseIP(targetAddr).To4(),
	)

	logger.Info("Starting dhcp client")
	client.Start()

	defer func() {
		logger.Info("Stopping dhcp client")
		client.Stop()
	}()

	<-ctx.Done()
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

	targetAddrsStr := os.Getenv("TARGET_ADDRS")
	if targetAddrsStr == "" {
		logger.Error("TARGET_ADDRS is not set")
		os.Exit(1)
	}

	logger.Debug("Pulled list of target addresses", "targets", targetAddrsStr)

	wg := &sync.WaitGroup{}
	ctx, cancel := context.WithCancel(context.Background())

	targetAddrs := strings.Split(targetAddrsStr, ",")
	for _, targetAddr := range targetAddrs {
		if targetAddr == "" {
			logger.Error("Got empty target address")
			os.Exit(1)
		}

		logger.Debug("Starting client for target address", "target", targetAddr)

		wg.Add(1)
		go runClient(ctx, wg, logger, iface, targetAddr)
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL)
	for {
		sig := <-c
		logger.Info("Received signal, exiting", "signal", sig)
		break
	}

	cancel()
	wg.Wait()
}
