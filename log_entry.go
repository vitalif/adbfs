package adbfs

import (
	"fmt"
	"os"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/hanwen/go-fuse/fuse"
	"github.com/zach-klippenstein/goadb/util"
	"golang.org/x/net/trace"
)

/*
LogEntry reports results, errors, and statistics for an individual operation.
Each method can only be called once, and will panic on subsequent calls.

If an error is reported, it is logged as a separate entry.

Example Usage

	func DoTheThing(path string) fuse.Status {
		logEntry := StartOperation("DoTheThing", path)
		defer FinishOperation(log) // Where log is a logrus logger.

		result, err := perform(path)
		if err != nil {
			logEntry.Error(err)
			return err
		}

		logEntry.Result(result)
		return logEntry.Status(fuse.OK)
	}
*/
type LogEntry struct {
	name      string
	path      string
	args      string
	startTime time.Time
	err       error
	result    string
	status    string

	trace trace.Trace

	cacheUsed bool
	cacheHit  bool
}

var traceEntryFormatter = new(logrus.JSONFormatter)

// StartOperation creates a new LogEntry with the current time.
// Should be immediately followed by a deferred call to FinishOperation.
func StartOperation(name string, path string) *LogEntry {
	return &LogEntry{
		name:      name,
		path:      path,
		startTime: time.Now(),
		trace:     trace.New(name, path),
	}
}

func StartFileOperation(name string, args string) *LogEntry {
	name = "File " + name
	return &LogEntry{
		name:      name,
		args:      args,
		startTime: time.Now(),
		trace:     trace.New(name, args),
	}
}

// ErrorMsg records a failure result.
// Panics if called more than once.
func (r *LogEntry) ErrorMsg(err error, msg string) {
	r.Error(fmt.Errorf("%s: %v", msg, err))
}

// Error records a failure result.
// Panics if called more than once.
func (r *LogEntry) Error(err error) {
	if r.err != nil {
		panic(fmt.Sprintf("err already set to '%s', can't set to '%s'", r.err, err))
	}
	r.err = err
}

// Result records a non-failure result.
// Panics if called more than once.
func (r *LogEntry) Result(msg string, args ...interface{}) {
	result := fmt.Sprintf(msg, args...)
	if r.result != "" {
		panic(fmt.Sprintf("result already set to '%s', can't set to '%s'", r.result, result))
	}
	r.result = result
}

// Status records the fuse.Status result of an operation.
func (r *LogEntry) Status(status fuse.Status) fuse.Status {
	if r.status != "" {
		panic(fmt.Sprintf("status already set to '%s', can't set to '%s'", r.status, status))
	}
	r.status = status.String()
	return status
}

// CacheUsed records that a cache was used to attempt to retrieve a result.
func (r *LogEntry) CacheUsed(hit bool) {
	if r.cacheUsed {
		panic(fmt.Sprintf("cache use already reported"))
	}
	r.cacheUsed = true
	r.cacheHit = hit
}

// FinishOperation should be deferred. It will log the duration of the operation, as well
// as any results and/or errors.
func (r *LogEntry) FinishOperation(log *logrus.Logger) {
	entry := log.WithFields(logrus.Fields{
		"duration_ms": calculateDurationMillis(r.startTime),
		"status":      r.status,
		"pid":         os.Getpid(),
	})

	if r.path != "" {
		entry = entry.WithField("path", r.path)
	}
	if r.args != "" {
		entry = entry.WithField("args", r.args)
	}
	if r.result != "" {
		entry = entry.WithField("result", r.result)
	}
	if r.cacheUsed {
		entry = entry.WithField("cache_hit", r.cacheHit)
	}

	entry.Debug(r.name)

	if r.err != nil {
		log.Errorln(util.ErrorWithCauseChain(r.err))
	}

	r.logTrace(entry)
}

func (r *LogEntry) logTrace(entry *logrus.Entry) {
	var msg string
	// Use a different formatter for logging to HTML trace viewer since the TextFormatter will include color escape codes.
	msgBytes, err := traceEntryFormatter.Format(entry)
	if err != nil {
		msg = fmt.Sprint(entry)
	} else {
		msg = string(msgBytes)
	}
	r.trace.LazyPrintf("%s", msg)

	if r.err != nil {
		r.trace.SetError()
		r.trace.LazyPrintf("%s", util.ErrorWithCauseChain(r.err))
	}
	r.trace.Finish()
}

func calculateDurationMillis(startTime time.Time) int64 {
	return time.Now().Sub(startTime).Nanoseconds() / time.Millisecond.Nanoseconds()
}