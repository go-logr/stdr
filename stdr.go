/*
Copyright 2019 The logr Authors.

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

// Package strd implements github.com/go-logr/logr.Logger in terms of
// Go's standard log package.
package stdr

import (
	"bytes"
	"fmt"
	"log"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"

	"github.com/go-logr/logr"
)

// The global verbosity level.  See SetVerbosity().
var globalVerbosity int = 0

// SetVerbosity sets the global level against which all info logs will be
// compared.  If this is greater than or equal to the "V" of the logger, the
// message will be logged.  A higher value here means more logs will be written.
// The previous verbosity value is returned.  This is not concurrent-safe -
// callers must be sure to call it from only one goroutine.
func SetVerbosity(v int) int {
	old := globalVerbosity
	globalVerbosity = v
	return old
}

// New returns a logr.Logger which is implemented by Go's standard log package,
// or something like it.  If std is nil, this will call functions in the log
// package instead.
//
// Example: stdr.New(log.New(os.Stderr, "", log.LstdFlags)))
func New(std StdLogger) logr.Logger {
	return NewWithOptions(std, Options{})
}

// NewWithOptions returns a logr.Logger which is implemented by Go's standard
// log package, or something like it.  See New for details.
func NewWithOptions(std StdLogger, opts Options) logr.Logger {
	if opts.Depth < 0 {
		opts.Depth = 0
	}

	sl := &logger{
		std:       std,
		prefix:    "",
		values:    nil,
		depth:     opts.Depth,
		logCaller: opts.LogCaller,
	}
	return logr.New(sl)
}

// Options carries parameters which influence the way logs are generated.
type Options struct {
	// Depth biases the assumed number of call frames to the "true" caller.
	// This is useful when the calling code calls a function which then calls
	// stdr (e.g. a logging shim to another API).  Values less than zero will
	// be treated as zero.
	Depth int

	// LogCaller tells glogr to add a "caller" key to some or all log lines.
	// The glog implementation always logs this information in its per-line
	// header, whether this option is set or not.
	LogCaller MessageClass

	// TODO: add an option to log the date/time
}

// MessageClass indicates which category or categories of messages to consider.
type MessageClass int

const (
	None MessageClass = iota
	All
	Info
	Error
)

// StdLogger is the subset of the Go stdlib log.Logger API that is needed for
// this adapter.
type StdLogger interface {
	// Output is the same as log.Output and log.Logger.Output.
	Output(calldepth int, logline string) error
}

type logger struct {
	std       StdLogger
	prefix    string
	values    []interface{}
	depth     int
	logCaller MessageClass
}

var _ logr.LogSink = &logger{}
var _ logr.CallDepthLogSink = &logger{}

func flatten(kvList ...interface{}) string {
	if len(kvList)%2 != 0 {
		kvList = append(kvList, "<no-value>")
	}
	// Empirically bytes.Buffer is faster than strings.Builder for this.
	buf := bytes.NewBuffer(make([]byte, 0, 1024))
	for i := 0; i < len(kvList); i += 2 {
		k, ok := kvList[i].(string)
		if !ok {
			k = fmt.Sprintf("<non-string-key-%d>", i/2)
		}
		v := kvList[i+1]

		if i > 0 {
			buf.WriteRune(' ')
		}
		buf.WriteRune('"')
		buf.WriteString(k)
		buf.WriteRune('"')
		buf.WriteRune('=')
		buf.WriteString(pretty(v))
	}
	return buf.String()
}

func pretty(value interface{}) string {
	return prettyWithFlags(value, 0)
}

const (
	flagRawString = 0x1
)

// TODO: This is not fast. Most of the overhead goes here.
func prettyWithFlags(value interface{}, flags uint32) string {
	// Handling the most common types without reflect is a small perf win.
	switch v := value.(type) {
	case bool:
		return strconv.FormatBool(v)
	case string:
		if flags&flagRawString > 0 {
			return v
		}
		// This is empirically faster than strings.Builder.
		return `"` + v + `"`
	case int:
		return strconv.FormatInt(int64(v), 10)
	case int8:
		return strconv.FormatInt(int64(v), 10)
	case int16:
		return strconv.FormatInt(int64(v), 10)
	case int32:
		return strconv.FormatInt(int64(v), 10)
	case int64:
		return strconv.FormatInt(int64(v), 10)
	case uint:
		return strconv.FormatUint(uint64(v), 10)
	case uint8:
		return strconv.FormatUint(uint64(v), 10)
	case uint16:
		return strconv.FormatUint(uint64(v), 10)
	case uint32:
		return strconv.FormatUint(uint64(v), 10)
	case uint64:
		return strconv.FormatUint(v, 10)
	case uintptr:
		return strconv.FormatUint(uint64(v), 10)
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}

	buf := bytes.NewBuffer(make([]byte, 0, 256))
	t := reflect.TypeOf(value)
	if t == nil {
		return "null"
	}
	v := reflect.ValueOf(value)
	switch t.Kind() {
	case reflect.Bool:
		return strconv.FormatBool(v.Bool())
	case reflect.String:
		if flags&flagRawString > 0 {
			return v.String()
		}
		// This is empirically faster than strings.Builder.
		return `"` + v.String() + `"`
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(int64(v.Int()), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.FormatUint(uint64(v.Uint()), 10)
	case reflect.Float32:
		return strconv.FormatFloat(float64(v.Float()), 'f', -1, 32)
	case reflect.Float64:
		return strconv.FormatFloat(v.Float(), 'f', -1, 64)
	case reflect.Struct:
		buf.WriteRune('{')
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" {
				// reflect says this field is only defined for non-exported fields.
				continue
			}
			if i > 0 {
				buf.WriteRune(',')
			}
			buf.WriteRune('"')
			name := f.Name
			if tag, found := f.Tag.Lookup("json"); found {
				if comma := strings.Index(tag, ","); comma != -1 {
					name = tag[:comma]
				} else {
					name = tag
				}
			}
			buf.WriteString(name)
			buf.WriteRune('"')
			buf.WriteRune(':')
			buf.WriteString(pretty(v.Field(i).Interface()))
		}
		buf.WriteRune('}')
		return buf.String()
	case reflect.Slice, reflect.Array:
		buf.WriteRune('[')
		for i := 0; i < v.Len(); i++ {
			if i > 0 {
				buf.WriteRune(',')
			}
			e := v.Index(i)
			buf.WriteString(pretty(e.Interface()))
		}
		buf.WriteRune(']')
		return buf.String()
	case reflect.Map:
		buf.WriteRune('{')
		// This does not sort the map keys, for best perf.
		it := v.MapRange()
		i := 0
		for it.Next() {
			if i > 0 {
				buf.WriteRune(',')
			}
			// JSON only does string keys.
			buf.WriteRune('"')
			buf.WriteString(prettyWithFlags(it.Key().Interface(), flagRawString))
			buf.WriteRune('"')
			buf.WriteRune(':')
			buf.WriteString(pretty(it.Value().Interface()))
			i++
		}
		buf.WriteRune('}')
		return buf.String()
	case reflect.Ptr, reflect.Interface:
		return pretty(v.Elem().Interface())
	}
	return fmt.Sprintf(`"<unhandled-%s>"`, t.Kind().String())
}

type callerID struct {
	File string `json:"file"`
	Line int    `json:"line"`
}

func (l logger) caller() callerID {
	// +1 for this frame, +1 for Info/Error.
	_, file, line, ok := runtime.Caller(l.depth + 2)
	if !ok {
		return callerID{"<unknown>", 0}
	}
	return callerID{filepath.Base(file), line}
}

func (l *logger) Init(info logr.RuntimeInfo) {
	l.depth += info.CallDepth
}

func (l logger) Enabled(level int) bool {
	return globalVerbosity >= level
}

func (l logger) Info(level int, msg string, kvList ...interface{}) {
	args := make([]interface{}, 0, 64) // using a constant here impacts perf
	if l.logCaller == All || l.logCaller == Info {
		args = append(args, "caller", l.caller())
	}
	args = append(args, "level", level, "msg", msg)
	args = append(args, l.values...)
	args = append(args, kvList...)
	argsStr := flatten(args...)
	l.output(l.depth+1, fmt.Sprintln(l.prefix, argsStr))
}

func (l logger) Error(err error, msg string, kvList ...interface{}) {
	args := make([]interface{}, 0, 64) // using a constant here impacts perf
	if l.logCaller == All || l.logCaller == Error {
		args = append(args, "caller", l.caller())
	}
	args = append(args, "msg", msg)
	var loggableErr interface{}
	if err != nil {
		loggableErr = err.Error()
	}
	args = append(args, "error", loggableErr)
	args = append(args, l.values...)
	args = append(args, kvList...)
	argsStr := flatten(args...)
	l.output(l.depth+1, fmt.Sprintln(l.prefix, argsStr))
}

func (l logger) output(calldepth int, s string) {
	depth := calldepth + 2 // offset for this adapter

	// ignore errors - what can we really do about them?
	if l.std != nil {
		_ = l.std.Output(depth, s)
	} else {
		_ = log.Output(depth, s)
	}
}

// WithName returns a new logr.Logger with the specified name appended.  stdr
// uses '/' characters to separate name elements.  Callers should not pass '/'
// in the provided name string, but this library does not actually enforce that.
func (l logger) WithName(name string) logr.LogSink {
	if len(l.prefix) > 0 {
		l.prefix = l.prefix + "/"
	}
	l.prefix += name
	return &l

}

// WithValues returns a new logr.Logger with the specified key-and-values
// saved.
func (l logger) WithValues(kvList ...interface{}) logr.LogSink {
	// Three slice args forces a copy.
	n := len(l.values)
	l.values = append(l.values[:n:n], kvList...)
	return &l

}

func (l logger) WithCallDepth(depth int) logr.LogSink {
	l.depth += depth
	return &l
}

// Underlier exposes access to the underlying logging implementation.  Since
// callers only have a logr.Logger, they have to know which implementation is
// in use, so this interface is less of an abstraction and more of way to test
// type conversion.
type Underlier interface {
	GetUnderlying() StdLogger
}

// GetUnderlying returns the StdLogger underneath this logger.  Since StdLogger
// is itself an interface, the result may or may not be a Go log.Logger.
func (l logger) GetUnderlying() StdLogger {
	return l.std
}
