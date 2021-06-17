package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/sts"
)

type JSONError struct {
	Error string `json:"error"`
}

func errorHandler(w io.Writer, err error) {
	je := &JSONError{}
	if aerr, ok := err.(awserr.Error); ok {
		switch aerr.Code() {
		default:
			je.Error = aerr.Error()
		}
	} else {
		je.Error = err.Error()
	}
	json.NewEncoder(w).Encode(je)
}

type HTTPRequestInfo struct {
	Method    string  `json:"method"`
	URL       string  `json:"url"`
	Referer   string  `json:"referer"`
	UserAgent string  `json:"userAgent"`
	Status    int     `json:"status"`
	Duration  float64 `json:"duration"`
}

type responseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func wrapResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{ResponseWriter: w}
}

func (rw *responseWriter) Status() int {
	return rw.status
}

func (rw *responseWriter) WriteHeader(code int) {
	if rw.wroteHeader {
		return
	}

	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
	rw.wroteHeader = true
}

func loggingMiddleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				rw.WriteHeader(http.StatusInternalServerError)
				log.Printf("{\"err\"}")
			}
		}()

		start := time.Now()
		info := HTTPRequestInfo{
			Method:    r.Method,
			URL:       r.URL.String(),
			Referer:   r.Referer(),
			UserAgent: r.UserAgent(),
		}
		wrapped := wrapResponseWriter(rw)
		h.ServeHTTP(wrapped, r)
		info.Duration = float64(time.Since(start)) / float64(time.Second)
		b, err := json.Marshal(info)
		if err != nil {
			rw.WriteHeader(http.StatusInternalServerError)
			return
		}
		log.Print(string(b))
	})
}

func main() {
	loggingLevelEnv := os.Getenv("AWS_LOG_LEVEL")
	loggingLevel := aws.LogOff
	if loggingLevelEnv != "" {
		l, _ := strconv.Atoi(loggingLevelEnv)
		loggingLevel = aws.LogLevelType(l)
	}
	mux := http.NewServeMux()

	mux.Handle("/", http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Write([]byte("WORKING"))
	}))

	mux.Handle("/identity", http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		s, err := session.NewSession(
			&aws.Config{
				MaxRetries:                    aws.Int(1),
				CredentialsChainVerboseErrors: aws.Bool(true),
				LogLevel:                      aws.LogLevel(loggingLevel),
				Logger:                        aws.NewDefaultLogger(),
			})
		if err != nil {
			errorHandler(rw, err)
			return
		}

		svc := sts.New(s)
		input := &sts.GetCallerIdentityInput{}

		result, err := svc.GetCallerIdentity(input)
		if err != nil {
			errorHandler(rw, err)
			return
		}

		json.NewEncoder(rw).Encode(result)
	}))

	srv := http.Server{
		Addr:    ":8888",
		Handler: loggingMiddleware(mux),
	}

	idleConnsClosed := make(chan struct{})
	go func() {
		sigint := make(chan os.Signal, 1)
		signal.Notify(sigint, os.Interrupt)
		<-sigint

		// We received an interrupt signal, shut down.
		if err := srv.Shutdown(context.Background()); err != nil {
			// Error from closing listeners, or context timeout:
			log.Printf("HTTP server Shutdown: %v", err)
		}
		close(idleConnsClosed)
	}()

	log.Print("Running server...")
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		// Error starting or closing listener:
		log.Fatalf("HTTP server ListenAndServe: %v", err)
	}

	<-idleConnsClosed
}
