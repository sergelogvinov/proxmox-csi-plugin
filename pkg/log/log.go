/*
Copyright 2023 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package logger is cli logger configurator.
package logger

import (
	"os"

	log "github.com/sirupsen/logrus"
)

const (
	// LevelTrace is the log level for tracing
	LevelTrace = "trace"
	// LevelDebug is the log level for debugging
	LevelDebug = "debug"
	// LevelInfo is the log level for informational messages
	LevelInfo = "info"
	// LevelWarn is the log level for warnings
	LevelWarn = "warn"
	// LevelError is the log level for errors
	LevelError = "error"
	// LevelFatal is the log level for fatal errors
	LevelFatal = "fatal"
	// LevelPanic is the log level for panics
	LevelPanic = "panic"
)

// Levels is a slice of all log levels
var Levels = []string{
	LevelTrace, LevelDebug, LevelInfo, LevelWarn,
	LevelError, LevelFatal, LevelPanic,
}

// Configure configures the logger.
func Configure(entry *log.Entry, level string) {
	logger := entry.Logger
	logger.SetOutput(os.Stdout)

	logger.SetFormatter(&log.TextFormatter{
		// DisableColors: true,
		// FullTimestamp: true,
		DisableTimestamp:       true,
		DisableLevelTruncation: true,
	})
	logger.SetLevel(logLevel(level))
}

func logLevel(level string) log.Level {
	switch level {
	case LevelTrace:
		return log.TraceLevel
	case LevelDebug:
		return log.DebugLevel
	case LevelInfo:
		return log.InfoLevel
	case LevelWarn:
		return log.WarnLevel
	case LevelError:
		return log.ErrorLevel
	case LevelFatal:
		return log.FatalLevel
	case LevelPanic:
		return log.PanicLevel
	}

	return log.DebugLevel
}
