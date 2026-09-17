package api

import (
	"compress/gzip"
	"net/http"
	"strings"
	"sync"
)

// gzipPool reuses compressors across requests.
var gzipPool = sync.Pool{
	New: func() any { return gzip.NewWriter(nil) },
}

// gzipWriter compresses lazily: it switches gzip on only for a 2xx
// response with a body, so error responses, 204s and 304s pass through
// unchanged. Content-Length is dropped when compressing since the encoded
// size is unknown.
type gzipWriter struct {
	http.ResponseWriter
	gz          *gzip.Writer
	wroteHeader bool
}

func (g *gzipWriter) WriteHeader(code int) {
	if g.wroteHeader {
		return
	}
	g.wroteHeader = true
	if code >= 200 && code < 300 && code != http.StatusNoContent {
		g.Header().Del("Content-Length")
		g.Header().Set("Content-Encoding", "gzip")
		g.gz = gzipPool.Get().(*gzip.Writer)
		g.gz.Reset(g.ResponseWriter)
	}
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipWriter) Write(b []byte) (int, error) {
	if !g.wroteHeader {
		g.WriteHeader(http.StatusOK)
	}
	if g.gz != nil {
		return g.gz.Write(b)
	}
	return g.ResponseWriter.Write(b)
}

// Flush implements http.Flusher.
func (g *gzipWriter) Flush() {
	if g.gz != nil {
		_ = g.gz.Flush()
	}
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// close finishes the gzip stream and returns the compressor to the pool.
func (g *gzipWriter) close() {
	if g.gz == nil {
		return
	}
	_ = g.gz.Close()
	gzipPool.Put(g.gz)
	g.gz = nil
}

// withGzip compresses responses for clients that accept gzip. Vary is set
// on every response so intermediaries key on Accept-Encoding. HEAD
// requests are never compressed: there is no body to compress and the
// headers should describe the GET response.
func withGzip(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Accept-Encoding")
		if r.Method == http.MethodHead || !acceptsGzip(r) {
			next.ServeHTTP(w, r)
			return
		}
		gw := &gzipWriter{ResponseWriter: w}
		defer gw.close()
		next.ServeHTTP(gw, r)
	})
}

// acceptsGzip reports whether Accept-Encoding lists gzip with a non-zero
// quality.
func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		enc, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.TrimSpace(enc) != "gzip" {
			continue
		}
		q := strings.TrimSpace(params)
		if strings.HasPrefix(q, "q=") && strings.TrimSpace(q[2:]) == "0" {
			return false
		}
		return true
	}
	return false
}
