package main

import (
	"boot.dev/linko/internal/build"
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pkg/errors"

	"boot.dev/linko/internal/linkoerr"
	"boot.dev/linko/internal/store"
)

type stackTracer interface { //type for error with a stacktrace
	error
	StackTrace() errors.StackTrace
}

type multiError interface {
	error            // must have an error
	Unwrap() []error // must implement unwrap
}

func main() {

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	httpPort := flag.Int("port", 8899, "port to listen on")
	dataDir := flag.String("data", "./data", "directory to store data")
	flag.Parse()

	status := run(ctx, cancel, *httpPort, *dataDir)
	cancel()
	os.Exit(status)
}

type closeFunc func() error

func Close(f *bufio.Writer) error {
	return f.Flush() // forces any data stored to be written to destination

}

func replaceAttr(groups []string, a slog.Attr) slog.Attr {
	if a.Value.Kind() != slog.KindAny { //slog sotres errors as kindany, we skip anything that isnt kindany
		return a
	}
	err, ok := a.Value.Any().(error)
	if !ok {
		return a
	}

	// check if this error has a stack trace attached to it
	// ie if it was wrapped with withStack()
	// stackErr gives us access to .stackTrace()
	if stackErr, ok := errors.AsType[multiError](err); ok {
		errs := stackErr.Unwrap()
		if len(errs) > 1 {
			//group each error under numbered keys inside a top level errors group
			var errAttrs []slog.Attr
			for i, e := range errs {
				key := fmt.Sprintf("%d", i)
				errAttrs = append(errAttrs, slog.Attr{
					Key: key,
					Value: slog.GroupValue(
						slog.String("message", e.Error()),
					),
				})
			}
		}

		attrs := []slog.Attr{
			{Key: "message", Value: slog.StringValue(stackErr.Error())},
			{Key: "stack_trace", Value: slog.StringValue(fmt.Sprintf("%+v", stackErr.Error()))},
		}

		attrs = append(attrs, linkoerr.Attrs(err)...)

		return slog.Attr{
			Key:   a.Key,
			Value: slog.GroupValue(attrs...),
		}
	}
	return slog.Attr{
		Key:   a.Key,
		Value: slog.StringValue(err.Error()),
	}
}

func initializeLogger() (*slog.Logger, closeFunc, error) {
	logFile := os.Getenv("LINKO_LOG_FILE")

	if logFile == "" {
		return slog.New(slog.NewTextHandler(os.Stderr, nil)), func() error { return nil }, nil
	}

	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to open log file: %w", err)
	}
	bufferedF := bufio.NewWriterSize(f, 1024)

	debugHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level:       slog.LevelDebug, //debug and above
		ReplaceAttr: replaceAttr,
	})

	infoHandler := slog.NewTextHandler(bufferedF, &slog.HandlerOptions{
		Level:       slog.LevelInfo,
		ReplaceAttr: replaceAttr,
	})
	return slog.New(slog.NewMultiHandler(debugHandler, infoHandler)).With(
		slog.String("git_sha", build.GitSHA),
		slog.String("build_time", build.BuildTime),
		slog.String("env", os.Getenv("ENV")),
		slog.String("hostname", os.Hostname()),
	), func() error { return bufferedF.Flush() }, nil

}

func run(ctx context.Context, cancel context.CancelFunc, httpPort int, dataDir string) int {
	logger, closeLogger, err := initializeLogger()
	if err != nil {
		log.Printf("Failed to initialize logger: %v\n", err)
	}

	st, err := store.New(dataDir, logger)
	if err != nil {
		logger.Error("failed to create store: %v\n", err)
		return 1
	}
	s := newServer(*st, httpPort, cancel, logger)
	var serverErr error
	go func() {
		serverErr = s.start()
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer func() {
		logger.Debug("Server is shutting down") // we log first, then flush if not it might not be recorded
		closeLogger()
	}()
	defer cancel()

	if err := s.shutdown(shutdownCtx); err != nil {
		logger.Info("failed to shutdown server: %v\n", err)
		return 1
	}
	if serverErr != nil {
		logger.Error("server error: %v\n", serverErr)
		return 1
	}
	return 0
}
