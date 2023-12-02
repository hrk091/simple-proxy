package main

import (
	"crypto/tls"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	target := mustgetenv("TARGET_URL")
	port := getenv("PORT", "8888")
	log.Printf("Proxying to %s", target)
	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	h := &ProxyHandler{TargetURL: target}
	return http.ListenAndServe(":"+port, h)
}

type ProxyHandler struct {
	TargetURL string
}

func (h *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	target, err := url.Parse(h.TargetURL)
	if err != nil {
		log.Fatal(err)
	}
	rewriteRequestURL(r, target)
	r.Host = ""
	r.RequestURI = ""
	log.Printf("==> %+v", r)

	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		log.Printf("Error forwarding request: %+v", err)
		http.Error(w, "Error forwarding request.", http.StatusBadRequest)
		return
	}
	log.Printf("<== status: %v", resp.Status)
	defer resp.Body.Close()

	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)

	_, err = io.Copy(w, resp.Body)
	if err != nil {
		http.Error(w, "Error writing response", http.StatusInternalServerError)
	}
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
