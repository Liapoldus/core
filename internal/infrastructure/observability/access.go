package observability

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"
)

type AccessRecord struct {
	RequestID string
	Listener  string
	Route     string
	Method    string
	Host      string
	Path      string
	Status    int
	Duration  float64
	Bytes     int64
}

type AccessFields struct {
	RequestIDHeader string
	Timestamp       string
	RequestID       string
	Listener        string
	Route           string
	Method          string
	Host            string
	Path            string
	Status          string
	Duration        string
	Bytes           string
}

type AccessLogger struct {
	writers []io.Writer
	fields  AccessFields
	mutex   sync.Mutex
}

func NewAccessLogger(writers []io.Writer, fields AccessFields) *AccessLogger {
	return &AccessLogger{writers: append([]io.Writer(nil), writers...), fields: fields}
}

func (l *AccessLogger) Write(record AccessRecord) {
	if l == nil || len(l.writers) == 0 {
		return
	}
	values := map[string]any{
		l.fields.Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		l.fields.RequestID: record.RequestID,
		l.fields.Listener:  record.Listener,
		l.fields.Route:     record.Route,
		l.fields.Method:    record.Method,
		l.fields.Host:      record.Host,
		l.fields.Path:      record.Path,
		l.fields.Status:    record.Status,
		l.fields.Duration:  record.Duration,
		l.fields.Bytes:     record.Bytes,
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return
	}
	encoded = append(encoded, '\n')
	l.mutex.Lock()
	defer l.mutex.Unlock()
	for _, writer := range l.writers {
		_, _ = writer.Write(encoded)
	}
}

func (l *AccessLogger) RequestID(request *http.Request) string {
	if l == nil || request == nil {
		return ""
	}
	return request.Header.Get(l.fields.RequestIDHeader)
}

func (l *AccessLogger) EnsureRequestID(response http.ResponseWriter, request *http.Request) string {
	if l == nil || request == nil {
		return ""
	}
	requestID := l.RequestID(request)
	if requestID == "" {
		var value [16]byte
		if _, err := rand.Read(value[:]); err != nil {
			return ""
		}
		requestID = hex.EncodeToString(value[:])
		request.Header.Set(l.fields.RequestIDHeader, requestID)
	}
	if response != nil {
		response.Header().Set(l.fields.RequestIDHeader, requestID)
	}
	return requestID
}
