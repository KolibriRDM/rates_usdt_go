package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	reflectionv1 "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/status"
)

func TestMainProcess(t *testing.T) {
	if os.Getenv("RAPIRA_TEST_MAIN_PROCESS") != "1" {
		return
	}
	os.Args = []string{"rapira_rates"}
	main()
}

type mainProcess struct {
	cmd     *exec.Cmd
	done    chan struct{}
	err     error
	logPath string
	conn    *grpc.ClientConn
	exports atomic.Int64
}

func startMainProcess(t *testing.T, dsn string, shutdown time.Duration) *mainProcess {
	t.Helper()
	process := &mainProcess{done: make(chan struct{}), logPath: filepath.Join(t.TempDir(), "app.log")}
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/traces" {
			t.Errorf("unexpected exporter request: %s %s", r.Method, r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read exported traces: %v", err)
		} else if len(body) > 0 {
			process.exports.Add(1)
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(collector.Close)
	logFile, err := os.Create(process.logPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logFile.Close() })
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	process.cmd = exec.Command(binary, "-test.run=^TestMainProcess$")
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "OTEL_") || key == "DATABASE_URL" || key == "GRPC_ADDRESS" || key == "STARTUP_TIMEOUT" || key == "SHUTDOWN_TIMEOUT" || key == "RAPIRA_TEST_MAIN_PROCESS" {
			continue
		}
		process.cmd.Env = append(process.cmd.Env, entry)
	}
	process.cmd.Env = append(process.cmd.Env,
		"RAPIRA_TEST_MAIN_PROCESS=1", "DATABASE_URL="+dsn,
		"GRPC_ADDRESS=127.0.0.1:0", "STARTUP_TIMEOUT=5s", "SHUTDOWN_TIMEOUT="+shutdown.String(),
		"OTEL_EXPORTER_OTLP_ENDPOINT="+collector.URL, "OTEL_BSP_SCHEDULE_DELAY=60000",
	)
	process.cmd.Stdout = logFile
	process.cmd.Stderr = logFile
	if err := process.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		process.err = process.cmd.Wait()
		close(process.done)
	}()
	t.Cleanup(func() {
		select {
		case <-process.done:
		default:
			_ = process.cmd.Process.Kill()
			select {
			case <-process.done:
			case <-time.After(5 * time.Second):
				t.Error("child process did not exit after kill")
			}
		}
		if t.Failed() {
			logs, _ := os.ReadFile(process.logPath)
			t.Logf("app output:\n%s", logs)
		}
	})
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-process.done:
			t.Fatalf("app exited during startup: %v", process.err)
		case <-deadline.C:
			t.Fatal("app startup timed out")
		case <-ticker.C:
			logs, err := os.ReadFile(process.logPath)
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range strings.Split(string(logs), "\n") {
				var record struct {
					Message string `json:"msg"`
					Address string `json:"address"`
				}
				if json.Unmarshal([]byte(line), &record) != nil || record.Message != "gRPC-сервер запущен" {
					continue
				}
				process.conn, err = grpc.NewClient(record.Address, grpc.WithTransportCredentials(insecure.NewCredentials()))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = process.conn.Close() })
				return process
			}
		}
	}
}

func (p *mainProcess) signal(t *testing.T, signal os.Signal) {
	t.Helper()
	if err := p.cmd.Process.Signal(signal); err != nil {
		t.Fatal(err)
	}
}

func (p *mainProcess) wait(t *testing.T) {
	t.Helper()
	select {
	case <-p.done:
		if p.err != nil {
			t.Fatalf("app exited with an error: %v", p.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("app did not finish shutdown")
	}
	if p.exports.Load() == 0 {
		t.Fatal("shutdown did not flush buffered traces")
	}
}

func TestMainShutdownIntegration(t *testing.T) {
	dsn := os.Getenv("RAPIRA_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set RAPIRA_TEST_DATABASE_URL to run process integration tests")
	}
	for _, signal := range []os.Signal{syscall.SIGTERM, os.Interrupt} {
		t.Run("idle "+signal.String(), func(t *testing.T) {
			process := startMainProcess(t, dsn, 2*time.Second)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			response, err := healthv1.NewHealthClient(process.conn).Check(ctx, &healthv1.HealthCheckRequest{})
			if err != nil {
				t.Fatal(err)
			}
			if response.Status != healthv1.HealthCheckResponse_SERVING {
				t.Fatalf("unexpected health status: %v", response.Status)
			}
			process.signal(t, signal)
			process.wait(t)
		})
	}
	for _, force := range []bool{false, true} {
		name := "waits for active RPC"
		shutdown := 3 * time.Second
		if force {
			name = "forces stop after timeout"
			shutdown = 700 * time.Millisecond
		}
		t.Run(name, func(t *testing.T) {
			process := startMainProcess(t, dsn, shutdown)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			stream, err := reflectionv1.NewServerReflectionClient(process.conn).ServerReflectionInfo(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := stream.Send(&reflectionv1.ServerReflectionRequest{
				MessageRequest: &reflectionv1.ServerReflectionRequest_ListServices{ListServices: ""},
			}); err != nil {
				t.Fatal(err)
			}
			response, err := stream.Recv()
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, service := range response.GetListServicesResponse().GetService() {
				if service.Name == "rate.v1.RateService" {
					found = true
				}
			}
			if !found {
				t.Fatal("RateService is not registered")
			}
			healthCtx, cancelHealth := context.WithCancel(ctx)
			defer cancelHealth()
			watch, err := healthv1.NewHealthClient(process.conn).Watch(healthCtx, &healthv1.HealthCheckRequest{})
			if err != nil {
				t.Fatal(err)
			}
			initial, err := watch.Recv()
			if err != nil || initial.GetStatus() != healthv1.HealthCheckResponse_SERVING {
				t.Fatalf("initial health response: %v, error: %v", initial, err)
			}
			process.signal(t, syscall.SIGTERM)
			stopping, err := watch.Recv()
			if err != nil || stopping.GetStatus() != healthv1.HealthCheckResponse_NOT_SERVING {
				t.Fatalf("shutdown health response: %v, error: %v", stopping, err)
			}
			cancelHealth()
			received := make(chan error, 1)
			go func() {
				_, err := stream.Recv()
				received <- err
			}()
			select {
			case err := <-received:
				t.Fatalf("active RPC stopped before draining or timeout: %v", err)
			case <-time.After(100 * time.Millisecond):
			}
			if !force {
				if err := stream.CloseSend(); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-received:
				if force {
					if status.Code(err) != codes.Unavailable && status.Code(err) != codes.Canceled {
						t.Fatalf("expected interrupted RPC, got %v", err)
					}
				} else if !errors.Is(err, io.EOF) {
					t.Fatalf("RPC did not complete gracefully: %v", err)
				}
			case <-time.After(4 * time.Second):
				t.Fatal("active RPC did not finish")
			}
			process.wait(t)
		})
	}
}
