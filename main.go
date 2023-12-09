package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/rs/cors"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

var (
	debug = os.Getenv("DEBUG") != ""
)

func run() error {
	target := mustgetenv("TARGET_URL")
	port := getenv("PORT", "8888")
	cacheDir := getenv("CACHE_DIR", "")

	ctx := context.Background()
	cache := NewLocalCache(cacheDir)
	cache.Run(ctx)

	log.Printf("Proxying to %s", target)
	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{}
	c := cors.Default()

	h := c.Handler(&ProxyHandler{targetURL: target, cache: cache})
	return http.ListenAndServe(":"+port, h)
}

type LocalCache struct {
	data     map[string][]byte
	filePath string
}

func NewLocalCache(cacheDir string) *LocalCache {
	var fp string
	if cacheDir != "" {
		fp = path.Join(cacheDir, "cache.json")
	}
	return &LocalCache{filePath: fp, data: map[string][]byte{}}
}

func (c *LocalCache) useCache() bool {
	return c.filePath != ""
}

// Save saves the cache to the file
func (c *LocalCache) Save() error {
	buf, err := json.Marshal(c.data)
	if err != nil {
		return err
	}
	return os.WriteFile(c.filePath, buf, 0644)
}

// Load loads the cache from the file
func (c *LocalCache) Load() error {
	buf, err := os.ReadFile(c.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return json.Unmarshal(buf, &c.data)
}

func (c *LocalCache) Run(ctx context.Context) {
	if !c.useCache() {
		return
	}

	if err := c.Load(); err != nil {
		log.Printf("Error loading cache: %+v", err)
	}

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if err := c.Save(); err != nil {
					log.Printf("Error saving cache: %+v", err)
				}
				if debug {
					log.Printf("Cache saved.")
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}

type ProxyHandler struct {
	targetURL string
	cache     *LocalCache
}

func (h *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	p := r.URL.String()
	log.Printf("---")
	log.Printf("path: %s", p)

	if r.Method == http.MethodGet && h.cache.useCache() {
		if cached, ok := h.cache.data[p]; ok {
			log.Printf("==> Cache hit")
			if _, err := io.Copy(w, bytes.NewReader(cached)); err != nil {
				e := fmt.Errorf("Error writing response: %+v\n", err)
				http.Error(w, e.Error(), http.StatusInternalServerError)
			}
			return
		} else {
			var keys []string
			for k := range h.cache.data {
				keys = append(keys, k)
			}
			log.Printf("Cache missed: current keys: %+v", keys)
		}
	}

	target, err := url.Parse(h.targetURL)
	if err != nil {
		log.Fatal(err)
	}
	rewriteRequestURL(r, target)
	r.Host = ""
	r.RequestURI = ""
	// remove If-None-Match and If-Modified-Since to force-fetch when cache missed in proxy
	r.Header.Del("If-None-Match")
	r.Header.Del("If-Modified-Since")
	if debug {
		log.Printf("==> req[%s]: %+v", p, r)
	}

	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		log.Printf("Error forwarding request: %+v", err)
		http.Error(w, err.Error(), resp.StatusCode)
		return
	}
	if debug {
		log.Printf("<== resp[%s]: %s, %+v", p, resp.Status, resp)
	}
	defer resp.Body.Close()

	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)

	if r.Method != http.MethodGet || !h.cache.useCache() {
		if _, err := io.Copy(w, resp.Body); err != nil {
			e := fmt.Errorf("Error reading response: %+v\n", err)
			http.Error(w, e.Error(), http.StatusInternalServerError)
		}
		return
	}

	// TODO: Make gzip optional
	log.Printf("==> Caching response: %s", p)
	gr, err := gzip.NewReader(io.TeeReader(resp.Body, w))
	if errors.Is(err, io.EOF) {
		log.Printf("<== EOF: %s", p)
		return
	} else if err != nil {
		log.Printf("Error: create gzip reader: %+v", err)
		return
	}
	defer gr.Close()

	buf := &bytes.Buffer{}
	if _, err := io.Copy(buf, gr); err != nil {
		log.Printf("Error: read response by gzip reader: %+v", err)
		return
	}

	h.cache.data[p] = buf.Bytes()
	log.Printf("<== Cache created: %s", p)

}

func mustgetenv(name string) string {
	v, ok := os.LookupEnv(name)
	if !ok {
		log.Fatalf("%s environment variable not set.", name)
	}
	return v
}

func getenv(name string, _default string) string {
	v, ok := os.LookupEnv(name)
	if !ok {
		return _default
	}
	return v
}

// Copied from httputil.ReverseProxy
func rewriteRequestURL(req *http.Request, target *url.URL) {
	targetQuery := target.RawQuery
	req.URL.Scheme = target.Scheme
	req.URL.Host = target.Host
	req.URL.Path, req.URL.RawPath = joinURLPath(target, req.URL)
	if targetQuery == "" || req.URL.RawQuery == "" {
		req.URL.RawQuery = targetQuery + req.URL.RawQuery
	} else {
		req.URL.RawQuery = targetQuery + "&" + req.URL.RawQuery
	}
}

func joinURLPath(a, b *url.URL) (path, rawpath string) {
	if a.RawPath == "" && b.RawPath == "" {
		return singleJoiningSlash(a.Path, b.Path), ""
	}
	// Same as singleJoiningSlash, but uses EscapedPath to determine
	// whether a slash should be added
	apath := a.EscapedPath()
	bpath := b.EscapedPath()

	aslash := strings.HasSuffix(apath, "/")
	bslash := strings.HasPrefix(bpath, "/")

	switch {
	case aslash && bslash:
		return a.Path + b.Path[1:], apath + bpath[1:]
	case !aslash && !bslash:
		return a.Path + "/" + b.Path, apath + "/" + bpath
	}
	return a.Path + b.Path, apath + bpath
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	}
	return a + b
}
