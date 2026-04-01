<!--markdownlint-disable-->


# Observability

OBservability is the ability to understand what a system is doing, usually with logs, metrics and traces


Logs: Event records from our system. Each log usually includes a **timestamp** a **severity level** and a **message**

Metrics: Aggregate measurements over time, like request count, error rate and latency

Traces: **per-request** execution path, often across services, that show wwhere time is spent and where failures happen


Alerts: notification sent when an important signal crosses a threshold


## Logs we should add to our system

1) Lifecycle Logs: Start up and shut down


# Go log package


We prefer to use log over fmt.print because it **allows us to change where logs go to**, **has built-in timestamp functionality and other metadata mangement built in** and **fatal errors can automatically exit the program**


## Logger

Logger is an instance of log.Logger. Generally its better to use the logger object than the log package functions directly, because


1) we can easily change where the logs go all in one place
2) We can add prefxies to the logs, again all in one place
3) we can change where the logs go at run time, also all in one place


**We want to send errs to os.stderr instead of os.stdout because stdout is typicallyu used for the main output of the program


```Go

var logger = log.New(os.Stderr, "Message: ", log.LstdFlags)
// first argument is output stream
// second is the prefix
// third is log flags, which can include things like timestamps, file names and line numbers
```


## Logging Requests

A very common thing is to log requests of a web service

Cleaner ways to implement this is a middleware function that logs the request after it has been served


```Go

func requestLogger(logger *log.Logger) func(http.Handler) http.Handler{ // takes the next handler aand returns a new one
    return func(next.http.Handler) http.Handler{
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request){
            next.ServeHttp(w, r)
            logger.Printf("some shit")
        })
    }
}

```

then we can use it in our endpoint as such

```Go

mux.Handle("/api/shorten", requestLogger(logger)(http.HandlerFunc(apiCfg.handleshortenurl)))
```


# Global Logger vs Dependency Injection

Globals are generally a bad idea because testing and debugging is a pain in the ass. We want to use dependency injection


DI means: pass a function or method's dependencies as arguments. This makes testing much easier because we dont need to continuously mutate shared state


# Logger Configuration

It is much more standard to decide what goes to stderr and what goes to a file based on the environment our app is running in


logger can write to both stderr and a file at the same time


**io.MultiWriter** takes multiple io.Writer objects and writes to all of them

```Go

file, err := os.OpenFile(logFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)

if err != nil{
    log.Fatalf("failed to open log file: %v", err)

}


multiWriter := io.MultiWriter(os.Stderr, file)
logger := log.New(multiWriter, "INFO: ", log.LstdFlags)
```



# Buffered Logging

Because we are writing to disk everytime, it can really slow down the application. THe better solution is to use bufio.Writer around the file. This allows uus to write log messages to a in-memory buffer, and only when that buffer is full does it write to disk



We must remember to flush (write to disk) before program exits or any pending messages will be lost



# Structured Logging:

Structured Logging is typically done in go with log/slog standard library package


```go
slog.Error("login failed",
	"user_id", 9284,
	"timestamp", "2024-10-01T12:34:56Z",
	"ip_address", "102.32.21.192")

```


log/slog package introduces **handlers** which arbitrailiy accept key value pair rs and format them into a record log


Two built in handlers are:

slog.NewTextHandler: Formats as text
slog.NewJSONHandler: Formats as JSON

Then we create our logger with 

```Go```

logger := slog.New(slog.NewTexthandler(os.Stderr, nil))
logger.INfo("This is a info message")



Attr is a key value pair that represents a single piece of structured log data


# Log Levels

Log levels arent specific to structured logging, but log/slog provides a build in way to handle them


Standard library defines 4 levels by default, we can define new levels if needed


1) slog.LevelError: Error messages
2) slog.LevelWarn: Warning messages
3) slog.LevelInfo: informational messages
4) slog.LevelDebug: Debug messages: useful for development and debugging



## Filtering


We can also filter out logs by using different handlers with different minimum levels
```Go

// logs DEBUG and above
debugLogger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
	Level: slog.LevelDebug,
}))

// logs ERROR and above
errorLogger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
	Level: slog.LevelError,
}))
```


A better approach is to have a multihandler that routes logs to different destinations by level

```Go


debugHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
	Level: slog.LevelDebug,
})

logFile, err := os.OpenFile("linko.access.log", os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
if err != nil {
	return err
}
defer logFile.Close()
infoHandler := slog.NewTextHandler(logFile, &slog.HandlerOptions{
	Level: slog.LevelInfo,
})

logger := slog.New(slog.NewMultiHandler(
	debugHandler,
	infoHandler,
))



```


## Log Levels


Debug: Lowest Level, detailed information needed for debugging but not necessary for normal operation

Info: Used to record important events that are **NOT** errors

Warn: Gray area between info and error: Lke a "i think you shouldnt be doing this"

Errors: Errors, duh
They should include enough information to diagnose the problem, things like error messages, stack trace and context


# Good Logging

Good logs provide enough information to diagnose where something went wrong

5 Strategies

1) Log each thing only once
2) One Log per event
3) Think about who log is for
4) Provide Context
5) Privacy- aware, not all relevant details can be logged 


# Stack Traces
Go's standard logging libraries don't give us stack traces out of the box, but several third party packages do.


A popular one is github.com/pkg/errors

# Slog Groups

Slog Groups allow us to organize our stack trace messages


slog.Group() function creates group attribute for use in log calls

```Go

logger.Info("user logged in", 
    slog.Group("user",
          slog.String("name", "frodo")
        )

    )

```


This produces nested output in JSON

```Go

{
  "level": "INFO",
  "msg": "user logged in",
  "user": { "name": "frodo", "role": "ringbearer" }
}

```


and dotted keys in text format:

```Go  


level=INFO msg="user logged in" user.name=frodo user.role=ringbearer
```


# Handle Errors Once


We want to avoid the "log and rethrow" pattern that occurs when a sub function throws an error but returns it to its caller and then its caller alos logs the same error


fmt.Errorf() has a %w verb that lets us wrap an error with additional context in a way that can be unwrapped later


A better approach is to instead **Handle the error only once in parent function, while still adding information to the error in the child function using fmt.Errorf**


```Go

func order(purchase Purchase) {
	if err := validatePurchase(purchase); err != nil {
		slog.Error("Failed to validate purchase", "error", err)
		return
	}
	// happy path...
}

func validatePurchase(purchase Purchase) error {
	for i, item := range purchase.Items {
		if err := validateItem(item); err != nil {
			return fmt.Errorf("failed to validate item %d (%v): %w", i, item, err)
		}
	}
	return nil
}
```
