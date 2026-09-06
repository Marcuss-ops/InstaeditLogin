package api

import (
	"bufio"
	"compress/gzip"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
)

// gzipWriterPool recycles gzip Writers across responses. A fresh
// gzip.NewWriter allocates ~hundreds of KB of window/dictionary state
// per JSON response; Reset() reinitializes a pooled writer to the exact
// NewWriter state (same default compression level), so byte output is
// identical while the steady-state allocation drops to zero.
var gzipWriterPool = sync.Pool{
	New: func() any { return gzip.NewWriter(nil) },
}

// gzipJSONMiddleware compresses JSON responses only when the client advertises
// gzip support. Streaming, upgrades, already-encoded responses, and empty
// responses are left untouched.
func gzipJSONMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodHead || !acceptsGzip(req) {
			next.ServeHTTP(w, req)
			return
		}
		gw := &gzipResponseWriter{ResponseWriter: w}
		defer gw.close()
		next.ServeHTTP(gw, req)
	})
}

func acceptsGzip(req *http.Request) bool {
	return req != nil && strings.Contains(strings.ToLower(req.Header.Get("Accept-Encoding")), "gzip")
}

type gzipResponseWriter struct {
	http.ResponseWriter
	writer      *gzip.Writer
	wroteHeader bool
	compressed  bool
}

func isJSONContentType(contentType string) bool {
	contentType = strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	return contentType == "application/json" || strings.HasSuffix(contentType, "+json")
}

func (w *gzipResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	contentType := w.Header().Get("Content-Type")
	alreadyEncoded := strings.TrimSpace(w.Header().Get("Content-Encoding")) != ""
	if status >= 200 && status != http.StatusNoContent && status != http.StatusNotModified &&
		isJSONContentType(contentType) && !alreadyEncoded {
		w.Header().Del("Content-Length")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
		gw := gzipWriterPool.Get().(*gzip.Writer)
		gw.Reset(w.ResponseWriter)
		w.writer = gw
		w.compressed = true
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *gzipResponseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if !w.compressed {
		return w.ResponseWriter.Write(p)
	}
	return w.writer.Write(p)
}

func (w *gzipResponseWriter) Flush() {
	if w.writer != nil {
		_ = w.writer.Flush()
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *gzipResponseWriter) ReadFrom(src io.Reader) (int64, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if !w.compressed {
		if readerFrom, ok := w.ResponseWriter.(io.ReaderFrom); ok {
			return readerFrom.ReadFrom(src)
		}
		return io.Copy(w.ResponseWriter, src)
	}
	return io.Copy(w.writer, src)
}

func (w *gzipResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}

func (w *gzipResponseWriter) Push(target string, opts *http.PushOptions) error {
	if pusher, ok := w.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, opts)
	}
	return http.ErrNotSupported
}

func (w *gzipResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *gzipResponseWriter) close() {
	if w.writer != nil {
		_ = w.writer.Close()
		gzipWriterPool.Put(w.writer)
		w.writer = nil
	}
}
