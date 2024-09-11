package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/digineo/go-dhclient"
	"github.com/google/gopacket/layers"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	dhcpAcquiredLeasesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "dhcp_acquired_leases_total",
			Help: "The number of times a lease was acquired, labeled by IP",
		}, []string{"ip"},
	)
	dhcpExpiredLeasesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "dhcp_expired_leases_total",
			Help: "The number of times a lease has expired, labeled by IP",
		}, []string{"ip"},
	)
	dhcpFailedLeasesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "dhcp_failed_leases_total",
			Help: "The number of times a lease failed to be acquired, labeled by IP",
		}, []string{"ip"},
	)
	dhcpLeaseExpiryTimestampSeconds = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "dhcp_lease_expiry_timestamp_seconds",
			Help: "A timestamp representing the expiry time for a lease as a unix timestamp, labeled by IP",
		}, []string{"ip"},
	)
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

	myAcquiredMetric := dhcpAcquiredLeasesTotal.WithLabelValues(targetAddr)
	myAcquiredMetric.Add(0)
	myExpiryMetric := dhcpLeaseExpiryTimestampSeconds.WithLabelValues(targetAddr)
	myExpiryMetric.Set(0)
	myFailedMetric := dhcpFailedLeasesTotal.WithLabelValues(targetAddr)
	myFailedMetric.Add(0)
	myExpiredMetric := dhcpExpiredLeasesTotal.WithLabelValues(targetAddr)
	myExpiredMetric.Add(0)

	logger := baseLogger.With("target", targetAddr)
	logger.Info("Will continually request a lease for target addr")

	client := dhclient.Client{
		Iface:  iface,
		Logger: logger,
		OnBound: func(lease *dhclient.Lease) {
			logger.Info("Got lease", "addr", lease.FixedAddress, "ttl", time.Until(lease.Expire))
			myAcquiredMetric.Inc()
			myExpiryMetric.Set(float64(lease.Expire.Unix()))
		},
		OnExpire: func(lease *dhclient.Lease) {
			if lease == nil {
				logger.Warn("Acquiring lease failed, will retry")
				myFailedMetric.Inc()
				return
			}

			logger.Info("Lease expired", "addr", lease.FixedAddress, "lease", lease)
			myExpiredMetric.Inc()
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

	metricChan := make(chan struct{})
	http.Handle("/metrics", promhttp.Handler())
	go func() {
		if err := http.ListenAndServe("127.0.0.1:1337", nil); err != nil {
			logger.Error("Unexpected error while running metrics server", "err", err)
			close(metricChan)
			return
		}
	}()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL)
	select {
	case sig := <-c:
		logger.Info("Received signal, exiting", "signal", sig)
		break
	case <-metricChan:
		logger.Error("Metric server failed, exiting")
		break
	}

	cancel()
	wg.Wait()
}
