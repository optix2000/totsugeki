package proxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type StriveAPIProxy struct {
	Client             *http.Client
	Server             *http.Server
	GGStriveAPIURL     string
	PatchedAPIURL      string
	statsQueue         chan<- *http.Request
	wg                 sync.WaitGroup
	statsGetPrediction StatsGetPrediction
}

type StriveAPIProxyOptions struct {
	AsyncStatsSet   bool
	PredictStatsGet bool
}

func (s *StriveAPIProxy) proxyRequest(r *http.Request) (*http.Response, error) {
	apiURL, err := url.Parse(s.GGStriveAPIURL) // TODO: Const this
	if err != nil {
		fmt.Println(err)
		return nil, err
	}
	apiURL.Path = r.URL.Path

	r.URL = apiURL
	r.Host = ""
	r.RequestURI = ""
	return s.Client.Do(r)
}

// Proxy everything else
func (s *StriveAPIProxy) HandleCatchall(w http.ResponseWriter, r *http.Request) {
	s.statsGetPrediction.HandleCatchallPath(r.URL.Path)
	resp, err := s.proxyRequest(r)
	if err != nil {
		fmt.Println(err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()
	// Copy headers
	for name, values := range resp.Header {
		w.Header()[name] = values
	}
	w.WriteHeader(resp.StatusCode)
	reader := io.TeeReader(resp.Body, w) // For dumping API payloads
	_, err = io.ReadAll(reader)
	if err != nil {
		fmt.Println(err)
	}
}

// GGST uses the URL from this API after initial launch so we need to intercept this.
func (s *StriveAPIProxy) HandleGetEnv(w http.ResponseWriter, r *http.Request) {
	resp, err := s.proxyRequest(r)
	if err != nil {
		fmt.Println(err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()
	// Copy headers
	for name, values := range resp.Header {
		w.Header()[name] = values
	}
	w.WriteHeader(resp.StatusCode)
	buf, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Println(err)
	} else {
		s.statsGetPrediction.HandleGetEnv()
	}
	buf = bytes.Replace(buf, []byte(s.GGStriveAPIURL), []byte(s.PatchedAPIURL), -1)
	w.Write(buf)
}

// Copy of HandleCatchall that sends a copy of login data to statsGetPrediction
func (s *StriveAPIProxy) HandleLogin(w http.ResponseWriter, r *http.Request) {
	resp, err := s.proxyRequest(r)
	if err != nil {
		fmt.Println(err)
		w.WriteHeader(http.StatusInternalServerError)
		return

	}
	defer resp.Body.Close()
	// Copy headers
	for name, values := range resp.Header {
		w.Header()[name] = values

	}
	w.WriteHeader(resp.StatusCode)
	reader := io.TeeReader(resp.Body, w) // For dumping API payloads
	ret, err := io.ReadAll(reader)
	if err != nil {
		fmt.Println(err)
	} else {
		s.statsGetPrediction.HandleLoginData(ret)
	}

}

// Handles using statsGetPrediction or falls back
func (s *StriveAPIProxy) HandleGetStats(w http.ResponseWriter, r *http.Request) {
	if !s.statsGetPrediction.HandleGetStats(w, r) {
		s.HandleCatchall(w, r)
	}
}

func (s *StriveAPIProxy) Shutdown() {
	fmt.Println("Shutting down proxy...")

	err := s.Server.Shutdown(context.Background())
	if err != nil {
		fmt.Println(err)
	}

	s.stopStatsSender()

	fmt.Println("Waiting for connections to complete...")
	s.wg.Wait()
}

func CreateStriveProxy(listen string, GGStriveAPIURL string, PatchedAPIURL string, options *StriveAPIProxyOptions) *StriveAPIProxy {

	client := &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
			ResponseHeaderTimeout: 1 * time.Minute, // Some people have _really_ slow internet to Japan.
			MaxIdleConns:          2,
			MaxIdleConnsPerHost:   2,
			MaxConnsPerHost:       4,
			IdleConnTimeout:       90 * time.Second, // Drop idle connection after 90 seconds to balance between being nice to ASW and keeping things fast.
			TLSHandshakeTimeout:   30 * time.Second,
		},
		Timeout: 3 * time.Minute, // 2x the slowest request I've seen.
	}

	proxy := &StriveAPIProxy{
		Client:             client,
		Server:             &http.Server{Addr: listen},
		GGStriveAPIURL:     GGStriveAPIURL,
		PatchedAPIURL:      PatchedAPIURL,
		statsGetPrediction: CreateStatsGetPrediction(options.PredictStatsGet, GGStriveAPIURL, client),
	}

	statsSet := proxy.HandleCatchall

	if options.AsyncStatsSet {
		statsSet = proxy.HandleStatsSet
		proxy.statsQueue = proxy.startStatsSender()
	}

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Route("/api", func(r chi.Router) {
		r.HandleFunc("/sys/get_env", proxy.HandleGetEnv)
		r.HandleFunc("/user/login", proxy.HandleLogin)
		r.HandleFunc("/statistics/get", proxy.HandleGetStats)
		r.HandleFunc("/statistics/set", statsSet)
		r.HandleFunc("/*", proxy.HandleCatchall)
	})

	proxy.Server.Handler = r
	return proxy
}
