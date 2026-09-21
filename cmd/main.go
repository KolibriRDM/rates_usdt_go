package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	ratev1 "example.com/rapira_rates/gen/rate/v1"
	"example.com/rapira_rates/internal/client/rapira"
	"example.com/rapira_rates/internal/config"
	"example.com/rapira_rates/internal/repository"
	"example.com/rapira_rates/internal/service"
	"example.com/rapira_rates/internal/storage"
	"example.com/rapira_rates/internal/telemetry"
	grpcserver "example.com/rapira_rates/internal/transport/grpc"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Println("rapira_rates: --database-url, --grpc-address, --startup-timeout, --shutdown-timeout (see README.md)")
			return
		}
		log.Fatal(err)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{}))

	ctxDB, cancelDB := context.WithTimeout(context.Background(), cfg.StartupTimeout)
	pool, err := storage.NewPostgres(ctxDB, cfg.DatabaseURL)
	cancelDB()
	if err != nil {
		logger.Error("Не удалось подключиться к БД", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	logger.Info("Подключение к БД установлено")

	ctxTelemetry, cancelTelemetry := context.WithTimeout(context.Background(), cfg.StartupTimeout)

	tracerProvider, err := telemetry.NewTracerProvider(ctxTelemetry)

	cancelTelemetry()
	if err != nil {
		logger.Error("Не удалось настроить трассировку", "error", err)
		return
	}
	defer func() {
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancelShutdown()
		err = tracerProvider.Shutdown(shutdownCtx)
		if err != nil {
			logger.Error("Не удалось завершить трассировку", "error", err)
		}
	}()

	otel.SetTracerProvider(tracerProvider)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	repo := repository.NewRateRepository(pool)
	rapiraClient := rapira.NewClient("https://api.rapira.net", 5*time.Second)
	rateService := service.NewRateService(repo, rapiraClient, "USDT/RUB")
	rateHandler := grpcserver.NewRateHandler(rateService, logger)

	server := grpc.NewServer(grpc.StatsHandler(otelgrpc.NewServerHandler()))
	ratev1.RegisterRateServiceServer(server, rateHandler)
	healthServer := health.NewServer()
	healthv1.RegisterHealthServer(server, healthServer)

	listener, err := net.Listen("tcp", cfg.GRPCAddress)
	if err != nil {
		logger.Error("Ошибка работы gRPC-сервера", "error", err)
		stop()
		return
	}
	defer func() {
		if closeErr := listener.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			logger.Error("Не удалось закрыть listener", "error", closeErr)
		}
	}()
	reflection.Register(server)

	go func() {
		serveErr := server.Serve(listener)
		if serveErr != nil && !errors.Is(serveErr, grpc.ErrServerStopped) {
			logger.Error("Ошибка работы gRPC-сервера", "error", serveErr)
			stop()
		}
	}()
	logger.Info("gRPC-сервер запущен", "address", listener.Addr().String())

	<-ctx.Done()

	healthServer.Shutdown()
	grpcStopTimer := time.AfterFunc(cfg.ShutdownTimeout, server.Stop)
	server.GracefulStop()
	grpcStopTimer.Stop()
	logger.Info("Сервис остановлен")
}
